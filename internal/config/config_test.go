package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

func open(t *testing.T, dir string) *Store {
	t.Helper()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return s
}

func TestDefaultsWithoutFile(t *testing.T) {
	s := open(t, t.TempDir())
	got := s.Settings()
	if got != Defaults() {
		t.Fatalf("settings %+v, want defaults %+v", got, Defaults())
	}
	d := Defaults()
	if d.Rotate != RotateAfterSession || !d.Confirm || !d.AllowRemote || d.Port != DefaultPort || d.FPS != 60 || d.BitrateMbps != 0 || d.Theme != ThemeSystem {
		t.Fatalf("unexpected defaults %+v", d)
	}
	if g := s.Groups(); len(g) != 1 || g[0] != DefaultGroup {
		t.Fatalf("groups %v, want [%s]", g, DefaultGroup)
	}
	if len(s.Devices()) != 0 {
		t.Fatal("devices not empty")
	}
	if _, err := os.Stat(s.Path()); !os.IsNotExist(err) {
		t.Fatalf("Open must not create the file: %v", err)
	}
}

func TestRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s := open(t, dir)
	if err := s.UpdateSettings(func(st *Settings) {
		st.Rotate = Rotate12h
		st.FixedPassword = "abcd1234"
		st.Confirm = false
		st.AllowRemote = false
		st.FPS = 144
		st.BitrateMbps = 14
		st.Port = 21919
		st.Theme = ThemeDark
		st.DefaultConnectionMode = ConnectionView
		st.AllowSessionClipboard = false
		st.AllowSessionFiles = false
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddGroup("公司机房"); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 11, 25, 9, 30, 0, 0, time.Local)
	d, err := s.Connected("192.168.1.20", at)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateDevice(d.ID, func(d *Device) { d.Name = "办公室"; d.Group = "公司机房"; d.Note = "n" }); err != nil {
		t.Fatal(err)
	}

	r := open(t, dir)
	if r.Settings() != s.Settings() {
		t.Fatalf("settings after reload %+v, want %+v", r.Settings(), s.Settings())
	}
	if g := r.Groups(); len(g) != 2 || g[1] != "公司机房" {
		t.Fatalf("groups %v", g)
	}
	devs := r.Devices()
	if len(devs) != 1 {
		t.Fatalf("devices %+v", devs)
	}
	got := devs[0]
	if got.Name != "办公室" || got.Group != "公司机房" || got.Addr != "192.168.1.20:21118" || got.Note != "n" || !got.LastConnected.Equal(at) {
		t.Fatalf("device %+v", got)
	}
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(s.Path())
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != 0o600 {
			t.Fatalf("file mode %v, want 0600 (it holds the fixed password)", fi.Mode().Perm())
		}
	}
}

func TestLegacySessionDefaults(t *testing.T) {
	dir := t.TempDir()
	raw := `{"version":1,"settings":{"defaultConnectionMode":"invalid","theme":"dark"}}`
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	st := open(t, dir).Settings()
	if st.DefaultConnectionMode != ConnectionControl || !st.AllowSessionClipboard || !st.AllowSessionFiles {
		t.Fatalf("legacy session defaults: %+v", st)
	}
}

func TestInvalidSettingsAreRepaired(t *testing.T) {
	dir := t.TempDir()
	raw := `{"version":1,"settings":{"rotate":"weird","port":80,"fps":7,"bitrateMbps":5,"theme":"pink","fixedPassword":"short"}}`
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	s := open(t, dir)
	st := s.Settings()
	d := Defaults()
	if st.Rotate != d.Rotate || st.Port != d.Port || st.FPS != d.FPS || st.BitrateMbps != d.BitrateMbps || st.Theme != d.Theme || st.FixedPassword != "" {
		t.Fatalf("settings not repaired: %+v", st)
	}
}

func TestAtomicWriteLeavesNoTemp(t *testing.T) {
	dir := t.TempDir()
	s := open(t, dir)
	for i := range 5 {
		if _, err := s.Connected(fmt.Sprintf("10.0.0.%d", i), time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	ents, _ := os.ReadDir(dir)
	if len(ents) != 1 || ents[0].Name() != FileName {
		var names []string
		for _, e := range ents {
			names = append(names, e.Name())
		}
		t.Fatalf("dir holds %v, want only %s", names, FileName)
	}
	b, err := os.ReadFile(s.Path())
	if err != nil {
		t.Fatal(err)
	}
	var f file
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatalf("saved file is not JSON: %v", err)
	}
}

// A failed write keeps the previous file intact (the temporary file is the
// one that fails) and the error is reported.
func TestUnwritableDirectory(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("permission bits")
	}
	dir := t.TempDir()
	s := open(t, dir)
	if _, err := s.Connected("10.0.0.1", time.Now()); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(s.Path())
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0o700)
	_, err := s.Connected("10.0.0.2", time.Now())
	if !IsSaveError(err) {
		t.Fatalf("save into a read-only directory: %v, want a SaveError", err)
	}
	if s.Err() == "" {
		t.Fatal("Err() empty after a failed save")
	}
	if len(s.Devices()) != 2 {
		t.Fatal("the change must stay in memory")
	}
	after, _ := os.ReadFile(s.Path())
	if string(after) != string(before) {
		t.Fatal("previous file changed")
	}
	os.Chmod(dir, 0o700)
	if err := s.UpdateSettings(func(st *Settings) { st.FPS = 30 }); err != nil {
		t.Fatal(err)
	}
	if s.Err() != "" {
		t.Fatalf("Err() %q after a good save", s.Err())
	}
}

func TestCorruptFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(dir)
	if s == nil {
		t.Fatal("Open returned no store")
	}
	if !errors.Is(err, ErrCorrupt) {
		t.Fatalf("Open error %v, want ErrCorrupt", err)
	}
	if s.Err() == "" {
		t.Fatal("Err() must explain the reset")
	}
	if s.Settings() != Defaults() {
		t.Fatal("settings are not the defaults")
	}
	bad, err := os.ReadFile(path + ".bad")
	if err != nil || string(bad) != "{not json" {
		t.Fatalf("backup %q, %v", bad, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("corrupt file still in place")
	}
	// Saving works again and clears the message.
	if err := s.UpdateSettings(func(st *Settings) { st.FPS = 90 }); err != nil {
		t.Fatal(err)
	}
	if s.Err() != "" {
		t.Fatalf("Err() %q after saving", s.Err())
	}
	if open(t, dir).Settings().FPS != 90 {
		t.Fatal("not saved")
	}
}

func TestConcurrentAccess(t *testing.T) {
	dir := t.TempDir()
	s := open(t, dir)
	var wg sync.WaitGroup
	for i := range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range 5 {
				if _, err := s.Connected(fmt.Sprintf("10.0.%d.%d", i, j), time.Now()); err != nil {
					t.Error(err)
				}
				_ = s.UpdateSettings(func(st *Settings) { st.FPS = 30 })
				_ = s.Devices()
				_ = s.Settings()
			}
		}()
	}
	wg.Wait()
	if n := len(open(t, dir).Devices()); n != 80 {
		t.Fatalf("reloaded %d devices, want 80", n)
	}
}

func TestDeviceDedupeByAddress(t *testing.T) {
	s := open(t, t.TempDir())
	t1 := time.Now().Add(-time.Hour)
	a, err := s.Connected("192.168.1.20", t1)
	if err != nil {
		t.Fatal(err)
	}
	if a.Name != "192.168.1.20" || a.Group != DefaultGroup || a.ID == "" {
		t.Fatalf("new device %+v", a)
	}
	if err := s.UpdateDevice(a.ID, func(d *Device) { d.Name = "客厅" }); err != nil {
		t.Fatal(err)
	}
	t2 := time.Now()
	b, err := s.Connected("192.168.1.20:21118", t2) // same machine, explicit port
	if err != nil {
		t.Fatal(err)
	}
	if b.ID != a.ID || b.Name != "客厅" || !b.LastConnected.Equal(t2) {
		t.Fatalf("updated device %+v (first %+v)", b, a)
	}
	if n := len(s.Devices()); n != 1 {
		t.Fatalf("%d devices, want 1", n)
	}
	if _, err := s.Connected("192.168.1.20:22000", t2); err != nil { // another port: another entry
		t.Fatal(err)
	}
	if _, err := s.AddDevice("x", "192.168.1.20", ""); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("manual duplicate: %v, want ErrDuplicate", err)
	}
	if _, err := s.AddDevice("x", "  ", ""); !errors.Is(err, ErrInvalidAddr) {
		t.Fatalf("empty address: %v, want ErrInvalidAddr", err)
	}
	d, err := s.AddDevice("", "nas.local", "")
	if err != nil {
		t.Fatal(err)
	}
	if d.Name != "nas.local" || d.Addr != "nas.local:21118" || !d.LastConnected.IsZero() {
		t.Fatalf("manual device %+v", d)
	}
	if err := s.DeleteDevice(d.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteDevice(d.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete twice: %v", err)
	}
	if NormalizeAddr("[::1]") != "[::1]:21118" || NormalizeAddr("10.0.0.1:80") != "10.0.0.1:80" {
		t.Fatal("NormalizeAddr")
	}
}

func TestGroups(t *testing.T) {
	s := open(t, t.TempDir())
	if err := s.AddGroup("家人"); err != nil {
		t.Fatal(err)
	}
	if err := s.AddGroup(" 家人 "); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("duplicate group: %v", err)
	}
	if err := s.AddGroup(""); !errors.Is(err, ErrEmptyName) {
		t.Fatal("empty group accepted")
	}
	d, _ := s.AddDevice("妈妈", "10.0.0.9", "家人")
	if d.Group != "家人" {
		t.Fatalf("group %q", d.Group)
	}
	// Moving to an unknown group creates it.
	if err := s.UpdateDevice(d.ID, func(d *Device) { d.Group = "新组" }); err != nil {
		t.Fatal(err)
	}
	if g := s.Groups(); len(g) != 3 || g[2] != "新组" {
		t.Fatalf("groups %v", g)
	}
	// An empty name or group falls back.
	if err := s.UpdateDevice(d.ID, func(d *Device) { d.Name = " "; d.Group = "" }); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Device(d.ID)
	if got.Name != DisplayAddr(got.Addr) || got.Group != DefaultGroup {
		t.Fatalf("device %+v", got)
	}
}

func TestDirOverride(t *testing.T) {
	t.Setenv(EnvDir, "/tmp/kkdesk-x")
	if d, err := Dir(); err != nil || d != "/tmp/kkdesk-x" {
		t.Fatalf("Dir %q %v", d, err)
	}
	t.Setenv(EnvDir, "")
	d, err := Dir()
	if err != nil {
		t.Fatal(err)
	}
	base, _ := os.UserConfigDir()
	if d != filepath.Join(base, "kkdesk") {
		t.Fatalf("Dir %q", d)
	}
}

// The connect page trims what is typed, so a fixed password cannot start
// or end with whitespace; one saved before this rule is trimmed on load.
func TestFixedPasswordWhitespace(t *testing.T) {
	if err := ValidFixedPassword(" abcdefgh"); err == nil {
		t.Fatal("leading space accepted")
	}
	if err := ValidFixedPassword("abcdefgh\t"); err == nil {
		t.Fatal("trailing tab accepted")
	}
	if err := ValidFixedPassword("abcd efgh"); err != nil {
		t.Fatalf("inner space refused: %v", err)
	}
	s := Defaults()
	s.FixedPassword = " abcdefgh "
	s.repair()
	if s.FixedPassword != "abcdefgh" {
		t.Fatalf("repaired fixed password = %q", s.FixedPassword)
	}
}

func TestConnectedPinsHostKeyOnce(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now()
	first, firstUse, err := s.ConnectedWithHostKey("my-host", at, "key-a")
	if err != nil || !firstUse {
		t.Fatalf("first connection = %+v, %v, %v", first, firstUse, err)
	}
	if _, _, err := s.ConnectedWithHostKey("MY-HOST:21118", at, "key-b"); !errors.Is(err, ErrHostKeyChanged) {
		t.Fatalf("competing first connection: %v", err)
	}
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.HostKey("MY-HOST:21118"); got != "key-a" {
		t.Fatalf("saved identity = %q", got)
	}
	if d, firstUse, err := s.ConnectedWithHostKey("my-host", at, "key-a"); err != nil || firstUse || d.ID != first.ID {
		t.Fatalf("reconnect = %+v, %v, %v", d, firstUse, err)
	}
}

// A configuration that exists but cannot be read right now (another program
// holds it open, say) must not be replaced by defaults.
func TestUnreadableConfigIsNotOverwritten(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	saved := `{"version":1,"settings":{"allowRemote":false,"fixedPassword":"keep-me-123","port":21118}}`
	if err := os.WriteFile(path, []byte(saved), 0o600); err != nil {
		t.Fatal(err)
	}
	reads := 0
	readConfig = func(string) ([]byte, error) {
		reads++
		return nil, errors.New("sharing violation")
	}
	t.Cleanup(func() { readConfig = os.ReadFile })
	s, err := Open(dir)
	if err == nil {
		t.Fatal("Open succeeded on an unreadable file")
	}
	if reads < 2 {
		t.Errorf("read %d times, want a retry", reads)
	}
	if s.Settings().AllowRemote {
		t.Error("an unreadable configuration turned remote access on")
	}
	if st := s.Settings(); st.AllowSessionFiles || st.AllowSessionClipboard {
		t.Errorf("an unreadable configuration turned file or clipboard access on: %+v", st)
	}
	if err := s.SetTempPassword(TempPassword{Password: "abc123", Since: time.Now()}); !IsSaveError(err) {
		t.Errorf("SetTempPassword = %v, want a SaveError", err)
	}
	if err := s.UpdateSettings(func(st *Settings) { st.Port = 1234 }); !IsSaveError(err) {
		t.Errorf("UpdateSettings = %v, want a SaveError", err)
	}
	if s.Err() == "" {
		t.Error("the UI error was cleared")
	}
	if b, _ := os.ReadFile(path); string(b) != saved {
		t.Fatalf("configuration was overwritten:\n%s", b)
	}
}

// A file that becomes readable on a retry loads normally.
func TestConfigReadRetry(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(`{"version":1,"settings":{"allowRemote":false}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	fails := 1
	readConfig = func(p string) ([]byte, error) {
		if fails > 0 {
			fails--
			return nil, errors.New("sharing violation")
		}
		return os.ReadFile(p)
	}
	t.Cleanup(func() { readConfig = os.ReadFile })
	s, err := Open(dir)
	if err != nil || s.Settings().AllowRemote {
		t.Fatalf("Open = %v, allowRemote %v; want the file loaded on retry", err, s.Settings().AllowRemote)
	}
}

func TestTrustedViewers(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if s.ViewerTrusted("k1") {
		t.Fatal("trusted before TrustViewer")
	}
	if err := s.TrustViewer("k1"); err != nil {
		t.Fatal(err)
	}
	if s2, _ := Open(dir); !s2.ViewerTrusted("k1") {
		t.Fatal("a trusted controller was not saved")
	}
	for i := range maxTrustedViewers + 5 {
		if err := s.TrustViewer(fmt.Sprintf("n%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	if s.ViewerTrusted("k1") || !s.ViewerTrusted(fmt.Sprintf("n%d", maxTrustedViewers+4)) {
		t.Fatal("the oldest controller should be dropped past the limit, the newest kept")
	}
}

func TestLegacySettingsPrivacyDefaults(t *testing.T) {
	dir := t.TempDir()
	s := open(t, dir)
	if err := os.WriteFile(s.Path(), []byte(`{"settings":{"privacy":"unknown"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	got := open(t, dir).Settings()
	if got.Privacy != PrivacyAsk || got.LockAfterDisconnect {
		t.Fatalf("privacy = %q, lock after disconnect = %v", got.Privacy, got.LockAfterDisconnect)
	}
}
