#!/bin/sh
# Builds the web UI (unless --skip-web) and cross-compiles the daemon and fakeamp
# for the Pi (linux/arm64) into dist/. Version: $VERSION or the VERSION file.
set -eu

usage() {
    echo "usage: scripts/build.sh [--skip-web]" >&2
    exit 2
}

skip_web=0
for a in "$@"; do
    case "$a" in
        --skip-web) skip_web=1 ;;
        *) usage ;;
    esac
done

root=$(cd "$(dirname "$0")/.." && pwd)
cd "$root"

VERSION=${VERSION:-$(cat VERSION)}
case "$VERSION" in '' | .* | rollback | request | *[!0-9A-Za-z._-]*) echo "build: bad version '$VERSION'" >&2; exit 1 ;; esac
[ "${#VERSION}" -le 64 ] || { echo "build: version longer than 64 characters" >&2; exit 1; }

if [ "$skip_web" = 0 ]; then
    (cd web && npm ci && npm run build)
fi
if [ ! -f internal/web/dist/index.html.gz ]; then
    echo "build: internal/web/dist/index.html.gz is missing; build the web UI first (run without --skip-web)" >&2
    exit 1
fi

mkdir -p dist
export CGO_ENABLED=0 GOOS=linux GOARCH=arm64 GOARM64=v8.0
go build -trimpath -ldflags "-s -w -buildid= -X main.version=$VERSION" \
    -o dist/pi-velvetronix ./cmd/pi-velvetronix
go build -trimpath -ldflags "-s -w -buildid=" -o dist/fakeamp ./cmd/fakeamp
echo "build: dist/pi-velvetronix and dist/fakeamp, version $VERSION"
