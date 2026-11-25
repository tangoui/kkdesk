package main

import (
	"io"
	"testing"

	"github.com/tangoui/kkdesk/internal/config"
)

// Flags given on the command line win over the settings file.
func TestFlagsOverrideConfig(t *testing.T) {
	store, err := config.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateSettings(func(s *config.Settings) {
		s.Port, s.FPS, s.BitrateMbps, s.Confirm, s.Rotate = 22000, 144, 14, true, config.Rotate12h
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetTempPassword(config.TempPassword{Password: "222222"}); err != nil {
		t.Fatal(err)
	}

	o, _, err := parseFlags(nil)
	if err != nil {
		t.Fatal(err)
	}
	cfg := hostConfig(o, store, io.Discard)
	if cfg.Addr != ":22000" || cfg.FPS != 144 || cfg.BitrateMbps != 14 || cfg.AutoAccept || cfg.Rotate != config.Rotate12h || cfg.Password != "222222" || cfg.OnPassword == nil {
		t.Fatalf("from settings: %+v", cfg)
	}

	o, _, err = parseFlags([]string{"-listen", ":21918", "-fps", "30", "-bitrate", "0", "-autoaccept", "-password", "123456"})
	if err != nil {
		t.Fatal(err)
	}
	cfg = hostConfig(o, store, io.Discard)
	if cfg.Addr != ":21918" || cfg.FPS != 30 || cfg.BitrateMbps != 0 || !cfg.AutoAccept || cfg.Password != "123456" || cfg.Rotate != config.RotateNever || cfg.OnPassword != nil {
		t.Fatalf("flags: %+v", cfg)
	}
}
