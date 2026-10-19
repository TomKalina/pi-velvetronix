#!/bin/sh
# Installs or updates pi-velvetronix on the Pi. Runs as root from the unpacked
# install bundle: pi-velvetronix-install-<ver>/deploy/install-remote.sh.
#
# stdin: optional lines WEB_PASSWORD=... and AP_PSK=... (scripts/install.sh sends
# secrets/velvetronix.env). A missing or empty value keeps the existing secret.
# Safe to re-run. Touches no Wi-Fi profile except its own hotspot vox-ap.
set -eu
umask 022

# Read secrets before anything else, then detach stdin so no later command can
# consume it or wait on it.
WEB_PASSWORD=''
AP_PSK=''
cr=$(printf '\r')
if [ ! -t 0 ]; then
    while IFS= read -r line || [ -n "$line" ]; do
        line=${line%"$cr"}
        case "$line" in
            WEB_PASSWORD=*) WEB_PASSWORD=${line#WEB_PASSWORD=} ;;
            AP_PSK=*) AP_PSK=${line#AP_PSK=} ;;
        esac
    done
fi
exec < /dev/null

die() {
    echo "install-remote: $*" >&2
    exit 1
}

B=/opt/pi-velvetronix
S=/var/lib/pi-velvetronix
U=/var/lib/pi-velvetronix-update
LIB=/usr/local/lib/pi-velvetronix
UNITS=/etc/systemd/system
RULES=/etc/polkit-1/rules.d/50-pi-velvetronix.rules
KEYFILE=/etc/NetworkManager/system-connections/vox-ap.nmconnection
AP_SSID=VOX-Velvetronix
AP_IP=10.42.0.1

# --- checks (nothing is changed before they all pass) -------------------------

[ "$(id -u)" -eq 0 ] || die "must run as root"
[ "$(uname -m)" = aarch64 ] || die "expected aarch64, got $(uname -m)"
for c in systemctl systemd-sysusers nmcli runuser sha256sum od; do
    command -v "$c" > /dev/null || die "missing command: $c"
done
nmcli -t general status > /dev/null || die "NetworkManager is not running"
# Existing dir is left as is: polkitd owns its permissions.
[ -d "$(dirname "$RULES")" ] || die "missing $(dirname "$RULES") (polkitd not installed?)"

here=$(cd "$(dirname "$0")" && pwd)
bundle=$(dirname "$here")
bin=$bundle/pi-velvetronix
manifest=$bundle/manifest.json
{ [ -f "$bin" ] && [ -f "$manifest" ]; } || die "not an install bundle: $bundle"

ver=$(sed -n 's/.*"version"[[:space:]]*:[[:space:]]*"\([0-9A-Za-z._-]*\)".*/\1/p' "$manifest")
case "$ver" in '' | .* | rollback | request) die "bad version in manifest.json: '$ver'" ;; esac
[ "${#ver}" -le 64 ] || die "version too long"
want=$(sed -n 's/.*"sha256"[[:space:]]*:[[:space:]]*"\([0-9a-f]\{64\}\)".*/\1/p' "$manifest")
have=$(sha256sum "$bin" | cut -d' ' -f1)
{ [ -n "$want" ] && [ "$want" = "$have" ]; } || die "binary sha256 does not match manifest.json"
[ "$(od -An -tx1 -N4 "$bin" | tr -d ' \n')" = 7f454c46 ] || die "binary is not ELF"
[ "$(od -An -tx1 -j18 -N2 "$bin" | tr -d ' \n')" = b700 ] || die "binary is not aarch64"

if [ -n "$WEB_PASSWORD" ]; then
    [ "${#WEB_PASSWORD}" -ge 8 ] && [ "${#WEB_PASSWORD}" -le 72 ] ||
        die "WEB_PASSWORD must be 8..72 bytes"
fi
if [ -n "$AP_PSK" ]; then
    case "$AP_PSK" in *\\*) die "AP_PSK must not contain a backslash" ;; esac
    printf '%s\n' "$AP_PSK" | LC_ALL=C grep -qx '[[:graph:]]\{8,63\}' ||
        die "AP_PSK must be 8..63 printable ASCII characters without spaces"
fi

# Hotspot: keep the UUID (and the PSK unless a new one was given) of our keyfile.
ap_uuid=''
ap_psk=$AP_PSK
if [ -f "$KEYFILE" ]; then
    ap_uuid=$(sed -n 's/^uuid=//p' "$KEYFILE" | head -n 1)
    [ -n "$ap_psk" ] || ap_psk=$(sed -n 's/^psk=//p' "$KEYFILE" | head -n 1)
fi
[ -n "$ap_psk" ] || die "AP_PSK is required on first install (no existing $KEYFILE)"
other=$(nmcli -t -e yes -f NAME,UUID,FILENAME connection show |
    awk -F: -v kf="$KEYFILE" '$1 == "vox-ap" && $3 != kf { print $2 " " $3 }')
[ -z "$other" ] || die "another profile named vox-ap exists ($other); delete or rename it first"

# --- system user, scripts, units, config ---------------------------------------

install -D -m 0644 "$here/sysusers/pi-velvetronix.conf" /etc/sysusers.d/pi-velvetronix.conf
systemd-sysusers /etc/sysusers.d/pi-velvetronix.conf

install -d -m 0755 "$LIB"
install -m 0755 -o root -g root "$here/lib/boot-guard" "$here/lib/apply-update" "$LIB/"
if [ -f "$bundle/fakeamp" ]; then
    install -m 0755 -o root -g root "$bundle/fakeamp" "$LIB/fakeamp"
fi

install -m 0644 -o root -g root \
    "$here/systemd/pi-velvetronix.service" \
    "$here/systemd/pi-velvetronix-updater.path" \
    "$here/systemd/pi-velvetronix-updater.service" \
    "$UNITS/"

cat "$here/polkit/50-pi-velvetronix.rules" > "$RULES.tmp"
chmod 0644 "$RULES.tmp"
mv -f "$RULES.tmp" "$RULES"

reboot_note=''
# put SRC DST: install a 0644 config file, remember when it changed.
put() {
    if ! cmp -s "$1" "$2"; then
        install -D -m 0644 -o root -g root "$1" "$2"
        reboot_note="$reboot_note $2"
    fi
}
put "$here/swap/90-pi-velvetronix.conf" /etc/rpi/swap.conf.d/90-pi-velvetronix.conf
put "$here/journald/90-pi-velvetronix.conf" /etc/systemd/journald.conf.d/90-pi-velvetronix.conf

# Headless amp controller: the per-user audio stack is not needed and can grab the
# amp's MIDI port. Masking is idempotent; units this image does not ship are skipped.
pipewire_units='pipewire.socket pipewire.service pipewire-pulse.socket pipewire-pulse.service wireplumber.service'
for u in $pipewire_units; do
    systemctl --global mask "$u" > /dev/null 2>&1 || true
done

# --- release ------------------------------------------------------------------

setlink() {
    ln -sfn "$2" "$B/$1.new"
    mv -T "$B/$1.new" "$B/$1"
}

install -d -m 0755 "$B" "$B/releases"
rm -rf "$B"/releases/.*.tmp
T=$B/releases/.$ver.tmp
install -d -m 0755 "$T"
install -m 0755 -o root -g root "$bin" "$T/pi-velvetronix"
install -m 0644 -o root -g root "$manifest" "$T/manifest.json"
rm -rf "$B/releases/$ver"
mv -T "$T" "$B/releases/$ver"
# The release must be on disk before current points at it.
sync

old=$(readlink "$B/current" 2>/dev/null || true)
if [ "$old" != "releases/$ver" ]; then
    [ -z "$old" ] || setlink previous "$old"
    setlink current "releases/$ver"
fi
cur=$(readlink "$B/current")
prev=$(readlink "$B/previous" 2>/dev/null || true)
for d in "$B"/releases/*; do
    [ -e "$d" ] || continue
    r=releases/${d##*/}
    [ "$r" = "$cur" ] || [ "$r" = "$prev" ] || rm -rf "$d"
done

# Only $S itself (its parent is root-owned): nothing inside the daemon-owned dir is
# created or chowned by root. The daemon creates inbox/ on start.
install -d -o pi-velvetronix -g pi-velvetronix -m 0750 "$S"
install -d -o root -g root -m 0755 "$U"
# The update state used to live in the daemon-owned $S/update; rm -rf does not follow
# links planted there.
rm -rf "$S/update"
# A manual install supersedes an unconfirmed or interrupted update; its counter must not
# roll back and its marker must not pause the bridge.
rm -f "$U/pending" "$U/applying" "$S/confirmed"
sync

# --- hotspot ------------------------------------------------------------------

[ -n "$ap_uuid" ] || ap_uuid=$(cat /proc/sys/kernel/random/uuid)
# Written as a keyfile so the PSK never appears on a command line. NM ignores
# hidden files, so the temp file is never loaded half-written.
tmp=$(dirname "$KEYFILE")/.vox-ap.nmconnection.tmp
(
    umask 077
    {
        printf '%s\n' '[connection]' 'id=vox-ap' "uuid=$ap_uuid" 'type=wifi' \
            'interface-name=wlan0' 'autoconnect=false' 'autoconnect-priority=-100' ''
        printf '%s\n' '[wifi]' 'mode=ap' "ssid=$AP_SSID" 'band=bg' 'channel=6' ''
        # pmf=1 (disable), as nmcli does for its own hotspots: with the default a Mac
        # (macOS 27) could not join the brcmfmac AP (tested 2026-10-19).
        printf '%s\n' '[wifi-security]' 'pmf=1' 'key-mgmt=wpa-psk' 'proto=rsn;' 'pairwise=ccmp;' \
            'group=ccmp;' "psk=$ap_psk" ''
        printf '%s\n' '[ipv4]' 'method=shared' "address1=$AP_IP/24" ''
        printf '%s\n' '[ipv6]' 'method=ignore'
    } > "$tmp"
)
chown root:root "$tmp"
chmod 0600 "$tmp"
mv -f "$tmp" "$KEYFILE"
nmcli connection reload

# --- services -----------------------------------------------------------------

systemctl daemon-reload
if [ -n "$WEB_PASSWORD" ]; then
    # Stopped first so the running daemon cannot rewrite the invalidated sessions.
    systemctl stop pi-velvetronix.service
    printf '%s\n' "$WEB_PASSWORD" |
        runuser -u pi-velvetronix -- "$B/current/pi-velvetronix" set-password --state "$S"
fi
systemctl enable --now pi-velvetronix-updater.path
systemctl enable pi-velvetronix.service
# restart, not enable --now: a re-run must pick up the new binary and password.
systemctl restart --no-block pi-velvetronix.service

i=0
until systemctl is-active --quiet pi-velvetronix.service; do
    i=$((i + 1))
    if [ "$i" -gt 60 ]; then
        systemctl status --no-pager pi-velvetronix.service || true
        journalctl -u pi-velvetronix.service -n 30 --no-pager || true
        die "pi-velvetronix.service is not active after 60 s"
    fi
    sleep 1
done

# --- summary (no secrets) -----------------------------------------------------

host=$(hostname)
echo
prev_ver=${prev#releases/}
echo "pi-velvetronix $ver is running (previous: ${prev_ver:-none})"
if [ -n "$WEB_PASSWORD" ]; then echo "Web password: updated"; else echo "Web password: unchanged"; fi
if [ -n "$AP_PSK" ]; then echo "Hotspot PSK: updated"; else echo "Hotspot PSK: unchanged"; fi
echo "PipeWire user audio stack: masked for all users ($pipewire_units)"
echo "Open:"
echo "  http://$host.local/"
echo "  http://vox.local/"
for ip in $(hostname -I 2>/dev/null || true); do
    case "$ip" in *:*) ;; *) echo "  http://$ip/" ;; esac
done
echo "Fallback hotspot: SSID $AP_SSID, then http://$AP_IP/"
[ -z "$reboot_note" ] || echo "Reboot to apply:$reboot_note"
