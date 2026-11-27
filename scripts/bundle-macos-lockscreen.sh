#!/bin/bash
# Add launchd job definitions to a tan-built bundle before signing it.
set -euo pipefail

root=$(cd "$(dirname "$0")" && pwd)
if [ "$#" -eq 0 ]; then
    printf 'Usage: bash %s <kkdesk.app> [<kkdesk.app> ...]\n' "$0" >&2
    exit 2
fi

for app in "$@"; do
    contents="$app/Contents"
    if [ ! -x "$contents/MacOS/kkdesk" ]; then
        printf 'Missing executable in %s\n' "$app" >&2
        exit 1
    fi
    for kind in daemon agent; do
        directory=LaunchDaemons
        if [ "$kind" = agent ]; then directory=LaunchAgents; fi
        name="com.kkdesk.lockscreen.$kind.plist"
        mkdir -p "$contents/Library/$directory"
        install -m 0644 "$root/macos-lockscreen/$name" "$contents/Library/$directory/$name"
        plutil -lint "$contents/Library/$directory/$name"
    done
done
