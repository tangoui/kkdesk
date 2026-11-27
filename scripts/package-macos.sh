#!/bin/bash
# Make a drag-to-install disk image from the same signed app used by updates.
set -euo pipefail

app=$1
image=$2
work=$(mktemp -d)
mounted=
cleanup() {
    if [ -n "$mounted" ]; then
        hdiutil detach "$mounted" >/dev/null || hdiutil detach -force "$mounted" >/dev/null || return
    fi
    rm -rf "$work"
}
trap cleanup EXIT

stage="$work/stage"
mount="$work/mount"
mkdir "$stage" "$mount"
ditto "$app" "$stage/kkdesk.app"
ln -s /Applications "$stage/Applications"
hdiutil create -volname kkdesk -srcfolder "$stage" -fs HFS+ -format UDZO "$image"
codesign --force --sign - "$image"
codesign --verify --strict "$image"
hdiutil attach -readonly -nobrowse -mountpoint "$mount" "$image"
mounted="$mount"
test "$(readlink "$mount/Applications")" = /Applications
codesign --verify --deep --strict "$mount/kkdesk.app"
for plist in LaunchAgents/com.kkdesk.lockscreen.agent.plist LaunchDaemons/com.kkdesk.lockscreen.daemon.plist; do
    cmp "$app/Contents/Library/$plist" "$mount/kkdesk.app/Contents/Library/$plist"
done
cmp "$app/Contents/MacOS/kkdesk" "$mount/kkdesk.app/Contents/MacOS/kkdesk"
