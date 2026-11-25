package main

// End-to-end rehearsal of a self-update, run by scripts/rehearse-update.sh
// (which builds and signs kkdesk 0.0.1 and 0.0.2 and serves the 0.0.2
// release). With TANGO_E2E=1, KKDESK_E2E_UPDATE=<installed 0.0.1 .app>,
// KKDESK_E2E_UPDATE_URL=<its latest.json> and KKDESK_E2E_UPDATE_WANT=<the
// 0.0.2 .app>: 0.0.1 checks for updates from 设置 › 通用, downloads and
// installs 0.0.2 and restarts; the installed executable must then be 0.0.2
// and the updater must see the new version confirm itself.

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/tangoui/tango/automation"
	"github.com/tangoui/tango/ui/a11y"
)

func TestE2EUpdate(t *testing.T) {
	bundle, url, want := os.Getenv("KKDESK_E2E_UPDATE"), os.Getenv("KKDESK_E2E_UPDATE_URL"), os.Getenv("KKDESK_E2E_UPDATE_WANT")
	if bundle == "" || url == "" || want == "" {
		t.Skip("run scripts/rehearse-update.sh")
	}
	exe := filepath.Join(bundle, "Contents", "MacOS", "kkdesk")
	newExe, err := os.ReadFile(filepath.Join(want, "Contents", "MacOS", "kkdesk"))
	if err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(os.Getenv("HOME"), "Library", "Application Support", "tango-updater", "com.kkdesk.desktop")

	app := automation.LaunchOptions(t, exe, automation.Options{
		Args:    []string{"-listen", "127.0.0.1:0", "-applog", ""},
		Env:     []string{"KKDESK_UPDATE_URL=" + url, "KKDESK_CONFIG_DIR=" + t.TempDir(), "TANGO_LAUNCH_BACKGROUND=1"},
		Timeout: 30 * time.Second,
	})
	w := app.Window("kkdesk")
	if w.FindRegexp(a11y.RoleButton, "^(稍后|复制并分享)$").Name() == "稍后" {
		w.Find(a11y.RoleButton, "稍后").Click()
	}
	w.Find(a11y.RoleMenuItem, "设置").Click()
	w.Find(a11y.RoleMenuItem, "通用").Click()
	w.FindContains(a11y.RoleNone, "kkdesk 0.0.1").Wait()
	w.Find(a11y.RoleButton, "检查更新").Click()

	u := app.Window("软件更新") // updateui follows the system language (Chinese here)
	u.FindRegexp(a11y.RoleButton, "^(下载并安装|Download and Install)$").Click()
	u.FindRegexp(a11y.RoleButton, "^(立即重启|Restart Now)$").Click()

	// The helper replaces the bundle once 0.0.1 has quit, opens 0.0.2 and
	// waits for it to confirm (updateui.Start calls Confirm at once).
	deadline := time.Now().Add(90 * time.Second)
	for {
		got, _ := os.ReadFile(exe)
		_, pendingErr := os.Stat(filepath.Join(state, "pending.json"))
		_, rolledErr := os.Stat(filepath.Join(state, "rolledback"))
		if rolledErr == nil {
			b, _ := os.ReadFile(filepath.Join(state, "rolledback"))
			t.Fatalf("the update was rolled back: %s", b)
		}
		if bytes.Equal(got, newExe) && os.IsNotExist(pendingErr) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("not updated in time: executable is 0.0.2: %v, pending.json still there: %v", bytes.Equal(got, newExe), pendingErr == nil)
		}
		time.Sleep(500 * time.Millisecond)
	}
	if out, err := exec.Command("codesign", "--verify", "--deep", "--strict", bundle).CombinedOutput(); err != nil {
		t.Fatalf("installed bundle signature: %v\n%s", err, out)
	}
	t.Logf("updated to 0.0.2 and confirmed; state dir: %s", state)
}
