#!/bin/bash
# Loopback measurement on one Mac: the animation window keeps the screen
# changing, mmdesk -host shares the screen, mmdesk -connect 127.0.0.1 views it
# (view-only). Usage: scripts/loopback.sh <outdir> [seconds] [extra host flags...]
# MMDESK_EXCLUDE_SELF=0 keeps mmdesk windows in the capture (no SCK
# exclusion filter, as in a session from another machine; see host.go).
set -euo pipefail
out=${1:?outdir}; secs=${2:-60}; shift 2 || true
root=$(cd "$(dirname "$0")/.." && pwd)
bin=$out/bin; mkdir -p "$bin"
cd "$root"
CGO_ENABLED=1 go build -o "$bin/mmdesk" . 2>/dev/null
CGO_ENABLED=1 go build -o "$bin/anim" ./scripts/anim 2>/dev/null
export TANGO_LAUNCH_BACKGROUND=1
"$bin/anim" >"$out/anim.out" 2>&1 & anim=$!
sleep 2
"$bin/mmdesk" -host -autoaccept -password 123456 -log "$out/host.jsonl" "$@" >"$out/host.out" 2>&1 & host=$!
for _ in $(seq 1 50); do grep -q 'listening' "$out/host.out" 2>/dev/null && break; sleep 0.2; done
"$bin/mmdesk" -connect 127.0.0.1:21118 -password 123456 -view-only -log "$out/view.jsonl" >"$out/view.out" 2>&1 & view=$!
sleep "$secs"
kill $view 2>/dev/null || true; sleep 1
kill $host 2>/dev/null || true; kill $anim 2>/dev/null || true
wait 2>/dev/null || true
go run ./scripts/summary "$out/host.jsonl" "$out/view.jsonl" | tee "$out/summary.txt"
