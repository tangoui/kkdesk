//go:build !darwin && !windows

package app

import "mmdesk/internal/stats"

// PrepareProcess does nothing on this platform.
func PrepareProcess() {}

// CaptureHelp reports that this platform cannot share its screen.
func CaptureHelp() string { return "此平台暂不支持共享屏幕。" }

// InjectHelp reports that this platform cannot inject input.
func InjectHelp() string { return "此平台暂不支持远程输入。" }

// InjectLimits returns "".
func InjectLimits() string { return "" }

func captureExcludeApp(remote string) string { return "" }

func hostCPU() func() map[string]float64 { return stats.ProcessCPU() }
