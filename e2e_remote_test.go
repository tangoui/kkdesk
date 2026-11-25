package main

import (
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/tangoui/tango/automation"
	"github.com/tangoui/tango/ui/a11y"

	"github.com/tangoui/kkdesk/internal/rendezvous"
)

func TestE2ERemote(t *testing.T) {
	if os.Getenv("TANGO_E2E") == "" || testing.Short() {
		t.Skip("set TANGO_E2E=1 to open the desktop windows")
	}
	const token = "test-service-token-for-local-e2e"
	const secret = "test-turn-secret-for-local-e2e-only"
	serverTLS, publicKey, err := rendezvous.LoadOrCreateIdentity(filepath.Join(t.TempDir(), "identity.pem"))
	if err != nil {
		t.Fatal(err)
	}
	relay, err := rendezvous.StartRelay(rendezvous.RelayConfig{
		UDPAddr: "127.0.0.1:0", BindIP: "127.0.0.1", PublicIP: net.IPv4(127, 0, 0, 1),
		TLSAddr: "127.0.0.1:0", TLS: serverTLS.Clone(),
		MinPort: 49260, MaxPort: 49359, Secret: secret,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = relay.Close() })
	broker, err := rendezvous.NewServer(token, secret, []string{"turns:" + relay.TLSAddr.String() + "?transport=tcp"})
	if err != nil {
		t.Fatal(err)
	}
	broker.RelayOnly = true
	server := httptest.NewUnstartedServer(broker)
	server.TLS = serverTLS
	server.StartTLS()
	t.Cleanup(server.Close)

	start := func() *automation.App {
		app := automation.LaunchOptions(t, ".", automation.Options{
			Args: []string{"-listen", "127.0.0.1:0", "-applog", ""},
			Env:  []string{"KKDESK_CONFIG_DIR=" + t.TempDir(), "TANGO_LAUNCH_BACKGROUND=1"},
		})
		t.Cleanup(func() {
			if t.Failed() {
				t.Log(app.Output())
			}
		})
		w := app.Window("kkdesk")
		if w.FindRegexp(a11y.RoleButton, "^(稍后|复制并分享)$").Name() == "稍后" {
			w.Find(a11y.RoleButton, "稍后").Click()
		}
		w.Find(a11y.RoleMenuItem, "设置").Click()
		w.Find(a11y.RoleMenuItem, "网络").Click()
		w.Find(a11y.RoleTextbox, "跨网服务地址").SetValue(server.URL)
		w.Find(a11y.RoleTextbox, "跨网服务器公钥").SetValue(publicKey)
		w.Find(a11y.RoleTextbox, "跨网服务密钥").SetValue(token)
		w.Find(a11y.RoleButton, "保存跨网配置").Click()
		w.Find(a11y.RoleMenuItem, "连接").Click()
		return app
	}
	host, guest := start(), start()
	hostWindow, guestWindow := host.Window("kkdesk"), guest.Window("kkdesk")
	code := hostWindow.FindRegexp(a11y.RoleText, "^KD-[A-Z2-7]{12}$").Name()
	guestWindow.Find(a11y.RoleTextbox, "对方地址").SetValue(code)
	guestWindow.Find(a11y.RoleRadio, "传文件").Click()
	guestWindow.Find(a11y.RoleButton, "连接").Click()
	hostWindow.FindContains(a11y.RoleButton, "接受").Click()
	guest.Window("文件传输 - "+code).Find(a11y.RoleTextbox, "对方路径").Wait()
}
