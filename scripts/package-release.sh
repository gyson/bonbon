#!/bin/sh
# Called by make release-assets after the frontend has been built.
set -eu
version=${1:?Usage: sh scripts/package-release.sh VERSION}
printf '%s\n' "$version" | LC_ALL=C grep -Eq '^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$' || {
    printf 'Release version must have the form 0.0.1\n' >&2
    exit 1
}
cd "$(dirname "$0")/.."
output=bin/releases/v$version
# Require a fresh output directory so stale assets cannot enter a release.
mkdir -p bin/releases
mkdir "$output"
for arch in arm64 amd64; do
    stage=$(mktemp -d "$output/.stage.XXXXXX")
    trap 'rm -rf "$stage"' EXIT
    CGO_ENABLED=0 GOOS=darwin GOARCH=$arch go build -trimpath \
        -ldflags "-X main.instanceDirName=.bonbon -X main.version=$version" \
        -o "$stage/bonbon" ./cmd/bonbon
    tar -czf "$output/bonbon_${version}_darwin_${arch}.tar.gz" -C "$stage" bonbon
    rm -rf "$stage"
done
cp internal/install/install.sh "$output/install.sh"
(cd "$output" && shasum -a 256 ./*.tar.gz install.sh | sed 's|  ./|  |' > checksums.txt)
printf 'Release assets: %s\n' "$output"
