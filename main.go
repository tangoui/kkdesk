// kkdesk is a remote desktop built with Tango. Its window is drawn by
// package ui (no WebView); the remote screen is a native video view hosted
// with uihost.NativeHost.
//
// One binary, two roles: without flags it opens the main window (what
// "tan dev" runs) and shares this screen; -host does the same and prints
// one machine-readable line, -connect controls another machine (see
// flags.go).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/tangoui/tango"
	"github.com/tangoui/tango/uihost"

	"github.com/tangoui/kkdesk/internal/app"
	appui "github.com/tangoui/kkdesk/internal/ui"
)

// State is what survives a hot reload under tan dev: exported,
// gob-encodable fields.
type State struct {
	Form appui.Form
}

var st State

// version is set at build time: -ldflags "-X main.version=$(git describe --tags --always --dirty)".
var version = "dev"

func main() {
	app.Version = version
	app.PrepareProcess() // Windows: per-monitor DPI awareness, before any window or display query
	o, fs, err := parseFlags(os.Args[1:])
	if errors.Is(err, flag.ErrHelp) {
		return
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "kkdesk:", err)
		fs.Usage()
		os.Exit(2)
	}
	if o.selftest {
		if !app.Selftest(app.SelftestConfig{FPS: o.fps, MaxWidth: o.maxWidth, BitrateMbps: o.bitrate}) {
			os.Exit(1)
		}
		return
	}
	if err := run(o); err != nil {
		fmt.Fprintln(os.Stderr, "kkdesk:", err)
		os.Exit(1)
	}
}

func run(o options) error {
	var appLog *app.AppLog
	if o.appLog != "" {
		l, err := app.OpenAppLog(o.appLog)
		if err != nil {
			fmt.Fprintln(os.Stderr, "kkdesk: diagnostic log:", err)
		} else {
			appLog = l
			defer l.Close()
			log.Printf("kkdesk: diagnostic log %s, args %q", l.Path, os.Args[1:])
			app.LogSystemInfo()
		}
	}

	var logw io.Writer = io.Discard
	if o.logPath != "" {
		f, err := os.Create(o.logPath)
		if err != nil {
			return err
		}
		defer f.Close()
		logw = f
	}

	uihost.Persist(&st) // before the first frame
	if o.connect != "" {
		st.Form = appui.Form{PeerAddr: o.connect, Password: o.password}
	}

	// The main window is created in OnReady; host and viewer notify it.
	var mainWin atomic.Pointer[appui.Main]
	invalidate := func() {
		if m := mainWin.Load(); m != nil {
			m.Invalidate()
		}
	}
	hostPW := ""
	if o.connect == "" {
		hostPW = o.password
	}
	host := app.NewHost(app.HostConfig{
		Password:    hostPW,
		FPS:         o.fps,
		MaxWidth:    o.maxWidth,
		BitrateMbps: o.bitrate,
		Log:         logw,
		AutoAccept:  o.autoAccept,
	}, func() { invalidate() })

	// Never prompt for permissions here (measurement runs); explain instead.
	if o.host {
		if help := app.CaptureHelp(); help != "" {
			log.Printf("kkdesk: %s", help)
			return errors.New(help)
		}
		if help := app.InjectHelp(); help != "" {
			fmt.Fprintln(os.Stderr, "kkdesk:", help)
		}
		if lim := app.InjectLimits(); lim != "" {
			log.Printf("kkdesk: %s", lim)
		}
	}
	if o.connect == "" {
		if err := host.SetEnabled(true); err != nil && o.host {
			log.Printf("kkdesk: %v", err)
			return err
		}
		if o.host {
			line := fmt.Sprintf("kkdesk host listening on %s password %s", host.ListenAddr(), host.State().Password)
			fmt.Println(line)
			appLog.FileOnly("%s (local addresses %v)", line, app.LocalIPv4())
		}
		if !o.host {
			if help := app.CaptureHelp(); help != "" {
				log.Printf("kkdesk: %s", help)
			}
		}
	}

	quit := true // closing the last window quits, on macOS too
	a := tango.New(tango.Options{Name: "kkdesk", ID: "com.kkdesk.desktop", QuitOnLastClose: &quit})

	var viewer *app.Viewer
	openMain := func(hidden bool, errMsg string) {
		m, err := appui.OpenMain(appui.MainOptions{
			App: a, Host: host, Form: &st.Form, ViewerLog: logw,
			ViewOnly: o.viewOnly, Hidden: hidden, Error: errMsg,
		})
		if err != nil {
			log.Fatal(err)
		}
		mainWin.Store(m)
	}
	a.OnReady(func() {
		if o.connect == "" {
			openMain(false, "")
			return
		}
		viewer = app.NewViewer(app.ViewerConfig{Addr: o.connect, Password: o.password, ViewOnly: o.viewOnly, Log: logw}, nil)
		go func() {
			if err := viewer.Connect(context.Background()); err != nil {
				fmt.Fprintln(os.Stderr, "kkdesk:", err)
				a.Post(func() { openMain(false, err.Error()) })
				return
			}
			a.Post(func() {
				err := appui.OpenSession(a, viewer, func(msg string) {
					if msg != "" {
						fmt.Fprintln(os.Stderr, "kkdesk:", msg)
					}
				})
				if err != nil {
					fmt.Fprintln(os.Stderr, "kkdesk: session window:", err)
					viewer.Close()
					a.Quit()
				}
			})
		}()
	})

	// SIGINT/SIGTERM (measurement scripts): say goodbye to the peer and
	// flush the stats before exiting.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sig
		a.Post(func() {
			if viewer != nil {
				viewer.Close()
			}
			go func() {
				host.Close()
				a.Quit()
			}()
		})
	}()

	err := a.Run()
	host.Close()
	if viewer != nil {
		viewer.Close()
		done := make(chan struct{})
		viewer.OnEnd(func() { close(done) })
		select {
		case <-done:
		case <-time.After(2 * time.Second): // still dialing
		}
	}
	return err
}
