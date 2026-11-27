package appui

import (
	"runtime"

	"github.com/tangoui/tango/ui"
	"github.com/tangoui/tango/ui/layout"

	"github.com/tangoui/kkdesk/internal/lockscreen"
)

type lockScreenSettingsState struct {
	info   lockscreen.ComponentInfo
	err    error
	loaded bool
	busy   bool
}

func (m *Main) checkLockScreenSettings(manage bool, install bool) {
	m.lockScreen.busy = true
	go func() {
		var actionErr error
		if manage {
			actionErr = lockscreen.ManageComponent(install)
		}
		info, err := lockscreen.Component()
		if actionErr != nil {
			err = actionErr
		}
		m.o.App.Post(func() {
			m.lockScreen = lockScreenSettingsState{info: info, err: err, loaded: true}
			m.Invalidate()
		})
	}()
}

func (m *Main) lockScreenSettings(c *ui.Context, f *ui.FormW) {
	s := &m.lockScreen
	if lockscreen.PlatformStart() == nil {
		helpGap(c)
		f.Item("锁屏时允许远程操作").Help("此系统暂不支持锁屏远程操作。")
		return
	}
	if !s.loaded && !s.busy {
		m.checkLockScreenSettings(false, false)
	}
	helpGap(c)
	item := f.Item("锁屏时允许远程操作")
	if s.err != nil {
		item.Error(s.err.Error())
	} else if s.info.State == lockscreen.ComponentNeedsApproval {
		item.Help("在 macOS「系统设置 › 通用 › 登录项与扩展」中批准 kkdesk。")
	} else if s.info.State == lockscreen.ComponentRunning && !s.info.UsingInstall {
		item.Help("退出当前 kkdesk 后，从安装位置打开：" + s.info.InstalledPath)
	} else if runtime.GOOS == "darwin" {
		item.Help("组件需要屏幕录制和辅助功能权限。")
	} else {
		item.Help("安装、更新和卸载需要管理员授权。")
	}
	item.Body(func() {
		ui.Row(c, func() {
			if s.busy {
				ui.Text(c, "正在检查锁屏远控组件…")
				return
			}
			switch s.info.State {
			case lockscreen.ComponentNeedsSignature:
				ui.Text(c, lockscreen.ErrNeedsSignature.Error())
			case lockscreen.ComponentNeedsApproval:
				ui.Text(c, "等待系统批准")
				if ui.Button(c, "卸载").Clicked() {
					m.checkLockScreenSettings(true, false)
				}
			case lockscreen.ComponentAbsent:
				ui.Text(c, "未安装")
				if ui.Button(c, "安装").Clicked() {
					m.checkLockScreenSettings(true, true)
				}
			case lockscreen.ComponentStopped, lockscreen.ComponentNeedsUpdate, lockscreen.ComponentRunning:
				switch s.info.State {
				case lockscreen.ComponentNeedsUpdate:
					ui.Text(c, "锁屏远控组件需要更新")
					if ui.Button(c, "更新").Clicked() {
						m.checkLockScreenSettings(true, true)
					}
				case lockscreen.ComponentStopped:
					ui.Text(c, "已安装，服务未启动")
					if ui.Button(c, "启动服务").Clicked() {
						m.checkLockScreenSettings(true, true)
					}
				case lockscreen.ComponentRunning:
					ui.Text(c, "已安装")
				}
				if ui.Button(c, "卸载").Clicked() {
					m.checkLockScreenSettings(true, false)
				}
			}
			if ui.Button(c, "刷新").Clicked() {
				m.checkLockScreenSettings(false, false)
			}
		}, layout.Gap(8), layout.Middle(), layout.WrapItems())
	})
}
