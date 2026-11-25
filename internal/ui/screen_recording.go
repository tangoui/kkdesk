package appui

import (
	"context"
	"log"
	"os/exec"
	"runtime"
	"time"

	"github.com/tangoui/tango/ui"
	"github.com/tangoui/tango/ui/layout"

	"github.com/tangoui/kkdesk/internal/app"
)

type screenRecordingGuide struct {
	enabled bool
	missing bool
	open    bool
}

func (g *screenRecordingGuide) update(enabled bool, shown *bool, missing func() bool) {
	if g.enabled == enabled {
		return
	}
	g.enabled = enabled
	g.refresh(enabled && missing())
	if !enabled {
		*shown = false
	}
	if g.missing && !*shown {
		g.open = true
		*shown = true
	}
}

func (g *screenRecordingGuide) refresh(missing bool) {
	g.missing = missing
	if !missing {
		g.open = false
	}
}

func (g *screenRecordingGuide) entry(c *ui.Context) {
	if g.missing && ui.Button(c, "配置屏幕录制权限").Clicked() {
		g.open = true
		c.Invalidate()
	}
}

func (g *screenRecordingGuide) dialog(c *ui.Context, dev bool, openSettings func() error) {
	md := ui.Modal(c, &g.open, "需要屏幕录制权限").
		Centered().Width(min(460, c.Size().W-32)).MaskClosable(false).
		OkText("打开系统设置").CancelText("稍后").Body(func() {
		ui.Column(c, func() {
			ui.Text(c, "允许屏幕录制后，对方才能看到这台 Mac 的屏幕。")
			if dev {
				ui.Text(c, "请在「录屏与系统录音」中允许启动 tan dev 的终端应用（如 MinCode）。").Secondary()
			} else {
				ui.Text(c, "请在「录屏与系统录音」中开启 kkdesk 的权限。").Secondary()
			}
			ui.Text(c, "列表中没有对应应用时，点「＋」添加。").Secondary()
			ui.Text(c, "如果 macOS 提示退出并重新打开，请按提示操作。").Secondary()
		}, layout.Gap(8))
	})
	if !md.Ok() {
		return
	}
	if err := openSettings(); err != nil {
		log.Printf("kkdesk: open Screen Recording settings: %v", err)
		ui.Message(c, "error", "无法打开系统设置，请手动前往「隐私与安全性 › 录屏与系统录音」。")
		return
	}
	g.open = false
	c.Invalidate()
}

func screenRecordingMissing() bool {
	return runtime.GOOS == "darwin" && app.CaptureHelp() != ""
}

func openScreenRecordingSettings() error {
	const screenRecordingPane = "x-apple.systempreferences:com.apple.preference.security?Privacy_ScreenCapture"
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, "/usr/bin/open", screenRecordingPane).Run()
}
