package appui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/tangoui/tango/sys/autostart"
	"github.com/tangoui/tango/ui"
)

type loginItemSetting struct {
	loaded bool
	on     bool
	err    error
}

var kkdeskLoginItem = autostart.Options{ID: "com.kkdesk.desktop", Name: "kkdesk"}

func (m *Main) loginItemSettings(c *ui.Context, f *ui.FormW) {
	state := &m.set.loginItem
	if !state.loaded {
		state.on, state.err = autostart.IsEnabled(kkdeskLoginItem)
		state.loaded = true
	}
	exe, exeErr := os.Executable()
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	cache, _ := os.UserCacheDir()
	dev := os.Getenv("TANGO_DEV_ROLE") != "" || os.Getenv("TANGO_DEV_WINDOW_STATE") != "" ||
		(cache != "" && pathInDir(exe, filepath.Join(cache, "tango", "dev"))) || pathInDir(exe, os.TempDir())

	item := f.Item("开机启动")
	switch {
	case errors.Is(state.err, autostart.ErrUnsupported):
		item.Help("当前平台暂不支持。")
	case state.err != nil:
		item.Error("无法读取登录项：" + state.err.Error())
	case exeErr != nil:
		item.Error("无法定位 kkdesk 程序。")
	case dev:
		item.Help("请从固定位置启动安装版，再开启此项。")
	default:
		item.Help("登录当前系统账户后自动打开 kkdesk。")
	}
	item.Body(func() {
		controlRow(c, func() {
			on := state.on
			disabled := state.err != nil || (dev || exeErr != nil) && !on
			if !ui.Switch(c, &on).A11yLabel("登录时自动启动").Disabled(disabled).Changed() {
				return
			}
			setLoginItem(c, state, on)
		})
	})
}

func setLoginItem(c *ui.Context, state *loginItemSetting, on bool) {
	action := autostart.Disable
	if on {
		action = autostart.Enable
	}
	if err := action(kkdeskLoginItem); err != nil {
		ui.Message(c, "error", "无法更改登录项："+err.Error())
		return
	}
	state.on, state.err = autostart.IsEnabled(kkdeskLoginItem)
	if state.err == nil && state.on != on {
		ui.Message(c, "warning", "请在系统的启动应用设置中启用 kkdesk。")
	}
	c.Invalidate()
}

func pathInDir(path, dir string) bool {
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
