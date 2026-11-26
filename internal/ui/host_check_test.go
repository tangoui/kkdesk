package appui

import (
	"context"
	"testing"
	"time"

	"github.com/tangoui/tango/ui"
)

func TestHostCheckDialogAnswers(t *testing.T) {
	for _, tc := range []struct {
		button string
		want   bool
	}{{"一致，继续连接", true}, {"取消", false}} {
		m := &Main{}
		hc := &hostCheck{addr: "192.168.1.5:21118", code: "KD-ABCDEFGHIJKL", reply: make(chan bool, 1)}
		m.hostCheck = hc
		d := ui.NewDriver(func(c *ui.Context) { m.hostCheckDialog(c) }, ui.DriverOptions{Accessibility: true})
		d.Frame()
		if err := d.ClickLabel(tc.button); err != nil {
			t.Fatal(err)
		}
		select {
		case got := <-hc.reply:
			if got != tc.want {
				t.Errorf("%s answered %v, want %v", tc.button, got, tc.want)
			}
		default:
			t.Fatalf("%s did not answer", tc.button)
		}
		if m.hostCheck != nil {
			t.Errorf("%s left the check pending", tc.button)
		}
	}
}

// A second check while one is pending is declined at once instead of
// replacing the first (whose connection would then wait forever).
func TestHostCheckOneAtATime(t *testing.T) {
	m := &Main{}
	m.hostCheck = &hostCheck{addr: "a", code: "KD-AAAAAAAAAAAA", reply: make(chan bool, 1)}
	done := make(chan bool, 1)
	go func() { done <- m.confirmHost(context.Background(), "b", "KD-BBBBBBBBBBBB") }()
	select {
	case ok := <-done:
		if ok {
			t.Fatal("a second check was accepted")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a second check blocked")
	}
	if m.hostCheck.addr != "a" {
		t.Fatal("the pending check was replaced")
	}
}
