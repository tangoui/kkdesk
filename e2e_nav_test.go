package main

// End-to-end test of the 我的设备, 远程会话, 文件传输 and 设置 pages: one
// kkdesk instance with its own configuration directory connects to itself
// (files only: no screen capture) and is driven through Tango's
// automation channel; windows stay in the background. It runs only with
// TANGO_E2E=1 and KKDESK_E2E_NAV=<scratch dir> (the configuration goes to
// <dir>/cfg, a test file to <dir>/src, uploads to <dir>/dst):
//
//	TANGO_E2E=1 KKDESK_E2E_NAV=/tmp/kkdesk-nav go test -run E2ENav -v .
//
// KKDESK_E2E_SHOTS=<dir> saves page-{devices,sessions,files,settings}.png
// there. KKDESK_E2E_ACCESSIBILITY_SHOTS saves the macOS permission state.
// It listens on 21918 and moves to 21919.

import (
	"net"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/tangoui/tango/automation"
	"github.com/tangoui/tango/sys/autostart"
	"github.com/tangoui/tango/ui/a11y"

	"github.com/tangoui/kkdesk/internal/config"
)

func TestE2ENav(t *testing.T) {
	root := os.Getenv("KKDESK_E2E_NAV")
	if root == "" {
		t.Skip("KKDESK_E2E_NAV not set")
	}
	// Start from scratch: a configuration left by an earlier run (devices,
	// theme, port) would change what the pages show.
	for _, d := range []string{"cfg", "dst"} {
		if err := os.RemoveAll(filepath.Join(root, d)); err != nil {
			t.Fatal(err)
		}
	}
	shots := os.Getenv("KKDESK_E2E_SHOTS")
	shot := func(w *automation.Window, name string) {
		if shots != "" {
			time.Sleep(3500 * time.Millisecond) // toasts fade out
			w.SaveScreenshot(filepath.Join(shots, name))
		}
	}
	cfgDir := filepath.Join(root, "cfg")
	src, dst := filepath.Join(root, "src"), filepath.Join(root, "dst")
	for _, d := range []string{cfgDir, src, dst} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(src, "hello.txt"), []byte("kkdesk nav e2e\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	app := automation.LaunchOptions(t, ".", automation.Options{
		Args:    []string{autostart.LoginArg, "-listen", ":21918", "-applog", ""},
		Env:     []string{"KKDESK_CONFIG_DIR=" + cfgDir, "TANGO_LAUNCH_BACKGROUND=1"},
		Timeout: 20 * time.Second,
	})
	defer func() {
		if t.Failed() {
			t.Logf("output:\n%s", app.Output())
		}
	}()
	w := app.Window("kkdesk")
	if w.FindRegexp(a11y.RoleButton, "^(稍后|复制并分享)$").Name() == "稍后" {
		w.Find(a11y.RoleButton, "稍后").Click()
		w.Find(a11y.RoleDialog, "需要屏幕录制权限").WaitGone()
	}
	w.FindContains(a11y.RoleNone, "等待连接").Wait()
	accessibilityShot := func(w *automation.Window, name string) {
		if dir := os.Getenv("KKDESK_E2E_ACCESSIBILITY_SHOTS"); dir != "" {
			w.SaveScreenshot(filepath.Join(dir, name))
		}
	}
	accessibilityEntry := func(save bool) {
		t.Helper()
		const entry = "配置辅助功能权限"
		if runtime.GOOS != "darwin" {
			if w.Find(a11y.RoleButton, entry).Exists() {
				t.Fatal("macOS permission entry visible on another platform")
			}
			return
		}
		status := w.FindRegexp(a11y.RoleNone, "^辅助功能 (未授权|已授权)$")
		status.Wait()
		if status.Name() == "辅助功能 已授权" {
			if w.Find(a11y.RoleButton, entry).Exists() {
				t.Fatal("permission button still visible after granting")
			}
			if save {
				accessibilityShot(w, "desktop-granted.png")
			}
			return
		}
		if save {
			w.FindContains(a11y.RoleNone, "辅助功能未授权").Wait()
			accessibilityShot(w, "desktop-denied.png")
		}
		w.Find(a11y.RoleButton, entry).Click()
		dialog := w.Find(a11y.RoleDialog, "需要辅助功能权限")
		dialog.Wait()
		w.Find(a11y.RoleButton, "我已授权，重新检测").Wait()
		w.Find(a11y.RoleButton, "打开系统设置").Wait()
		w.Press("Escape")
		dialog.WaitGone()
	}
	accessibilityEntry(true)
	nav := func(page string) { t.Helper(); w.Find(a11y.RoleMenuItem, page).Click() }
	shot(w, "page-connect.png")
	w.Find(a11y.RoleTextbox, "对方地址").Wait()
	dump := func(what string) { t.Helper(); t.Logf("%s:\n%s", what, w.Dump()) }

	// The temporary password is saved in the configuration.
	tempPW := func() string {
		t.Helper()
		store, err := config.Open(cfgDir)
		if err != nil {
			t.Fatal(err)
		}
		return store.TempPassword().Password
	}

	// connectFiles connects to this machine in 传文件 mode from the 连接
	// page and accepts the request; the file window opens.
	connectFiles := func(pw string) *automation.Window {
		t.Helper()
		nav("连接")
		w.Find(a11y.RoleTextbox, "对方地址").SetValue("127.0.0.1:21918")
		w.Find(a11y.RoleTextbox, "临时密码").SetValue(pw)
		w.Find(a11y.RoleRadio, "传文件").Click()
		w.Find(a11y.RoleButton, "连接").Click()
		accept := w.FindContains(a11y.RoleButton, "接受")
		accept.Wait()
		shot(w, "dialog-confirm.png")
		accept.Click()
		return app.Window("文件传输 - 127.0.0.1:21918")
	}

	// 1. Connect once: the device appears in 我的设备, online.
	pw1 := tempPW()
	if len(pw1) != 6 {
		t.Fatalf("temporary password %q not saved", pw1)
	}
	fw := connectFiles(pw1)
	nav("我的设备")
	w.FindContains(a11y.RoleNone, "127.0.0.1").Wait()
	w.FindContains(a11y.RoleNone, "在线").Wait()

	// 2. Add a device by hand, make a group, rename, move, delete.
	w.Find(a11y.RoleButton, "新建分组").Click()
	w.Find(a11y.RoleTextbox, "分组名称").SetValue("公司机房")
	w.Find(a11y.RoleButton, "创建").Click()
	w.Find(a11y.RoleButton, "添加设备").Click()
	w.Find(a11y.RoleTextbox, "设备地址").SetValue("127.0.0.1:21997")
	w.Find(a11y.RoleTextbox, "设备名称").SetValue("测试机")
	w.Find(a11y.RoleButton, "添加").Click()
	w.FindContains(a11y.RoleRow, "测试机").Wait()
	w.FindContains(a11y.RoleRow, "离线").Wait() // nothing listens on 21997

	more := func(name string) { t.Helper(); w.Find(a11y.RoleButton, "更多操作 "+name).Click() }
	more("测试机")
	w.Find(a11y.RoleMenuItem, "改名").Click()
	w.Find(a11y.RoleTextbox, "新名称").SetValue("机房备用机")
	w.Find(a11y.RoleButton, "保存").Click()
	w.FindContains(a11y.RoleRow, "机房备用机").Wait()
	more("机房备用机")
	w.Find(a11y.RoleMenuItem, "移动到分组").Click()
	w.Find(a11y.RoleCombobox, "目标分组").Click()
	w.Find(a11y.RoleOption, "公司机房").Click()
	w.Find(a11y.RoleButton, "移动").Click()
	w.FindContains(a11y.RoleRow, "公司机房 · 1").Wait()
	shot(w, "page-devices.png")

	// Delete asks first; 删除 in the dialog removes it.
	more("机房备用机")
	w.Find(a11y.RoleMenuItem, "删除").Click()
	w.FindContains(a11y.RoleNone, "确定从我的设备中删除").Wait()
	w.Find(a11y.RoleButton, "删除").Click()
	w.FindContains(a11y.RoleRow, "机房备用机").WaitGone()
	store, _ := config.Open(cfgDir)
	if devs := store.Devices(); len(devs) != 1 || devs[0].Addr != "127.0.0.1:21918" {
		t.Fatalf("address book on disk: %+v", devs)
	}
	if g := store.Groups(); len(g) != 2 || g[1] != "公司机房" {
		t.Fatalf("groups on disk: %v", g)
	}

	// 3. 文件传输: a small upload, then the history on the page.
	goTo := func(field, path string) {
		t.Helper()
		f := fw.Find(a11y.RoleTextbox, field)
		f.SetValue(path)
		f.Focus()
		fw.Press("Enter")
		f.WaitValue(path)
	}
	goTo("本机路径", src)
	goTo("对方路径", dst)
	fw.Find(a11y.RoleRow, "hello.txt").Nth(0).ClickPointer()
	fw.Find(a11y.RoleButton, "上传 →").Click()
	fw.FindContains(a11y.RoleText, "完成 ·").Wait()
	if b, err := os.ReadFile(filepath.Join(dst, "hello.txt")); err != nil || string(b) != "kkdesk nav e2e\n" {
		t.Fatalf("uploaded file: %q %v", b, err)
	}
	nav("文件传输")
	w.FindContains(a11y.RoleRow, "hello.txt").Wait()
	w.FindContains(a11y.RoleRow, "完成").Wait()
	w.FindContains(a11y.RoleRow, "上传到 127.0.0.1:21918").Wait()
	w.Find(a11y.RoleButton, "打开文件传输").Click() // brings the open window up
	shot(w, "page-files.png")

	// 4. 远程会话 lists the session; 断开 ends it.
	nav("远程会话")
	w.FindContains(a11y.RoleRow, "127.0.0.1:21918").Wait()
	w.FindContains(a11y.RoleRow, "传文件").Wait()
	w.FindContains(a11y.RoleNone, "正在控制本机").Wait()
	shot(w, "page-sessions.png")
	dump("sessions")
	w.Find(a11y.RoleButton, "断开").Nth(0).Click()
	w.Find(a11y.RoleHeading, "没有进行中的会话").Wait()
	waitClosed(t, app, "文件传输 - 127.0.0.1:21918")

	// 每次连接后更换: the session rotated the temporary password.
	pw2 := tempPW()
	if pw2 == pw1 || len(pw2) != 6 {
		t.Fatalf("temporary password %q after the session (was %q)", pw2, pw1)
	}

	// 5. 设置: a fixed password works for connecting.
	nav("设置")
	w.Find(a11y.RoleTextbox, "固定密码").SetValue("fixed-e2e-1")
	w.Find(a11y.RoleButton, "保存").Click()
	w.FindContains(a11y.RoleNone, "已设置").Wait()
	fw = connectFiles("fixed-e2e-1")
	fw.Find(a11y.RoleTextbox, "对方路径").Wait()
	nav("远程会话")
	w.Find(a11y.RoleButton, "断开").Nth(0).Click()
	w.Find(a11y.RoleHeading, "没有进行中的会话").Wait()
	waitClosed(t, app, "文件传输 - 127.0.0.1:21918")

	// The port moves to 21919: the host listens there, not on 21918.
	nav("设置")
	w.Find(a11y.RoleMenuItem, "局域网").Click()
	w.Find(a11y.RoleSpinButton, "监听端口").SetValue("21919")
	w.Find(a11y.RoleButton, "应用").Click()
	w.FindContains(a11y.RoleNone, "端口 21919").Wait()
	waitDial(t, "127.0.0.1:21919", true)
	waitDial(t, "127.0.0.1:21918", false)

	w.Find(a11y.RoleMenuItem, "安全").Click()
	accessibilityEntry(false)
	for _, label := range []string{"新会话默认允许剪贴板同步", "新会话默认允许文件传输"} {
		setting := w.Find(a11y.RoleSwitch, label)
		setting.WaitState(a11y.Checked)
		setting.Click()
		setting.WithoutState(a11y.Checked).Wait()
	}
	shot(w, "page-settings.png")
	w.Find(a11y.RoleMenuItem, "跨网连接").Click()
	w.FindContains(a11y.RoleNone, "未配置").Wait()
	shot(w, "page-settings-remote.png")

	// Dark theme, saved.
	w.Find(a11y.RoleMenuItem, "通用").Click()
	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		w.Find(a11y.RoleSwitch, "登录时自动启动").Wait()
	}
	w.Find(a11y.RoleRadio, "仅查看").Click()
	w.Find(a11y.RoleRadio, "仅查看").WaitState(a11y.Checked)
	w.Find(a11y.RoleRadio, "深色").Click()
	shot(w, "page-settings-dark.png")
	nav("连接")
	w.Find(a11y.RoleRadio, "仅查看").WaitState(a11y.Checked)
	store, _ = config.Open(cfgDir)
	if st := store.Settings(); st.Theme != config.ThemeDark || st.Port != 21919 || st.FixedPassword != "fixed-e2e-1" ||
		st.DefaultConnectionMode != config.ConnectionView || st.AllowSessionClipboard || st.AllowSessionFiles {
		t.Fatalf("settings on disk: %+v", st)
	}
}

// waitClosed waits until no window has the title.
func waitClosed(t *testing.T, app *automation.App, title string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		open := false
		for _, wi := range app.Windows() {
			if wi.Title == title {
				open = true
			}
		}
		if !open {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("window %q still open", title)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// waitDial waits until a TCP connect to addr succeeds (want) or fails.
func waitDial(t *testing.T, addr string, want bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		c, err := net.DialTimeout("tcp", addr, time.Second)
		if c != nil {
			c.Close()
		}
		if (err == nil) == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("dial %s: %v, want success=%v", addr, err, want)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
