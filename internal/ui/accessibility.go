package appui

import (
	"context"
	"log"
	"os/exec"
	"runtime"
	"time"

	"github.com/tangoui/tango/ui"
	"github.com/tangoui/tango/ui/layout"

	"github.com/tangoui/kkdesk/internal/inject"
)

type accessibilityGuide struct {
	missing bool
	open    bool
	next    time.Time
}

func (g *accessibilityGuide) refresh(granted bool) {
	g.missing = !granted
	if granted {
		g.open = false
	}
}

func (g *accessibilityGuide) entry(c *ui.Context) {
	if g.missing && ui.Button(c, "配置辅助功能权限").Small().Clicked() {
		g.open = true
		c.Invalidate()
	}
}

func (g *accessibilityGuide) dialog(c *ui.Context, dev bool, check func() bool, openSettings func() error) {
	ui.Modal(c, &g.open, "需要辅助功能权限").
		Centered().Width(min(480, c.Size().W-32)).MaskClosable(false).
		Footer(func() {
			if ui.Button(c, "我已授权，重新检测").Clicked() {
				g.refresh(check())
				if g.missing {
					ui.Message(c, "info", "仍未检测到辅助功能权限。已开启权限后如仍无效，请重启 kkdesk。")
				}
				c.Invalidate()
			}
			if ui.Button(c, "打开系统设置").Primary().Clicked() {
				if err := openSettings(); err != nil {
					log.Printf("kkdesk: open Accessibility settings: %v", err)
					ui.Message(c, "error", "无法打开系统设置，请手动前往「隐私与安全性 › 辅助功能」。")
				}
			}
		}).Body(func() {
		ui.Column(c, func() {
			ui.Text(c, "允许辅助功能后，对方才能用鼠标和键盘控制这台 Mac；不授权时对方只能看画面。")
			if dev {
				ui.Text(c, "开发模式下，请给启动 tan dev 的终端应用开启权限。").Secondary()
			} else {
				ui.Text(c, "请在「隐私与安全性 › 辅助功能」中开启 kkdesk。列表里没有 kkdesk 时，点「＋」添加。").Secondary()
			}
			ui.Text(c, "临时签名的版本更新后可能需要重新授权。如果列表里已有 kkdesk 但权限仍无效，点「−」删除旧条目，再重新添加并重启 kkdesk。").Secondary()
		}, layout.Gap(8))
	})
}

func openAccessibilitySettings() error {
	inject.RequestPermission()
	const pane = "x-apple.systempreferences:com.apple.preference.security?Privacy_Accessibility"
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, "/usr/bin/open", pane).Run()
}

func (m *Main) refreshAccessibility(c *ui.Context) {
	if runtime.GOOS != "darwin" {
		return
	}
	visible := !m.o.Hidden
	if s := m.surface.Load(); s != nil {
		visible = s.IsVisible()
	}
	if !visible {
		m.accessibility.next = time.Time{}
		return
	}
	now := time.Now()
	if !now.Before(m.accessibility.next) {
		m.accessibility.refresh(inject.HasPermission())
		m.accessibility.next = now.Add(2 * time.Second)
	}
	c.WakeAt(m.accessibility.next)
}

func (m *Main) permissions(c *ui.Context) {
	if runtime.GOOS != "darwin" {
		return
	}
	ui.Column(c, func() {
		if m.screenRecording.enabled {
			permissionRow(c, "屏幕录制", m.screenRecording.missing, func() { m.screenRecording.entry(c) })
		}
		permissionRow(c, "辅助功能", m.accessibility.missing, func() { m.accessibility.entry(c) })
	}, layout.Gap(6))
}

func permissionRow(c *ui.Context, name string, missing bool, entry func()) {
	status, color := "已授权", c.Theme.Success.Base
	if missing {
		status, color = "未授权", c.Theme.Warning.Base
	}
	ui.Row(c, func() {
		dot(c, color, 6)
		ui.Text(c, name+" "+status).Size(12).Secondary()
		if missing {
			entry()
		}
	}, layout.Gap(8), layout.Middle(), layout.WrapItems())
}
