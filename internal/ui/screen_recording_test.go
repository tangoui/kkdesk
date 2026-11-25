package appui

import (
	"testing"

	"github.com/tangoui/tango/ui"
)

func TestScreenRecordingGuide(t *testing.T) {
	var guide screenRecordingGuide
	var shown bool
	missing := true
	check := func() bool { return missing }
	opened := 0
	driver := ui.NewDriver(func(c *ui.Context) {
		guide.entry(c)
		guide.dialog(c, true, func() error {
			opened++
			return nil
		})
	}, ui.DriverOptions{Accessibility: true})
	click := func(label string) {
		t.Helper()
		if err := driver.ClickLabel(label); err != nil {
			t.Fatal(err)
		}
	}

	guide.update(false, &shown, check)
	if guide.open || guide.missing {
		t.Fatal("prompted with remote access disabled")
	}
	guide.update(true, &shown, check)
	driver.Frame()
	click("稍后")
	if guide.open || !guide.missing || opened != 0 {
		t.Fatal("dismissing the prompt should keep its entry available")
	}
	click("配置屏幕录制权限")
	click("打开系统设置")
	if guide.open || opened != 1 {
		t.Fatalf("settings opened %d times, prompt open %v", opened, guide.open)
	}

	guide = screenRecordingGuide{} // the main window was recreated by hot reload
	guide.update(true, &shown, check)
	if guide.open || !guide.missing {
		t.Fatal("hot reload should keep the entry without prompting again")
	}
	guide.update(false, &shown, check)
	guide.update(true, &shown, check)
	if !guide.open {
		t.Fatal("enabling remote access again should prompt")
	}
	missing = false
	guide.refresh(check())
	if guide.open || guide.missing {
		t.Fatal("granting permission should clear the prompt and the entry")
	}
}
