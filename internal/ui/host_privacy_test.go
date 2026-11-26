package appui

import (
	"testing"
	"time"

	"github.com/tangoui/tango/ui"
)

func TestPrivacyConsentDialogActions(t *testing.T) {
	for _, label := range []string{"允许", "拒绝"} {
		t.Run(label, func(t *testing.T) {
			deadline := time.Now().Add(30 * time.Second)
			var allow, reject bool
			d := ui.NewDriver(func(c *ui.Context) {
				a, r := privacyConsentDialog(c, deadline)
				allow, reject = allow || a, reject || r
			}, ui.DriverOptions{Accessibility: true})
			d.Frame()
			if err := d.ClickLabel(label); err != nil {
				t.Fatal(err)
			}
			if allow != (label == "允许") || reject != (label == "拒绝") {
				t.Fatalf("clicked %s, allow=%v reject=%v", label, allow, reject)
			}
		})
	}
}

func TestPrivacyGraceCountdownUsesCeiling(t *testing.T) {
	if got := lockGraceText(59*time.Second + time.Millisecond); got != "连接已断开，60 秒后锁屏" {
		t.Fatal(got)
	}
	if got := lockGraceText(-time.Second); got != "正在锁屏…" {
		t.Fatal(got)
	}
}
