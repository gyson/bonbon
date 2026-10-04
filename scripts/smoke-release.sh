#!/bin/sh
# Exercise a release executable with an isolated instance and no desktop menu.
set -eu
binary=${1:?Usage: sh scripts/smoke-release.sh BINARY VERSION}
version=${2:?Expected release version is required}
[ "$("$binary" --version)" = "bonbon $version" ]
instance=$(mktemp -d "${TMPDIR:-/tmp}/bonbon-smoke.XXXXXX")
cleanup() {
    "$binary" --dir "$instance" server stop || return 1
    rm -rf "$instance"
}
trap cleanup EXIT
trap 'exit 1' HUP INT TERM
output=$("$binary" --dir "$instance" server start --no-menubar)
printf '%s\n' "$output"
url=$(printf '%s\n' "$output" | grep -Eo 'http://127\.0\.0\.1:[0-9]+' | head -1)
[ -n "$url" ]
curl -fsS "$url/" > "$instance/index.html"
grep -q '<title>BonBon</title>' "$instance/index.html"
result=$("$binary" --dir "$instance" query 'SELECT 1 AS value')
[ "$result" = '{"columns":["value"],"rows":[[1]],"truncated":false}' ]
printf 'Release smoke check passed for %s\n' "$version"
