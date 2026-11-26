#!/bin/bash
set -euo pipefail
app=${LOCKSPIKE_APP:-"$(dirname "$0")/KKDeskLockSpike.app"}
if [[ $# != 2 ]]; then
    printf '用法：sm.sh status|register|unregister daemon|agent|loginwindow\n'
    exit 2
fi
exec "$app/Contents/MacOS/lockspike" --sm "$1" "$2"
