package appui

import (
	"errors"
	"fmt"
	"strings"

	"github.com/tangoui/tango/ui"
	"github.com/tangoui/tango/ui/icon"
	"github.com/tangoui/tango/ui/icon/antd"
	"github.com/tangoui/tango/ui/layout"
	"github.com/tangoui/tango/ui/text"

	"github.com/tangoui/kkdesk/internal/app"
	"github.com/tangoui/kkdesk/internal/config"
)

// devicesState is the 我的设备 page: filters and the open dialog.
type devicesState struct {
	search string
	filter string // "all" (default), "online", "offline"

	addOpen                    bool
	addName, addAddr, addGroup string
	addErr                     string

	groupOpen bool
	groupName string
	groupErr  string

	renameOpen bool
	renameID   string
	renameName string

	moveOpen  bool
	moveID    string
	moveGroup string

	delOpen bool
	delID   string

	pwOpen bool
	pwID   string
	pwMode string
	pw     string // never saved
}

var devFilterOpts = []ui.Option{
	{Label: "全部", Value: "all"},
	{Label: "在线", Value: "online"},
	{Label: "离线", Value: "offline"},
}

// devRow is one table row: a group heading (dev nil) or a device.
type devRow struct {
	group string
	count int
	dev   config.Device
	head  bool
}

// syncProbeTargets makes the prober follow the address book. Any goroutine.
func (m *Main) syncProbeTargets() {
	if m.prober == nil {
		return
	}
	var addrs []string
	for _, d := range m.o.Store.Devices() {
		addrs = append(addrs, d.Addr)
	}
	m.prober.SetTargets(addrs)
}

// status of an entry.
func (m *Main) devStatus(d config.Device) app.ProbeStatus {
	if m.prober == nil {
		return app.ProbeUnknown
	}
	return m.prober.Status(d.Addr)
}

// devicesPage is 我的设备: the address book by group, with the online state,
// the three ways to connect and the edit actions.
func (m *Main) devicesPage(c *ui.Context) {
	s := &m.dev
	if s.filter == "" {
		s.filter = "all"
	}
	devs := m.o.Store.Devices()
	online := 0
	for _, d := range devs {
		if m.devStatus(d) == app.ProbeOnline {
			online++
		}
	}
	ui.Row(c, func() {
		ui.Title(c, "我的设备", 4)
		if len(devs) > 0 {
			ui.Text(c, fmt.Sprintf("%d 台，%d 台在线", len(devs), online)).Size(13).Tertiary().NoWrap()
		}
		ui.Spacer(c)
		if ui.Button(c, "新建分组").Icon(antd.FolderAddOutlined).Clicked() {
			s.groupOpen, s.groupName, s.groupErr = true, "", ""
		}
		if ui.Button(c, "添加设备").Primary().Icon(icon.Plus).Clicked() {
			s.addOpen, s.addName, s.addAddr, s.addGroup, s.addErr = true, "", "", config.DefaultGroup, ""
		}
	}, layout.Gap(12), layout.Middle())
	m.connFeedback(c, "devices")

	if len(devs) == 0 {
		panel(c, func() {
			ui.Empty(c).Title("还没有设备").Description("连接成功的电脑会自动出现在这里，也可以点「添加设备」手动添加。")
			ui.Row(c, func() {
				if ui.Button(c, "去连接").Clicked() {
					m.nav = "connect"
					c.Invalidate()
				}
			}, layout.JustifyContent(layout.JustifyCenter))
		}, layout.Pad(48), layout.Gap(16))
	} else {
		ui.Row(c, func() {
			ui.Search(c, &s.search).Placeholder("搜索名称、地址或备注").AllowClear().Width(260).A11yLabel("搜索设备")
			ui.Segmented(c, &s.filter, devFilterOpts).A11yLabel("筛选")
		}, layout.Gap(12), layout.Middle(), layout.WrapItems())
		m.devicesTable(c, devs)
	}
	m.devDialogs(c)
}

// visibleRows groups the entries that pass the search and filter.
func (m *Main) visibleRows(devs []config.Device) []devRow {
	s := &m.dev
	q := strings.ToLower(strings.TrimSpace(s.search))
	var rows []devRow
	for _, g := range m.o.Store.Groups() {
		var in []config.Device
		for _, d := range devs {
			if d.Group != g {
				continue
			}
			if q != "" && !strings.Contains(strings.ToLower(d.Name+" "+d.Addr+" "+d.Note), q) {
				continue
			}
			st := m.devStatus(d)
			if s.filter == "online" && st != app.ProbeOnline || s.filter == "offline" && st != app.ProbeOffline {
				continue
			}
			in = append(in, d)
		}
		if len(in) == 0 {
			continue
		}
		rows = append(rows, devRow{group: g, count: len(in), head: true})
		for _, d := range in {
			rows = append(rows, devRow{group: g, dev: d})
		}
	}
	return rows
}

func (m *Main) devicesTable(c *ui.Context, devs []config.Device) {
	th := c.Theme
	rows := m.visibleRows(devs)
	if len(rows) == 0 {
		panel(c, func() {
			ui.Empty(c).Simple().Description("没有符合条件的设备")
		}, layout.Pad(24))
		return
	}
	cols := []ui.TableColumn{
		{Key: "name", Title: "设备", MinWidth: 180, Render: func(i int) {
			r := rows[i]
			if r.head {
				ui.Text(c, fmt.Sprintf("%s · %d", r.group, r.count)).Size(13).Strong().Secondary()
				return
			}
			ui.Row(c, func() {
				ui.Row(c, func() {
					ui.Icon(c, antd.DesktopOutlined).Size(16).Color(th.Primary.Base)
				}, layout.Size(32, 32), layout.Center()).Bg(th.Primary.Bg).Radius(th.RadiusSM)
				ui.Column(c, func() {
					ui.Text(c, r.dev.Name).Weight(text.Weight(500)).Ellipsis()
					if r.dev.Note != "" {
						ui.Text(c, r.dev.Note).Size(12).Tertiary().Ellipsis()
					}
				}, layout.Grow(1), layout.MinW(0))
			}, layout.Gap(10), layout.Middle())
		}},
		{Key: "addr", Title: "地址", Width: 190, Render: func(i int) {
			if !rows[i].head {
				mono(c, config.DisplayAddr(rows[i].dev.Addr), 13, text.Regular, th.Text)
			}
		}},
		{Key: "status", Title: "状态", Width: 100, Render: func(i int) {
			if rows[i].head {
				return
			}
			switch m.devStatus(rows[i].dev) {
			case app.ProbeOnline:
				ui.Badge(c).Status("success", "在线")
			case app.ProbeOffline:
				ui.Badge(c).Status("default", "离线")
			default:
				ui.Badge(c).Status("processing", "探测中")
			}
		}},
		{Key: "last", Title: "最近连接", Width: 130, Text: func(i int) string {
			if rows[i].head {
				return ""
			}
			if t := rows[i].dev.LastConnected; !t.IsZero() {
				return whenText(t)
			}
			return "从未连接"
		}},
		{Key: "actions", Title: "操作", Width: 300, Render: func(i int) {
			if !rows[i].head {
				m.devActions(c, rows[i].dev)
			}
		}},
	}
	ui.Table(c, cols, len(rows)).
		RowKey(func(i int) string {
			if rows[i].head {
				return "g:" + rows[i].group
			}
			return rows[i].dev.ID
		}).
		Span(func(sp ui.TableSpan) (int, int) {
			if rows[sp.Row].head {
				if sp.Col == 0 {
					return 1, len(cols)
				}
				return 1, 0
			}
			return 1, 1
		}).
		RowClass(func(i int) ui.RowStyle {
			if rows[i].head {
				return ui.RowStyle{Bg: th.FillQuaternary}
			}
			return ui.RowStyle{}
		}).
		NoHover().A11yLabel("我的设备").Show()
}

// devActions are the buttons of one row.
func (m *Main) devActions(c *ui.Context, d config.Device) {
	s := &m.dev
	ask := func(mode string) {
		s.pwOpen, s.pwID, s.pwMode, s.pw = true, d.ID, mode, ""
	}
	ui.Row(c, func() {
		if ui.Button(c, "连接").Primary().Small().Clicked() {
			ask(modeControl)
		}
		if ui.Button(c, "仅查看").Small().Clicked() {
			ask(modeView)
		}
		if ui.Button(c, "传文件").Small().Clicked() {
			ask(modeTransfer)
		}
		items := []ui.MenuItem{
			{Key: "rename", Label: "改名", Icon: icon.Edit},
			{Key: "move", Label: "移动到分组", Icon: antd.FolderOutlined},
			{Divider: true},
			{Key: "delete", Label: "删除", Icon: icon.Delete, Danger: true},
		}
		k := ui.Dropdown(c, items).Trigger(ui.TriggerClick).Placement(ui.BottomEnd).Body(func() {
			ui.IconButton(c, icon.More, "更多操作 "+d.Name).Size(24, 16)
		}).Clicked()
		switch k {
		case "rename":
			s.renameOpen, s.renameID, s.renameName = true, d.ID, d.Name
		case "move":
			s.moveOpen, s.moveID, s.moveGroup = true, d.ID, d.Group
		case "delete":
			s.delOpen, s.delID = true, d.ID
		}
	}, layout.Gap(6), layout.Middle())
}

// connFeedback shows the state of a connection started from page.
func (m *Main) connFeedback(c *ui.Context, page string) {
	m.mu.Lock()
	connecting, errMsg, from := m.connecting, m.connErr, m.connFrom
	m.mu.Unlock()
	if from != page {
		return
	}
	switch {
	case connecting:
		ui.Alert(c, "info", "正在连接，等待对方确认…").ShowIcon(true).Show()
	case errMsg != "":
		if ui.Alert(c, "error", errMsg).ShowIcon(true).Closable().Show().Closed() {
			m.mu.Lock()
			m.connErr = ""
			m.mu.Unlock()
		}
	}
}

// storeErr reports a failed save as a toast; the change stays in memory.
func storeErr(c *ui.Context, err error) {
	if err != nil {
		ui.Message(c, "error", userErr(err))
	}
}

// userErr is an address book error in words.
func userErr(err error) string {
	switch {
	case errors.Is(err, config.ErrInvalidAddr):
		return "地址无效"
	case errors.Is(err, config.ErrNotFound):
		return "设备已被删除"
	case errors.Is(err, config.ErrEmptyName):
		return "名称不能为空"
	case config.IsSaveError(err):
		return "无法保存配置：" + err.Error()
	case errors.Is(err, config.ErrDuplicate):
		msg := err.Error()
		if i := strings.LastIndex(msg, ": "); i > 0 {
			msg = msg[:i]
		}
		return msg
	}
	return err.Error()
}

func (m *Main) groupOpts() []ui.Option {
	var opts []ui.Option
	for _, g := range m.o.Store.Groups() {
		opts = append(opts, ui.Option{Label: g, Value: g})
	}
	return opts
}

// devDialogs are the dialogs of the page (one is open at a time).
func (m *Main) devDialogs(c *ui.Context) {
	s := &m.dev
	st := m.o.Store
	width := min(440, c.Size().W-32)

	if s.addOpen {
		md := ui.Modal(c, &s.addOpen, "添加设备").Width(width).OkText("添加")
		var submit bool
		md.Body(func() {
			f := ui.Form(c).Vertical()
			f.Body(func() {
				it := f.Item("地址").Required()
				if s.addErr != "" {
					it.Error(s.addErr)
				}
				it.Body(func() {
					submit = ui.Input(c, &s.addAddr).Placeholder("例如 192.168.1.20 或 192.168.1.20:21118").A11yLabel("设备地址").Submitted()
				})
				f.Item("名称").Help("留空时用地址作名称").Body(func() {
					ui.Input(c, &s.addName).Placeholder("例如 办公室台式机").A11yLabel("设备名称")
				})
				f.Item("分组").Body(func() {
					ui.Select(c, &s.addGroup, m.groupOpts()).A11yLabel("分组")
				})
			})
		})
		if md.Ok() || submit {
			if _, err := st.AddDevice(s.addName, s.addAddr, s.addGroup); err != nil && !isSaveErr(err) {
				s.addErr = userErr(err)
			} else {
				storeErr(c, err)
				s.addOpen = false
				m.syncProbeTargets()
				ui.Message(c, "success", "已添加")
			}
			c.Invalidate()
		}
	}

	if s.groupOpen {
		md := ui.Modal(c, &s.groupOpen, "新建分组").Width(width).OkText("创建")
		var submit bool
		md.Body(func() {
			f := ui.Form(c).Vertical()
			f.Body(func() {
				it := f.Item("分组名称").Required()
				if s.groupErr != "" {
					it.Error(s.groupErr)
				}
				it.Body(func() {
					submit = ui.Input(c, &s.groupName).Placeholder("例如 公司机房").A11yLabel("分组名称").Submitted()
				})
			})
		})
		if md.Ok() || submit {
			if err := st.AddGroup(s.groupName); err != nil && !isSaveErr(err) {
				s.groupErr = userErr(err)
			} else {
				storeErr(c, err)
				s.groupOpen = false
			}
			c.Invalidate()
		}
	}

	if s.renameOpen {
		md := ui.Modal(c, &s.renameOpen, "改名").Width(width).OkText("保存")
		var submit bool
		md.Body(func() {
			f := ui.Form(c).Vertical()
			f.Body(func() {
				f.Item("名称").Help("留空时用地址作名称").Body(func() {
					submit = ui.Input(c, &s.renameName).A11yLabel("新名称").Submitted()
				})
			})
		})
		if md.Ok() || submit {
			storeErr(c, st.UpdateDevice(s.renameID, func(d *config.Device) { d.Name = s.renameName }))
			s.renameOpen = false
			c.Invalidate()
		}
	}

	if s.moveOpen {
		md := ui.Modal(c, &s.moveOpen, "移动到分组").Width(width).OkText("移动")
		md.Body(func() {
			f := ui.Form(c).Vertical()
			f.Body(func() {
				f.Item("分组").Help("需要新的分组时先点「新建分组」").Body(func() {
					ui.Select(c, &s.moveGroup, m.groupOpts()).A11yLabel("目标分组")
				})
			})
		})
		if md.Ok() {
			storeErr(c, st.UpdateDevice(s.moveID, func(d *config.Device) { d.Group = s.moveGroup }))
			s.moveOpen = false
			c.Invalidate()
		}
	}

	if s.delOpen {
		d, _ := st.Device(s.delID)
		md := ui.Modal(c, &s.delOpen, "删除设备").Width(width).OkText("删除").Danger()
		md.Body(func() {
			ui.Text(c, fmt.Sprintf("确定从我的设备中删除「%s」（%s）吗？删除后可以再次添加。", d.Name, config.DisplayAddr(d.Addr)))
		})
		if md.Ok() {
			if err := st.DeleteDevice(s.delID); err != nil && !isSaveErr(err) {
				ui.Message(c, "error", userErr(err))
			} else {
				storeErr(c, err)
				ui.Message(c, "success", "已删除")
			}
			s.delOpen = false
			m.syncProbeTargets()
			c.Invalidate()
		}
	}

	if s.pwOpen {
		d, ok := st.Device(s.pwID)
		verb := map[string]string{modeControl: "连接", modeView: "仅查看", modeTransfer: "传文件"}[s.pwMode]
		md := ui.Modal(c, &s.pwOpen, verb+" "+d.Name).Width(width).OkText(verb)
		var submit bool
		md.Body(func() {
			f := ui.Form(c).Vertical()
			f.Body(func() {
				f.Item("密码").Help("对方的临时密码或固定密码，不会保存").Body(func() {
					in := ui.Input(c, &s.pw).Password().Placeholder("输入密码").A11yLabel("连接密码")
					submit = in.Submitted()
				})
			})
			ui.Text(c, "地址 "+config.DisplayAddr(d.Addr)).Size(12).Tertiary()
		})
		if (md.Ok() || submit) && ok {
			pw := strings.TrimSpace(s.pw)
			s.pw, s.pwOpen = "", false
			m.connect(d.Addr, pw, s.pwMode)
			c.Invalidate()
		}
		if !s.pwOpen {
			s.pw = ""
		}
	}
}

// isSaveErr reports an error of writing the file (the change was made in
// memory) as opposed to a refused change.
func isSaveErr(err error) bool { return config.IsSaveError(err) }
