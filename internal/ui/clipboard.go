package appui

import (
	"errors"
	"sync/atomic"

	"github.com/tangoui/tango"
	"github.com/tangoui/tango/sys/clipboard"
)

// Clipboard is the system clipboard text for clipsync, through Tango. The
// host is made before the app, so the app is set later (SetApp); until
// then reads and writes fail and syncing waits.
type Clipboard struct {
	app atomic.Pointer[tango.App]
}

var errNoApp = errors.New("clipboard: app not running yet")

// SetApp sets the app whose clipboard is used.
func (c *Clipboard) SetApp(a *tango.App) { c.app.Store(a) }

// ReadText returns the clipboard text.
func (c *Clipboard) ReadText() (string, error) {
	a := c.app.Load()
	if a == nil {
		return "", errNoApp
	}
	return clipboard.ReadText(a)
}

// WriteText replaces the clipboard content with text.
func (c *Clipboard) WriteText(s string) error {
	a := c.app.Load()
	if a == nil {
		return errNoApp
	}
	return clipboard.WriteText(a, s)
}
