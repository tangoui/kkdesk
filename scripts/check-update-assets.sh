#!/bin/bash
# Use a temporary signing key to exercise the release manifest and patch path.
set -euo pipefail

build=$(cd "$1" && pwd)
out=$(cd "$2" && pwd)/test-update-assets
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
mkdir "$work/previous-project"
version=$(jq -er .version tan.json)
previous=0.0.0
jq --arg version "$previous" '.version = $version' tan.json > "$work/previous-project/tan.json"
# The previous manifest uses the same executable bytes. This reliably
# yields a small patch even when the actual previous release is unavailable.
tan keygen -o "$work/test-key"
tan release-manifest -C "$work/previous-project" -build "$build" -key "$work/test-key.key" \
    -windows exe -linux dir -o "$work/previous"
tan release-manifest -build "$build" -key "$work/test-key.key" -windows exe -linux dir \
    -prev "$work/previous" -o "$out"
manifest="$out/latest.json"
jq -e --arg version "$version" '
    (.signature | length > 0) and
    (.manifest.app == "com.kkdesk.desktop") and (.manifest.version == $version) and
    (.manifest.platforms | length == 5)
' "$manifest" >/dev/null

check_platform() {
    local platform=$1 format=$2 suffix=$3
    local asset="kkdesk-$version-$platform$suffix"
    jq -e --arg platform "$platform" --arg format "$format" --arg asset "$asset" --arg previous "$previous" '
        .manifest.platforms[$platform] |
        .format == $format and .url == $asset and
        (.patches | length == 1 and .[0].from == $previous)
    ' "$manifest" >/dev/null
    local url digest
    while IFS=$'\t' read -r url digest; do
        printf '%s  %s\n' "$digest" "$out/$url" | shasum -a 256 --check
    done < <(jq -r --arg platform "$platform" '
        .manifest.platforms[$platform] | ., .patches[] | [.url, .sha256] | @tsv
    ' "$manifest")
}

check_platform darwin-arm64 app .app.tar.gz
check_platform darwin-amd64 app .app.tar.gz
check_platform windows-amd64 exe .exe
check_platform linux-amd64 dir .tar.gz
check_platform linux-arm64 dir .tar.gz
cmp "$build/windows-amd64/kkdesk.exe" "$out/kkdesk-$version-windows-amd64.exe"
