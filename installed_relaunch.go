package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tangoui/kkdesk/internal/lockscreen"
	"github.com/tangoui/tango/sys/autostart"
)

var errInstalledRelaunchUnconfirmed = errors.New("上次切换未能打开安装版 kkdesk，请手动重试")

type installedAppPlatform interface {
	Component() (lockscreen.ComponentInfo, error)
	StartInstalled(path string) error
}

type windowsDesktopInstall struct {
	protected bool
	userExe   string
	stale     bool
}

type installedRelaunch struct {
	platform installedAppPlatform
	pending  string
}

func wantsAutomaticInstalledRelaunch(args []string, configured bool, devRole string, handoff bool) bool {
	return !configured && devRole == "" && !handoff &&
		(len(args) == 0 || (len(args) == 1 && args[0] == autostart.LoginArg))
}

func (r *installedRelaunch) attempt(automatic bool) (bool, error) {
	info, err := r.platform.Component()
	if err != nil || !info.NeedsInstalledLaunch() {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(r.pending), 0700); err != nil {
		return false, err
	}
	mode := os.O_CREATE | os.O_WRONLY
	if automatic {
		mode |= os.O_EXCL
	}
	f, err := os.OpenFile(r.pending, mode, 0600)
	if automatic && errors.Is(err, os.ErrExist) {
		return false, errInstalledRelaunchUnconfirmed
	}
	if err != nil {
		return false, err
	}
	if err := f.Close(); err != nil {
		return false, err
	}
	if err := r.platform.StartInstalled(info.InstalledPath); err != nil {
		return false, fmt.Errorf("无法从安装位置重新启动 kkdesk：%w", err)
	}
	return true, nil
}

// A replacement that reached the main window acknowledges the handoff.
func (r *installedRelaunch) confirm() error {
	if _, err := os.Stat(r.pending); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	info, _ := r.platform.Component()
	if info.UsingInstall {
		return os.Remove(r.pending)
	}
	return nil
}
