#!/bin/bash
set -euo pipefail
src=$(cd "$(dirname "$0")" && pwd)
out=${1:-/Users/admin/min/outputs/kkdesk-lockscreen-spike/mac}
mkdir -p "$out"
out=$(cd "$out" && pwd)
source "$src/plist.sh"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
app="$tmp/KKDeskLockSpike.app"
contents="$app/Contents"
mkdir -p "$contents"/{MacOS,Library/LaunchAgents,Library/LaunchDaemons}
plist="$contents/Info.plist"
plutil -create xml1 "$plist"
plutil -insert CFBundleExecutable -string lockspike "$plist"
plutil -insert CFBundleIdentifier -string com.kkdesk.lockspike "$plist"
plutil -insert CFBundleName -string KKDeskLockSpike "$plist"
plutil -insert CFBundlePackageType -string APPL "$plist"
plutil -insert CFBundleVersion -string 1 "$plist"
plutil -insert CFBundleShortVersionString -string 0.1 "$plist"
plutil -insert LSMinimumSystemVersion -string 14.0 "$plist"

xcrun clang -fobjc-arc -fblocks -mmacosx-version-min=14.0 \
    -Wall -Wextra -Werror -Wno-unused-parameter \
    -framework Foundation -framework CoreGraphics -framework CoreImage -framework IOKit \
    -framework ImageIO -framework ScreenCaptureKit -framework Security -framework ServiceManagement \
    "$src/probe.m" -o "$contents/MacOS/lockspike"
for kind in daemon agent loginwindow; do
    label="com.kkdesk.lockspike.sm.$kind"
    directory=LaunchAgents
    session=Aqua
    case "$kind" in
        daemon) directory=LaunchDaemons; session= ;;
        loginwindow) session=LoginWindow ;;
    esac
    make_job_plist "$contents/Library/$directory/$label.plist" "$label" \
        BundleProgram Contents/MacOS/lockspike "$session" "" lockspike --sm-marker "$label"
done

codesign --force --sign "${LOCKSPIKE_SIGN_IDENTITY:--}" --options runtime "$app"
codesign --verify --strict "$app"
if [[ -e "$out/KKDeskLockSpike.app" ]]; then
    printf '请用空目录构建，以保留现有 app 和 TCC 授权记录。\n' >&2
    exit 1
fi
ditto "$app" "$out/KKDeskLockSpike.app"
for script in install uninstall jobs probe-after-lock sm; do
    install -m 0755 "$src/$script.sh" "$out/$script.sh"
done
install -m 0644 "$src/plist.sh" "$src/README.md" "$out"
