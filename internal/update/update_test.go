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

func TestDesktopInstallUpdateTarget(t *testing.T) {
	for _, tc := range []struct {
		name, platform, exe, appImage, userExe, target string
		protected, disabled                            bool
	}{
		{name: "Windows per-user exe", platform: "windows", exe: "user/kkdesk.exe"},
		{name: "Windows protected copy", platform: "windows", protected: true, userExe: "user/kkdesk.exe", target: "user/kkdesk.exe"},
		{name: "Windows without user installer", platform: "windows", protected: true, disabled: true},
		{name: "Linux AppImage", platform: "linux", appImage: "/home/me/kkdesk.AppImage", disabled: true},
		{name: "Linux deb", platform: "linux", exe: "/usr/bin/kkdesk", disabled: true},
		{name: "Linux archive", platform: "linux", exe: "/home/me/kkdesk/kkdesk"},
		{name: "macOS", platform: "darwin", exe: "/Applications/kkdesk.app/Contents/MacOS/kkdesk"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u := New("1.2.3", func(string) string { return "" })
			u = ForDesktopInstall(u, tc.platform, tc.exe, tc.appImage, tc.protected, tc.userExe)
			if (u == nil) != tc.disabled {
				t.Fatalf("updater disabled = %v, want %v", u == nil, tc.disabled)
			}
			if u != nil && u.Target != tc.target {
				t.Errorf("target = %q, want %q", u.Target, tc.target)
			}
		})
	}
}
