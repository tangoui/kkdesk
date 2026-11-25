package appui

import (
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	tin "github.com/tangoui/tango/ui/input"

	"github.com/tangoui/kkdesk/internal/files"
)

func selNames(sel map[string]bool, names []string) []string {
	var out []string
	for _, n := range names {
		if sel[n] {
			out = append(out, n)
		}
	}
	return out
}

func TestClickSelect(t *testing.T) {
	names := []string{"a", "b", "c", "d", "e"}
	sel, anchor := clickSelect(map[string]bool{}, names, -1, 1, 0)
	if got := selNames(sel, names); !slices.Equal(got, []string{"b"}) || anchor != 1 {
		t.Fatalf("click: %v anchor %d", got, anchor)
	}
	sel, anchor = clickSelect(sel, names, anchor, 3, tin.ModShift)
	if got := selNames(sel, names); !slices.Equal(got, []string{"b", "c", "d"}) || anchor != 1 {
		t.Fatalf("shift: %v anchor %d", got, anchor)
	}
	sel, anchor = clickSelect(sel, names, anchor, 2, tin.ModMeta)
	if got := selNames(sel, names); !slices.Equal(got, []string{"b", "d"}) || anchor != 2 {
		t.Fatalf("cmd toggle off: %v anchor %d", got, anchor)
	}
	sel, anchor = clickSelect(sel, names, anchor, 4, tin.ModCtrl)
	if got := selNames(sel, names); !slices.Equal(got, []string{"b", "d", "e"}) || anchor != 4 {
		t.Fatalf("ctrl toggle on: %v anchor %d", got, anchor)
	}
	sel, _ = clickSelect(sel, names, 0, 1, tin.ModShift|tin.ModMeta)
	if got := selNames(sel, names); !slices.Equal(got, []string{"a", "b", "d", "e"}) {
		t.Fatalf("cmd+shift adds range: %v", got)
	}
	sel, anchor = clickSelect(sel, names, anchor, 0, 0)
	if got := selNames(sel, names); !slices.Equal(got, []string{"a"}) || anchor != 0 {
		t.Fatalf("plain click resets: %v anchor %d", got, anchor)
	}
	sel, _ = clickSelect(map[string]bool{}, names, -1, 2, tin.ModShift)
	if got := selNames(sel, names); !slices.Equal(got, []string{"c"}) {
		t.Fatalf("shift without anchor: %v", got)
	}
}

func TestFormat(t *testing.T) {
	for _, c := range []struct {
		n    int64
		want string
	}{{0, "0 B"}, {1000, "1000 B"}, {248 << 10, "248 KB"}, {3565158, "3.4 MB"}, {200 << 20, "200 MB"}, {1288490189, "1.2 GB"}, {5 << 30, "5 GB"}} {
		if got := fmtBytes(c.n); got != c.want {
			t.Errorf("fmtBytes(%d) = %q, want %q", c.n, got, c.want)
		}
	}
	for _, c := range []struct {
		d    time.Duration
		want string
	}{{200 * time.Millisecond, "1 秒"}, {16 * time.Second, "16 秒"}, {200 * time.Second, "3 分 20 秒"}, {65 * time.Minute, "1 小时 5 分"}} {
		if got := fmtDuration(c.d); got != c.want {
			t.Errorf("fmtDuration(%v) = %q, want %q", c.d, got, c.want)
		}
	}
	if got := joinPath(`C:\`, "a", `\`); got != `C:\a` {
		t.Errorf("joinPath root = %q", got)
	}
	if got := joinPath("/x/y", "a", "/"); got != "/x/y/a" {
		t.Errorf("joinPath = %q", got)
	}
}

func TestErrText(t *testing.T) {
	for _, c := range []struct {
		err  error
		want string
	}{
		{files.ErrDenied, "对方已关闭文件访问"},
		{&files.RemoteError{Code: "denied"}, "对方已关闭文件访问"},
		{fmt.Errorf("wrap: %w", files.ErrCanceled), "已取消"},
		{&files.RemoteError{Code: "exists", Detail: "/a"}, "目标已存在（/a）"},
		{&files.RemoteError{Code: "permission", Detail: "/r: permission denied"}, "没有权限（/r: permission denied）"},
		{files.ErrChecksum, "校验失败，已删除不完整的文件"},
		{&files.RemoteError{Code: "io", Detail: "/d/f: disk full"}, "磁盘已满"},
		{&files.PartialError{Failed: 1, Total: 3, First: &files.RemoteError{Code: "denied"}}, "1 个文件失败（共 3 个），第一个：对方已关闭文件访问"},
		{errors.Join(files.ErrClosed), "连接已断开"},
	} {
		if got := errText(c.err); got != c.want {
			t.Errorf("errText(%v) = %q, want %q", c.err, got, c.want)
		}
	}
}

func TestQueueOrder(t *testing.T) {
	jobs := []*job{{id: 1, state: jobDone}, {id: 2, state: jobFailed}, {id: 3, state: jobRunning}, {id: 4, state: jobWaiting}, {id: 5, state: jobWaiting}}
	var ids []int
	for _, j := range queueOrder(jobs) {
		ids = append(ids, j.id)
	}
	if !slices.Equal(ids, []int{3, 4, 5, 2, 1}) {
		t.Fatalf("order %v", ids)
	}
}
