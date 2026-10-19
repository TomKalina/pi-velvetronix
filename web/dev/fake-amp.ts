// Dev only: a simulated VT100X behind a fake WebSocket and a fake HTTP API, so the UI can run in a
// browser without the Pi or the amp. Follows docs/protocol.md (§4 replies, §6.2 type-change burst)
// and the bridge rules of docs/design.md §7 (save flag for 4C/11, forbidden frames). Never bundled
// into the production build (only dev/preview.ts imports it).

import type { AmpStatus, FetchFn, Status, UpdateInfo } from "../src/api";
import { dec14, enc14, pack7, unpack7 } from "../src/protocol/encoding";
import { applyParam, decodeProgram, encodeProgram, SLOT_PARAM_COUNT, type EffectSlot, type Program } from "../src/protocol/program";
import { EFFECT_PARAMS, type EffectParam } from "../src/protocol/tables";
import type { SocketLike } from "../src/ws";

const HEADER = [0xf0, 0x42, 0x30, 0x00, 0x01, 0x34];
const korg = (...data: number[]) => Uint8Array.from([...HEADER, ...data, 0xf7]);
const ACK = () => korg(0x23);
const NAK = () => korg(0x24);
const SLOT_OF_SUB: Record<number, EffectSlot> = { 1: "pedal1", 2: "pedal2", 4: "reverb" };
const PARAM_ID: Record<EffectSlot, number> = { pedal1: 0x05, pedal2: 0x06, reverb: 0x08 };

function defaultValue(spec: EffectParam | null): number {
  if (!spec) return 0;
  switch (spec.unit) {
    case "0-100": return 50;
    case "mHz": return Math.max(spec.min, 1000);
    case "ms": return spec.max > 100 ? 350 : 20;
    default: return 0;
  }
}

export function effectDefaults(slot: EffectSlot, type: number): number[] {
  return EFFECT_PARAMS[slot][type].map(defaultValue);
}

interface Sound {
  name: string; model: number; amp: number[]; nr: number;
  p1: [number, boolean, number[]?]; p2: [number, boolean, number[]?]; rev: [number, boolean, number[]?];
}

function program({ name, model, amp, nr, p1, p2, rev }: Sound): Program {
  const p = decodeProgram(new Uint8Array(62));
  return {
    ...p, name, ampModel: model, amp, nr,
    pedal1Type: p1[0], pedal1On: p1[1], pedal1: p1[2] ?? effectDefaults("pedal1", p1[0]),
    pedal2Type: p2[0], pedal2On: p2[1], pedal2: p2[2] ?? effectDefaults("pedal2", p2[0]),
    reverbType: rev[0], reverbOn: rev[1], reverb: rev[2] ?? effectDefaults("reverb", rev[0]),
  };
}

const amp = (gain: number, treble: number, middle: number, bass: number, vol: number, pres: number, res: number, bright = 0, lowCut = 0, midBoost = 0, bias = 0, cls = 1) =>
  [gain, treble, middle, bass, vol, pres, res, bright, lowCut, midBoost, bias, cls];

const SLOTS: Sound[] = [
  { name: "CLEAN SPARKLE", model: 0, amp: amp(28, 62, 48, 50, 70, 55, 40, 1), nr: 10, p1: [0, true, [40, 55, 30, 1, 0, 0]], p2: [5, false], rev: [1, true, [28, 35, 12, 50, 50]] },
  { name: "AC30 CHIME", model: 7, amp: amp(45, 66, 52, 44, 68, 48, 50, 1), nr: 15, p1: [4, false], p2: [1, false], rev: [3, true, [20, 30, 8, 50, 50]] },
  { name: "PLEXI CRUNCH", model: 8, amp: amp(64, 58, 66, 52, 66, 60, 55), nr: 25, p1: [2, true, [35, 55, 62, 50, 50, 50]], p2: [6, true, [380, 22, 25, 45, 10, 5]], rev: [0, true, [18, 25, 0, 50, 50]] },
  { name: "JCM LEAD", model: 10, amp: amp(78, 60, 70, 48, 64, 62, 50, 0, 0, 1), nr: 35, p1: [6, true, [62, 50, 52, 55, 45, 55]], p2: [6, true, [420, 30, 30, 40, 15, 10]], rev: [2, true, [22, 45, 20, 40, 60]] },
  { name: "RECTO CHUG", model: 13, amp: amp(82, 55, 35, 70, 62, 58, 70, 1, 1, 0, 2), nr: 55, p1: [3, false], p2: [0, false, [600, 50, 50, 0, 0, 40]], rev: [0, false] },
  { name: "SURF TREM", model: 2, amp: amp(30, 70, 45, 40, 70, 60, 35, 1), nr: 10, p1: [4, false], p2: [4, true, [5200, 60, 50, 30, 50, 0]], rev: [1, true, [55, 60, 10, 40, 40]] },
  { name: "PUNK 77", model: 9, amp: amp(72, 65, 62, 55, 66, 55, 50), nr: 20, p1: [9, false], p2: [2, false], rev: [1, false] },
  { name: "", model: 19, amp: amp(50, 50, 50, 50, 60, 50, 50, 1), nr: 0, p1: [1, false], p2: [3, false], rev: [0, false] },
];

export class FakeAmp {
  mode = 0;
  program = 2;
  latencyMs = 8;
  slots: Uint8Array[] = SLOTS.map((s) => encodeProgram(program(s)));
  current: Program = decodeProgram(this.slots[2]);
  /** Frames received, for debugging in the console: window.fakeAmp.log */
  log: string[] = [];

  constructor(private readonly reply: (frame: Uint8Array) => void, private readonly error: (code: string, func: number | null) => void) {}

  private later(f: Uint8Array, extra = 0): void {
    setTimeout(() => this.reply(f), this.latencyMs + extra);
  }

  receive(kind: number, f: Uint8Array): void {
    this.log.push(Array.from(f, (b) => b.toString(16).padStart(2, "0")).join(" "));
    if (f.length < 3 || f[0] !== 0xf0 || f[f.length - 1] !== 0xf7 || (kind !== 0 && kind !== 1)) return this.error("bad_frame", null);
    if (f[1] === 0x7e) return this.later(Uint8Array.from([0xf0, 0x7e, 0x00, 0x06, 0x02, 0x42, 0x34, 0x01, 0x00, 0x00, 0x01, 0x00, 0x01, 0x00, 0xf7]));
    if (!HEADER.every((b, i) => f[i] === b)) return this.error("forbidden", null);
    const fn = f[6];
    switch (fn) {
      case 0x12: return this.later(korg(0x42, this.mode, this.program));
      case 0x10: return this.later(korg(0x40, ...pack7(encodeProgram(this.current))));
      case 0x1c: return this.later(korg(0x4c, 0x00, f[8], ...pack7(this.slots[f[8]])));
      case 0x4e:
        this.mode = 0;
        this.program = f[8];
        this.current = decodeProgram(this.slots[f[8]]);
        return this.later(ACK());
      case 0x4c:
        if (kind !== 1) return this.error("save_flag_required", fn);
        this.slots[f[8]] = unpack7(f.subarray(9, -1));
        return this.later(ACK());
      case 0x11:
        if (kind !== 1) return this.error("save_flag_required", fn);
        this.slots[f[8]] = encodeProgram(this.current);
        return this.later(korg(0x21, 0x00, f[8]));
      case 0x41: return this.param(f[7], f[8], dec14(f[9], f[10]));
      default: return this.error("forbidden", fn);
    }
  }

  private param(id: number, sub: number, value: number): void {
    // Effect type change: ACK, then the new type's default values as a burst (P§6.2).
    if (id === 0x03 && sub !== 0) {
      const slot = SLOT_OF_SUB[sub];
      if (!slot || !EFFECT_PARAMS[slot][value]) return this.later(NAK());
      const defaults = effectDefaults(slot, value);
      this.current = { ...applyParam(this.current, id, sub, value), [slot]: defaults };
      this.later(ACK());
      for (let n = 0; n < SLOT_PARAM_COUNT[slot]; n++) this.later(korg(0x41, PARAM_ID[slot], n, ...enc14(defaults[n])), 10 + n * 6);
      return;
    }
    // The amp rejects indices the current type does not use (P§6.2).
    for (const slot of ["pedal1", "pedal2", "reverb"] as const) {
      if (id === PARAM_ID[slot]) {
        const type = slot === "pedal1" ? this.current.pedal1Type : slot === "pedal2" ? this.current.pedal2Type : this.current.reverbType;
        if (!EFFECT_PARAMS[slot][type][sub]) return this.later(NAK());
      }
    }
    const next = applyParam(this.current, id, sub, value);
    try {
      encodeProgram(next);
    } catch {
      return this.later(NAK());
    }
    this.current = next;
    this.later(ACK());
  }
}

const AMP_STATUS: AmpStatus = {
  state: "connected", card: "1", name: "Valvetronix X (simulace)", usb_id: "0944:0117", device: "/dev/snd/midiC1D0",
  identity: "F0 7E 00 06 02 42 34 01 00 00 01 00 01 00 F7", error: null,
};

/** A WebSocket that speaks design §7 with a FakeAmp behind it. */
export class FakeSocket implements SocketLike {
  binaryType: BinaryType = "arraybuffer";
  onopen: ((ev: unknown) => void) | null = null;
  onmessage: ((ev: { data: unknown }) => void) | null = null;
  onclose: ((ev: { code: number }) => void) | null = null;
  onerror: ((ev: unknown) => void) | null = null;
  readonly amp: FakeAmp;

  constructor(ampState: AmpStatus["state"]) {
    this.amp = new FakeAmp(
      (f) => this.deliver(Uint8Array.from([0, ...f]).buffer),
      (code, func) => this.deliver(JSON.stringify({ t: "error", code, func })),
    );
    setTimeout(() => {
      this.onopen?.({});
      this.deliver(JSON.stringify({ t: "hello", version: "preview" }));
      this.deliver(JSON.stringify({ t: "amp", amp: { ...AMP_STATUS, state: ampState } }));
    }, 20);
  }

  private deliver(data: unknown): void {
    this.onmessage?.({ data });
  }

  send(data: string | ArrayBufferView): void {
    if (typeof data === "string") {
      if (data.includes('"ping"')) setTimeout(() => this.deliver(JSON.stringify({ t: "pong" })), 5);
      return;
    }
    const b = new Uint8Array(data.buffer, data.byteOffset, data.byteLength);
    this.amp.receive(b[0], b.slice(1));
  }

  close(): void {}
}

const UPDATE: UpdateInfo = { state: "idle", current_version: "0.3.0-preview", previous_version: "0.2.0", pending: false, last_result: "installed 0.2.0" };

export function fakeStatus(ampState: AmpStatus["state"]): Status {
  return {
    version: "0.3.0-preview", hostname: "velvetronix", uptime_s: 5423,
    addresses: ["192.168.1.86"],
    wifi: { mode: "sta", ssid: "Zkušebna", ip: "192.168.1.86", signal: 72, ap_ssid: "VOX-Velvetronix", ap_ip: "10.42.0.1", last_error: null },
    mdns: { hostname: "velvetronix", alias: "vox", alias_state: "established" },
    amp: { ...AMP_STATUS, state: ampState },
    controller: { active: true },
    update: UPDATE,
    auth: { configured: true },
  };
}

/** HTTP API of design §5 with canned answers. */
export function fakeFetch(ampState: AmpStatus["state"]): FetchFn {
  return async (path, init) => {
    const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
    const method = init?.method ?? "GET";
    if (path === "/api/session") return json({ authenticated: true, configured: true });
    if (path === "/api/status") return json(fakeStatus(ampState));
    if (path.startsWith("/api/wifi/networks")) {
      return json({
        networks: [
          { ssid: "Zkušebna", signal: 72, security: "WPA2", in_use: true, saved: true },
          { ssid: "Sousedi 5G", signal: 40, security: "WPA2", in_use: false, saved: false },
        ],
        saved: [{ uuid: "1", name: "home-Zkušebna", ssid: "Zkušebna", priority: 10, active: true }],
      });
    }
    if (path === "/api/update" && method === "GET") return json(UPDATE);
    return json({ ok: true });
  };
}
