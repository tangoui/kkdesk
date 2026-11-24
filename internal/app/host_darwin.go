//go:build darwin

package app

import (
	"os"

	"github.com/tangoui/kkdesk/internal/capture"
	"github.com/tangoui/kkdesk/internal/inject"
	"github.com/tangoui/kkdesk/internal/stats"
)

// Guidance texts for missing macOS permissions.
const (
	ScreenRecordingHelp = "需要屏幕录制权限：请在 系统设置 › 隐私与安全性 › 屏幕录制 中允许 kkdesk（或运行它的终端），然后重新启动 kkdesk。"
	AccessibilityHelp   = "需要辅助功能权限才能被远程操作：请在 系统设置 › 隐私与安全性 › 辅助功能 中允许 kkdesk（或运行它的终端）。"
)

// PrepareProcess sets process-wide state before anything else runs. Nothing
// to do on macOS.
func PrepareProcess() {}

// CaptureHelp returns guidance when screen capture is not permitted, "" when
// it is.
func CaptureHelp() string {
	if capture.HasPermission() {
		return ""
	}
	return ScreenRecordingHelp
}

// InjectHelp returns guidance when remote input cannot be injected, ""
// when it can.
func InjectHelp() string {
	if inject.HasPermission() {
		return ""
	}
	return AccessibilityHelp
}

// InjectLimits describes what injection cannot reach even when permitted.
func InjectLimits() string { return "" }

// captureExcludeApp returns the application whose windows capture hides
// for a controller at remote: kkdesk itself when the controller runs on
// this machine (see excludeSelfWindows).
func captureExcludeApp(remote string) string {
	if excludeSelfWindows(remote, LocalIPv4(), os.Getenv("KKDESK_EXCLUDE_SELF")) {
		return ExcludeAppName()
	}
	return ""
}

// hostCPU samples this process and the system processes that do the
// capture compositing and hardware encoding.
func hostCPU() func() map[string]float64 {
	return stats.ProcessCPU("VTEncoderXPCService", "WindowServer")
}
