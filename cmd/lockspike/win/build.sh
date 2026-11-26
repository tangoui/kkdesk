#!/bin/bash
set -euo pipefail
src=$(cd "$(dirname "$0")" && pwd)
repo=$(cd "$src/../../.." && pwd)
out=${1:-/Users/admin/min/outputs/kkdesk-lockscreen-spike/win}
mkdir -p "$out"
out=$(cd "$out" && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
cd "$repo"

export GOOS=windows GOARCH=amd64 CGO_ENABLED=1
export CC=x86_64-w64-mingw32-gcc CXX=x86_64-w64-mingw32-g++
exe=kkdesk-lockspike.exe
go build -trimpath -o "$tmp/$exe" ./cmd/lockspike/win
x86_64-w64-mingw32-objdump -p "$tmp/$exe" >"$tmp/imports.txt"
dlls=$(awk '/DLL Name:/ {print tolower($3)}' "$tmp/imports.txt" | sort -u)
[[ -n $dlls ]]
unexpected=$(printf '%s\n' "$dlls" | grep -Ev '^(d3d11|dxgi|kernel32|user32|wtsapi32|ucrtbase|msvcrt|api-ms-win-crt-[a-z0-9-]+)\.dll$' || true)
if [[ -n $unexpected ]]; then
    printf 'Unexpected imported DLLs:\n%s\n' "$unexpected" >&2
    exit 1
fi
{
    go version
    file "$tmp/$exe" | cut -d: -f2-
    printf 'Imported system DLLs:\n%s\n' "$dlls"
} >"$tmp/build.log"
(cd "$tmp" && shasum -a 256 "$exe" > SHA256SUMS)
install -m 0644 "$tmp/$exe" "$tmp/build.log" "$tmp/SHA256SUMS" "$src/README.md" "$out"
