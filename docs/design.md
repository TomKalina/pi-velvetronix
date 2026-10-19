# pi-velvetronix: design and frozen contract

This document is the single source of truth for interfaces between the parts of the
project. Implementers must follow it exactly; any change has to be made here first.

Protocol reference for everything VOX-specific: `docs/protocol.md` (copied from the
research spec). Section numbers like "P§4" refer to it.

## 1. Overview

```
Browser (TS)                 Raspberry Pi Zero 2 W (Go daemon)            VOX VT100X
------------                 ---------------------------------            ----------
UI + VOX protocol  <-- WS -->  bridge: frame check, allowlist,  <-ALSA->  USB-MIDI SysEx
(owns state, queue,            write guard, single controller   rawmidi
 matching, timeouts)           HTTP: login, status, Wi-Fi, update
                               NetworkManager (nmcli), Avahi (D-Bus)
                               systemd: notify + watchdog, A/B releases
```

- The browser implements the VOX protocol (P§4-P§9). The daemon only moves whole SysEx
  frames, validates them, and refuses anything that could write to amp memory unless the
  frame is marked as an explicit user save.
- Nothing ever writes to the amp automatically: not on daemon start, not on WS connect,
  not on update. Writes happen only after a user action in the UI.
- Exactly one controlling browser session at a time.

## 2. Repository layout

```
go.mod                         module github.com/TomKalina/pi-velvetronix
cmd/pi-velvetronix/            daemon main (flags, wiring, subcommands)
cmd/fakeamp/                   VOX simulator on a rawmidi device (test tool)
internal/midi/                 SysEx stream framer, frame validation, host->amp allowlist,
                               rawmidi discovery + port I/O (linux), Port interface
internal/vox/                  minimal Go helpers used by fakeamp: pack7/unpack7, constants
internal/bridge/               controller session, bounded send queue, pause, amp status
internal/auth/                 password (bcrypt), sessions, login rate limit
internal/netmgr/               nmcli wrapper + STA/AP supervisor
internal/mdns/                 Avahi D-Bus alias publisher (vox.local)
internal/update/               update package validation + staging + status files
internal/sdnotify/             sd_notify READY/WATCHDOG over $NOTIFY_SOCKET (no deps)
internal/web/                  HTTP server, handlers, WS endpoint, embedded assets
internal/web/dist/             build output of web/ (index.html.gz); .gitkeep committed
web/                           TypeScript frontend (protocol lib + UI), vitest tests
deploy/                        systemd units, boot-guard, apply-update, polkit, sysusers,
                               swap config, remote install script
scripts/                       build.sh, release.sh, install.sh (run on the Mac)
docs/                          design.md, protocol.md, testing.md
```

## 3. Daemon CLI

Binary: `pi-velvetronix`. Version is injected with `-ldflags "-X main.version=<ver>"`
(default `dev`).

```
pi-velvetronix [serve] [flags]          run the daemon (default subcommand)
  --listen       :80                    HTTP listen address
  --state        /var/lib/pi-velvetronix  state directory (writable)
  --midi-match   "Valvetronix"          case-insensitive substring matched against the ALSA
                                        card id, name and longname (/proc/asound/cards) to
                                        pick the amp; e.g. "VirMIDI" for loopback tests
  --midi-device  ""                     explicit rawmidi node (e.g. /dev/snd/midiC1D1);
                                        overrides --midi-match
  --ap-con       vox-ap                 NetworkManager connection id of the fallback hotspot
  --no-network                          disable netmgr and mdns (local dev on the Mac)
  --update-dir   /var/lib/pi-velvetronix-update  root-owned update directory, only read
                                        by the daemon (§4)
pi-velvetronix set-password --state DIR read new web password from stdin (one line,
                                        8..72 bytes), write DIR/auth.json, invalidate sessions
pi-velvetronix version                  print version and exit
```

Startup order in `serve`:
1. Start the watchdog ticker immediately if `WATCHDOG_USEC` is set (interval = half).
2. Ensure `state/inbox` exists (mode 0750).
3. Load auth; if `auth.json` is missing the daemon still starts, every login fails with
   `no_password`, and status shows `auth.configured=false`.
4. Bind HTTP, start serving.
5. Self-check: `GET http://127.0.0.1:<port>/healthz` (2 s timeout). On 200: send
   `READY=1`, then atomically write `state/confirmed` = own version (= confirm the
   release). The daemon never removes `pending`; boot-guard does on the next start.
   On failure: log and `os.Exit(1)` (systemd restarts; boot-guard counts and rolls back).
6. Start bridge, netmgr supervisor, mdns alias.

The self-check must not depend on the amp, on home Wi-Fi, or on the hotspot.

## 4. State directory (`/var/lib/pi-velvetronix`)

| Path | Owner | Content |
|---|---|---|
| `auth.json` | daemon | `{"bcrypt":"<hash>","changed":"RFC3339"}` |
| `sessions.json` | daemon | `[{"h":"<sha256 hex of token>","exp":"RFC3339"}]` max 16 |
| `inbox/<ver>/{manifest.json,pi-velvetronix}` | daemon | staged update |
| `inbox/request` | daemon | one line: version. Written via `inbox/.request.tmp` + rename |
| `confirmed` | daemon | one line: version that passed its self-check |

All daemon writes are atomic: temp file in the same dir, fsync, rename, fsync dir.

Update directory `/var/lib/pi-velvetronix-update` (root:root 0755, files 0644). Only root
writes here (boot-guard, apply-update, install-remote); the daemon only reads it
(`ProtectSystem=strict` makes it read-only for the daemon):

| Path | Writer | Content |
|---|---|---|
| `pending` | updater / boot-guard | `"<ver> <attempts>"`; unconfirmed while `<ver>` != `state/confirmed` |
| `applying` | updater | the request, moved here from `inbox/request` while the updater runs |
| `last-result` | updater / boot-guard | one line, e.g. `installed 0.1.1`, `failed sha256`, `failed io-error`, `rolled-back 0.1.2 -> releases/0.1.1` |

Root scripts never write into, chown in, or follow links in a directory the daemon
user can write. In `state/` they only rename `inbox/request` away (after a regular-file,
non-symlink check), remove entries (`rm` does not follow links) and read
`inbox/<ver>/*` (copied with `cp -P` into a root-owned dir and checked there) and
`confirmed` (regular non-symlink file, at most 64 bytes). Every value read is reduced to
`[0-9A-Za-z._-]`. Temp files live only in root-owned dirs.

- Confirm: boot-guard sees `pending` and `state/confirmed` == its version: removes
  `pending` (nothing else) and exits without counting. apply-update removes
  `state/confirmed` right before writing a new `pending`.
- Ordering: apply-update copies and verifies the release, writes `pending`, runs `sync`,
  and only then swaps `current`.
- Stale `applying` (power cut during apply): boot-guard removes it unless
  `pi-velvetronix-updater.service` is `activating`. install-remote removes `applying`
  and `pending` on every (re)install, as well as `state/confirmed` and the old
  daemon-owned `state/update/`.
- Unexpected errors in apply-update (e.g. ENOSPC) write `failed io-error` and remove
  the temp and inbox copies; stale `releases/.*.tmp` are pruned.

Release layout (root-owned, written only by the root updater):

```
/opt/pi-velvetronix/releases/<ver>/{pi-velvetronix,manifest.json}
/opt/pi-velvetronix/current  -> releases/<ver>
/opt/pi-velvetronix/previous -> releases/<old>
```

## 5. HTTP API

- JSON bodies, `Content-Type: application/json; charset=utf-8`.
- Auth: cookie `vtx_session=<base64url 32 random bytes>; Path=/; HttpOnly; SameSite=Strict;
  Max-Age=2592000`. Server stores only sha256 of the token.
- All non-GET requests pass through `http.NewCrossOriginProtection()`.
- Errors: HTTP status + `{"error":"<code>","detail":"<optional text>"}`.
- Unauthenticated access to a protected endpoint: `401 {"error":"unauthorized"}`.

| Method | Path | Auth | Request | Response |
|---|---|---|---|---|
| GET | `/` | no | | embedded `index.html` (gzip if accepted), `Cache-Control: no-cache` |
| GET | `/healthz` | no | | `200 ok` (text) if embedded asset present and state dir writable, else `503` |
| GET | `/api/session` | no | | `{"authenticated":bool,"configured":bool}` |
| POST | `/api/login` | no | `{"password":"..."}` | `200 {"ok":true}` + cookie; `401 bad_password`; `429 {"error":"rate_limited","retry_after":s}`; `503 no_password` |
| POST | `/api/logout` | yes | | `200 {"ok":true}`, clears cookie |
| GET | `/api/status` | yes | | `Status` (below) |
| GET | `/api/wifi/networks` | yes | `?rescan=1` optional | `{"networks":[Network],"saved":[Saved]}` |
| POST | `/api/wifi/connect` | yes | `{"ssid":"...","password":"..."}` | `202 {"ok":true}`; connect runs async, result visible in status |
| POST | `/api/wifi/forget` | yes | `{"uuid":"..."}` | `200 {"ok":true}`; `409 active_connection` if it is the active one |
| POST | `/api/wifi/hotspot` | yes | `{"minutes":1..30}` | `202 {"ok":true}`; hotspot now, back to home Wi-Fi after N minutes if available |
| GET | `/api/update` | yes | | `UpdateInfo` |
| POST | `/api/update` | yes | raw body = `.tar.gz` package, `Content-Type: application/gzip` | `202 {"ok":true,"version":"x"}` or `400/409/413 {"error":code}` |

Types:

```ts
type Status = {
  version: string; hostname: string; uptime_s: number;
  addresses: string[];                       // IPv4 of wlan0 (and others), e.g. ["192.168.1.86"]
  wifi: {
    mode: "sta" | "ap" | "connecting" | "off" | "unknown";
    ssid: string | null; ip: string | null; signal: number | null;   // signal 0..100
    ap_ssid: string; ap_ip: string;          // "VOX-Velvetronix", "10.42.0.1"
    last_error: string | null;               // e.g. "connect_failed: HomeNet"
  };
  mdns: { hostname: string; alias: string; alias_state: "established"|"registering"|"collision"|"failed"|"off" };
  amp: AmpStatus;
  controller: { active: boolean };
  update: UpdateInfo;
  auth: { configured: boolean };
};
type AmpStatus = {
  state: "disconnected" | "detected" | "connected" | "busy" | "error";
  // disconnected: no matching rawmidi device
  // detected: device present and opened, identity not (yet) confirmed
  // connected: device open AND a Device Inquiry reply containing 42 34 01 was received
  // busy: device exists but open() returned EBUSY (another program holds it)
  // error: other open/IO error (detail in `error`)
  card: string | null; name: string | null; usb_id: string | null; device: string | null;
  identity: string | null;   // hex of the inquiry reply, e.g. "F0 7E 00 06 02 42 34 01 ..."
  error: string | null;
};
type UpdateInfo = {
  state: "idle" | "staged" | "applying";
  current_version: string; previous_version: string | null;
  pending: boolean;          // update-dir pending exists and its version != state/confirmed
  last_result: string | null;
};
type Network = { ssid: string; signal: number; security: string; in_use: boolean; saved: boolean };
type Saved = { uuid: string; name: string; ssid: string; priority: number; active: boolean };
```

Update error codes: `too_large` (413, limit 40 MiB), `not_gzip`, `bad_tar`,
`unexpected_entry`, `missing_entry`, `bad_manifest`, `wrong_name`, `wrong_arch`,
`bad_version`, `same_version`, `sha256_mismatch`, `not_elf`, `not_aarch64`,
`busy` (409, an update is already staged/applying), `io_error` (500).

## 6. Update package

File name: `pi-velvetronix-<ver>-linux-arm64.tar.gz`. Exactly two regular files at top
level, no directories, links, or other entries:

```
manifest.json
pi-velvetronix
```

```json
{"name":"pi-velvetronix","version":"0.1.1","arch":"linux/arm64","binary":"pi-velvetronix",
 "sha256":"<64 hex>","built":"2026-10-19T10:00:00Z"}
```

Validation (daemon, before staging), in this order: gzip magic `1f 8b`; tar parse; entry
allow-list and size caps (manifest <= 4 KiB, binary <= 40 MiB); manifest JSON; `name ==
"pi-velvetronix"`; `arch == "linux/arm64"`; `version` matches `^[0-9A-Za-z._-]{1,64}$`,
does not start with `.`, is not `request` or `rollback` (inbox names; `bad_version`), and
differs from the running version; sha256 of binary; ELF magic, ELFCLASS64, little endian,
`EM_AARCH64`, `ET_EXEC` or `ET_DYN`. The root updater re-checks sha256 and ELF.

A raw `.img`, `.img.xz`, a bare binary, a zip, or a package for another architecture is
rejected with a specific code. After staging the daemon sets update state `staged`,
pauses the bridge, and writes `inbox/request`. A staging error removes the partial
`inbox/<ver>`. The path unit starts the updater, which swaps symlinks and restarts the
service.

## 7. WebSocket `/ws`

- Requires the session cookie (checked before Accept, else HTTP 401). Origin must equal
  Host (coder/websocket default). Read limit 4 KiB.
- Single controller, keyed by owner (sha256 of the session token, never logged). If a
  controller of a different session is connected, the server sends the text message
  `{"t":"busy"}` and closes the new socket with status code 4001, reason `busy`. If it is
  the same session (another tab, or a quick reconnect before the old socket timed out), the
  new socket takes over: the old one gets `{"t":"replaced"}` and is closed with status code
  4002, reason `replaced`; the new one gets `hello` as usual. The client does not
  auto-reconnect after 4001 or 4002 (after 4002 that would make two tabs take the slot from
  each other forever); the user reconnects with "Zkusit znovu" / "Převzít zpět".
- Liveness: server sends WS Ping every 10 s; no Pong within 5 s closes the socket and
  frees the controller slot. The client also sends `{"t":"ping"}` every 10 s; the server
  answers `{"t":"pong"}`.

Server -> client text messages:

```ts
{ t: "hello", version: string }                        // first message after accept
{ t: "amp", amp: AmpStatus }                           // on connect and on every change
{ t: "update", update: UpdateInfo }                    // on change
{ t: "error", code: BridgeError, func: number | null, detail?: string } // a client frame was rejected
{ t: "pong" }
{ t: "busy" }                                          // then close 4001
{ t: "replaced" }                                      // then close 4002
type BridgeError = "bad_frame" | "forbidden" | "save_flag_required" | "amp_disconnected"
                 | "queue_full" | "updating" | "write_failed";
```

`func` is the KORG function byte of the rejected frame (frame[6]), or `null` for frames
that are not KORG frames.

Binary messages:

- Client -> server: `[kind][F0 ... F7]`. `kind` 0x00 = normal, 0x01 = user-confirmed save.
  Exactly one SysEx frame per message, total length <= 512 bytes.
- Server -> client: `[0x00][F0 ... F7]`, one complete frame received from the amp.
  Realtime bytes (F8-FF) are never forwarded. Frames from the amp are forwarded only to the
  current controller; with no controller they are dropped.

Server-side frame checks (all must pass, else `error`):

1. `bad_frame`: length < 3, first byte != F0, last byte != F7, any inner byte >= 0x80, or
   kind not in {0,1}.
2. Allow-list (exact lengths, full frame incl. F0/F7):

| Frame | Len | kind 0 | kind 1 |
|---|---|---|---|
| `F0 7E (00|7F) 06 01 F7` device inquiry | 6 | yes | yes |
| `F0 42 30 00 01 34 12 F7` mode request | 8 | yes | yes |
| `F0 42 30 00 01 34 10 F7` current program request | 8 | yes | yes |
| `F0 42 30 00 01 34 1C 0m pp F7` program dump request | 10 | yes | yes |
| `F0 42 30 00 01 34 31 00 pp F7` custom dump request | 10 | yes | yes |
| `F0 42 30 00 01 34 41 ID SUB lo hi F7` parameter change | 12 | yes | yes |
| `F0 42 30 00 01 34 4E mm pp F7` mode change (select) | 10 | yes | yes |
| `F0 42 30 00 01 34 4C 00 pp <71> F7` write program to slot | 81 | `save_flag_required` | yes |
| `F0 42 30 00 01 34 11 00 pp F7` program write request | 10 | `save_flag_required` | yes |
| anything else (incl. `40` buffer load, `65` custom write) | | `forbidden` | `forbidden` |

3. `updating` while update state is `staged`/`applying`; `amp_disconnected` when the amp
   state is not `detected`/`connected`; `queue_full` when the bounded queue (32 frames) is
   full.

Every accepted kind-1 frame is logged: `save requested: func=4C slot=3`.

The daemon itself sends exactly one frame on its own: a Device Inquiry right after
opening the device (and again every 30 s while state is `detected`), to confirm identity.
The inquiry reply is consumed by the daemon and also forwarded to a connected controller.

## 8. Go package contracts

```go
// internal/midi
type Framer struct{ /* max frame 4096 */ }
func (f *Framer) Feed(p []byte, emit func(frame []byte))   // drops F8-FF, aborts on stray status
func CheckHostFrame(kind byte, frame []byte) (fn int, err error) // returns errors below
var ErrBadFrame, ErrForbidden, ErrSaveFlagRequired error
type Port interface { io.ReadWriteCloser }
type Device struct { Card, ID, Name, LongName, UsbID, Node string }
func FindDevices(match string) ([]Device, error)   // parses /proc/asound/cards, /proc/asound/cardN/usbid, /dev/snd/midiC*D*
func OpenPort(node string) (Port, error)            // O_RDWR|O_NONBLOCK|O_CLOEXEC; EBUSY -> ErrBusy
func IsIdentity(frame []byte) bool                  // F0 7E xx 06 02 42 34 01 ...

// internal/bridge
type Bridge struct{ ... }
func New(match, device string, log *slog.Logger) *Bridge
func (b *Bridge) Run(ctx context.Context)                       // discovery loop (poll 1 s), I/O
func (b *Bridge) Attach(owner string) (c *Controller, ok bool)  // ok=false if another owner is active; same non-empty owner takes over
type Controller struct{ ... }
func (c *Controller) Send(kind byte, frame []byte) error         // validates + enqueues
func (c *Controller) Frames() <-chan []byte                      // frames from amp
func (c *Controller) Replaced() <-chan struct{}                  // closed when a same-owner Attach took over
func (c *Controller) Detach()
func (b *Bridge) Status() AmpStatus
func (b *Bridge) Subscribe() (<-chan AmpStatus, func())          // status change notifications
func (b *Bridge) SetPaused(bool)                                 // during update
var ErrAmpDisconnected, ErrQueueFull, ErrUpdating error

// internal/netmgr
type Manager struct{ ... }
func New(apCon string, log *slog.Logger) *Manager
func (m *Manager) Run(ctx context.Context)          // supervisor loop
func (m *Manager) Status() WifiStatus
func (m *Manager) Networks(ctx context.Context, rescan bool) ([]Network, []Saved, error)
func (m *Manager) Connect(ctx context.Context, ssid, psk string) error  // create or update profile, then activate async
func (m *Manager) Forget(ctx context.Context, uuid string) error
func (m *Manager) Hotspot(minutes int) error
func (m *Manager) IPv4() net.IP                     // current wlan0 IPv4 or nil

// internal/mdns
func PublishAlias(ctx context.Context, alias string, ip func() net.IP, state func(string), log *slog.Logger)

// internal/update
func Validate(r io.Reader, running string) (*Package, error)  // returns *Error{Code}
func (p *Package) Stage(stateDir string) error                 // writes inbox/<ver>/..., then inbox/request
func ReadInfo(stateDir, running string) UpdateInfo           // also reads Dir
func Confirm(stateDir, version string) error                 // writes state/confirmed after READY
var Dir = "/var/lib/pi-velvetronix-update"                     // set from --update-dir

// internal/auth
func Load(stateDir string) (*Store, error)
func (s *Store) Configured() bool
func (s *Store) Login(password, remoteIP string) (token string, err error) // ErrBadPassword, ErrRateLimited{RetryAfter}, ErrNoPassword
func (s *Store) Check(token string) bool
func (s *Store) Logout(token string)
func SetPassword(stateDir, password string) error

// internal/sdnotify
func Ready() error; func Watchdog() error; func WatchdogInterval() (time.Duration, bool)
```

### Supervisor rules (netmgr)

- Address NM profiles by UUID. Never delete a profile except in `Forget`.
- Boot: wait up to 60 s for wlan0 to be connected in infrastructure mode.
- STA lost for > 90 s: `nmcli --wait 30 connection up id <apCon>` (mode `ap`).
- In AP mode with no activity from 10.42.0.0/24 for 5 min, or when a requested hotspot
  timer expires: rescan; try up to 3 visible saved networks, highest priority first, then
  strongest signal (`--wait 45` each, stop at the first that connects); if all fail
  return to AP. Backoff between attempts 2, 5, 10 min.
- `Connect`: if a profile for that SSID exists, modify its PSK; else `nmcli connection add
  type wifi ifname wlan0 con-name "home-<ssid>" ssid <ssid> wifi-sec.key-mgmt wpa-psk
  wifi-sec.psk <psk> connection.autoconnect yes connection.autoconnect-priority 10
  connection.autoconnect-retries 0`. Then `connection up` async. Failure is reported in
  `last_error`; the profile stays.
- Never log PSKs or passwords; exec logging must redact the value after `wifi-sec.psk`.
- All exec calls: argv only (no shell), `LC_ALL=C`, timeouts, `nmcli -t -e yes`.

## 9. Frontend contract (web/)

Build: `npm ci && npm run build` writes `../internal/web/dist/index.html.gz` (single file,
inline JS + CSS, gzip -9, deterministic). Tooling pins: esbuild 0.28.2, typescript 7.0.2,
vitest 5.0.5, vite 8.3.4, @types/node 24.19.2.

### 9.1 Protocol library `web/src/protocol/` (pure, no DOM)

```ts
// encoding.ts
export function enc14(v: number): [number, number];
export function dec14(lo: number, hi: number): number;
export function pack7(data: Uint8Array): Uint8Array;     // 62 -> 71
export function unpack7(p: Uint8Array): Uint8Array;      // 71 -> 62, throws on invalid
// messages.ts: frame builders return full frames F0..F7 (Uint8Array)
export const msg: {
  deviceInquiry(): Uint8Array; modeRequest(): Uint8Array; currentProgramRequest(): Uint8Array;
  programRequest(slot: number): Uint8Array;               // 1C 00 pp
  selectProgram(slot: number): Uint8Array;                // 4E 00 pp
  writeProgram(slot: number, p: Program): Uint8Array;     // 4C 00 pp <71>
  param(id: number, sub: number, value: number): Uint8Array; // 41 ID SUB lo hi
  noiseReduction(v: number): Uint8Array; effectOn(slot: EffectSlot, on: boolean): Uint8Array;
  ampModel(m: number): Uint8Array; effectType(slot: EffectSlot, t: number): Uint8Array;
  ampParam(n: number, v: number): Uint8Array; effectParam(slot: EffectSlot, n: number, v: number): Uint8Array;
};
export type Parsed =
  | { kind: "ack" } | { kind: "nak" } | { kind: "format_error" }
  | { kind: "write_completed"; slot: number } | { kind: "write_error"; slot: number }
  | { kind: "mode"; mode: "user" | "preset" | "manual"; program: number }          // 42
  | { kind: "mode_change"; mode: "user" | "preset" | "manual"; program: number }   // 4E (amp -> host)
  | { kind: "current_program"; program: Program }                                  // 40
  | { kind: "program"; bank: number; slot: number; program: Program }              // 4C
  | { kind: "param"; id: number; sub: number; value: number }                      // 41
  | { kind: "identity"; major: number; minor: number }
  | { kind: "unknown"; frame: Uint8Array };
export function parse(frame: Uint8Array): Parsed;   // never throws; malformed -> unknown
// program.ts
export type EffectSlot = "pedal1" | "pedal2" | "reverb";
export interface Program { raw: Uint8Array /* 62 bytes, reserved bits preserved */; name: string;
  nr: number; pedal1On: boolean; pedal2On: boolean; reverbOn: boolean; ampModel: number;
  amp: number[] /* 12 values, P§5.1 order: gain..class */;
  pedal1Type: number; pedal1: number[] /* 6 */; pedal2Type: number; pedal2: number[] /* 6 */;
  reverbType: number; reverb: number[] /* 5 */ }
export function decodeProgram(struct62: Uint8Array): Program;
export function encodeProgram(p: Program): Uint8Array;   // validates ranges, starts from p.raw
export function applyParam(p: Program, id: number, sub: number, value: number): Program;
// tables.ts: names + ranges for UI (Czech labels live in the UI, not here)
export const AMP_MODELS, PEDAL1_TYPES, PEDAL2_TYPES, REVERB_TYPES, AMP_PARAMS, EFFECT_PARAMS;
// client.ts
export interface Transport { send(frame: Uint8Array, save: boolean): void;
  onFrame(cb: (f: Uint8Array) => void): void; onError(cb: (e: { code: string; func: number | null }) => void): void }
export class VoxClient {
  constructor(t: Transport, opts?: { now?: () => number; setTimeout?: typeof setTimeout; clearTimeout?: typeof clearTimeout });
  readAll(): Promise<AmpState>;              // P§8.1: inquiry (optional), 12, 10, 1C x 8
  select(slot: number): Promise<void>;       // 4E, ACK, then re-read 10
  setParam(id: number, sub: number, v: number): void;  // coalesced per (id,sub), fire-and-forget
  setEffectType(slot: EffectSlot, t: number): Promise<void>; // P§6.2 burst rule
  save(slot: number, p: Program): Promise<SaveResult>; // P§8.3: 4C(save) -> 4E -> 1C compare
  // SaveResult = ("verified" | "mismatch" | "unverified") + optional "-inactive": after an ACKed
  // 4C a failed 4E only adds "-inactive" and a failed 1C gives "unverified"; neither fails the save
  onState(cb: (s: AmpState) => void): void;  // fired on every model change (incl. amp notifications)
  reset(): void;                             // drop queue + state (transport reconnect)
}
export interface AmpState { mode: "user" | "preset" | "manual" | null; program: number | null;
  current: Program | null; slots: (Program | null)[] /* 8 */; ready: boolean }
```

Rules: one request in flight; responses matched by function byte (+ m/p) per P§4.1;
unsolicited `41`/`4E`/`21` always go to the notification path; timeouts per P§9.2 with a
real timer; reads retried once, writes never; server `error` messages fail the in-flight
request with that code. Initial state is only what the amp reported; UI defaults are never
sent.

### 9.2 UI (`web/src/ui/`, Czech)

Screens: login; main control (status bar, programs, amp, effects, save); Wi-Fi settings;
firmware update; info. Controls are disabled until `readAll()` completed and the amp
state is `connected`. "Uložit do slotu" requires an explicit confirm dialog showing slot
and name. Power Level is not offered (not digitally connected).

## 10. Deploy contract

- System user `pi-velvetronix` (sysusers), member of `audio`.
- Units: `pi-velvetronix.service` (Type=notify, User=pi-velvetronix, ExecStartPre=+boot-guard,
  TimeoutStartSec=60, WatchdogSec=30, Restart=on-failure, StartLimitIntervalSec=0),
  `pi-velvetronix-updater.path` + `.service` (root oneshot `apply-update`,
  `ReadWritePaths=/opt/pi-velvetronix /var/lib/pi-velvetronix /var/lib/pi-velvetronix-update`).
  The daemon unit has no `ReadWritePaths`: only its `StateDirectory` is writable.
- Directories: `/var/lib/pi-velvetronix` (daemon, 0750), `/var/lib/pi-velvetronix-update`
  (root:root 0755, §4), created by the installer, which also removes the old
  daemon-owned `state/update/`.
- PipeWire: the installer masks the per-user audio stack for all users
  (`systemctl --global mask pipewire.socket pipewire.service pipewire-pulse.socket
  pipewire-pulse.service wireplumber.service`, idempotent). The Pi is a headless amp
  controller and PipeWire can grab the amp's MIDI port.
- Polkit rule `/etc/polkit-1/rules.d/50-pi-velvetronix.rules` granting the NM actions to
  user `pi-velvetronix`.
- Hotspot profile `vox-ap` created by the installer (root), SSID `VOX-Velvetronix`,
  WPA2-PSK (RSN/CCMP), `ipv4.method shared`, `ipv4.addresses 10.42.0.1/24`,
  `autoconnect no`, random PSK.
- Secrets (web password, AP PSK) are generated on the Mac by `scripts/install.sh` into
  `secrets/velvetronix.env` (gitignored), sent to the Pi over SSH stdin, never on argv,
  never logged.
