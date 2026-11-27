// Package update configures kkdesk's self-update: Tango's service/updater
// checks a signed manifest published with each GitHub release, downloads
// the new version (a binary patch when possible), installs it after kkdesk
// quits and rolls back if the new version does not start. The window is
// Tango's updateui. Releases are made by .github/workflows/release.yml.
package update

import (
	"strings"

	"github.com/tangoui/tango/service/updater"
)

const (
	// ManifestURL is the signed manifest (latest.json) of the newest
	// release. GitHub redirects it to the asset of the latest release; the
	// files it lists have absolute URLs of that release.
	ManifestURL = "https://github.com/tangoui/kkdesk/releases/latest/download/latest.json"
	// PublicKey verifies the manifest (tan keygen; the private key is the
	// TANGO_UPDATE_KEY secret of the release workflow).
	PublicKey = "3jqU7A3Q7d3I1KjTQWbh4gf9+zOBDn1bIqYNJqy9imI="
	// AppID must match the manifest's "app" (the id in tan.json).
	AppID = "com.kkdesk.desktop"

	// EnvURL replaces ManifestURL, for testing a release from a local
	// server; the manifest must still be signed with the release key.
	EnvURL = "KKDESK_UPDATE_URL"
	// EnvOff turns self-update off (any non-empty value).
	EnvOff = "KKDESK_NO_UPDATE"
)

// New returns the updater for this build, or nil when it cannot update
// itself: a development build (tan dev, or a version that is not a release
// number such as "dev" or a git hash) or EnvOff set. getenv is os.Getenv.
func New(version string, getenv func(string) string) *updater.Updater {
	if getenv("TANGO_DEV_ROLE") != "" || getenv(EnvOff) != "" || !isRelease(version) {
		return nil
	}
	url := ManifestURL
	if v := getenv(EnvURL); strings.HasPrefix(v, "https://") || strings.HasPrefix(v, "http://") {
		url = v
	}
	return &updater.Updater{ManifestURL: url, PublicKey: PublicKey, Version: version, AppID: AppID}
}

// ForDesktopInstall keeps self-update on user-owned files. Published Linux
// updates are directory archives, so AppImage and deb have their own upgrade path.
func ForDesktopInstall(u *updater.Updater, platform, exe, appImage string, protected bool, userExe string) *updater.Updater {
	if u == nil {
		return nil
	}
	switch platform {
	case "windows":
		if protected {
			if userExe == "" {
				return nil
			}
			u.Target = userExe
		}
	case "linux":
		if appImage != "" || exe == "/usr/bin/kkdesk" || exe == "/bin/kkdesk" {
			return nil
		}
	}
	return u
}

// isRelease reports a release version number (1.2.3, 0.1.0-beta.1).
func isRelease(v string) bool {
	if v == "" || v[0] < '0' || v[0] > '9' {
		return false
	}
	_, err := updater.CompareVersions(v, "0.0.0")
	return err == nil
}
