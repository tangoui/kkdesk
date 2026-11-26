package appui

import (
	"context"
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tangoui/tango"
	"github.com/tangoui/tango/ui"
	"github.com/tangoui/tango/ui/a11y"
	"github.com/tangoui/tango/ui/icon"
	"github.com/tangoui/tango/ui/icon/antd"
	tin "github.com/tangoui/tango/ui/input"
	"github.com/tangoui/tango/ui/layout"
	"github.com/tangoui/tango/ui/text"
	"github.com/tangoui/tango/uihost"

	"github.com/tangoui/kkdesk/internal/app"
	"github.com/tangoui/kkdesk/internal/files"
)

// fileRowH is the height of one file row (the prototype's .file: 7px
// padding around a 13px line).
const fileRowH = 34

// homeTimeout bounds the first request: the host answers it only after its
// user accepted the connection.
const homeTimeout = app.ConfirmTimeout + 15*time.Second

// fileWins holds the open 文件传输 window of each session.
var fileWins sync.Map // *app.Viewer → *filesWin

// filesWin is the 文件传输 window of one session: this machine on the left,
// the peer on the right, the transfer queue below (prototype 文件传输 page).
type filesWin struct {
	app     *tango.App
	v       *app.Viewer
	cl      *files.Client
	own     bool // 传文件 mode: the window is the session, closing it ends it
	ctx     context.Context
	cancel  context.CancelFunc
	runner  atomic.Pointer[ui.Runner]
	surface atomic.Pointer[tango.Surface]
	closing atomic.Bool // close confirmed or session over: do not ask

	// mu guards everything below; the page holds it while it builds and
	// the background goroutines (listings, transfers) take it briefly.
	mu       sync.Mutex
	local    *filePane
	remote   *filePane
	active   *filePane // last pane used: where 新建文件夹 creates
	jobs     []*job
	nextJob  int
	working  bool // the queue goroutine runs
	toasts   []toast
	ready    bool   // the host answered (file access granted)
	connErr  string // the host could not be reached for files
	askClose bool   // close requested while transfers run
	mkdir    bool   // 新建文件夹 dialog open
	mkName   string
}

type toast struct{ status, text string }

// filePane is one side of the window.
type filePane struct {
	remote    bool
	title     string
	sep       string // path separator of that side
	path      string // directory listed
	edit      string // path field
	parent    string // "" at a volume root
	entries   []files.Entry
	truncated bool
	sel       map[string]bool // selected names
	anchor    int             // Shift range anchor (row index), -1 none
	loading   bool
	err       string // listing failed (shown when nothing is listed)
	gen       int    // listing request generation
	scrollTop bool   // scroll to the top on the next frame
}

// OpenFiles opens (or brings up) the 文件传输 window of a connected
// session. own makes the window own the session (传文件 mode): closing it
// ends the session, and onEnd gets the end reason ("" for a normal close)
// after the window closed. Call on the main thread.
func OpenFiles(a *tango.App, v *app.Viewer, own bool, onEnd func(msg string)) error {
	if old, ok := fileWins.Load(v); ok {
		if s := old.(*filesWin).surface.Load(); s != nil {
			s.Show()
			s.Focus()
		}
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	w := &filesWin{app: a, v: v, own: own, ctx: ctx, cancel: cancel,
		local:  &filePane{title: "本机", sep: string(filepath.Separator), anchor: -1, sel: map[string]bool{}},
		remote: &filePane{remote: true, title: peerName(v.Addr()), sep: "/", anchor: -1, sel: map[string]bool{}}}
	w.active = w.local
	r, s, err := uihost.Open(a, uihost.Options{Theme: themeMode(), Surface: tango.SurfaceOptions{
		Title:     "文件传输 - " + v.Addr(),
		Width:     1120,
		Height:    780,
		MinWidth:  760,
		MinHeight: 520,
		Resizable: true,
		Centered:  true,
	}}, w.page, ui.RunnerOptions{})
	if err != nil {
		cancel()
		return err
	}
	w.runner.Store(r)
	w.surface.Store(s)
	fileWins.Store(v, w)
	v.AddNotify(r.Invalidate)

	var once sync.Once
	finish := func() {
		once.Do(func() {
			if onEnd != nil {
				onEnd(v.State().Err)
			}
		})
	}
	s.OnClose(func() bool {
		if w.closing.Load() {
			return true
		}
		w.mu.Lock()
		busy := w.busyLocked()
		if busy {
			w.askClose = true
		}
		w.mu.Unlock()
		if busy {
			w.invalidate()
			return false
		}
		return true
	})
	s.OnClosed(func() {
		w.closing.Store(true)
		w.mu.Lock()
		w.cancelAllLocked()
		w.mu.Unlock()
		w.cancel()
		fileWins.Delete(v)
		if own {
			v.Close()
			v.OnEnd(func() { a.Post(finish) })
		}
	})
	v.OnEnd(func() {
		w.invalidate()
		if !w.closing.Swap(true) {
			time.AfterFunc(closeDelay, func() { a.Post(s.Close) })
		}
	})

	w.cl = v.Files()
	w.mu.Lock()
	w.loadLocked(w.local, files.LocalHome().Dir)
	if w.cl == nil {
		w.connErr = "对方不支持文件传输"
	}
	w.mu.Unlock()
	if w.cl != nil {
		go w.start()
	}
	return nil
}

// peerName is how the window names the peer (its address without the
// default port).
func peerName(addr string) string {
	return strings.TrimSuffix(addr, fmt.Sprintf(":%d", app.Port))
}

func (w *filesWin) invalidate() {
	if r := w.runner.Load(); r != nil {
		r.Invalidate()
	}
}

// start asks the host for its home folder (and, implicitly, whether file
// access is granted) and lists it.
func (w *filesWin) start() {
	ctx, cancel := context.WithTimeout(w.ctx, homeTimeout)
	defer cancel()
	h, err := w.cl.Home(ctx)
	w.mu.Lock()
	defer w.mu.Unlock()
	defer w.invalidate()
	if err != nil {
		if w.ctx.Err() == nil {
			w.connErr = errText(err)
			w.toastLocked("error", w.connErr)
		}
		return
	}
	w.ready, w.connErr = true, ""
	if h.Sep != "" {
		w.remote.sep = h.Sep
	}
	w.loadLocked(w.remote, h.Dir)
}

func (w *filesWin) toastLocked(status, msg string) {
	w.toasts = append(w.toasts, toast{status, msg})
	w.invalidate()
}

// loadLocked lists dir into p in the background. On failure the previous
// listing stays and the error is shown as a message.
func (w *filesWin) loadLocked(p *filePane, dir string) {
	p.gen++
	gen := p.gen
	p.loading = true
	go func() {
		var l files.Listing
		var err error
		if p.remote {
			ctx, cancel := context.WithTimeout(w.ctx, 30*time.Second)
			l, err = w.cl.List(ctx, dir)
			cancel()
		} else {
			l, err = files.ListLocal(dir)
		}
		w.mu.Lock()
		defer w.mu.Unlock()
		defer w.invalidate()
		if p.gen != gen {
			return
		}
		p.loading = false
		if err != nil {
			if w.ctx.Err() != nil {
				return
			}
			msg := errText(err)
			p.edit = p.path
			if p.path == "" {
				p.err = msg
			}
			w.toastLocked("error", "无法打开 "+dir+"："+msg)
			return
		}
		if l.Path != p.path {
			p.sel, p.anchor, p.scrollTop = map[string]bool{}, -1, true
		} else { // refresh: keep the selection that still exists
			keep := map[string]bool{}
			for _, e := range l.Entries {
				if p.sel[e.Name] {
					keep[e.Name] = true
				}
			}
			p.sel = keep
			p.anchor = min(p.anchor, len(l.Entries)-1)
		}
		p.path, p.edit, p.parent, p.err = l.Path, l.Path, l.Parent, ""
		p.entries, p.truncated = l.Entries, l.Truncated
	}()
}

// selected returns the selected entries in listing order.
func (p *filePane) selected() []files.Entry {
	var out []files.Entry
	for _, e := range p.entries {
		if p.sel[e.Name] {
			out = append(out, e)
		}
	}
	return out
}

func (w *filesWin) page(c *ui.Context) {
	w.mu.Lock()
	defer w.mu.Unlock()
	th := c.Theme
	for _, t := range w.toasts {
		ui.Message(c, t.status, t.text)
	}
	w.toasts = nil
	ui.Column(c, func() {
		w.toolbar(c)
		ui.Column(c, func() {
			ui.Row(c, func() {
				w.pane(c, w.local)
				w.pane(c, w.remote)
			}, layout.Gap(12), layout.Grow(1), layout.MinH(0), layout.AlignItems(layout.AlignStretch))
			w.queue(c)
		}, layout.Grow(1), layout.MinH(0), layout.Gap(16), layout.Padding(insets(16, 20, 20, 20)))
	}, layout.Grow(1)).Bg(th.BgLayout)
	w.mkdirDialog(c)
	w.closeDialog(c)
}

// toolbar is the prototype's content head: title, peer and actions.
func (w *filesWin) toolbar(c *ui.Context) {
	th := c.Theme
	st := w.v.State()
	up := w.ready && len(w.local.sel) > 0 && w.remote.path != ""
	down := w.ready && len(w.remote.sel) > 0 && w.local.path != ""
	ui.Row(c, func() {
		ui.Text(c, "文件传输").Size(16).Strong().NoWrap()
		switch {
		case st.Phase == app.ViewerClosed:
			msg := st.Err
			if msg == "" {
				msg = "连接已断开"
			}
			ui.Tag(c, msg).Color("error")
		case w.connErr != "":
			ui.Tag(c, w.remote.title+" · "+w.connErr).Color("error")
		case !w.ready:
			ui.Tag(c, "等待 "+w.remote.title+" 确认…").Color("processing")
		default:
			ui.Row(c, func() {
				dot(c, th.Success.Base, 6)
				ui.Text(c, w.remote.title).Size(12).Color(th.Success.Text).NoWrap()
			}, layout.Gap(6), layout.Middle(), layout.PadVH(1, 8)).
				Bg(th.Success.Bg).Border(1, th.Success.Border).Radius(th.RadiusSM)
		}
		if st.FirstHostCode != "" && st.Phase != app.ViewerClosed {
			ui.Tag(c, "对方设备码 "+st.FirstHostCode).Color("blue")
		}
		ui.Spacer(c)
		if ui.Button(c, "上传 →").AutoInsertSpace(false).Disabled(!up).Clicked() {
			w.enqueueLocked(true, w.local.selected(), w.remote.path)
		}
		if ui.Button(c, "← 下载").AutoInsertSpace(false).Disabled(!down).Clicked() {
			w.enqueueLocked(false, w.remote.selected(), w.local.path)
		}
		canMk := w.active.path != "" && (!w.active.remote || w.ready)
		if ui.Button(c, "新建文件夹").Icon(antd.FolderAddOutlined).Disabled(!canMk).Clicked() {
			w.mkdir, w.mkName = true, ""
		}
		if ui.Button(c, "刷新").Icon(icon.Reload).AutoInsertSpace(false).Clicked() {
			w.refreshLocked()
		}
	}, layout.Gap(8), layout.Middle(), layout.PadVH(12, 20)).Bg(th.BgContainer).Paint(bottomLine(th.Split))
}

// refreshLocked lists both panes again.
func (w *filesWin) refreshLocked() {
	if w.local.path != "" {
		w.loadLocked(w.local, w.local.path)
	}
	switch {
	case w.remote.path != "":
		w.loadLocked(w.remote, w.remote.path)
	case w.cl != nil && !w.ready && w.connErr != "":
		w.connErr = ""
		go w.start()
	}
}

// pane is one file list: head with name, 上级 and the editable path, the
// column header, the rows and a status line.
func (w *filesWin) pane(c *ui.Context, p *filePane) {
	th := c.Theme
	label := "本机路径"
	if p.remote {
		label = "对方路径"
	}
	panel(c, func() {
		ui.Row(c, func() {
			ui.Text(c, p.title).Weight(text.Weight(500)).NoWrap()
			if ui.Button(c, "上级").Icon(icon.ArrowUp).Small().AutoInsertSpace(false).Disabled(p.parent == "").Clicked() {
				w.active = p
				w.loadLocked(p, p.parent)
			}
			in := ui.Input(c, &p.edit).Size(ui.Small).A11yLabel(label).Layout(layout.Grow(1), layout.MinW(0))
			if in.Submitted() && strings.TrimSpace(p.edit) != "" {
				w.active = p
				w.loadLocked(p, strings.TrimSpace(p.edit))
			}
		}, layout.Gap(8), layout.Middle(), layout.PadVH(8, 12)).Paint(bottomLine(th.Split))
		ui.Row(c, func() {
			ui.Text(c, "名称").Size(12).Tertiary().Layout(layout.Grow(1), layout.MinW(0))
			ui.Text(c, "大小").Size(12).Tertiary().Align(ui.AlignEnd).Layout(layout.W(80))
			ui.Text(c, "修改时间").Size(12).Tertiary().Layout(layout.W(120))
		}, layout.Gap(8), layout.Middle(), layout.Padding(insets(6, 12, 6, 36))).Paint(bottomLine(th.Split))
		switch {
		case len(p.entries) == 0 && p.err != "":
			ui.Column(c, func() {
				ui.Empty(c).Simple().Description(p.err)
			}, layout.Grow(1), layout.Center())
		case len(p.entries) == 0 && (p.loading || p.path == ""):
			ui.Column(c, func() {
				ui.Spin(c)
			}, layout.Grow(1), layout.Center())
		case len(p.entries) == 0:
			ui.Column(c, func() {
				ui.Empty(c).Simple().Description("空文件夹")
			}, layout.Grow(1), layout.Center())
		default:
			vl := ui.VirtualList(c, len(p.entries), fileRowH, func(i int) { w.fileRow(c, p, i) },
				layout.Grow(1), layout.MinH(0)).A11yLabel(p.title + " 文件列表")
			if p.scrollTop {
				p.scrollTop = false
				vl.ScrollToIndex(0)
			}
		}
		ui.Row(c, func() {
			n := fmt.Sprintf("%d 项", len(p.entries))
			if p.truncated {
				n = fmt.Sprintf("只显示前 %d 项", len(p.entries))
			}
			if k := len(p.sel); k > 0 {
				n += fmt.Sprintf(" · 已选 %d 项", k)
			}
			ui.Text(c, n).Size(12).Tertiary().NoWrap()
			if p.loading && len(p.entries) > 0 {
				ui.Text(c, "正在读取…").Size(12).Tertiary().NoWrap()
			}
		}, layout.Gap(8), layout.Middle(), layout.PadVH(6, 12)).Paint(topLine(th.Split))
	}, layout.Grow(1), layout.Basis(0), layout.MinW(0), layout.MinH(0)).Clip()
}

// fileRow is one entry: icon, name, size and time; click selects (Cmd/Ctrl
// toggles, Shift extends), double click opens a folder.
func (w *filesWin) fileRow(c *ui.Context, p *filePane, i int) {
	th := c.Theme
	e := p.entries[i]
	sel := p.sel[e.Name]
	row := ui.Row(c, func() {
		ic, col := antd.FileOutlined, th.TextTertiary
		if e.IsDir {
			ic, col = antd.FolderFilled, th.Warning.Base
		}
		ui.Icon(c, ic).Size(16).Color(col)
		name := ui.Text(c, e.Name).Size(13).Ellipsis().Layout(layout.Grow(1), layout.MinW(0))
		if e.Hidden {
			name.Tertiary()
		}
		size := "—"
		if !e.IsDir {
			size = fmtBytes(e.Size)
		}
		ui.Text(c, size).Size(12).Tertiary().NoWrap().Align(ui.AlignEnd).Layout(layout.W(80))
		ui.Text(c, fmtMTime(e.ModTime)).Size(12).Tertiary().NoWrap().Layout(layout.W(120))
	}, layout.Grow(1), layout.Gap(8), layout.Middle(), layout.PadVH(0, 12))
	row.Semantics(a11y.RoleRow, e.Name)
	if sel {
		row.A11yState(a11y.Selected)
	}
	ia := row.Interact(tin.CursorDefault)
	switch {
	case sel:
		row.Bg(th.Primary.Bg)
	case ia.Hovered:
		row.Bg(th.FillQuaternary)
	}
	if !ia.Clicked {
		return
	}
	w.active = p
	if ia.Clicks >= 2 && e.IsDir {
		w.loadLocked(p, e.Path)
		return
	}
	names := make([]string, len(p.entries))
	for k, en := range p.entries {
		names[k] = en.Name
	}
	p.sel, p.anchor = clickSelect(p.sel, names, p.anchor, i, c.PointerMods())
	c.Invalidate()
}

// clickSelect applies a click on row i to a selection: a plain click
// selects only i, Cmd (macOS) or Ctrl toggles i, Shift selects the range
// from the anchor to i. It returns the new selection and anchor.
func clickSelect(sel map[string]bool, names []string, anchor, i int, mods tin.Modifiers) (map[string]bool, int) {
	switch {
	case mods&tin.ModShift != 0 && anchor >= 0 && anchor < len(names):
		out := map[string]bool{}
		if mods&(tin.ModMeta|tin.ModCtrl) != 0 {
			for k, v := range sel {
				out[k] = v
			}
		}
		a, b := min(anchor, i), max(anchor, i)
		for k := a; k <= b; k++ {
			out[names[k]] = true
		}
		return out, anchor
	case mods&(tin.ModMeta|tin.ModCtrl) != 0:
		out := map[string]bool{}
		for k, v := range sel {
			out[k] = v
		}
		if out[names[i]] {
			delete(out, names[i])
		} else {
			out[names[i]] = true
		}
		return out, i
	}
	return map[string]bool{names[i]: true}, i
}

// mkdirDialog asks for the name of a new folder in the active pane.
func (w *filesWin) mkdirDialog(c *ui.Context) {
	if !w.mkdir {
		return
	}
	p := w.active
	open := true
	var submit bool
	md := ui.Modal(c, &open, "新建文件夹").Width(min(400, c.Size().W-32)).OkText("创建").Body(func() {
		ui.Column(c, func() {
			ui.Text(c, "位置："+p.title+"  "+p.path).Size(12).Tertiary().Ellipsis()
			submit = ui.Input(c, &w.mkName).Placeholder("文件夹名称").A11yLabel("文件夹名称").Submitted()
		}, layout.Gap(8))
	})
	name := strings.TrimSpace(w.mkName)
	if (md.Ok() || submit) && name != "" {
		if strings.ContainsAny(name, `/\`) || name == "." || name == ".." {
			ui.Message(c, "error", "文件夹名称不能包含 / 或 \\")
			return
		}
		w.mkdir = false
		go w.makeDir(p, joinPath(p.path, name, p.sep), name)
		return
	}
	if md.Cancelled() || !open {
		w.mkdir = false
	}
}

func (w *filesWin) makeDir(p *filePane, path, name string) {
	var err error
	if p.remote {
		ctx, cancel := context.WithTimeout(w.ctx, 30*time.Second)
		err = w.cl.Mkdir(ctx, path)
		cancel()
	} else {
		err = files.MkdirLocal(path)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if err != nil {
		w.toastLocked("error", "无法新建文件夹："+errText(err))
		return
	}
	p.sel, p.anchor = map[string]bool{name: true}, -1
	w.loadLocked(p, p.path)
}

// closeDialog confirms closing the window while transfers run.
func (w *filesWin) closeDialog(c *ui.Context) {
	if !w.askClose {
		return
	}
	open := true
	n := 0
	for _, j := range w.jobs {
		if j.state == jobWaiting || j.state == jobRunning {
			n++
		}
	}
	md := ui.Modal(c, &open, "取消传输并关闭？").Width(min(420, c.Size().W-32)).OkText("取消传输并关闭").CancelText("继续传输").Danger().Body(func() {
		ui.Text(c, fmt.Sprintf("还有 %d 个传输没有完成。关闭窗口会取消它们，已传了一半的文件会被删除。", n))
	})
	switch {
	case md.Ok():
		w.askClose = false
		w.cancelAllLocked()
		w.closing.Store(true)
		if s := w.surface.Load(); s != nil {
			go w.app.Post(s.Close)
		}
	case md.Cancelled() || !open:
		w.askClose = false
	}
}

// joinPath joins a directory and a name with sep.
func joinPath(dir, name, sep string) string {
	if strings.HasSuffix(dir, sep) {
		return dir + name
	}
	return dir + sep + name
}

// errText turns a files error into a message for the user.
func errText(err error) string {
	var pe *files.PartialError
	if errors.As(err, &pe) {
		return fmt.Sprintf("%d 个文件失败（共 %d 个），第一个：%s", pe.Failed, pe.Total, errText(pe.First))
	}
	detail := ""
	var re *files.RemoteError
	if errors.As(err, &re) {
		detail = re.Detail
	}
	with := func(s string) string {
		if detail != "" {
			return s + "（" + detail + "）"
		}
		return s
	}
	switch {
	case errors.Is(err, files.ErrDenied):
		return "对方已关闭文件访问"
	case errors.Is(err, files.ErrCanceled), errors.Is(err, context.Canceled):
		return "已取消"
	case errors.Is(err, files.ErrExists):
		return with("目标已存在")
	case errors.Is(err, files.ErrPermission):
		return with("没有权限")
	case errors.Is(err, files.ErrChecksum):
		return "校验失败，已删除不完整的文件"
	case errors.Is(err, files.ErrNotFound):
		return with("找不到文件或文件夹")
	case errors.Is(err, files.ErrClosed):
		return "连接已断开"
	case errors.Is(err, files.ErrVersion):
		return "对方的 kkdesk 版本不兼容，请升级"
	case errors.Is(err, files.ErrInvalid):
		return with("路径无效")
	case errors.Is(err, context.DeadlineExceeded):
		return "对方没有响应"
	case strings.Contains(detail, "disk full"):
		return "磁盘已满"
	case detail != "":
		return detail
	}
	log.Printf("kkdesk files: %v", err)
	return err.Error()
}

// fmtBytes formats a size like the prototype: 248 KB, 3.4 MB, 1.2 GB.
func fmtBytes(n int64) string {
	const k = 1024
	switch {
	case n < k:
		return fmt.Sprintf("%d B", n)
	case n < k*k:
		return fmt.Sprintf("%.0f KB", float64(n)/k)
	case n < k*k*k:
		return trimFloat(float64(n)/(k*k)) + " MB"
	}
	return trimFloat(float64(n)/(k*k*k)) + " GB"
}

func trimFloat(f float64) string {
	if f >= 100 {
		return fmt.Sprintf("%.0f", f)
	}
	return strings.TrimSuffix(fmt.Sprintf("%.1f", f), ".0")
}

// fmtMTime formats a modification time: today's as 15:04, this year's
// as 1月2日 15:04, older as 2006-01-02.
func fmtMTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	now := time.Now()
	switch {
	case t.Year() == now.Year() && t.YearDay() == now.YearDay():
		return "今天 " + t.Format("15:04")
	case t.Year() == now.Year():
		return t.Format("1月2日 15:04")
	}
	return t.Format("2006-01-02")
}

// fmtDuration formats a remaining or elapsed time: 16 秒, 3 分 20 秒,
// 1 小时 5 分.
func fmtDuration(d time.Duration) string {
	s := int(d.Round(time.Second) / time.Second)
	switch {
	case s < 60:
		return fmt.Sprintf("%d 秒", max(s, 1))
	case s < 3600:
		return fmt.Sprintf("%d 分 %d 秒", s/60, s%60)
	}
	return fmt.Sprintf("%d 小时 %d 分", s/3600, s/60%60)
}
