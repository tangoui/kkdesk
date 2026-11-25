package update

import "testing"

func TestNew(t *testing.T) {
	for _, tc := range []struct {
		name, version string
		env           map[string]string
		want          bool
	}{
		{"release build", "0.1.0", nil, true},
		{"patch release", "1.2.3", nil, true},
		{"go build without a version", "dev", nil, false},
		{"git describe of an untagged tree", "c28f015-dirty", nil, false},
		{"empty", "", nil, false},
		{"tan dev", "0.1.0", map[string]string{"TANGO_DEV_ROLE": "worker"}, false},
		{"turned off", "0.1.0", map[string]string{EnvOff: "1"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u := New(tc.version, func(k string) string { return tc.env[k] })
			if (u != nil) != tc.want {
				t.Fatalf("New(%q, %v) = %v, want an updater: %v", tc.version, tc.env, u, tc.want)
			}
			if u == nil {
				return
			}
			if u.ManifestURL != ManifestURL || u.PublicKey != PublicKey || u.AppID != AppID || u.Version != tc.version {
				t.Fatalf("updater %+v", u)
			}
		})
	}
}

// KKDESK_UPDATE_URL points at another manifest (a local test server); the
// signature is still checked with the built-in key, and only http(s) is
// accepted.
func TestManifestOverride(t *testing.T) {
	env := map[string]string{EnvURL: "http://127.0.0.1:8765/latest.json"}
	u := New("0.1.0", func(k string) string { return env[k] })
	if u == nil || u.ManifestURL != env[EnvURL] || u.PublicKey != PublicKey {
		t.Fatalf("override: %+v", u)
	}
	env[EnvURL] = "file:///tmp/latest.json"
	if u := New("0.1.0", func(k string) string { return env[k] }); u == nil || u.ManifestURL != ManifestURL {
		t.Fatalf("a file URL was accepted: %+v", u)
	}
}
