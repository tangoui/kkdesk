package appui

import (
	"testing"

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
