package appui

import (
	"runtime"

	"github.com/tangoui/tango/ui"
	"github.com/tangoui/tango/ui/layout"

	"github.com/tangoui/kkdesk/internal/lockscreen"
	"github.com/tangoui/kkdesk/internal/session"
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
			if actionErr == nil {
				m.o.Host.ClearLockScreenFailure()
			}
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

func (m *Main) lockScreenSettings(c *ui.Context, f *ui.FormW, lastFailure session.LockScreenState) {
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
	} else if s.info.NeedsInstalledLaunch() {
		item.Help("安装位置为 " + s.info.InstalledPath)
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
			switch {
			case s.info.NeedsInstalledLaunch():
				ui.Text(c, "已安装，当前 kkdesk 未从安装位置运行")
				if ui.Button(c, "卸载").Clicked() {
					m.checkLockScreenSettings(true, false)
				}
			case s.info.State == lockscreen.ComponentUnsupported && s.err == nil:
				ui.Text(c, "此系统暂不支持锁屏远程操作")
			case s.info.State == lockscreen.ComponentNeedsSignature:
				ui.Text(c, lockscreen.ErrNeedsSignature.Error())
			case s.info.State == lockscreen.ComponentNeedsApproval:
				ui.Text(c, "等待系统批准")
				if ui.Button(c, "卸载").Clicked() {
					m.checkLockScreenSettings(true, false)
				}
			case s.info.State == lockscreen.ComponentAbsent && s.err == nil:
				if s.info.InstalledPath == "" {
					ui.Text(c, "当前 kkdesk 安装包没有锁屏远控组件")
				} else {
					ui.Text(c, "未安装")
				}
				if s.info.InstalledPath != "" && ui.Button(c, "安装").Clicked() {
					m.checkLockScreenSettings(true, true)
				}
			case s.info.State == lockscreen.ComponentStopped || s.info.State == lockscreen.ComponentNeedsUpdate || s.info.State == lockscreen.ComponentRunning:
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
					switch lastFailure {
					case session.LockScreenServiceUnavailable:
						ui.Text(c, "已安装，锁屏时无法连接助手")
					case session.LockScreenSignatureRequired:
						ui.Text(c, "无法验证锁屏远控组件的签名")
					case session.LockScreenComponentError:
						ui.Text(c, "锁屏远控组件出错，详见本机日志")
					default:
						ui.Text(c, "已安装")
					}
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
