#!/bin/bash
# Rehearses a self-update on this Mac before a release: builds two signed
# versions of kkdesk (0.0.1 and 0.0.2; their executables are built with the
# tango_dev tag for Tango's automation channel),
# signs a manifest for 0.0.2 with the release key (a binary patch from
# 0.0.1 included), serves it on 127.0.0.1:8765, then runs TestE2EUpdate:
# 0.0.1 checks for updates from 设置 › 通用, downloads, installs and
# restarts into 0.0.2, which confirms the update.
#
#   scripts/rehearse-update.sh [workdir]     # default /tmp/kkdesk-update-rehearsal
#
# Needs: tan, the update key (TANGO_UPDATE_KEY_FILE, default
# ~/.config/kkdesk-release/update.key) and a codesign identity
# (TANGO_SIGN_IDENTITY, default the first one in the keychain). The
# versions are 0.0.x so they never get ahead of a real release; the
# updater state they leave is removed at the end.
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
work=${1:-/tmp/kkdesk-update-rehearsal}
export TANGO_UPDATE_KEY_FILE=${TANGO_UPDATE_KEY_FILE:-$HOME/.config/kkdesk-release/update.key}
identity=${TANGO_SIGN_IDENTITY:-$(security find-identity -v -p codesigning | sed -n 's/^ *1) [0-9A-F]* "\(.*\)"$/\1/p')}
port=8765
state="$HOME/Library/Application Support/tango-updater/com.kkdesk.desktop"
[ -f "$TANGO_UPDATE_KEY_FILE" ] || { echo "no update key at $TANGO_UPDATE_KEY_FILE" >&2; exit 1; }
[ -n "$identity" ] || { echo "no codesign identity (set TANGO_SIGN_IDENTITY)" >&2; exit 1; }
[ ! -e "$state" ] || { echo "updater state exists at $state; move it away first" >&2; exit 1; }

rm -rf "$work" && mkdir -p "$work"
echo "work dir $work, identity: $identity"

# build <version>: a signed .app with automation, then its release dir.
build() {
	local v=$1 src="$work/src-$1"
	mkdir -p "$src"
	git -C "$root" archive HEAD | tar -x -C "$src"
	python3 - "$src/tan.json" "$v" <<'PY'
import json, sys
p, v = sys.argv[1], sys.argv[2]
c = json.load(open(p)); c["version"] = v
json.dump(c, open(p, "w"), indent=2)
PY
	(cd "$src" && tan build -platform darwin/"$(go env GOARCH)" -o build >/dev/null)
	# The automation channel only exists with the tango_dev tag, which tan
	# build does not take: swap in an executable built with it (and with
	# tango_release, as tan build adds, which turns self-update on).
	(cd "$src" && CGO_ENABLED=1 go build -tags tango_dev,tango_release -trimpath -ldflags "-s -w" -o build/darwin-*/kkdesk.app/Contents/MacOS/kkdesk .)
	tan sign -identity "$identity" "$src"/build/darwin-*/kkdesk.app >/dev/null
	local prev=()
	[ "$v" = 0.0.2 ] && prev=(-prev "$work/release-0.0.1")
	(cd "$src" && tan release-manifest -build build -o "$work/release-$v" -url "http://127.0.0.1:$port/" ${prev[@]+"${prev[@]}"})
}
build 0.0.1
build 0.0.2

mkdir -p "$work/install"
cp -R "$work"/src-0.0.1/build/darwin-*/kkdesk.app "$work/install/"

(cd "$work/release-0.0.2" && exec python3 -m http.server "$port" --bind 127.0.0.1 >"$work/http.log" 2>&1) &
server=$!
cleanup() {
	kill "$server" 2>/dev/null || true
	pkill -f "$work/install/kkdesk.app" 2>/dev/null || true
	if [ -d "$state" ]; then
		mkdir -p "$work/updater-state" && cp -R "$state/." "$work/updater-state/" && rm -rf "$state"
	fi
}
trap cleanup EXIT
sleep 1

cd "$root"
TANGO_E2E=1 KKDESK_E2E_UPDATE="$work/install/kkdesk.app" KKDESK_E2E_UPDATE_URL="http://127.0.0.1:$port/latest.json" KKDESK_E2E_UPDATE_WANT="$(echo "$work"/src-0.0.2/build/darwin-*/kkdesk.app)" \
	go test -run E2EUpdate -count=1 -v . 2>&1 | tee "$work/test.log"
