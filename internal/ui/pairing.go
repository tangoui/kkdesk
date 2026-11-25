package appui

import (
	"fmt"
	"image"
	"strings"
	"time"

	qrcode "github.com/skip2/go-qrcode"
	"github.com/tangoui/tango/ui"
	"github.com/tangoui/tango/ui/icon/antd"
	"github.com/tangoui/tango/ui/layout"
	"github.com/tangoui/tango/ui/text"

	"github.com/tangoui/kkdesk/internal/app"
	"github.com/tangoui/kkdesk/internal/pairing"
)

type pairingUI struct {
	open           bool
	uri            string
	qr             image.Image
	pendingID      string
	bypass         bool
	rename, remove bool
	phoneKey, name string
}

func (m *Main) pairingQR(c *ui.Context) {
	p := &m.pairing
	now := time.Now()
	uri, expires, err := m.o.Host.PairingCode(now)
	if err == nil && uri != p.uri {
		var qr *qrcode.QRCode
		qr, err = qrcode.New(uri, qrcode.Medium)
		if err == nil {
			p.uri, p.qr = uri, qr.Image(320)
		}
	}
	if err == nil {
		c.WakeAt(minDeadline(now.Add(time.Second), expires))
	}
	ui.Modal(c, &p.open, "用手机扫码配对").Width(min(380, c.Size().W-32)).Footer(func() {
		ui.Row(c, func() {
			if ui.Button(c, "关闭").Clicked() {
				p.open = false
			}
		}, layout.JustifyContent(layout.JustifyEnd))
	}).Body(func() {
		if err != nil {
			ui.Alert(c, "warning", err.Error()).ShowIcon(true).Show()
			return
		}
		ui.Column(c, func() {
			ui.Text(c, "打开 kkdesk App，点右上角的扫码按钮").Secondary()
			size := max(120, min(264, c.Size().W-86))
			ui.Image(c, p.qr).Size(size, size)
			left := ((time.Until(expires) + time.Second - 1) / time.Second) * time.Second
			ui.Text(c, formatElapsed(max(0, left))+" 后自动刷新").Size(13).Tertiary()
		}, layout.Gap(12), layout.Middle(), layout.Padding(insets(8, 0, 0, 0)))
	})
	if !p.open {
		p.uri, p.qr = "", nil
		m.o.Host.ClearPairingCode()
	}
}

func minDeadline(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

func (m *Main) pairingConfirm(c *ui.Context, req *app.PairingRequest) {
	p := &m.pairing
	if p.pendingID != req.ID {
		p.pendingID, p.bypass = req.ID, false
		p.open, p.uri, p.qr = false, "", nil
	}
	now := time.Now()
	c.WakeAt(minDeadline(now.Add(time.Second), req.Deadline))
	left := max(0, int((time.Until(req.Deadline)+time.Second-1)/time.Second))
	open := true
	var accept, reject bool
	md := ui.Modal(c, &open, "手机配对请求").Width(min(420, c.Size().W-32)).MaskClosable(false).Header(func() {
		ui.Row(c, func() {
			ui.Avatar(c, "").Icon(antd.MobileOutlined).Size(44).Bg(c.Theme.Primary.Bg)
			ui.Column(c, func() {
				ui.Text(c, req.Name+" 请求配对").Size(16).Strong().Ellipsis()
				if req.Model != "" && req.Model != req.Name {
					ui.Text(c, req.Model).Size(12).Tertiary()
				}
			}, layout.Gap(2), layout.Grow(1), layout.MinW(0))
		}, layout.Gap(12), layout.Middle())
	}).Footer(func() {
		ui.Row(c, func() {
			reject = ui.Button(c, "拒绝").AutoInsertSpace(false).Clicked()
			accept = ui.Button(c, fmt.Sprintf("允许配对（%d）", left)).Primary().Clicked()
		}, layout.Gap(8), layout.JustifyContent(layout.JustifyEnd))
	}).Body(func() {
		ui.Column(c, func() {
			ui.Row(c, func() {
				ui.Text(c, "核对码").Secondary()
				mono(c, req.Code, 24, text.Semibold, c.Theme.Primary.Base)
			}, layout.Gap(12), layout.Middle())
			ui.Text(c, "核对手机上的四位数字后，再允许配对。").Secondary()
			ui.Checkbox(c, "以后这台手机连接时无需确认", &p.bypass)
			ui.Alert(c, "warning", "配对后，这台手机可以请求连接本机。").ShowIcon(true).Show()
		}, layout.Gap(12))
	})
	if accept || reject || md.Cancelled() || !open {
		m.o.Host.DecidePairing(req.ID, accept, p.bypass)
	}
}

func (m *Main) pairedPhones(c *ui.Context) {
	p := &m.pairing
	phones := m.o.Host.PairedPhones()
	if len(phones) == 0 {
		ui.Text(c, "暂无已配对的手机").Secondary()
		return
	}
	ui.Column(c, func() {
		for _, phone := range phones {
			ui.Key(c, phone.PublicKey, func() {
				ui.Column(c, func() {
					ui.Text(c, phone.Name).Strong()
					ui.Row(c, func() {
						on := phone.Bypass
						switchRow(c, &on, "无需确认", func(on bool) {
							phoneError(c, m.o.Host.UpdatePhone(phone.PublicKey, func(p *pairing.Phone) { p.Bypass = on }))
						})
						if ui.Button(c, "改名").Small().Clicked() {
							p.rename, p.phoneKey, p.name = true, phone.PublicKey, phone.Name
						}
						if ui.Button(c, "解除配对").Small().Danger().Clicked() {
							p.remove, p.phoneKey, p.name = true, phone.PublicKey, phone.Name
						}
					}, layout.Gap(8), layout.WrapItems(), layout.Middle())
				}, layout.Gap(6))
			})
		}
	}, layout.Gap(16))
}

func phoneError(c *ui.Context, err error) {
	if err != nil {
		ui.Message(c, "error", err.Error())
	}
	c.Invalidate()
}

func (m *Main) phoneSettingsDialog(c *ui.Context) {
	p := &m.pairing
	width := min(400, c.Size().W-32)
	if p.rename {
		var submit bool
		d := ui.Modal(c, &p.rename, "重命名手机").Width(width).OkText("保存").Body(func() {
			submit = ui.Input(c, &p.name).A11yLabel("手机名称").Submitted()
		})
		if d.Ok() || submit {
			name := strings.TrimSpace(p.name)
			if name == "" || len(name) > 256 {
				p.rename = true
				ui.Message(c, "error", "请输入有效的手机名称")
				return
			}
			phoneError(c, m.o.Host.UpdatePhone(p.phoneKey, func(p *pairing.Phone) { p.Name = name }))
			p.rename = false
		}
	} else if p.remove {
		d := ui.Modal(c, &p.remove, "解除手机配对？").Width(width).OkText("确认解除").Danger().Body(func() {
			ui.Text(c, "「"+p.name+"」需要重新扫码才能连接本机。")
		})
		if d.Ok() {
			p.remove = false
			phoneError(c, m.o.Host.RemovePhone(p.phoneKey))
		}
	}
}
