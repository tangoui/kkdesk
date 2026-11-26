package appui

import (
	"testing"

	"github.com/tangoui/tango/ui"
	"github.com/tangoui/tango/ui/a11y"
)

func TestAccessibilityGuide(t *testing.T) {
	var g accessibilityGuide
	granted := true
	opened := 0
	check := func() bool { return granted }
	d := ui.NewDriver(func(c *ui.Context) {
		g.entry(c)
		g.dialog(c, false, check, func() error {
			opened++
			return nil
		})
	}, ui.DriverOptions{Accessibility: true})
	click := func(label string) {
		t.Helper()
		if err := d.ClickLabel(label); err != nil {
			t.Fatal(err)
		}
	}
	g.refresh(check())
	d.Frame()
	if _, ok := d.A11yFind(a11y.RoleButton, "配置辅助功能权限"); ok {
		t.Fatal("configuration button visible with permission granted")
	}
	granted = false
	g.refresh(check())
	d.Frame()
	click("配置辅助功能权限")
	if _, ok := d.A11yFind(a11y.RoleDialog, "需要辅助功能权限"); !ok {
		t.Fatal("permission guide did not open")
	}
	click("打开系统设置")
	click("我已授权，重新检测")
	if !g.open || !g.missing || opened != 1 {
		t.Fatalf("guide after denial: %+v, settings opened %d times", g, opened)
	}
	granted = true
	click("我已授权，重新检测")
	if g.open || g.missing {
		t.Fatalf("grant should clear the dialog and entry: %+v", g)
	}
	d.Frame()
	if _, ok := d.A11yFind(a11y.RoleButton, "配置辅助功能权限"); ok {
		t.Fatal("permission entry remained after granting")
	}
}
