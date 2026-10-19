#!/bin/sh
# Runs all checks: web tests, Go vet + tests, then the cross build.
# The web UI is built before the Go steps because internal/web embeds its output.
set -eu
root=$(cd "$(dirname "$0")/.." && pwd)
cd "$root"

(cd web && npm ci && npm test && npm run build)
go vet ./...
go test ./...
scripts/build.sh --skip-web
