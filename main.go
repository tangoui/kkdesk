// mmdesk is a remote desktop built with Tango. Its window is drawn by
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

	"mmdesk/internal/app"
	"mmdesk/internal/capture"
	"mmdesk/internal/inject"
	appui "mmdesk/internal/ui"
)

// State is what survives a hot reload under tan dev: exported,
// gob-encodable fields.
type State struct {
	Form appui.Form
}

var st State

func main() {
	o, fs, err := parseFlags(os.Args[1:])
	if errors.Is(err, flag.ErrHelp) {
		return
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "mmdesk:", err)
		fs.Usage()
		os.Exit(2)
	}
	if err := run(o); err != nil {
		fmt.Fprintln(os.Stderr, "mmdesk:", err)
		os.Exit(1)
	}
}

func run(o options) error {
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
		BitrateMbps: o.bitrate,
		Log:         logw,
		AutoAccept:  o.autoAccept,
	}, func() { invalidate() })

	// Never prompt for permissions here (measurement runs); explain instead.
	if o.host {
		if !capture.HasPermission() {
			return errors.New(app.ScreenRecordingHelp)
		}
		if !inject.HasPermission() {
			fmt.Fprintln(os.Stderr, "mmdesk:", app.AccessibilityHelp)
		}
	}
	if o.connect == "" {
		if err := host.SetEnabled(true); err != nil && o.host {
			return err
		}
		if o.host {
			fmt.Printf("mmdesk host listening on %s password %s\n", host.ListenAddr(), host.State().Password)
		}
		if !o.host && !capture.HasPermission() {
			log.Printf("mmdesk: %s", app.ScreenRecordingHelp)
		}
	}

	quit := true // closing the last window quits, on macOS too
	a := tango.New(tango.Options{Name: "mmdesk", ID: "com.mmdesk.desktop", QuitOnLastClose: &quit})

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
				fmt.Fprintln(os.Stderr, "mmdesk:", err)
				a.Post(func() { openMain(false, err.Error()) })
				return
			}
			a.Post(func() {
				err := appui.OpenSession(a, viewer, func(msg string) {
					if msg != "" {
						fmt.Fprintln(os.Stderr, "mmdesk:", msg)
					}
				})
				if err != nil {
					fmt.Fprintln(os.Stderr, "mmdesk: session window:", err)
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
