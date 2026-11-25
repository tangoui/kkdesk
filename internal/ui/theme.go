package appui

import (
	"sync/atomic"

	"github.com/tangoui/tango"
	"github.com/tangoui/tango/ui"
	"github.com/tangoui/tango/ui/theme"
	"github.com/tangoui/tango/uihost"

	"github.com/tangoui/kkdesk/internal/config"
)

// themeSetting is 设置 › 通用 › 主题 (config.Theme*). The main window and
// the file windows follow it; the session window stays dark.
var themeSetting atomic.Value // string

func setThemeSetting(s string) { themeSetting.Store(s) }

func currentTheme() string {
	if s, ok := themeSetting.Load().(string); ok {
		return s
	}
	return config.ThemeSystem
}

// themeMode is the uihost mode a window opens with.
func themeMode() uihost.ThemeMode {
	switch currentTheme() {
	case config.ThemeLight:
		return uihost.ThemeLight
	case config.ThemeDark:
		return uihost.ThemeDark
	}
	return uihost.ThemeSystem
}

// applyTheme switches an open window to the current setting. Main thread.
func applyTheme(r *ui.Runner) {
	if r == nil {
		return
	}
	switch currentTheme() {
	case config.ThemeLight, config.ThemeDark:
		uihost.FollowSystemTheme(r, false)
		var opts []theme.Option
		if currentTheme() == config.ThemeDark {
			opts = append(opts, theme.Dark())
		}
		th := theme.AntDesign(opts...)
		r.Post(func() { r.SetTheme(th) })
	default:
		uihost.FollowSystemTheme(r, true)
	}
}

// switchTheme saves nothing: it applies s to the main window r and every
// open file window.
func switchTheme(a *tango.App, r *ui.Runner, s string) {
	setThemeSetting(s)
	a.Post(func() {
		applyTheme(r)
		fileWins.Range(func(_, w any) bool {
			applyTheme(w.(*filesWin).runner.Load())
			return true
		})
	})
}
