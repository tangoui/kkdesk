//go:build windows

package app

import (
	"syscall"

	"mmdesk/internal/stats"
)

var (
	user32                            = syscall.NewLazyDLL("user32.dll")
	procSetProcessDpiAwarenessContext = user32.NewProc("SetProcessDpiAwarenessContext")
	procSetProcessDPIAware            = user32.NewProc("SetProcessDPIAware")
)

// dpiAwarenessContextPerMonitorAwareV2 is DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2 ((HANDLE)-4).
const dpiAwarenessContextPerMonitorAwareV2 = ^uintptr(3)

// PrepareProcess makes the process per-monitor DPI aware (V2) so that
// display sizes in the logs, GetSystemMetrics in the injector and the
// window are in physical pixels. Tango does the same when its app starts,
// but the host prints system information and -selftest runs before (or
// without) the Tango app; Tango's later call then fails harmlessly.
func PrepareProcess() {
	if procSetProcessDpiAwarenessContext.Find() == nil {
		procSetProcessDpiAwarenessContext.Call(dpiAwarenessContextPerMonitorAwareV2)
	} else if procSetProcessDPIAware.Find() == nil {
		procSetProcessDPIAware.Call()
	}
}

// CaptureHelp returns "": Windows needs no screen capture permission.
func CaptureHelp() string { return "" }

// InjectHelp returns "": SendInput needs no permission (see InjectLimits).
func InjectHelp() string { return "" }

// InjectLimits describes what SendInput cannot reach from a normal-rights
// process (UIPI); Windows drops such input silently.
func InjectLimits() string {
	return "远程输入限制（UIPI）：mmdesk 以普通权限运行，无法操作以管理员身份运行的窗口，也无法操作 UAC 提示、锁屏和 Ctrl+Alt+Del 界面（安全桌面），这些输入会被 Windows 静默丢弃。"
}

// captureExcludeApp returns "": hiding windows from Desktop Duplication is
// not supported.
func captureExcludeApp(remote string) string { return "" }

// hostCPU samples this process; Desktop Duplication and the encoder run
// in-process (plus dwm.exe and the GPU driver, not sampled).
func hostCPU() func() map[string]float64 { return stats.ProcessCPU() }
