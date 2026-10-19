// VOX request/response engine, docs/protocol.md §4.1, §6.2, §7, §8, §9.

import { auditionPlan, type AuditionSettings } from "./audition";
import { dec14 } from "./encoding";
import { FN, FUNC_INDEX, funcOf, msg, parse, USER_SLOTS, type Mode, type Parsed } from "./messages";
import { applyParam, encodeProgram, SLOT_PARAM_COUNT, SLOT_PARAM_ID, SLOT_SUB, type EffectSlot, type Program } from "./program";

export interface Transport {
  send(frame: Uint8Array, save: boolean): void;
  onFrame(cb: (f: Uint8Array) => void): void;
  onError(cb: (e: { code: string; func: number | null }) => void): void;
}

export interface AmpState {
  mode: Mode | null;
  /** Active user/preset program; null in manual mode or before the first read. */
  program: number | null;
  current: Program | null;
  slots: (Program | null)[];
  ready: boolean;
}

/**
 * code: "timeout", "nak" (24), "format_error" (26), "reset", "send_failed",
 * or a server BridgeError code ("forbidden", "amp_disconnected", ...).
 */
export class VoxError extends Error {
  constructor(readonly code: string) {
    super(`VOX request failed: ${code}`);
    this.name = "VoxError";
  }
}

export interface VoxClientOptions {
  now?: () => number;
  setTimeout?: typeof setTimeout;
  clearTimeout?: typeof clearTimeout;
}

type SaveCheck = "verified" | "mismatch" | "unverified";
/** save() result; "-inactive" when the slot could not be made the active program. */
export type SaveResult = SaveCheck | `${SaveCheck}-inactive`;

export interface AuditionFailure {
  /** Step label from auditionPlan, e.g. "amp GAIN". */
  step: string;
  code: string;
}

export interface AuditionResult {
  total: number;
  /** Steps sent and answered (ACK or NAK). */
  done: number;
  /** Steps the amp did not confirm. */
  failed: AuditionFailure[];
  /** Error code that stopped the sequence early (timeout, reset, server error), else null. */
  aborted: string | null;
  /** Program VOLUME changed by the amp itself during the audition (it is never sent), else null. */
  volume: { from: number; to: number } | null;
}

type Outcome = { ok: true; reply: Parsed } | { ok: false; code: string };
type Timer = ReturnType<typeof setTimeout>;

interface Request {
  frame: Uint8Array;
  save: boolean;
  timeoutMs: number;
  retries: number;
  /** Set on requests that may be coalesced or deduplicated while queued. */
  key?: string;
  /** Hold the queue after the ACK until `count` `41 <id>` notifications or BURST_HOLD_MS. */
  hold?: { id: number; count: number };
  resolve(reply: Parsed): void;
  reject(e: VoxError): void;
}

/** Max wait for the default-value burst after an effect type ACK (P§6.2, estimate). */
const BURST_HOLD_MS = 200;
const READ_KEY_CURRENT = "read:current";
/** Program.amp index of VOLUME. */
const VOLUME_INDEX = 4;

/** P§9.2 timeouts and P§9.3 retries (reads once, writes and live edits never). */
function policy(frame: Uint8Array): { timeoutMs: number; retries: number } {
  switch (funcOf(frame)) {
    case null: return { timeoutMs: 1000, retries: 0 }; // device inquiry, optional (P§8.1)
    case FN.MODE_REQUEST:
    case FN.CURRENT_PROGRAM_DUMP_REQUEST:
    case FN.PROGRAM_DUMP_REQUEST:
      return { timeoutMs: 1500, retries: 1 };
    case FN.PROGRAM_DUMP:
    case FN.CURRENT_PROGRAM_DUMP:
      return { timeoutMs: 2000, retries: 0 };
    default: return { timeoutMs: 1000, retries: 0 }; // 41, 4E
  }
}

function failure(p: Parsed): Outcome | null {
  if (p.kind === "nak") return { ok: false, code: "nak" };
  if (p.kind === "format_error") return { ok: false, code: "format_error" };
  return null;
}

/** P§4.1: does reply p complete or fail the request that sent `frame`? null = not related. */
function match(frame: Uint8Array, p: Parsed): Outcome | null {
  const ok: Outcome = { ok: true, reply: p };
  switch (funcOf(frame)) {
    case null: return p.kind === "identity" ? ok : null;
    case FN.MODE_REQUEST:
      if (p.kind === "mode") return ok;
      return p.kind === "format_error" ? { ok: false, code: "format_error" } : null;
    case FN.CURRENT_PROGRAM_DUMP_REQUEST:
      return p.kind === "current_program" ? ok : failure(p);
    case FN.PROGRAM_DUMP_REQUEST:
      if (p.kind === "program") return p.bank === frame[7] && p.slot === frame[8] ? ok : null;
      return failure(p);
    default:
      return p.kind === "ack" ? ok : failure(p);
  }
}

/** Effect slot of a `41 03 ss tt` type-change frame, else null. */
function typeChangeSlot(frame: Uint8Array): EffectSlot | null {
  if (funcOf(frame) !== FN.PARAMETER_CHANGE || frame[7] !== 0x03) return null;
  for (const slot of ["pedal1", "pedal2", "reverb"] as const) if (frame[8] === SLOT_SUB[slot]) return slot;
  return null;
}

function bytesEqual(a: Uint8Array, b: Uint8Array): boolean {
  return a.length === b.length && a.every((v, i) => v === b[i]);
}

function initialState(): AmpState {
  return { mode: null, program: null, current: null, slots: new Array<Program | null>(USER_SLOTS).fill(null), ready: false };
}

export class VoxClient {
  private queue: Request[] = [];
  private inflight: Request | null = null;
  private timer: Timer | null = null;
  private hold: { id: number; remaining: number; timer: Timer } | null = null;
  private auditioning = false;
  private state: AmpState = initialState();
  private listeners: ((s: AmpState) => void)[] = [];
  private readonly setT: (cb: () => void, ms: number) => Timer;
  private readonly clearT: (t: Timer) => void;

  constructor(private readonly t: Transport, opts: VoxClientOptions = {}) {
    // Resolve the timer functions at call time so fake timers installed later still apply.
    this.setT = (cb, ms) => (opts.setTimeout ?? setTimeout)(cb, ms);
    this.clearT = (h) => (opts.clearTimeout ?? clearTimeout)(h);
    t.onFrame((f) => this.onFrame(f));
    t.onError((e) => this.onServerError(e));
  }

  onState(cb: (s: AmpState) => void): void {
    this.listeners.push(cb);
  }

  /** An audition is running: select, save, type changes and live edits are refused until it ends. */
  get busy(): boolean {
    return this.auditioning;
  }

  /** P§8.1: inquiry (optional, failure ignored), 12, 10, 1C x 8. */
  async readAll(): Promise<AmpState> {
    this.state.ready = false;
    try {
      await this.request(msg.deviceInquiry());
    } catch {
      // The inquiry is optional; the daemon already confirms identity.
    }
    const mode = (await this.request(msg.modeRequest())) as Extract<Parsed, { kind: "mode" }>;
    this.setMode(mode.mode, mode.program);
    await this.readCurrent();
    for (let slot = 0; slot < USER_SLOTS; slot++) {
      const r = (await this.request(msg.programRequest(slot))) as Extract<Parsed, { kind: "program" }>;
      this.setSlot(slot, r.program);
    }
    this.state.ready = true;
    this.emit();
    return this.snapshot();
  }

  /** P§8.2: 4E 00 pp, ACK, then re-read the Current Buffer. */
  async select(slot: number): Promise<void> {
    if (this.auditioning) throw new VoxError("busy");
    await this.request(msg.selectProgram(slot));
    this.setMode("user", slot);
    try {
      await this.readCurrent();
    } catch (e) {
      // The amp now holds another program; the old buffer must not stay editable.
      this.clearCurrent();
      throw e;
    }
  }

  /** Fire-and-forget live edit. Queued edits of the same (id, sub) are coalesced; the model changes on ACK. */
  setParam(id: number, sub: number, v: number): void {
    // The UI is locked during an audition; an edit now would land in the middle of the sequence.
    if (this.auditioning) return;
    const frame = msg.param(id, sub, v);
    const key = `param:${id}:${sub}`;
    const queued = this.queue.find((r) => r.key === key);
    if (queued) {
      queued.frame = frame;
      return;
    }
    const r: Request = {
      frame, save: false, key, ...policy(frame),
      resolve: () => this.applyLocal(r.frame),
      reject: () => {}, // NAK resync happens in complete(); a newer value supersedes a timeout.
    };
    this.enqueue(r);
  }

  /** Resolves on ACK; the queue is then held for the amp's default-value burst (P§6.2). */
  async setEffectType(slot: EffectSlot, t: number): Promise<void> {
    if (this.auditioning) throw new VoxError("busy");
    const frame = msg.effectType(slot, t);
    await this.request(frame);
    this.applyLocal(frame);
  }

  /**
   * P§8.3: 4C (save flag) -> ACK -> 4E 00 pp -> ACK -> 1C 00 pp -> byte compare. 4C is never retried.
   * After an ACKed 4C the slot is written, so a failed 4E or 1C does not fail the save:
   * "-inactive" = the slot was not made the active program, "unverified" = the read-back failed.
   */
  async save(slot: number, p: Program): Promise<SaveResult> {
    if (this.auditioning) throw new VoxError("busy");
    const struct = encodeProgram(p);
    let timedOut = false;
    try {
      await this.request(msg.writeProgram(slot, p), true);
    } catch (e) {
      // P§8.3: on a 4C timeout skip the reselect and verify by reading the slot back.
      if (!(e instanceof VoxError && e.code === "timeout")) throw e;
      timedOut = true;
    }
    let selected = false;
    if (!timedOut) {
      try {
        await this.request(msg.selectProgram(slot));
        this.setMode("user", slot);
        selected = true;
      } catch {
        // The slot is already written by the ACKed 4C.
      }
    }
    let check: SaveCheck;
    try {
      const r = (await this.request(msg.programRequest(slot))) as Extract<Parsed, { kind: "program" }>;
      this.setSlot(slot, r.program);
      check = bytesEqual(r.program.raw, struct) ? "verified" : "mismatch";
    } catch (e) {
      // Without a 4C ACK nothing confirms the write.
      if (timedOut) throw e;
      check = "unverified";
    }
    // 4E reloaded the Current Buffer from the slot.
    if (selected) this.refreshCurrent();
    return selected ? check : `${check}-inactive`;
  }

  /**
   * Applies a whole sound to the Current Buffer only (P§8.4), one ACKed `41` at a time in the order of
   * auditionPlan. Never writes amp memory and never touches VOLUME. A NAK is recorded and the next step
   * is sent, except that the params of an effect whose type change was refused are skipped (they were
   * checked against the new type). Any other failure (timeout, reset, server error) stops the sequence.
   * Only one audition runs at a time and select/save/setEffectType reject with VoxError("busy") meanwhile
   * (setParam is dropped). Invalid settings reject before anything is sent.
   */
  async audition(s: AuditionSettings, onProgress?: (done: number, total: number) => void): Promise<AuditionResult> {
    if (this.auditioning) throw new VoxError("busy");
    const steps = auditionPlan(s);
    const volumeBefore = this.state.current?.amp[VOLUME_INDEX] ?? null;
    this.auditioning = true;
    this.emit();
    const result: AuditionResult = { total: steps.length, done: 0, failed: [], aborted: null, volume: null };
    const refusedTypes = new Set<EffectSlot>();
    try {
      for (const step of steps) {
        if (step.slot && !step.isType && refusedTypes.has(step.slot)) {
          result.done++;
          onProgress?.(result.done, result.total);
          continue;
        }
        try {
          await this.request(step.frame, false, undefined, step.hold);
          this.applyLocal(step.frame);
        } catch (e) {
          const code = e instanceof VoxError ? e.code : "send_failed";
          result.failed.push({ step: step.label, code });
          if (code !== "nak" && code !== "format_error") {
            result.aborted = code;
            break;
          }
          if (step.slot && step.isType) refusedTypes.add(step.slot);
        }
        result.done++;
        onProgress?.(result.done, result.total);
      }
    } finally {
      this.auditioning = false;
      this.emit();
    }
    // VOLUME is never sent, but the amp may change it itself on a model change (P§6.2).
    const volumeAfter = this.state.current?.amp[VOLUME_INDEX] ?? null;
    if (volumeBefore !== null && volumeAfter !== null && volumeAfter !== volumeBefore) result.volume = { from: volumeBefore, to: volumeAfter };
    return result;
  }

  /** Drops queue, in-flight request and state (transport reconnect). */
  reset(): void {
    if (this.timer !== null) this.clearT(this.timer);
    if (this.hold) this.clearT(this.hold.timer);
    this.timer = null;
    this.hold = null;
    const dropped = [...(this.inflight ? [this.inflight] : []), ...this.queue];
    this.inflight = null;
    this.queue = [];
    this.state = initialState();
    for (const r of dropped) r.reject(new VoxError("reset"));
    this.emit();
  }

  private request(frame: Uint8Array, save = false, key?: string, hold?: Request["hold"]): Promise<Parsed> {
    return new Promise((resolve, reject) => {
      this.enqueue({ frame, save, key, hold, ...policy(frame), resolve, reject });
    });
  }

  private enqueue(r: Request): void {
    this.queue.push(r);
    this.pump();
  }

  private pump(): void {
    if (this.inflight || this.hold) return;
    const r = this.queue.shift();
    if (!r) return;
    this.inflight = r;
    this.transmit(r);
  }

  private transmit(r: Request): void {
    // Arm the timer before sending so a synchronous reply finds a complete in-flight record.
    this.timer = this.setT(() => this.onTimeout(r), r.timeoutMs);
    try {
      this.t.send(r.frame, r.save);
    } catch {
      this.complete(r, { ok: false, code: "send_failed" });
    }
  }

  private onTimeout(r: Request): void {
    if (this.inflight !== r) return;
    this.timer = null;
    if (r.retries > 0) {
      r.retries--;
      this.transmit(r);
      return;
    }
    this.complete(r, { ok: false, code: "timeout" });
  }

  private complete(r: Request, outcome: Outcome): void {
    if (this.inflight !== r) return;
    if (this.timer !== null) this.clearT(this.timer);
    this.timer = null;
    this.inflight = null;
    if (outcome.ok) {
      const slot = typeChangeSlot(r.frame);
      if (slot) this.startHold(SLOT_PARAM_ID[slot], SLOT_PARAM_COUNT[slot]);
      else if (r.hold) this.startHold(r.hold.id, r.hold.count);
      r.resolve(outcome.reply);
    } else {
      // P§8.5: after a live-edit NAK, resync the Current Buffer.
      if (funcOf(r.frame) === FN.PARAMETER_CHANGE && (outcome.code === "nak" || outcome.code === "format_error")) {
        this.refreshCurrent();
      }
      r.reject(new VoxError(outcome.code));
    }
    this.pump();
  }

  private startHold(id: number, count: number): void {
    const timer = this.setT(() => this.releaseHold(), BURST_HOLD_MS);
    this.hold = { id, remaining: count, timer };
  }

  private releaseHold(): void {
    if (!this.hold) return;
    this.clearT(this.hold.timer);
    this.hold = null;
    this.pump();
  }

  private onServerError(e: { code: string; func: number | null }): void {
    const r = this.inflight;
    if (r && funcOf(r.frame) === e.func) this.complete(r, { ok: false, code: e.code });
  }

  private onFrame(f: Uint8Array): void {
    const p = parse(f);
    // P§7: these never complete a pending request, even one with the same function byte.
    switch (p.kind) {
      case "param": return this.onParamNotification(p.id, p.sub, p.value);
      case "mode_change": return this.onModeChange(p.mode, p.program);
      case "write_completed": return this.refreshSlot(p.slot);
    }
    const r = this.inflight;
    const outcome = r && match(r.frame, p);
    // Anything unmatched (late replies, unknown frames) is dropped.
    if (r && outcome) this.complete(r, outcome);
  }

  private onParamNotification(id: number, sub: number, value: number): void {
    if (this.state.current) {
      this.state.current = applyParam(this.state.current, id, sub, value);
      this.emit();
    }
    if (this.hold && id === this.hold.id && --this.hold.remaining <= 0) this.releaseHold();
  }

  private onModeChange(mode: Mode, program: number): void {
    const prog = mode === "manual" ? null : program;
    const same = this.state.mode === mode && this.state.program === prog;
    const reading = this.inflight?.key === READ_KEY_CURRENT || this.queue.some((r) => r.key === READ_KEY_CURRENT);
    // An echo of our own select is idempotent: the refresh is already on its way.
    if (same && reading) return;
    this.setMode(mode, program);
    this.refreshCurrent(true);
  }

  private async readCurrent(): Promise<void> {
    const r = (await this.request(msg.currentProgramRequest(), false, READ_KEY_CURRENT)) as Extract<Parsed, { kind: "current_program" }>;
    this.state.current = r.program;
    this.emit();
  }

  /** clearOnFail: the amp reloaded its buffer, so on a failed read the old one is stale. */
  private refreshCurrent(clearOnFail = false): void {
    if (this.queue.some((r) => r.key === READ_KEY_CURRENT)) return;
    this.readCurrent().catch(() => {
      if (clearOnFail) this.clearCurrent();
    });
  }

  private clearCurrent(): void {
    this.state.current = null;
    this.emit();
  }

  private refreshSlot(slot: number): void {
    const key = `read:slot:${slot}`;
    if (this.queue.some((r) => r.key === key)) return;
    this.request(msg.programRequest(slot), false, key)
      .then((r) => this.setSlot(slot, (r as Extract<Parsed, { kind: "program" }>).program))
      .catch(() => {});
  }

  /** Applies an ACKed host-sent `41` frame to the model. */
  private applyLocal(frame: Uint8Array): void {
    if (!this.state.current) return;
    const value = dec14(frame[FUNC_INDEX + 3], frame[FUNC_INDEX + 4]);
    this.state.current = applyParam(this.state.current, frame[FUNC_INDEX + 1], frame[FUNC_INDEX + 2], value);
    this.emit();
  }

  private setMode(mode: Mode, program: number): void {
    this.state.mode = mode;
    this.state.program = mode === "manual" ? null : program;
    this.emit();
  }

  private setSlot(slot: number, p: Program): void {
    this.state.slots[slot] = p;
    this.emit();
  }

  private snapshot(): AmpState {
    return { ...this.state, slots: [...this.state.slots] };
  }

  private emit(): void {
    const s = this.snapshot();
    for (const cb of this.listeners) cb(s);
  }
}
