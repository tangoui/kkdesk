package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tangoui/tango/automation"
	"github.com/tangoui/tango/ui/a11y"

	"github.com/tangoui/kkdesk/internal/pairing"
	"github.com/tangoui/kkdesk/internal/session"
)

func TestE2EPairingDialogs(t *testing.T) {
	if os.Getenv("TANGO_E2E") == "" || testing.Short() {
		t.Skip("set TANGO_E2E=1 to open the desktop window")
	}
	dir := t.TempDir()
	instance := automation.LaunchOptions(t, ".", automation.Options{
		Args: []string{"-listen", "127.0.0.1:0", "-applog", ""},
		Env:  []string{"KKDESK_CONFIG_DIR=" + dir, "TANGO_LAUNCH_BACKGROUND=1"},
	})
	window := instance.Window("kkdesk")
	if first := window.FindRegexp(a11y.RoleButton, "^(稍后|复制并分享)$"); first.Name() == "稍后" {
		first.Click()
	}
	window.Find(a11y.RoleButton, "手机扫码连接").Click()
	window.Find(a11y.RoleText, "手动配对码").Wait()
	window.FindRegexp(a11y.RoleText, "^[A-Z2-7]{4}(-[A-Z2-7]{4}){3}$").Wait()
	window.FindRegexp(a11y.RoleText, "[0-9]{2}:[0-9]{2} 后自动刷新").Wait()
	shots := os.Getenv("KKDESK_PAIRING_SHOTS")
	if shots != "" {
		if err := os.MkdirAll(shots, 0755); err != nil {
			t.Fatal(err)
		}
	}
	screenshot := func(name string) string {
		path := filepath.Join(dir, name)
		// Accessibility appears before the modal opening animation completes.
		time.Sleep(350 * time.Millisecond)
		img := window.Screenshot()
		if err := os.WriteFile(path, img, 0600); err != nil {
			t.Fatal(err)
		}
		if shots != "" {
			if err := os.WriteFile(filepath.Join(shots, name), img, 0644); err != nil {
				t.Fatal(err)
			}
		}
		return path
	}
	shot := screenshot("qr.png")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "swift", "-", shot)
	cmd.Stdin = strings.NewReader(`import Foundation
import Vision
let request = VNDetectBarcodesRequest()
request.symbologies = [.qr]
let handler = VNImageRequestHandler(url: URL(fileURLWithPath: CommandLine.arguments[1]))
try handler.perform([request])
for barcode in request.results ?? [] {
    if let code = barcode.payloadStringValue { print(code) }
}
`)
	out, err := cmd.Output()
	if err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			t.Log(string(exit.Stderr))
		}
		t.Fatalf("decode rendered QR: %v", err)
	}
	uri, err := url.Parse(string(bytes.TrimSpace(out)))
	if err != nil || uri.Scheme != "kkdesk" || uri.Host != "pair" {
		t.Fatalf("rendered QR does not contain a pairing invitation: %v", err)
	}
	qr := uri.Query()
	ex, err := session.OpenMobileLAN(ctx, qr.Get("lan"))
	if err != nil {
		t.Fatal(err)
	}
	defer ex.Close()
	pub, key, _ := ed25519.GenerateKey(nil)
	hello := pairing.Message{Type: "hello", Purpose: "pair", PublicKey: pairing.Encode(pub), Nonce: pairing.Random()}
	if err := ex.Send(ctx, hello); err != nil {
		t.Fatal(err)
	}
	challenge, err := ex.Receive(ctx)
	if err != nil {
		t.Fatal(err)
	}
	tx := pairing.Transcript{Purpose: hello.Purpose, ClientNonce: hello.Nonce, HostNonce: challenge.Nonce,
		ClientKey: hello.PublicKey, HostKey: challenge.PublicKey}
	hostKey, _ := pairing.PublicKey(challenge.PublicKey)
	if pairing.Fingerprint(hostKey) != qr.Get("fp") || !tx.Verify(challenge.PublicKey, challenge) {
		t.Fatal("challenge does not match QR")
	}
	request := pairing.Message{Type: "request", Code: tx.CodeProof(qr.Get("c")), Name: "验收用 iPhone", Model: "iPhone 12 Pro"}
	tx.Sign(key, &request)
	if err := ex.Send(ctx, request); err != nil {
		t.Fatal(err)
	}
	pending, err := ex.Receive(ctx)
	if err != nil || pending.Status != "pending" || !tx.Verify(challenge.PublicKey, pending) {
		t.Fatalf("pairing pending: %+v, %v", pending, err)
	}
	allow := window.FindContains(a11y.RoleButton, "允许配对")
	allow.Wait()
	screenshot("confirm.png")
	allow.Click()
	approved, err := ex.Receive(ctx)
	if err != nil || approved.Status != "approved" || !tx.Verify(challenge.PublicKey, approved) {
		t.Fatalf("pairing approval: %+v, %v", approved, err)
	}
	ack := pairing.Message{Type: "pair_ack", Nonce: approved.Nonce}
	tx.Sign(key, &ack)
	if err := ex.Send(ctx, ack); err != nil {
		t.Fatal(err)
	}
	result, err := ex.Receive(ctx)
	if err != nil || result.Status != "ok" || !tx.Verify(challenge.PublicKey, result) {
		t.Fatalf("pairing result: %+v, %v", result, err)
	}
	ack = pairing.Message{Type: "pair_received", Nonce: approved.Nonce}
	tx.Sign(key, &ack)
	if err := ex.Send(ctx, ack); err != nil {
		t.Fatal(err)
	}
	committed, err := ex.Receive(ctx)
	if err != nil || committed.Status != "committed" || !tx.Verify(challenge.PublicKey, committed) {
		t.Fatalf("pairing commit confirmation: %+v, %v", committed, err)
	}
	window.Find(a11y.RoleMenuItem, "设置").Click()
	window.Find(a11y.RoleText, "验收用 iPhone").Wait()
	screenshot("paired-phones.png")
	rename := window.Find(a11y.RoleButton, "改名")
	rename.Click()
	window.Find(a11y.RoleTextbox, "手机名称").SetValue("已改名的 iPhone")
	window.Find(a11y.RoleButton, "保存").Click()
	window.Find(a11y.RoleText, "已改名的 iPhone").Wait()
	window.Find(a11y.RoleButton, "解除配对").Click()
	window.Find(a11y.RoleButton, "确认解除").Click()
	window.Find(a11y.RoleText, "暂无已配对的手机").Wait()
}
