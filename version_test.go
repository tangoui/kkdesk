package main

import (
	"encoding/json"
	"testing"
)

func TestAppVersion(t *testing.T) {
	var cfg struct{ Version string }
	if err := json.Unmarshal(tanJSON, &cfg); err != nil || cfg.Version == "" {
		t.Fatalf("tan.json version: %q %v", cfg.Version, err)
	}
	t.Setenv("TANGO_DEV_ROLE", "")
	if got := appVersion(); got != cfg.Version {
		t.Errorf("release build: %q, want tan.json's %q", got, cfg.Version)
	}
	t.Setenv("TANGO_DEV_ROLE", "worker")
	if got := appVersion(); got != cfg.Version+"-dev" {
		t.Errorf("tan dev: %q", got)
	}
	old := version
	defer func() { version = old }()
	version = "c28f015-dirty"
	if got := appVersion(); got != "c28f015-dirty" {
		t.Errorf("-ldflags override: %q", got)
	}
}
