#!/bin/sh
# Tests for deploy/ and scripts/release.sh:
#   - syntax (sh -n / dash -n), shellcheck when installed
#   - polkit rule (node)
#   - release.sh determinism with dummy binaries (when dist/ has no real build)
#   - install-remote.sh, apply-update and boot-guard end to end as root inside a
#     throwaway debian:trixie-slim container (docker), with systemctl, nmcli,
#     runuser and systemd-sysusers replaced by logging shims
# Usage: scripts/test-deploy.sh            (on the Mac)
#        scripts/test-deploy.sh --inner    (inside the container; do not run elsewhere)
set -eu

root=$(cd "$(dirname "$0")/.." && pwd)
fails=0
passes=0

ok() {
    passes=$((passes + 1))
}

bad() {
    fails=$((fails + 1))
    echo "FAIL: $*" >&2
}

check() { # check DESCRIPTION COMMAND...
    d=$1
    shift
    if "$@"; then ok; else bad "$d"; fi
}

summary() {
    echo "$1: $passes passed, $fails failed"
    [ "$fails" -eq 0 ]
}

# --- inside the container -----------------------------------------------------

inner() {
    [ -f /.dockerenv ] || { echo "--inner runs only inside the test container" >&2; exit 2; }
    LOG=/tmp/calls.log
    mkdir -p /shim /etc/polkit-1/rules.d /etc/NetworkManager/system-connections
    : > "$LOG"
    cat > /shim/systemctl << 'EOF'
#!/bin/sh
echo "systemctl $*" >> /tmp/calls.log
case "$*" in
    "show -P ActiveState "*) echo "${UPDATER_STATE:-inactive}" ;;
esac
exit 0
EOF
    # FAIL_CP=1 makes every cp fail, for the unexpected-error (io-error) path.
    cat > /shim/cp << 'EOF'
#!/bin/sh
[ -z "${FAIL_CP:-}" ] || exit 1
exec /bin/cp "$@"
EOF
    cat > /shim/nmcli << 'EOF'
#!/bin/sh
echo "nmcli $*" >> /tmp/calls.log
case "$*" in
    *"connection show") [ -z "${NMCLI_SHOW:-}" ] || printf '%s\n' "$NMCLI_SHOW" ;;
esac
exit 0
EOF
    cat > /shim/runuser << 'EOF'
#!/bin/sh
echo "runuser $*" >> /tmp/calls.log
cat > /tmp/runuser.stdin
EOF
    cat > /shim/systemd-sysusers << 'EOF'
#!/bin/sh
echo "systemd-sysusers $*" >> /tmp/calls.log
getent passwd pi-velvetronix > /dev/null ||
    useradd --system --user-group --home-dir /var/lib/pi-velvetronix --shell /usr/sbin/nologin pi-velvetronix
usermod -aG audio pi-velvetronix
EOF
    chmod 0755 /shim/*
    PATH=/shim:$PATH
    export PATH

    # mkbundle VER [ELF machine bytes]: dummy install bundle in /work/b-VER
    mkbundle() {
        b=/work/b-$1/pi-velvetronix-install-$1
        rm -rf "/work/b-$1"
        mkdir -p "$b"
        cp -R /src/deploy "$b/deploy"
        mkelf "$b/pi-velvetronix" "${2:-\267\000}" "$1"
        printf 'fakeamp\n' > "$b/fakeamp"
        mkmanifest "$1" "$b/pi-velvetronix" > "$b/manifest.json"
        echo "$b"
    }
    # mkelf FILE MACHINE TAG: 64-bit LE ELF header with e_machine=MACHINE, then TAG
    mkelf() {
        # shellcheck disable=SC2059 # MACHINE is an octal escape on purpose
        printf "\177ELF\002\001\001\000\000\000\000\000\000\000\000\000\002\000$2" > "$1"
        printf '%s\n' "$3" >> "$1"
        chmod 0755 "$1"
    }
    mkmanifest() { # mkmanifest VER BINARY
        printf '{"name":"pi-velvetronix","version":"%s","arch":"linux/arm64","binary":"pi-velvetronix","sha256":"%s","built":"2026-10-19T10:00:00Z"}\n' \
            "$1" "$(sha256sum "$2" | cut -d' ' -f1)"
    }
    secrets() { printf 'WEB_PASSWORD=%s\r\nAP_PSK=%s\n' "$1" "$2"; }
    called() { grep -qF -- "$1" "$LOG"; }
    link() { readlink "/opt/pi-velvetronix/$1" 2> /dev/null || true; }
    mode() { stat -c '%a %U:%G' "$1"; }
    kf=/etc/NetworkManager/system-connections/vox-ap.nmconnection
    B=/opt/pi-velvetronix
    S=/var/lib/pi-velvetronix
    U=/var/lib/pi-velvetronix-update

    # install-remote: refuses a first install without AP_PSK, changes nothing
    b=$(mkbundle 0.1.0)
    if printf 'WEB_PASSWORD=pw-1234567\n' | "$b/deploy/install-remote.sh" > /dev/null 2>&1; then
        bad "first install without AP_PSK must fail"
    else ok; fi
    check "nothing installed after refused install" test ! -e "$B"

    # install-remote: first install
    : > "$LOG"
    if secrets 'web pass "x" 1' 'Ap-Psk-123456789012' | "$b/deploy/install-remote.sh" > /tmp/out 2>&1; then ok; else
        bad "first install failed"
        cat /tmp/out >&2
    fi
    check "current -> 0.1.0" test "$(link current)" = releases/0.1.0
    check "no previous after first install" test -z "$(link previous)"
    check "release binary 0755 root" test "$(mode $B/releases/0.1.0/pi-velvetronix)" = "755 root:root"
    check "release manifest 0644 root" test "$(mode $B/releases/0.1.0/manifest.json)" = "644 root:root"
    check "boot-guard installed 0755" test "$(mode /usr/local/lib/pi-velvetronix/boot-guard)" = "755 root:root"
    check "apply-update installed" test -x /usr/local/lib/pi-velvetronix/apply-update
    check "fakeamp installed" test -x /usr/local/lib/pi-velvetronix/fakeamp
    check "units installed" test -f /etc/systemd/system/pi-velvetronix.service -a \
        -f /etc/systemd/system/pi-velvetronix-updater.path -a -f /etc/systemd/system/pi-velvetronix-updater.service
    check "sysusers conf" test -f /etc/sysusers.d/pi-velvetronix.conf
    check "user in audio" sh -c 'id -nG pi-velvetronix | grep -qw audio'
    check "swap conf" grep -qx 'Mechanism=zram' /etc/rpi/swap.conf.d/90-pi-velvetronix.conf
    check "journald conf" grep -qx 'RuntimeMaxUse=16M' /etc/systemd/journald.conf.d/90-pi-velvetronix.conf
    check "polkit rule installed" grep -q '"pi-velvetronix"' /etc/polkit-1/rules.d/50-pi-velvetronix.rules
    check "state dirs" test "$(mode $S) $(mode $U)" = "750 pi-velvetronix:pi-velvetronix 755 root:root"
    check "no update dir in state" test ! -e $S/update
    check "inbox left to the daemon" test ! -e $S/inbox
    for u in pipewire.socket pipewire.service pipewire-pulse.socket pipewire-pulse.service wireplumber.service; do
        check "masked $u" called "systemctl --global mask $u"
    done
    check "pipewire mask printed" grep -q 'PipeWire user audio stack: masked' /tmp/out
    check "keyfile 0600 root" test "$(mode $kf)" = "600 root:root"
    check "keyfile psk" grep -qx 'psk=Ap-Psk-123456789012' "$kf"
    for l in id=vox-ap type=wifi autoconnect=false mode=ap ssid=VOX-Velvetronix band=bg channel=6 \
        pmf=1 key-mgmt=wpa-psk 'proto=rsn;' 'pairwise=ccmp;' 'group=ccmp;' method=shared \
        address1=10.42.0.1/24 method=ignore; do
        check "keyfile has $l" grep -qxF "$l" "$kf"
    done
    uuid=$(sed -n 's/^uuid=//p' "$kf")
    check "keyfile uuid" sh -c "printf '%s' '$uuid' | grep -Eq '^[0-9a-f-]{36}$'"
    check "nmcli reload" called "nmcli connection reload"
    check "password via stdin" test "$(cat /tmp/runuser.stdin)" = 'web pass "x" 1'
    check "set-password argv" called "runuser -u pi-velvetronix -- /opt/pi-velvetronix/current/pi-velvetronix set-password --state /var/lib/pi-velvetronix"
    check "no secret in calls" sh -c "! grep -q -e 'web pass' -e Ap-Psk $LOG"
    check "no secret in output" sh -c "! grep -q -e 'web pass' -e Ap-Psk /tmp/out"
    check "updater path enabled" called "systemctl enable --now pi-velvetronix-updater.path"
    check "service restarted" called "systemctl restart --no-block pi-velvetronix.service"
    check "summary URLs" grep -q 'http://vox.local/' /tmp/out

    # install-remote: re-run without secrets keeps them; it also clears update state
    # and removes the old daemon-owned update dir without following links in it
    : > "$LOG"
    rm -f /tmp/runuser.stdin
    printf 'keep\n' > /tmp/victim
    mkdir -p $S/update
    ln -s /tmp/victim $S/update/pending
    printf '0.0.9 1\n' > $U/pending
    printf '0.0.9\n' > $U/applying
    printf '0.0.9\n' > $S/confirmed
    mkdir -p $B/releases/.0.0.9.tmp
    if "$b/deploy/install-remote.sh" < /dev/null > /tmp/out 2>&1; then ok; else
        bad "re-run failed"
        cat /tmp/out >&2
    fi
    check "re-run keeps uuid" grep -qx "uuid=$uuid" "$kf"
    check "re-run keeps psk" grep -qx 'psk=Ap-Psk-123456789012' "$kf"
    check "re-run no set-password" test ! -e /tmp/runuser.stdin
    check "re-run current unchanged" test "$(link current)" = releases/0.1.0
    check "re-run no previous" test -z "$(link previous)"
    check "re-run removed old update dir" test ! -e $S/update
    check "re-run left link target alone" test "$(cat /tmp/victim)" = keep
    check "re-run cleared pending applying confirmed" test ! -e $U/pending -a ! -e $U/applying -a ! -e $S/confirmed
    check "re-run pruned stale temp release" test ! -e $B/releases/.0.0.9.tmp

    # install-remote: new version, conflicting profile, bad bundle
    b2=$(mkbundle 0.1.1)
    if secrets '' 'New-Psk-99999999' | "$b2/deploy/install-remote.sh" > /tmp/out 2>&1; then ok; else
        bad "install 0.1.1 failed"
        cat /tmp/out >&2
    fi
    check "current -> 0.1.1" test "$(link current)" = releases/0.1.1
    check "previous -> 0.1.0" test "$(link previous)" = releases/0.1.0
    check "new psk" grep -qx 'psk=New-Psk-99999999' "$kf"
    check "polkit user check" grep -q 'subject.user !== "pi-velvetronix"' /etc/polkit-1/rules.d/50-pi-velvetronix.rules
    if NMCLI_SHOW='vox-ap:1111:/etc/NetworkManager/system-connections/other.nmconnection' \
        "$b2/deploy/install-remote.sh" < /dev/null > /dev/null 2>&1; then
        bad "conflicting vox-ap profile must fail"
    else ok; fi
    printf 'x' >> "$b2/pi-velvetronix"
    if "$b2/deploy/install-remote.sh" < /dev/null > /dev/null 2>&1; then bad "bad sha256 must fail"; else ok; fi
    if secrets '' 'short' | "$b/deploy/install-remote.sh" > /dev/null 2>&1; then bad "short PSK must fail"; else ok; fi
    check "keyfile untouched by refused runs" grep -qx 'psk=New-Psk-99999999' "$kf"

    # apply-update
    AU=/usr/local/lib/pi-velvetronix/apply-update
    BG=/usr/local/lib/pi-velvetronix/boot-guard
    result() { cat $U/last-result; }
    stage() { # stage VER [MACHINE]
        d=$S/inbox/$1
        mkdir -p "$d"
        chown pi-velvetronix:pi-velvetronix $S/inbox
        mkelf "$d/pi-velvetronix" "${2:-\267\000}" "$1"
        mkmanifest "$1" "$d/pi-velvetronix" > "$d/manifest.json"
        chown -R pi-velvetronix:pi-velvetronix "$d"
        request "$1"
    }
    request() { mkdir -p $S/inbox && printf '%s\n' "$1" > $S/inbox/request; }

    : > "$LOG"
    stage 0.2.0
    printf '0.1.1\n' > $S/confirmed
    mkdir -p $B/releases/.0.0.8.tmp
    check "apply-update runs" sh -c "$AU 2> /dev/null"
    check "installed result" test "$(result)" = "installed 0.2.0"
    check "current -> 0.2.0" test "$(link current)" = releases/0.2.0
    check "previous -> 0.1.1" test "$(link previous)" = releases/0.1.1
    check "pruned 0.1.0" test ! -e $B/releases/0.1.0
    check "pending written" test "$(cat $U/pending)" = "0.2.0 0"
    check "pending root-owned" test "$(mode $U/pending)" = "644 root:root"
    check "old confirmed marker removed" test ! -e $S/confirmed
    check "stale temp release pruned" test ! -e $B/releases/.0.0.8.tmp
    check "applying removed" test ! -e $U/applying
    check "request consumed" test ! -e $S/inbox/request
    check "inbox cleaned" test ! -e $S/inbox/0.2.0
    check "release root-owned" test "$(mode $B/releases/0.2.0/pi-velvetronix) $(mode $B/releases/0.2.0)" = "755 root:root 755 root:root"
    check "restart requested" called "systemctl restart --no-block pi-velvetronix.service"

    # boot-guard: three counted attempts, then rollback
    # (a symlink as confirmed is not followed, even when its target holds the version;
    # a marker of another version does not confirm either)
    printf '0.2.0\n' > /tmp/conf-target
    ln -s /tmp/conf-target $S/confirmed
    for n in 1 2 3; do
        [ "$n" -ne 2 ] || { rm -f $S/confirmed && printf '0.1.1\n' > $S/confirmed; }
        "$BG" 2> /dev/null
        check "attempt $n counted" test "$(cat $U/pending)" = "0.2.0 $n"
    done
    rm -f $S/confirmed
    "$BG" 2> /dev/null
    check "rollback after 3" test "$(link current)" = releases/0.1.1
    check "pending removed" test ! -e $U/pending
    check "rollback result" test "$(result)" = "rolled-back 0.2.0 -> releases/0.1.1"
    check "result root-owned, world-readable" test "$(mode $U/last-result)" = "644 root:root"
    check "boot-guard without pending" "$BG"

    # boot-guard: confirmed marker drops pending without counting, touches nothing else
    printf '0.2.0 1\n' > $U/pending
    printf '0.2.0\n' > $S/confirmed
    "$BG" 2> /dev/null
    check "confirmed drops pending" test ! -e $U/pending
    check "confirmed marker kept" test "$(cat $S/confirmed)" = 0.2.0
    check "confirmed no rollback" test "$(link current)" = releases/0.1.1
    check "confirmed result unchanged" test "$(result)" = "rolled-back 0.2.0 -> releases/0.1.1"
    rm -f $S/confirmed

    # boot-guard: a symlink as pending is removed, its target neither read nor written
    printf '0.2.0 0\n' > /tmp/pend-target
    ln -s /tmp/pend-target $U/pending
    "$BG" 2> /dev/null
    check "pending symlink removed" test ! -e $U/pending -a ! -L $U/pending
    check "pending symlink target untouched" test "$(cat /tmp/pend-target)" = "0.2.0 0"
    check "pending symlink no rollback" test "$(link current)" = releases/0.1.1

    # boot-guard: stale applying (power cut during apply-update) is removed unless the
    # updater is running
    printf '0.3.0\n' > $U/applying
    UPDATER_STATE=activating "$BG" 2> /dev/null
    check "applying kept while updater runs" test -e $U/applying
    UPDATER_STATE=inactive "$BG" 2> /dev/null
    check "stale applying removed" test ! -e $U/applying

    # failures: request consumed, inbox cleaned, nothing installed, no restart
    for c in "sha256" "not-elf" "not-aarch64" "version-in-use" "version-mismatch" "bad-version" "missing-files" "not-regular"; do
        : > "$LOG"
        case "$c" in
            sha256) stage 0.3.0 && printf 'x' >> $S/inbox/0.3.0/pi-velvetronix ;;
            not-elf) stage 0.3.0 && printf 'hello' > $S/inbox/0.3.0/pi-velvetronix &&
                mkmanifest 0.3.0 $S/inbox/0.3.0/pi-velvetronix > $S/inbox/0.3.0/manifest.json ;;
            not-aarch64) stage 0.3.0 '\076\000' ;;
            version-in-use) stage 0.1.1 ;;
            version-mismatch) stage 0.3.0 && sed -i 's/"0.3.0"/"0.3.1"/' $S/inbox/0.3.0/manifest.json ;;
            bad-version) request .. ;;
            missing-files) request 0.9.9 ;;
            not-regular) stage 0.3.0 && mv $S/inbox/0.3.0/pi-velvetronix /tmp/realbin &&
                ln -s /tmp/realbin $S/inbox/0.3.0/pi-velvetronix ;;
        esac
        "$AU" 2> /dev/null || bad "apply-update exit status ($c)"
        check "fails $c" test "$(result)" = "failed $c"
        check "no restart on $c" sh -c "! grep -q restart $LOG"
        check "inbox cleaned on $c" test -z "$(ls -A $S/inbox)"
        check "current kept on $c" test "$(link current)" = releases/0.1.1
        check "update dir intact on $c" test -d $U
        check "applying removed on $c" test ! -e $U/applying
    done
    check "no temp release left" sh -c "! ls -A $B/releases | grep -q '^\.'"

    # request planted as a symlink or a directory: removed, never followed or moved
    printf '0.3.0\n' > /tmp/req-target
    for kind in symlink dir; do
        : > "$LOG"
        stage 0.3.0
        rm -f $S/inbox/request
        if [ "$kind" = symlink ]; then ln -s /tmp/req-target $S/inbox/request; else mkdir $S/inbox/request; fi
        "$AU" 2> /dev/null || bad "apply-update exit status (request $kind)"
        check "request $kind fails" test "$(result)" = "failed bad-request"
        check "request $kind removed" test ! -e $S/inbox/request -a ! -L $S/inbox/request
        check "request $kind not moved" test ! -e $U/applying -a ! -L $U/applying
        check "request $kind no restart" sh -c "! grep -q restart $LOG"
        rm -rf $S/inbox/0.3.0
    done
    check "request symlink target untouched" test "$(cat /tmp/req-target)" = 0.3.0
    request request
    "$AU" 2> /dev/null
    check "version request refused" test "$(result)" = "failed bad-version"

    # unexpected error under set -e (cp fails): io-error result, temp and inbox copies
    # removed, nothing installed
    : > "$LOG"
    stage 0.3.0
    if FAIL_CP=1 "$AU" 2> /dev/null; then bad "apply-update must exit non-zero on io-error"; else ok; fi
    check "io-error result" test "$(result)" = "failed io-error"
    check "io-error no applying" test ! -e $U/applying
    check "io-error no temp release" sh -c "! ls -A $B/releases | grep -q '^\.'"
    check "io-error inbox cleaned" test -z "$(ls -A $S/inbox)"
    check "io-error current kept" test "$(link current)" = releases/0.1.1
    check "io-error no restart" sh -c "! grep -q restart $LOG"

    # manual rollback
    stage 0.4.0
    "$AU" 2> /dev/null
    check "0.4.0 installed" test "$(link current) $(link previous)" = "releases/0.4.0 releases/0.1.1"
    : > "$LOG"
    request rollback
    "$AU" 2> /dev/null
    check "manual rollback swaps" test "$(link current) $(link previous)" = "releases/0.1.1 releases/0.4.0"
    check "manual rollback result" test "$(result)" = "rolled-back 0.4.0 -> releases/0.1.1"
    check "manual rollback clears pending" test ! -e $U/pending
    check "manual rollback restarts" called "systemctl restart --no-block pi-velvetronix.service"
    request rollback
    "$AU" 2> /dev/null
    check "rollback again swaps back" test "$(link current)" = releases/0.4.0
    ln -sfn releases/0.4.0 $B/previous
    request rollback
    "$AU" 2> /dev/null
    check "rollback without distinct previous fails" test "$(result)" = "failed no-previous"

    # scripts/install.sh: secrets generation and the ssh/sudo/stdin chain, with
    # build/release stubbed and scp/ssh/sudo running locally
    mac=/work/mac
    mkdir -p "$mac/scripts" "$mac/dist"
    cp /src/scripts/install.sh "$mac/scripts/"
    echo 0.5.0 > "$mac/VERSION"
    b5=$(mkbundle 0.5.0)
    printf '#!/bin/sh\n' > "$mac/scripts/build.sh"
    printf '#!/bin/sh\ntar -czf %s -C %s pi-velvetronix-install-0.5.0\n' \
        "$mac/dist/pi-velvetronix-install-0.5.0.tar.gz" "$(dirname "$b5")" > "$mac/scripts/release.sh"
    chmod 0755 "$mac/scripts/"*.sh
    cat > /shim/scp << 'EOF'
#!/bin/sh
[ "$1" = -q ] && shift
cp "$1" "${2#*:}"
EOF
    cat > /shim/ssh << 'EOF'
#!/bin/sh
shift
exec sh -c "$1"
EOF
    cat > /shim/sudo << 'EOF'
#!/bin/sh
[ "$1" = -n ] && shift
exec "$@"
EOF
    chmod 0755 /shim/scp /shim/ssh /shim/sudo
    rm -f /tmp/runuser.stdin
    if "$mac/scripts/install.sh" user@localhost > /tmp/out 2>&1; then ok; else
        bad "install.sh failed"
        cat /tmp/out >&2
    fi
    env=$mac/secrets/velvetronix.env
    check "secrets file 0600" test "$(stat -c %a "$env")" = 600
    pw=$(sed -n 's/^WEB_PASSWORD=//p' "$env")
    psk=$(sed -n 's/^AP_PSK=//p' "$env")
    check "generated password" sh -c "printf '%s' '$pw' | grep -Eqx '[A-HJ-NP-Za-km-np-z2-9]{16}'"
    check "generated psk" sh -c "printf '%s' '$psk' | grep -Eqx '[A-HJ-NP-Za-km-np-z2-9]{20}'"
    check "password reached set-password" test "$(cat /tmp/runuser.stdin)" = "$pw"
    check "psk reached keyfile" grep -qx "psk=$psk" "$kf"
    check "0.5.0 installed via install.sh" test "$(link current)" = releases/0.5.0
    check "remote temp dir removed" sh -c '! ls -d /tmp/tmp.* > /dev/null 2>&1'
    check "remote bundle removed" test ! -e /tmp/pi-velvetronix-install-0.5.0.tar.gz
    check "install.sh prints no secret" sh -c "! grep -q -e '$pw' -e '$psk' /tmp/out"
    before=$(cat "$env")
    "$mac/scripts/install.sh" user@localhost > /dev/null 2>&1 || bad "install.sh re-run failed"
    check "install.sh reuses secrets" test "$(cat "$env")" = "$before"

    summary "deploy (container)"
}

# --- on the host ----------------------------------------------------------------

host() {
    cd "$root"
    for f in scripts/*.sh deploy/install-remote.sh deploy/lib/boot-guard deploy/lib/apply-update; do
        check "sh -n $f" sh -n "$f"
        if command -v dash > /dev/null; then check "dash -n $f" dash -n "$f"; fi
    done
    if command -v shellcheck > /dev/null; then
        check "shellcheck" shellcheck -s sh scripts/*.sh deploy/install-remote.sh deploy/lib/boot-guard deploy/lib/apply-update
    else
        echo "shellcheck not installed, skipped"
    fi

    # polkit rule: ES5 syntax via node's parser
    if command -v node > /dev/null; then
        check "polkit rule" node -e '
const fs = require("fs");
const rules = [];
const polkit = { Result: { YES: "yes", NOT_HANDLED: "nh" }, addRule: f => rules.push(f), log() {} };
new Function("polkit", fs.readFileSync("deploy/polkit/50-pi-velvetronix.rules", "utf8"))(polkit);
const nm = "org.freedesktop.NetworkManager.";
const svc = { user: "pi-velvetronix" };
const cases = [
  [nm + "wifi.scan", svc, "yes"],
  [nm + "settings.modify.system", svc, "yes"],
  [nm + "wifi.share.protected", svc, "yes"],
  [nm + "network-control", svc, "yes"],
  [nm + "network-control", { user: "velvetronix" }, "nh"],
  ["org.freedesktop.systemd1.manage-units", svc, "nh"],
  ["org.freedesktop.login1.reboot", svc, "nh"],
];
let bad = 0;
for (const [id, s, want] of cases) {
  const got = rules[0]({ id }, s);
  if (got !== want) { console.error("polkit:", id, JSON.stringify(s), "got", got, "want", want); bad++; }
}
process.exit(bad ? 1 : 0);
'
    else
        echo "node not installed, polkit test skipped"
    fi

    # release.sh: deterministic output; dummies only when there is no real build
    dummies=''
    for f in dist/pi-velvetronix dist/fakeamp; do
        if [ ! -e "$f" ]; then
            mkdir -p dist
            printf 'dummy %s\n' "$f" > "$f"
            chmod 0755 "$f"
            dummies="$dummies $f"
        fi
    done
    v=$(cat VERSION)
    out1=$(SOURCE_DATE_EPOCH=1760868000 scripts/release.sh | sed -n 's/^  //p')
    touch deploy/systemd/pi-velvetronix.service
    out2=$( (umask 077 && SOURCE_DATE_EPOCH=1760868000 scripts/release.sh) | sed -n 's/^  //p')
    check "release.sh deterministic" test "$out1" = "$out2"
    check "update package entries" test "$(tar -tzf "dist/pi-velvetronix-$v-linux-arm64.tar.gz" | tr '\n' ' ')" = "manifest.json pi-velvetronix "
    check "update package owner and mode" sh -c "tar -tvzf 'dist/pi-velvetronix-$v-linux-arm64.tar.gz' | awk '{print \$1, \$3, \$4}' | tr '\n' ' ' | grep -qx -- '-rw-r--r-- root root -rwxr-xr-x root root '"
    check "manifest built" sh -c "tar -xOzf 'dist/pi-velvetronix-$v-linux-arm64.tar.gz' manifest.json | grep -q '\"built\":\"2025-10-19T10:00:00Z\"'"
    check "install bundle has no AppleDouble" sh -c "! tar -tzf 'dist/pi-velvetronix-install-$v.tar.gz' | grep -q '/\._'"
    out3=$(scripts/release.sh | sed -n 's/^  //p')
    check "release.sh uses commit time by default" test -n "$out3"
    # reserved inbox names are rejected before anything is built or packed
    for bv in request rollback .hidden; do
        check "build.sh rejects version $bv" sh -c "! VERSION='$bv' scripts/build.sh --skip-web > /dev/null 2>&1"
        check "release.sh rejects version $bv" sh -c "! VERSION='$bv' scripts/release.sh > /dev/null 2>&1"
    done
    if [ -n "$dummies" ]; then
        # shellcheck disable=SC2086 # word splitting of the dummy list is intended
        rm -f $dummies "dist/pi-velvetronix-$v-linux-arm64.tar.gz" "dist/pi-velvetronix-install-$v.tar.gz"
        rmdir dist 2> /dev/null || true
    fi

    if command -v docker > /dev/null && docker info > /dev/null 2>&1; then
        if docker run --rm -v "$root":/src:ro debian:trixie-slim /src/scripts/test-deploy.sh --inner; then ok; else bad "container tests"; fi
    else
        bad "docker is not available; container tests not run"
    fi
    summary "deploy (host)"
}

case "${1:-}" in
    --inner) inner ;;
    '') host ;;
    *) echo "usage: scripts/test-deploy.sh" >&2; exit 2 ;;
esac
