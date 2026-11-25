package appui

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/tangoui/tango/ui"
	"github.com/tangoui/tango/ui/icon"
	"github.com/tangoui/tango/ui/layout"

	"github.com/tangoui/kkdesk/internal/app"
	"github.com/tangoui/kkdesk/internal/files"
)

type jobState int

const (
	jobWaiting jobState = iota
	jobRunning
	jobDone
	jobFailed
	jobCanceled
)

// job is one entry of the transfer queue: a file or folder copied in one
// direction. Jobs run one at a time, in order.
type job struct {
	id     int
	upload bool
	src    string // absolute path on the source side
	name   string
	dstDir string
	size   int64 // listed size (files) for the waiting row

	state     jobState
	t         *files.Transfer // while running
	cancelReq bool            // 取消 before the transfer started
	last      files.Progress  // final progress
	res       files.Result
	err       string
}

// enqueueLocked queues one job per selected entry and starts the queue.
func (w *filesWin) enqueueLocked(upload bool, sel []files.Entry, dstDir string) {
	if len(sel) == 0 || dstDir == "" {
		return
	}
	for _, e := range sel {
		w.nextJob++
		w.jobs = append(w.jobs, &job{id: w.nextJob, upload: upload, src: e.Path, name: e.Name, dstDir: dstDir, size: e.Size})
	}
	if !w.working {
		w.working = true
		go w.work()
	}
}

// busyLocked reports whether a job waits or runs.
func (w *filesWin) busyLocked() bool {
	for _, j := range w.jobs {
		if j.state == jobWaiting || j.state == jobRunning {
			return true
		}
	}
	return false
}

// cancelLocked cancels one job.
func (w *filesWin) cancelLocked(j *job) {
	switch j.state {
	case jobWaiting:
		j.state, j.err = jobCanceled, "已取消"
	case jobRunning:
		j.cancelReq = true
		if j.t != nil {
			go j.t.Cancel()
		}
	}
}

func (w *filesWin) cancelAllLocked() {
	for _, j := range w.jobs {
		w.cancelLocked(j)
	}
}

// work runs the queue until no job waits.
func (w *filesWin) work() {
	for {
		w.mu.Lock()
		var j *job
		for _, k := range w.jobs {
			if k.state == jobWaiting {
				j = k
				break
			}
		}
		if j == nil || w.closing.Load() && w.ctx.Err() != nil {
			w.working = false
			w.mu.Unlock()
			return
		}
		j.state = jobRunning
		w.mu.Unlock()
		w.invalidate()

		progress := func(files.Progress) { w.invalidate() }
		var t *files.Transfer
		var err error
		if j.upload {
			t, err = w.cl.Upload(w.ctx, j.src, j.dstDir, progress)
		} else {
			t, err = w.cl.Download(w.ctx, j.src, j.dstDir, progress)
		}
		var res files.Result
		if err == nil {
			w.mu.Lock()
			j.t = t
			cancel := j.cancelReq
			w.mu.Unlock()
			if cancel {
				t.Cancel()
			}
			res, err = t.Wait()
		}

		w.mu.Lock()
		j.res = res
		if t != nil {
			j.last = t.Progress()
		}
		j.t = nil
		switch {
		case err == nil:
			j.state = jobDone
		case j.cancelReq || errorsIsCanceled(err):
			j.state, j.err = jobCanceled, "已取消"
		default:
			j.state, j.err = jobFailed, errText(err)
			if !w.closing.Load() {
				w.toastLocked("error", j.name+"："+j.err)
			}
		}
		// Show what arrived (also after a failure: other files of a folder
		// may have been written).
		dst := w.local
		if j.upload {
			dst = w.remote
		}
		if dst.path == j.dstDir && !w.closing.Load() {
			if res.Name != "" && j.state == jobDone {
				dst.sel, dst.anchor = map[string]bool{res.Name: true}, -1
			}
			w.loadLocked(dst, dst.path)
		}
		rec := app.TransferRecord{Peer: w.v.Addr(), Name: j.name, Upload: j.upload, Bytes: res.Bytes, Files: res.Files, Elapsed: res.Elapsed, Err: j.err}
		switch j.state {
		case jobFailed:
			rec.Result = app.TransferFailed
		case jobCanceled:
			rec.Result = app.TransferCanceled
		}
		w.mu.Unlock()
		app.RecordTransfer(rec) // 文件传输 page history
		w.invalidate()
	}
}

// queue is the 传输队列 section below the panes.
func (w *filesWin) queue(c *ui.Context) {
	var running, waiting, finished int
	for _, j := range w.jobs {
		switch j.state {
		case jobRunning:
			running++
		case jobWaiting:
			waiting++
		default:
			finished++
		}
	}
	ui.Column(c, func() {
		ui.Row(c, func() {
			ui.Text(c, "传输队列").Strong().NoWrap()
			if len(w.jobs) > 0 {
				ui.Text(c, fmt.Sprintf("%d 进行中 · %d 等待 · %d 已结束", running, waiting, finished)).Size(12).Tertiary().NoWrap()
			}
			ui.Spacer(c)
			if ui.Button(c, "清除已完成").Text().Small().AutoInsertSpace(false).Disabled(finished == 0).Clicked() {
				keep := w.jobs[:0]
				for _, j := range w.jobs {
					if j.state == jobWaiting || j.state == jobRunning {
						keep = append(keep, j)
					}
				}
				w.jobs = keep
			}
		}, layout.Gap(8), layout.Middle())
		panel(c, func() {
			if len(w.jobs) == 0 {
				ui.Column(c, func() {
					ui.Text(c, "在左右两边选中文件或文件夹，点「上传 →」或「← 下载」。").Size(13).Tertiary()
				}, layout.Grow(1), layout.Center())
				return
			}
			ui.Scroll(c, func() {
				ui.Column(c, func() {
					order := queueOrder(w.jobs)
					for i, j := range order {
						w.jobRow(c, j, i == len(order)-1)
					}
				})
			}, layout.Grow(1))
		}, layout.H(196)).Clip()
	}, layout.Gap(10))
}

// jobRow is one queue entry (prototype .job): what goes where, the state
// line, 取消 and the progress bar.
func (w *filesWin) jobRow(c *ui.Context, j *job, last bool) {
	th := c.Theme
	p := j.last
	if j.t != nil {
		p = j.t.Progress()
	}
	pct := 0.0
	if p.Total > 0 {
		pct = float64(p.Done) / float64(p.Total) * 100
	}
	arrow, where := icon.ArrowRight, "到 "+w.remote.title+" "+j.dstDir
	if !j.upload {
		arrow, where = icon.ArrowLeft, "从 "+w.remote.title+" 存到 "+j.dstDir
	}
	status, status2 := "", ""
	switch j.state {
	case jobWaiting:
		status = "等待中"
	case jobRunning:
		status = fmt.Sprintf("%s / %s", fmtBytes(p.Done), fmtBytes(p.Total))
		if p.Total == 0 && p.Files == 0 {
			status = "准备中…"
		}
		if p.BytesPerSec > 0 {
			status += fmt.Sprintf(" · %s/s", fmtBytes(int64(p.BytesPerSec)))
			if left := p.Total - p.Done; left > 0 {
				status += " · 剩余 " + fmtDuration(time.Duration(float64(left)/p.BytesPerSec*float64(time.Second)))
			}
		}
		if p.Files > 1 {
			status += fmt.Sprintf(" · %d/%d 个文件", p.FilesDone, p.Files)
		}
	case jobDone:
		pct = 100
		status = "完成 · " + fmtBytes(j.res.Bytes)
		if j.res.Files > 1 {
			status += fmt.Sprintf(" · %d 个文件", j.res.Files)
		}
		if s := j.res.Elapsed.Seconds(); s >= 0.5 {
			status += fmt.Sprintf(" · 用时 %s · 平均 %s/s", fmtDuration(j.res.Elapsed), fmtBytes(int64(float64(j.res.Bytes)/s)))
		}
		if j.res.Name != "" && j.res.Name != j.name {
			status2 = "已存为 " + j.res.Name
		}
		if n := len(j.res.Skipped); n > 0 {
			status2 = joinNonEmpty(status2, fmt.Sprintf("跳过 %d 个链接或特殊文件", n))
		}
	case jobFailed:
		status = "失败：" + j.err
	case jobCanceled:
		status = "已取消"
	}
	opts := []layout.Option{layout.Gap(6), layout.PadVH(12, 16)}
	b := ui.Column(c, func() {
		ui.Row(c, func() {
			col := th.Primary.Base
			if j.state == jobFailed {
				col = th.Error.Base
			}
			ui.Icon(c, arrow).Size(14).Color(col)
			ui.Text(c, j.name).Size(13).Ellipsis().Layout(layout.MinW(0), layout.Shrink(1))
			ui.Text(c, where).Size(12).Tertiary().Ellipsis().Layout(layout.Grow(1), layout.MinW(0))
			st := ui.Text(c, status).Size(12).NoWrap()
			switch j.state {
			case jobFailed:
				st.Color(th.Error.Base)
			case jobDone:
				st.Color(th.Success.Text)
			default:
				st.Tertiary()
			}
			if j.state == jobWaiting || j.state == jobRunning {
				if ui.Button(c, "取消").Text().Small().AutoInsertSpace(false).Disabled(j.cancelReq).Clicked() {
					w.cancelLocked(j)
				}
			}
		}, layout.Gap(8), layout.Middle())
		pr := ui.Progress(c, pct).HideInfo().StrokeWidth(6).A11yLabel(j.name + " 进度")
		switch j.state {
		case jobDone:
			pr.Status("success")
		case jobFailed:
			pr.Status("exception")
		case jobRunning:
			pr.Status("active")
		case jobCanceled:
			pr.Color(th.TextQuaternary)
		}
		if status2 != "" {
			ui.Text(c, status2).Size(12).Tertiary()
		}
	}, opts...)
	if !last {
		b.Paint(bottomLine(th.Split))
	}
}

// queueOrder is the display order of the queue (prototype): the running
// job, then the waiting ones in order, then the finished ones, newest first.
func queueOrder(jobs []*job) []*job {
	out := make([]*job, 0, len(jobs))
	for _, st := range []jobState{jobRunning, jobWaiting} {
		for _, j := range jobs {
			if j.state == st {
				out = append(out, j)
			}
		}
	}
	for i := len(jobs) - 1; i >= 0; i-- {
		if j := jobs[i]; j.state != jobRunning && j.state != jobWaiting {
			out = append(out, j)
		}
	}
	return out
}

func joinNonEmpty(a, b string) string {
	if a == "" {
		return b
	}
	return a + " · " + b
}

func errorsIsCanceled(err error) bool {
	return errors.Is(err, files.ErrCanceled) || errors.Is(err, context.Canceled)
}
