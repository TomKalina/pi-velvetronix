#!/bin/sh
# Packs the output of scripts/build.sh into
#   dist/pi-velvetronix-<ver>-linux-arm64.tar.gz   update package (design.md §6)
#   dist/pi-velvetronix-install-<ver>.tar.gz       install bundle for deploy/install-remote.sh
# Both are byte-reproducible: ustar, owner root/0, normalized modes, sorted entries,
# mtime = SOURCE_DATE_EPOCH (default: last commit time), gzip -n.
set -eu

die() {
    echo "release: $*" >&2
    exit 1
}

root=$(cd "$(dirname "$0")/.." && pwd)
cd "$root"

VERSION=${VERSION:-$(cat VERSION)}
case "$VERSION" in '' | .* | rollback | request | *[!0-9A-Za-z._-]*) die "bad version '$VERSION'" ;; esac
[ "${#VERSION}" -le 64 ] || die "version longer than 64 characters"

epoch=${SOURCE_DATE_EPOCH:-$(git log -1 --format=%ct 2> /dev/null || true)}
case "$epoch" in '' | *[!0-9]*) die "no commit time available; set SOURCE_DATE_EPOCH" ;; esac

utc() { date -u -r "$epoch" "$1" 2> /dev/null || date -u -d "@$epoch" "$1"; }
built=$(utc +%Y-%m-%dT%H:%M:%SZ)
stamp=$(utc +%Y%m%d%H%M.%S)

sha256() {
    if command -v sha256sum > /dev/null; then sha256sum "$1"; else shasum -a 256 "$1"; fi | cut -d' ' -f1
}

# Only bsdtar (macOS tar, libarchive-tools) is supported: GNU tar spells the
# ownership/ordering options differently.
TAR=tar
command -v bsdtar > /dev/null && TAR=bsdtar
"$TAR" --version 2> /dev/null | grep -q bsdtar || die "bsdtar is required"

for f in dist/pi-velvetronix dist/fakeamp; do
    [ -f "$f" ] || die "$f is missing; run scripts/build.sh first"
done

work=$(mktemp -d "${TMPDIR:-/tmp}/pvx-release.XXXXXX")
trap 'rm -rf "$work"' EXIT
trap 'exit 1' HUP INT TERM

# normalize DIR: modes independent of umask and checkout, fixed mtime (UTC).
normalize() {
    find "$1" -type d -exec chmod 0755 {} +
    find "$1" -type f -perm -u+x -exec chmod 0755 {} +
    find "$1" -type f ! -perm -u+x -exec chmod 0644 {} +
    TZ=UTC0 find "$1" -exec touch -h -t "$stamp" {} +
}

# pack DIR OUT: archive the names read from stdin (relative to DIR), in that order.
# Two steps instead of a pipe so a tar failure is not masked by gzip.
pack() {
    (cd "$1" && COPYFILE_DISABLE=1 "$TAR" -c -n -f "$work/out.tar" -T - --format ustar \
        --uid 0 --gid 0 --uname root --gname root \
        --no-xattrs --no-acls --no-fflags --no-mac-metadata)
    gzip -n -9 -c "$work/out.tar" > "$2.tmp"
    rm -f "$work/out.tar"
    mv -f "$2.tmp" "$2"
}

# Update package: exactly manifest.json + pi-velvetronix.
pkg=$work/pkg
mkdir "$pkg"
cp dist/pi-velvetronix "$pkg/pi-velvetronix"
chmod 0755 "$pkg/pi-velvetronix"
printf '{"name":"pi-velvetronix","version":"%s","arch":"linux/arm64","binary":"pi-velvetronix","sha256":"%s","built":"%s"}\n' \
    "$VERSION" "$(sha256 "$pkg/pi-velvetronix")" "$built" > "$pkg/manifest.json"
normalize "$pkg"
update=dist/pi-velvetronix-$VERSION-linux-arm64.tar.gz
printf '%s\n' manifest.json pi-velvetronix | pack "$pkg" "$update"
[ "$("$TAR" -tzf "$update" | tr '\n' ' ')" = "manifest.json pi-velvetronix " ] ||
    die "unexpected entries in $update"

# Install bundle: pi-velvetronix-install-<ver>/{pi-velvetronix,manifest.json,fakeamp,deploy/}.
name=pi-velvetronix-install-$VERSION
b=$work/bundle/$name
mkdir -p "$b"
cp "$pkg/pi-velvetronix" "$pkg/manifest.json" "$b/"
cp dist/fakeamp "$b/fakeamp"
chmod 0755 "$b/fakeamp"
cp -R deploy "$b/deploy"
find "$b" -name .DS_Store -exec rm -f {} +
normalize "$work/bundle"
bundle=dist/$name.tar.gz
(cd "$work/bundle" && find "$name" | LC_ALL=C sort) | pack "$work/bundle" "$bundle"

echo "release: $VERSION (built $built)"
echo "  $(sha256 "$update")  $update"
echo "  $(sha256 "$bundle")  $bundle"
