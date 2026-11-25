package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestServerConfigPrecedence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.json")
	data := `{"listen":"127.0.0.1:9090","turn-public-ip":"203.0.113.10","relay-only":true,"max-signaling-connections":80,"turn-credential-ttl":"2h"}`
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"-config", path, "-listen", "127.0.0.1:9191", "-relay-only=false"},
		{"-listen", "127.0.0.1:9191", "-relay-only=false", "-config=" + path},
	} {
		cfg, err := parseConfig(args)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Listen != "127.0.0.1:9191" || cfg.RelayOnly || cfg.PublicIP != "203.0.113.10" ||
			cfg.MinPort != 49160 || cfg.MaxConnections != 80 || time.Duration(cfg.CredentialTTL) != 2*time.Hour {
			t.Fatalf("unexpected configuration: %+v", cfg)
		}
	}
}

func TestServerConfigRejectsInvalidValues(t *testing.T) {
	for _, data := range []string{
		`{"max-signal-connections":80}`,
		`{"max-signaling-connections":0}`,
		`{"turn-credential-ttl":"tomorrow"}`,
		`{"turn-credential-ttl":"0s"}`,
		`{"turn-credential-ttl":null}`,
		`{} {}`,
	} {
		path := filepath.Join(t.TempDir(), "server.json")
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := parseConfig([]string{"-config", path}); err == nil {
			t.Errorf("accepted invalid configuration %s", data)
		}
	}
}
