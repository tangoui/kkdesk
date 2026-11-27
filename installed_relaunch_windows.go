//go:build windows && amd64 && cgo

package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/tangoui/kkdesk/internal/lockscreen"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const installedHandoffArg = "--lockscreen-handoff="

type windowsInstalledApp struct{}

func newInstalledAppPlatform() installedAppPlatform { return windowsInstalledApp{} }

func desktopInstall() windowsDesktopInstall {
	exe, err := os.Executable()
	if err != nil || !lockscreen.IsProtectedExecutable(exe) {
		return windowsDesktopInstall{}
	}
	install := windowsDesktopInstall{protected: true}
	// The 32-bit NSIS installer writes this entry even for amd64 kkdesk.
	const uninstallKey = `Software\Microsoft\Windows\CurrentVersion\Uninstall\com.kkdesk.desktop`
	key, err := registry.OpenKey(registry.CURRENT_USER, uninstallKey, registry.QUERY_VALUE|registry.WOW64_32KEY)
	if err != nil {
		return install
	}
	defer key.Close()
	dir, _, err := key.GetStringValue("InstallLocation")
	if err != nil || !filepath.IsAbs(dir) {
		return install
	}
	path := filepath.Join(dir, "kkdesk.exe")
	if lockscreen.IsProtectedExecutable(path) {
		return install
	}
	same, err := lockscreen.MatchesProtectedExecutable(path)
	if err == nil {
		install.userExe = path
		install.stale = !same
	}
	return install
}

func (windowsInstalledApp) Component() (lockscreen.ComponentInfo, error) {
	return lockscreen.Component()
}

func (windowsInstalledApp) StartInstalled(path string) error {
	if windows.GetCurrentProcessToken().IsElevated() {
		return errors.New("请先用普通用户权限打开 kkdesk")
	}
	// An inherited handle refers to this process even after it exits. A
	// PID opened by the replacement can already have been recycled.
	self := windows.CurrentProcess()
	var parent windows.Handle
	if err := windows.DuplicateHandle(self, self, self, &parent, windows.SYNCHRONIZE, true, 0); err != nil {
		return fmt.Errorf("duplicate parent handle: %w", err)
	}
	defer windows.CloseHandle(parent)
	args := append([]string{installedHandoffArg + strconv.FormatUint(uint64(parent), 10)}, os.Args[1:]...)
	cmd := exec.Command(path, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{AdditionalInheritedHandles: []syscall.Handle{syscall.Handle(parent)}}
	if err := cmd.Start(); err != nil {
		return err
	}
	_ = cmd.Process.Release()
	return nil
}

func waitForInstalledParent(args []string) ([]string, bool, error) {
	if len(args) == 0 {
		return args, false, nil
	}
	value, ok := strings.CutPrefix(args[0], installedHandoffArg)
	if !ok {
		return args, false, nil
	}
	raw, err := strconv.ParseUint(value, 10, 64)
	if err != nil || raw == 0 || raw == uint64(windows.InvalidHandle) {
		return nil, true, errors.New("invalid lock screen handoff handle")
	}
	parent := windows.Handle(raw)
	defer windows.CloseHandle(parent)
	result, err := windows.WaitForSingleObject(parent, 30_000)
	if err != nil {
		return nil, true, fmt.Errorf("wait for previous kkdesk process: %w", err)
	}
	if result != windows.WAIT_OBJECT_0 {
		return nil, true, errors.New("timed out waiting for previous kkdesk process")
	}
	return args[1:], true, nil
}
