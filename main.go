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
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/tangoui/tango"
	"github.com/tangoui/tango/service/updater"
	"github.com/tangoui/tango/service/updater/updateui"
	"github.com/tangoui/tango/sys/single"
	"github.com/tangoui/tango/uihost"

	"github.com/tangoui/kkdesk/internal/app"
	"github.com/tangoui/kkdesk/internal/config"
	"github.com/tangoui/kkdesk/internal/pairing"
	"github.com/tangoui/kkdesk/internal/rendezvous"
	appui "github.com/tangoui/kkdesk/internal/ui"
	"github.com/tangoui/kkdesk/internal/update"
)

// State is what survives a hot reload under tan dev: exported,
// gob-encodable fields.
type State struct {
	Form appui.Form
}

var st State

// version overrides the version of tan.json, e.g. -ldflags
// "-X main.version=$(git describe --tags --always --dirty)" for test builds
// (which then do not update themselves: see internal/update).
var version = ""

// tanJSON is the project file; its "version" is the release version (tan
// build and tan release-manifest read the same field).
//
//go:embed tan.json
var tanJSON []byte

// appVersion is this build's version: the -ldflags override, else the
// version of tan.json, with "-dev" under tan dev.
func appVersion() string {
	if version != "" {
		return version
	}
	var t struct{ Version string }
	if err := json.Unmarshal(tanJSON, &t); err != nil || t.Version == "" {
		return "dev"
	}
	if os.Getenv("TANGO_DEV_ROLE") != "" {
		return t.Version + "-dev"
	}
	return t.Version
}

func main() {
	app.Version = appVersion()
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
			log.Printf("kkdesk: diagnostic log %s, args %q", l.Path, redactArgs(os.Args[1:]))
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
		if o.files {
			st.Form.Mode = appui.ModeTransfer
		}
	}

	// The main window is created in OnReady; host and viewer notify it.
	var mainWin atomic.Pointer[appui.Main]
	invalidate := func() {
		if m := mainWin.Load(); m != nil {
			m.Invalidate()
		}
	}
	quit := true // closing the last window quits, on macOS too
	a := tango.New(tango.Options{Name: "kkdesk", ID: "com.kkdesk.desktop", QuitOnLastClose: &quit})
	// One app per configuration folder: a second launch shows the running
	// window instead of opening the same settings and port again (both would
	// listen, and each would save its own copy of the settings over the
	// other's). Claimed before the settings are read.
	devRole := ""
	if !releaseBuild { // production builds ignore tan dev's variable
		devRole = os.Getenv("TANGO_DEV_ROLE")
	}
	if wantsSingleInstance(o, devRole) {
		dir, err := config.Dir()
		if err != nil {
			dir = "." // where openStore falls back to
		}
		primary, err := single.Claim(a, single.Options{Key: singleInstanceKey(dir)})
		if !primary {
			log.Printf("kkdesk: already running; handed over to it (%v)", err)
			return nil
		}
		if err != nil {
			log.Printf("kkdesk: single instance: %v", err)
		}
	}

	store := openStore()
	board := &appui.Clipboard{} // the app is set once it exists
	hcfg := hostConfig(o, store, logw)
	identity, identityErr := pairing.Open(filepath.Dir(store.Path()))
	if identityErr != nil {
		log.Printf("kkdesk: desktop identity in %s: %v", filepath.Dir(store.Path()), identityErr)
	}
	hcfg.Pairing, hcfg.IdentityErr = identity, identityErr
	hcfg.Viewers = store
	if !o.host { // measurement runs leave the clipboard alone
		hcfg.Clipboard = board
	}
	host := app.NewHost(hcfg, func() { invalidate() })

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
	if o.connect == "" && (o.host || store.Settings().AllowRemote) {
		if err := host.SetEnabled(true); err != nil && o.host {
			log.Printf("kkdesk: %v", err)
			return err
		}
		if o.host {
			fmt.Println("kkdesk host listening on", host.ListenAddr(), "password", host.State().Password)
			// The log is meant to be sent to others: no password in it.
			appLog.FileOnly("kkdesk host listening on %s (local addresses %v)", host.ListenAddr(), app.LocalIPv4())
		}
		if !o.host {
			if help := app.CaptureHelp(); help != "" {
				log.Printf("kkdesk: %s", help)
			}
		}
	}

	board.SetApp(a)

	// Self-update: builds made by tan build, started normally (not the
	// measurement modes -host and -connect).
	var upd *updater.Updater
	if releaseBuild && !o.host && o.connect == "" {
		upd = update.New(app.Version, os.Getenv)
	}

	var viewer *app.Viewer
	openMain := func(hidden bool, errMsg string) {
		m, err := appui.OpenMain(appui.MainOptions{
			App: a, Host: host, Store: store, Form: &st.Form, ViewerLog: logw, Clipboard: board, Updater: upd,
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
			if upd != nil {
				// Confirms a just-installed version (otherwise it is rolled
				// back), then checks once a day in the background.
				if _, err := updateui.Start(a, upd, updateui.Options{}); err != nil {
					log.Printf("kkdesk: update: %v", err)
				}
			}
			return
		}
		settings := store.Settings()
		viewer = app.NewViewer(app.ViewerConfig{Addr: o.connect, Password: o.password, ViewOnly: o.viewOnly, FilesOnly: o.files, Log: logw, Identity: identity,
			Remote: rendezvous.Config{URL: settings.RemoteURL, Token: settings.RemoteToken, PublicKey: settings.RemotePublicKey}, Clipboard: board, Devices: store}, nil)
		go func() {
			if err := viewer.Connect(context.Background()); err != nil {
				fmt.Fprintln(os.Stderr, "kkdesk:", err)
				a.Post(func() { openMain(false, err.Error()) })
				return
			}
			a.Post(func() {
				onEnd := func(msg string) {
					if msg != "" {
						fmt.Fprintln(os.Stderr, "kkdesk:", msg)
					}
				}
				var err error
				if o.files {
					err = appui.OpenFiles(a, viewer, true, onEnd)
				} else {
					err = appui.OpenSession(a, viewer, onEnd)
				}
				if err != nil {
					fmt.Fprintln(os.Stderr, "kkdesk: window:", err)
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

// openStore loads the configuration (设置, 我的设备). A problem is logged
// and shown in the main window; the program runs on with the defaults.
func openStore() *config.Store {
	dir, err := config.Dir()
	if err != nil {
		log.Printf("kkdesk: configuration directory: %v", err)
		dir = "."
	}
	store, err := config.Open(dir)
	if err != nil {
		log.Printf("kkdesk: configuration: %v", err)
	}
	return store
}

// hostConfig combines the flags and the settings; a flag given on the
// command line wins. With -password (or -host, which prints the password
// once) the temporary password stays for the run: measurement scripts
// connect again with it.
func hostConfig(o options, store *config.Store, logw io.Writer) app.HostConfig {
	st := store.Settings()
	cfg := app.HostConfig{
		Addr:                    o.listen,
		Fixed:                   st.FixedPassword,
		Rotate:                  st.Rotate,
		FPS:                     st.FPS,
		MaxWidth:                o.maxWidth,
		BitrateMbps:             st.BitrateMbps,
		Log:                     logw,
		AutoAccept:              o.autoAccept || !st.Confirm,
		Remote:                  rendezvous.Config{URL: st.RemoteURL, Token: st.RemoteToken, PublicKey: st.RemotePublicKey},
		DisableSessionClipboard: !st.AllowSessionClipboard,
		DisableSessionFiles:     !st.AllowSessionFiles,
	}
	if cfg.Addr == "" {
		cfg.Addr = fmt.Sprintf(":%d", st.Port)
		// The system refused the saved port (a Windows reserved range):
		// keep the port the host moved to, so the address stays the same
		// from the next start on. Not with -listen (measurement scripts).
		cfg.OnPortMoved = func(port int) {
			if err := store.UpdateSettings(func(s *config.Settings) { s.Port = port }); err != nil {
				log.Printf("kkdesk: save port: %v", err)
			}
		}
	}
	if o.set["fps"] {
		cfg.FPS = o.fps
	}
	if o.set["bitrate"] {
		cfg.BitrateMbps = o.bitrate
	}
	fixedPW := o.connect == "" && o.password != ""
	if o.host {
		cfg.Rotate = config.RotateNever // -host prints the password once
	}
	switch {
	case fixedPW:
		cfg.Password, cfg.Rotate = o.password, config.RotateNever
	case !o.host && st.Rotate != config.RotateAfterSession:
		// Timed or never: keep the saved password (NewHost rotates it at
		// once when it is due).
		if t := store.TempPassword(); t.Password != "" {
			cfg.Password, cfg.PasswordSince = t.Password, t.Since
		}
	}
	if !fixedPW {
		cfg.OnPassword = func(t config.TempPassword) {
			if err := store.SetTempPassword(t); err != nil {
				log.Printf("kkdesk: save temporary password: %v", err)
			}
		}
	}
	return cfg
}

// redactArgs hides the value of -password in command-line arguments for the
// diagnostic log.
func redactArgs(args []string) []string {
	out := append([]string(nil), args...)
	for i, a := range out {
		name, _, hasValue := strings.Cut(strings.TrimLeft(a, "-"), "=")
		if name != "password" || !strings.HasPrefix(a, "-") {
			continue
		}
		if hasValue {
			out[i] = a[:strings.Index(a, "=")+1] + "***"
		} else if i+1 < len(out) {
			out[i+1] = "***"
		}
	}
	return out
}

// wantsSingleInstance reports whether this launch is the app itself (not a
// -host or -connect measurement run, nor a tan dev worker, where old and new
// workers overlap during a reload).
func wantsSingleInstance(o options, devRole string) bool {
	return !o.host && o.connect == "" && devRole == ""
}

// singleInstanceKey names the app instance of a configuration folder, so a
// second instance with its own KKDESK_CONFIG_DIR still runs.
func singleInstanceKey(configDir string) string {
	if abs, err := filepath.Abs(configDir); err == nil {
		configDir = abs
	}
	if runtime.GOOS == "windows" { // one folder, however it is spelled
		configDir = strings.ToLower(configDir)
	}
	sum := sha256.Sum256([]byte(filepath.Clean(configDir)))
	return "com.kkdesk.desktop-" + hex.EncodeToString(sum[:8])
}
