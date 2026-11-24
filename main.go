// mmdesk is a remote desktop built with Tango. Its window is drawn by
// package ui (no WebView); the remote screen is a native video view hosted
// with uihost.NativeHost.
//
// One binary, two roles: without flags it opens the main window (what
// "tan dev" runs); -host shares this screen, -connect controls another
// machine (see flags.go).
package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/tangoui/tango"
	"github.com/tangoui/tango/ui"
	"github.com/tangoui/tango/ui/layout"
	"github.com/tangoui/tango/ui/theme"
	"github.com/tangoui/tango/uihost"
)

// State is what survives a hot reload under tan dev: exported,
// gob-encodable fields.
type State struct {
	PeerAddr string
	Password string
}

var (
	app    *tango.App
	runner *ui.Runner
	st     State
)

// page is the main window: this machine's address and temporary password
// on the left, the form to control another machine on the right
// (outputs/remote-desktop-ui/index.html). The actions are wired in
// internal/app.
func page(c *ui.Context) {
	ui.Scroll(c, func() {
		ui.Column(c, func() {
			ui.Title(c, "连接", 3)
			ui.Row(c, func() {
				ui.Card(c, "允许控制本机").Layout(layout.Grow(1)).Body(func() {
					ui.Paragraph(c, "把本机地址和临时密码告诉对方，对方就能连接这台电脑。")
				})
				ui.Card(c, "控制其他电脑").Layout(layout.Grow(1)).Body(func() {
					f := ui.Form(c).Vertical()
					f.Body(func() {
						f.Item("对方地址").Body(func() { ui.Input(c, &st.PeerAddr).Placeholder("192.168.1.20") })
						f.Item("临时密码").Body(func() { ui.Input(c, &st.Password).Placeholder("6 位数字") })
						f.Item("").NoLabel().Body(func() { ui.Button(c, "连接").Primary() })
					})
				})
			}, layout.Gap(16), layout.WrapItems())
		}, layout.Gap(16), layout.Pad(24))
	})
}

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
	_ = o // -host / -connect are wired in internal/app

	uihost.Persist(&st) // before the first frame

	quit := true // closing the window quits, on macOS too
	app = tango.New(tango.Options{Name: "mmdesk", ID: "com.mmdesk.desktop", QuitOnLastClose: &quit})
	app.OnReady(func() {
		r, _, err := uihost.Open(app, uihost.Options{Surface: tango.SurfaceOptions{
			Title:     "mmdesk",
			Width:     960,
			Height:    640,
			MinWidth:  640,
			MinHeight: 480,
			Resizable: true,
			Centered:  true,
		}}, page, ui.RunnerOptions{Theme: theme.AntDesign()})
		if err != nil {
			log.Fatal(err)
		}
		runner = r
	})
	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
