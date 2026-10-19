#!/bin/sh
# Builds a release on the Mac and installs it on the Pi over SSH.
#   scripts/install.sh [user@host]      default velvetronix@velvetronix.local
# The SSH user needs passwordless sudo. Secrets live in secrets/velvetronix.env
# (generated on first run, gitignored) and reach the Pi only via SSH stdin.
set -eu

die() {
    echo "install: $*" >&2
    exit 1
}

[ "$#" -le 1 ] || die "usage: scripts/install.sh [user@host]"
target=${1:-velvetronix@velvetronix.local}

root=$(cd "$(dirname "$0")/.." && pwd)
cd "$root"
VERSION=${VERSION:-$(cat VERSION)}
case "$VERSION" in '' | .* | rollback | request | *[!0-9A-Za-z._-]*) die "bad version '$VERSION'" ;; esac
export VERSION

secrets=$root/secrets/velvetronix.env
# Unambiguous alphabet: no 0/O/o, 1/l/I.
alphabet='A-HJ-NP-Za-km-np-z2-9'
random() { LC_ALL=C tr -dc "$alphabet" < /dev/urandom | head -c "$1"; }
if [ ! -f "$secrets" ]; then
    (
        umask 077
        mkdir -p "$(dirname "$secrets")"
        printf 'WEB_PASSWORD=%s\nAP_PSK=%s\n' "$(random 16)" "$(random 20)" > "$secrets.tmp"
        mv -f "$secrets.tmp" "$secrets"
    )
    echo "install: generated new secrets in $secrets"
fi
chmod 600 "$secrets"

scripts/build.sh
scripts/release.sh

name=pi-velvetronix-install-$VERSION
bundle=dist/$name.tar.gz
[ -f "$bundle" ] || die "$bundle is missing"

remote_tar=/tmp/$name.tar.gz
scp -q "$bundle" "$target:$remote_tar"
# VERSION is validated above, so it is safe in the remote command.
# shellcheck disable=SC2029 # client-side expansion is intended
ssh "$target" "set -e
d=\$(mktemp -d)
trap 'rm -rf \"\$d\" $remote_tar' EXIT
tar -xzf $remote_tar -C \"\$d\"
sudo -n sh -c \"cd \$d/$name/deploy && ./install-remote.sh\"" < "$secrets"

host=${target#*@}
echo
echo "install: done. Secrets: $secrets (not printed; WEB_PASSWORD is the web login)"
echo "install: open http://$host/ or http://vox.local/"
