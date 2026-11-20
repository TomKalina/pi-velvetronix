// MCP server for the pi-velvetronix daemon: logs in over HTTP, becomes the controller over /ws and
// drives the amp with the same protocol code as the web UI (web/src/protocol). docs/design.md §7.
//
// Env: VOX_URL (default http://velvetronix.local), VOX_PASSWORD (required).
// Several controllers share the amp, so open pages see our changes live. ?takeover=1 matters only
// when all slots are taken. The socket is closed IDLE_MS after the last call.

import { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import { StdioServerTransport } from "@modelcontextprotocol/sdk/server/stdio.js";
import { z } from "zod";

import type { AuditionSettings } from "../../web/src/protocol/audition";
import { VoxClient, VoxError, type AmpState, type Transport } from "../../web/src/protocol/client";
import type { EffectSlot, Program } from "../../web/src/protocol/program";
import {
  AMP_MODELS, AMP_PARAMS, BIAS_NAMES, CLASS_NAMES, EFFECT_PARAMS, PEDAL1_TYPES, PEDAL2_TYPES, REVERB_TYPES,
  USER_SLOT_NAMES, type EffectParam, type EffectType,
} from "../../web/src/protocol/tables";
import { SONGS, type SongPreset } from "../../web/src/ui/songs";

const BASE = (process.env.VOX_URL ?? "http://velvetronix.local").replace(/\/+$/, "");
const PASSWORD = process.env.VOX_PASSWORD ?? "";
const IDLE_MS = 8000;
const HELLO_TIMEOUT_MS = 5000;
const BUSY_CLOSE_CODE = 4001;
const KIND_NORMAL = 0x00;
const KIND_SAVE = 0x01;

const SLOTS: readonly EffectSlot[] = ["pedal1", "pedal2", "reverb"];
const SLOT_TYPES: Record<EffectSlot, readonly EffectType[]> = { pedal1: PEDAL1_TYPES, pedal2: PEDAL2_TYPES, reverb: REVERB_TYPES };
type AmpKey = keyof AuditionSettings["amp"];
/** Program.amp index of each audition amp key (VOLUME, index 4, is never sent). */
const AMP_INDEX: Record<Exclude<AmpKey, "model">, number> = {
  gain: 0, treble: 1, middle: 2, bass: 3, presence: 5, resonance: 6,
  brightCap: 7, lowCut: 8, midBoost: 9, bias: 10, ampClass: 11,
};

class UserError extends Error {}

// --- connection -------------------------------------------------------------------------------

class WsTransport implements Transport {
  private frameCbs: ((f: Uint8Array) => void)[] = [];
  private errorCbs: ((e: { code: string; func: number | null }) => void)[] = [];

  constructor(private readonly sock: WebSocket) {}

  send(frame: Uint8Array, save: boolean): void {
    if (this.sock.readyState !== WebSocket.OPEN) throw new Error("not connected");
    const m = new Uint8Array(frame.length + 1);
    m[0] = save ? KIND_SAVE : KIND_NORMAL;
    m.set(frame, 1);
    this.sock.send(m);
  }

  onFrame(cb: (f: Uint8Array) => void): void {
    this.frameCbs.push(cb);
  }

  onError(cb: (e: { code: string; func: number | null }) => void): void {
    this.errorCbs.push(cb);
  }

  message(data: unknown): void {
    if (data instanceof ArrayBuffer) {
      const b = new Uint8Array(data);
      if (b.length >= 2 && b[0] === KIND_NORMAL) for (const cb of this.frameCbs) cb(b.slice(1));
      return;
    }
    if (typeof data !== "string") return;
    try {
      const m = JSON.parse(data) as { t?: string; code?: string; func?: number | null };
      if (m.t === "error" && typeof m.code === "string") for (const cb of this.errorCbs) cb({ code: m.code, func: m.func ?? null });
    } catch {
      // not JSON
    }
  }
}

class Amp {
  private cookie: string | null = null;
  private sock: WebSocket | null = null;
  private client: VoxClient | null = null;
  private state: AmpState | null = null;
  private idle: ReturnType<typeof setTimeout> | null = null;
  private chain: Promise<unknown> = Promise.resolve();

  /** Runs fn with a connected, fully read client; calls are serialized. */
  use<T>(fn: (c: VoxClient, s: () => AmpState) => Promise<T>): Promise<T> {
    const run = async (): Promise<T> => {
      if (this.idle) clearTimeout(this.idle);
      try {
        const c = await this.connect();
        return await fn(c, () => this.state!);
      } finally {
        this.idle = setTimeout(() => this.close(), IDLE_MS);
      }
    };
    const p = this.chain.then(run, run);
    this.chain = p.catch(() => {});
    return p;
  }

  private async login(): Promise<string> {
    if (!PASSWORD) throw new UserError("VOX_PASSWORD is not set for the MCP server.");
    const res = await fetch(`${BASE}/api/login`, {
      method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ password: PASSWORD }),
    });
    if (res.status === 401) throw new UserError("Wrong VOX_PASSWORD.");
    if (!res.ok) throw new UserError(`Login failed: HTTP ${res.status} ${await res.text()}`);
    const c = res.headers.getSetCookie().map((h) => h.split(";")[0]).find((h) => h.startsWith("vtx_session="));
    if (!c) throw new UserError("Login returned no session cookie.");
    return c;
  }

  private async connect(): Promise<VoxClient> {
    if (this.client && this.sock?.readyState === WebSocket.OPEN && this.state?.ready) return this.client;
    this.close();
    for (let attempt = 0; ; attempt++) {
      this.cookie ??= await this.login();
      try {
        await this.open(this.cookie);
        break;
      } catch (e) {
        // A stale session fails the upgrade with 401; log in again once.
        if (e instanceof UserError || attempt > 0) throw e;
        this.cookie = null;
      }
    }
    try {
      this.state = await this.client!.readAll();
    } catch (e) {
      this.close();
      throw e;
    }
    return this.client!;
  }

  private open(cookie: string): Promise<void> {
    const url = `${BASE.replace(/^http/, "ws")}/ws?takeover=1`;
    // Node's WebSocket (undici) accepts headers; the daemon requires Origin to match Host.
    const sock = new WebSocket(url, { headers: { Cookie: cookie, Origin: BASE } } as unknown as string[]);
    sock.binaryType = "arraybuffer";
    const t = new WsTransport(sock);
    return new Promise((resolve, reject) => {
      let hello = false;
      const timer = setTimeout(() => {
        sock.close();
        reject(new Error("no hello from the daemon"));
      }, HELLO_TIMEOUT_MS);
      sock.onmessage = (ev) => {
        if (!hello && typeof ev.data === "string") {
          const m = JSON.parse(ev.data) as { t?: string };
          if (m.t === "busy") {
            clearTimeout(timer);
            reject(new UserError("Ovládání je otevřené v jiném prohlížeči (busy). Zavřete tam stránku komba a zkuste to znovu."));
            return;
          }
          if (m.t === "hello") {
            hello = true;
            clearTimeout(timer);
            this.sock = sock;
            this.client = new VoxClient(t);
            this.client.onState((s) => (this.state = s));
            resolve();
            return;
          }
        }
        t.message(ev.data);
      };
      sock.onclose = (ev) => {
        clearTimeout(timer);
        if (!hello) {
          reject(ev.code === BUSY_CLOSE_CODE
            ? new UserError("Ovládání je otevřené v jiném prohlížeči (busy). Zavřete tam stránku komba a zkuste to znovu.")
            : new Error(`socket closed (${ev.code})`));
          return;
        }
        if (this.sock === sock) {
          this.client?.reset();
          this.sock = null;
          this.client = null;
          this.state = null;
        }
      };
      sock.onerror = () => {}; // onclose follows
    });
  }

  close(): void {
    if (this.idle) clearTimeout(this.idle);
    this.idle = null;
    const s = this.sock;
    this.client?.reset();
    this.sock = null;
    this.client = null;
    this.state = null;
    s?.close(1000);
  }
}

const amp = new Amp();

// --- naming -----------------------------------------------------------------------------------

function norm(s: string): string {
  return s.toLowerCase().replace(/[^a-z0-9]/g, "");
}

function byName<T extends { id: number; name: string }>(what: string, list: readonly T[], v: number | string): number {
  if (typeof v === "number") {
    if (!list[v]) throw new UserError(`${what} ${v} does not exist (0..${list.length - 1}).`);
    return v;
  }
  const hit = list.find((x) => norm(x.name) === norm(v));
  if (!hit) throw new UserError(`Unknown ${what} "${v}". Options: ${list.map((x) => x.name).join(", ")}.`);
  return hit.id;
}

function slotIndex(name: string): number {
  const i = USER_SLOT_NAMES.indexOf(name.toUpperCase() as (typeof USER_SLOT_NAMES)[number]);
  if (i < 0) throw new UserError(`Unknown program slot "${name}". Options: ${USER_SLOT_NAMES.join(", ")}.`);
  return i;
}

/** Value used for an effect param the caller did not give after a type change. */
function defaultParam(spec: EffectParam): number {
  switch (spec.unit) {
    case "0-100": return 50;
    case "bool":
    case "voice": return 0;
    case "ms": return spec.name === "TIME" ? 350 : 20;
    case "mHz": return Math.min(spec.max, Math.max(spec.min, 1000));
  }
}

function describeEffect(slot: EffectSlot, on: boolean, type: number, params: readonly number[]) {
  const row = EFFECT_PARAMS[slot][type];
  const named: Record<string, number> = {};
  row.forEach((spec, n) => {
    if (spec) named[spec.name] = params[n];
  });
  return { on, type: SLOT_TYPES[slot][type]?.name ?? type, params: named };
}

function describeProgram(p: Program) {
  const model = AMP_MODELS[p.ampModel];
  const ampVals: Record<string, number | string> = {};
  AMP_PARAMS.forEach((spec) => {
    const v = p.amp[spec.index];
    const name = spec.index === 5 && model ? model.toneLabel : spec.name;
    if (spec.kind === "bias") ampVals[name] = BIAS_NAMES[v] ?? v;
    else if (spec.kind === "class") ampVals[name] = CLASS_NAMES[v] ?? v;
    else if (spec.kind === "switch") ampVals[name] = v ? "on" : "off";
    else ampVals[name] = v;
  });
  return {
    name: p.name,
    amp: { model: model?.name ?? p.ampModel, ...ampVals },
    nr: p.nr,
    pedal1: describeEffect("pedal1", p.pedal1On, p.pedal1Type, p.pedal1),
    pedal2: describeEffect("pedal2", p.pedal2On, p.pedal2Type, p.pedal2),
    reverb: describeEffect("reverb", p.reverbOn, p.reverbType, p.reverb),
  };
}

function describeState(s: AmpState) {
  return {
    mode: s.mode,
    program: s.program === null ? null : s.mode === "user" ? USER_SLOT_NAMES[s.program] : s.program,
    current: s.current ? describeProgram(s.current) : null,
    slots: Object.fromEntries(USER_SLOT_NAMES.map((n, i) => [n, s.slots[i]?.name ?? null])),
  };
}

// --- set_sound --------------------------------------------------------------------------------

const knob = z.number().int().min(0).max(100);
const onOff = z.union([z.boolean(), z.literal(0), z.literal(1)]);

const effectInput = z.object({
  on: z.boolean().optional(),
  type: z.union([z.number().int(), z.string()]).optional().describe("Effect type id or name, e.g. \"FUZZ\"."),
  params: z.record(z.string(), z.number().int()).optional()
    .describe("Params by name as listed by vox_catalog, e.g. {\"DRIVE\": 80, \"TONE\": 40}."),
}).strict();

const soundInput = {
  amp: z.object({
    model: z.union([z.number().int(), z.string()]).optional().describe("Amp model id or amp's own name, e.g. \"BRIT 800\"."),
    gain: knob.optional(), treble: knob.optional(), middle: knob.optional(), bass: knob.optional(),
    presence: knob.optional().describe("PRESENCE, or TONE on VOX AC30 / AC30TB."),
    resonance: knob.optional(),
    brightCap: onOff.optional(), lowCut: onOff.optional(), midBoost: onOff.optional(),
    bias: z.enum(BIAS_NAMES).optional(),
    ampClass: z.enum(CLASS_NAMES).optional(),
  }).strict().optional(),
  nr: knob.optional().describe("Noise reduction 0..100."),
  pedal1: effectInput.optional(),
  pedal2: effectInput.optional(),
  reverb: effectInput.optional(),
};
type SoundInput = { [K in keyof typeof soundInput]?: z.infer<(typeof soundInput)[K]> };

/** Merges a partial sound into the current program. Program VOLUME is never part of it. */
function mergeSound(cur: Program, inp: SoundInput): AuditionSettings {
  const a = inp.amp ?? {};
  const model = a.model === undefined ? cur.ampModel : byName("amp model", AMP_MODELS, a.model);
  const ampOut = { model } as AuditionSettings["amp"];
  for (const [key, index] of Object.entries(AMP_INDEX) as [Exclude<AmpKey, "model">, number][]) {
    const v = a[key];
    let n: number;
    if (v === undefined) n = cur.amp[index];
    else if (key === "bias") n = BIAS_NAMES.indexOf(v as (typeof BIAS_NAMES)[number]);
    else if (key === "ampClass") n = CLASS_NAMES.indexOf(v as (typeof CLASS_NAMES)[number]);
    else n = typeof v === "boolean" ? Number(v) : (v as number);
    ampOut[key] = n;
  }

  const fx = (slot: EffectSlot) => {
    const e = inp[slot] ?? {};
    const curType = cur[`${slot}Type`];
    const type = e.type === undefined ? curType : byName(`${slot} type`, SLOT_TYPES[slot], e.type);
    const row = EFFECT_PARAMS[slot][type];
    const given = new Map(Object.entries(e.params ?? {}).map(([k, v]) => [norm(k), v]));
    for (const k of given.keys()) {
      if (!row.some((s) => s && norm(s.name) === k)) {
        throw new UserError(`${slot} ${SLOT_TYPES[slot][type].name} has no param "${k}". Params: ${row.filter(Boolean).map((s) => s!.name).join(", ")}.`);
      }
    }
    const params = row.map((spec, n) => {
      if (!spec) return 0;
      const v = given.get(norm(spec.name));
      if (v !== undefined) return v;
      return type === curType ? cur[slot][n] : defaultParam(spec);
    });
    return { on: e.on ?? cur[`${slot}On`], type, params };
  };

  return { amp: ampOut, nr: inp.nr ?? cur.nr, pedal1: fx("pedal1"), pedal2: fx("pedal2"), reverb: fx("reverb") };
}

// --- tools ------------------------------------------------------------------------------------

function ok(v: unknown) {
  return { content: [{ type: "text" as const, text: JSON.stringify(v, null, 2) }] };
}

function fail(e: unknown) {
  const text = e instanceof UserError ? e.message
    : e instanceof VoxError ? `Amp request failed: ${e.code}`
    : e instanceof RangeError ? `Invalid value: ${e.message}`
    : `Error: ${e instanceof Error ? e.message : String(e)}`;
  return { isError: true, content: [{ type: "text" as const, text }] };
}

async function guarded(fn: () => Promise<unknown>) {
  try {
    return ok(await fn());
  } catch (e) {
    return fail(e);
  }
}

function songSummary(s: SongPreset) {
  return { id: s.id, title: s.title, artist: s.artist, year: s.year, genre: s.genre };
}

const server = new McpServer({ name: "velvetronix", version: "0.1.0" });

server.registerTool("vox_status", {
  title: "Amp status",
  description: "Reads the VOX Valvetronix X amp: mode, active program, the full current sound (Current Buffer) and the names of the 8 user programs A1..B4.",
  inputSchema: {},
}, () => guarded(() => amp.use(async (_c, s) => describeState(s()))));

server.registerTool("vox_catalog", {
  title: "Amp models and effects",
  description: "Lists amp models, effect types per slot and their params with ranges. Knobs are 0..100 (shown on the amp as 0.0..10.0), SPEED is in mHz, TIME/PRE DELAY in ms. Needs no amp connection.",
  inputSchema: {},
}, () => guarded(async () => ({
  ampModels: AMP_MODELS.map((m) => ({ id: m.id, name: m.name, brightCap: m.brightCap, toneLabel: m.toneLabel })),
  ampParams: AMP_PARAMS.filter((p) => p.index !== 4).map((p) => ({ name: p.name, kind: p.kind, min: p.min, max: p.max })),
  bias: BIAS_NAMES, ampClass: CLASS_NAMES,
  effects: Object.fromEntries(SLOTS.map((slot) => [slot, SLOT_TYPES[slot].map((t) => ({
    id: t.id, name: t.name, params: EFFECT_PARAMS[slot][t.id].filter(Boolean),
  }))])),
  notes: "Program VOLUME and the Power Level knob are not controllable here.",
})));

server.registerTool("vox_set_sound", {
  title: "Set sound live",
  description: "Sets a sound in the amp live (Current Buffer only, nothing is saved, program VOLUME is never touched). Every field is optional and merges into the current sound. When an effect type changes, params not given get neutral defaults (knobs 50).",
  inputSchema: soundInput,
}, (inp) => guarded(() => amp.use(async (c, s) => {
  const cur = s().current;
  if (!cur) throw new UserError("The current program is not loaded; try again.");
  const result = await c.audition(mergeSound(cur, inp));
  return { result, current: s().current ? describeProgram(s().current!) : null };
})));

server.registerTool("vox_list_songs", {
  title: "List song presets",
  description: "Lists the song presets built into pi-velvetronix (the Písničky tab). Optional case-insensitive search in title, artist and genre.",
  inputSchema: { query: z.string().optional() },
}, ({ query }) => guarded(async () => {
  const q = query ? norm(query) : "";
  return SONGS.filter((s) => !q || norm(`${s.title} ${s.artist} ${s.genre}`).includes(q)).map(songSummary);
}));

server.registerTool("vox_play_song", {
  title: "Play a song preset",
  description: "Sets one of the built-in song presets live (same as Zahrát in the UI; nothing is saved).",
  inputSchema: { id: z.string().describe("Song id from vox_list_songs.") },
}, ({ id }) => guarded(async () => {
  const song = SONGS.find((s) => s.id === id);
  if (!song) throw new UserError(`Unknown song id "${id}".`);
  return amp.use(async (c) => ({ song: { ...songSummary(song), description: song.description }, result: await c.audition(song) }));
}));

server.registerTool("vox_select_program", {
  title: "Select program",
  description: "Switches the amp to user program A1..B4. Live edits that were not saved are lost.",
  inputSchema: { slot: z.string().describe("A1..A4 or B1..B4.") },
}, ({ slot }) => guarded(() => amp.use(async (c, s) => {
  await c.select(slotIndex(slot));
  return describeState(s());
})));

server.registerTool("vox_save_program", {
  title: "Save program",
  description: "Writes the current sound into user program A1..B4 under a new name, OVERWRITING what is stored there. Use only when the user explicitly asked to save into that slot.",
  inputSchema: {
    slot: z.string().describe("A1..A4 or B1..B4."),
    name: z.string().max(16).regex(/^[\x20-\x7e]*$/, "ASCII only").describe("Up to 16 ASCII characters."),
  },
}, ({ slot, name }) => guarded(() => amp.use(async (c, s) => {
  const cur = s().current;
  if (!cur) throw new UserError("The current program is not loaded; try again.");
  const i = slotIndex(slot);
  const overwritten = s().slots[i]?.name ?? null;
  const check = await c.save(i, { ...cur, name });
  return { slot: USER_SLOT_NAMES[i], name, overwritten, check };
})));

server.registerTool("vox_disconnect", {
  title: "Release control",
  description: "Closes the controller connection at once so the browser gets control back (it also closes by itself after 8 s idle).",
  inputSchema: {},
}, () => guarded(async () => {
  amp.close();
  return { disconnected: true };
}));

await server.connect(new StdioServerTransport());
