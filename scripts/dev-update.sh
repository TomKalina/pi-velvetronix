#!/bin/sh
# Uploads an update package to a running Pi through the HTTP API and waits until
# the new version is running.
#   scripts/dev-update.sh [host] [package]
#   host     default velvetronix.local
#   package  default dist/pi-velvetronix-<VERSION>-linux-arm64.tar.gz
# The web password is read from secrets/velvetronix.env and never printed.
set -eu

die() {
    echo "dev-update: $*" >&2
    exit 1
}

[ "$#" -le 2 ] || die "usage: scripts/dev-update.sh [host] [package]"
root=$(cd "$(dirname "$0")/.." && pwd)
host=${1:-velvetronix.local}
VERSION=${VERSION:-$(cat "$root/VERSION")}
pkg=${2:-$root/dist/pi-velvetronix-$VERSION-linux-arm64.tar.gz}
secrets=$root/secrets/velvetronix.env
base=http://$host
[ -f "$pkg" ] || die "$pkg is missing; run scripts/build.sh and scripts/release.sh"
[ -f "$secrets" ] || die "$secrets is missing"

tmp=$(mktemp -d "${TMPDIR:-/tmp}/pvx-dev-update.XXXXXX")
trap 'rm -rf "$tmp"' EXIT
trap 'exit 1' HUP INT TERM
jar=$tmp/cookies
out=$tmp/body

# api METHOD PATH [curl args...]: prints the HTTP status, body goes to $out.
api() {
    m=$1
    p=$2
    shift 2
    curl -sS -o "$out" -w '%{http_code}' -b "$jar" -c "$jar" -X "$m" "$@" "$base$p"
}

field() { sed -n "s/.*\"$1\"[[:space:]]*:[[:space:]]*\"\\([^\"]*\\)\".*/\\1/p" "$out"; }

# The password goes to curl on stdin (JSON-escaped), never on argv.
code=$(sed -n 's/^WEB_PASSWORD=//p' "$secrets" | head -n 1 | tr -d '\r' |
    sed 's/[\\"]/\\&/g; s/.*/{"password":"&"}/' |
    api POST /api/login -H 'Content-Type: application/json' --data-binary @-)
[ "$code" = 200 ] || die "login failed: HTTP $code $(cat "$out")"

code=$(api GET /api/update)
[ "$code" = 200 ] || die "GET /api/update: HTTP $code $(cat "$out")"
before=$(field current_version)
before_result=$(field last_result)
[ -n "$before" ] || die "unexpected /api/update response: $(cat "$out")"
echo "dev-update: running $before, uploading $(basename "$pkg")"

code=$(api POST /api/update -H 'Content-Type: application/gzip' --data-binary "@$pkg")
cat "$out"
echo
[ "$code" = 202 ] || die "upload rejected: HTTP $code"

# The service restarts during the update, so failed requests are expected here.
i=0
while [ "$i" -lt 60 ]; do
    sleep 2
    i=$((i + 1))
    code=$(api GET /api/update 2> /dev/null) || continue
    [ "$code" = 200 ] || continue
    now=$(field current_version)
    result=$(field last_result)
    if [ -n "$now" ] && [ "$now" != "$before" ]; then
        cat "$out"
        echo
        echo "dev-update: $before -> $now"
        exit 0
    fi
    case "$result" in
        "$before_result") ;;
        failed* | rolled-back*) cat "$out"; echo; die "$result" ;;
    esac
done
cat "$out" 2> /dev/null || true
echo
die "version still $before after 120 s"
