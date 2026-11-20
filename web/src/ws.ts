// WebSocket transport to the daemon bridge, docs/design.md §7 and §9.1.

import type { AmpStatus, UpdateInfo } from "./api";
import type { Transport } from "./protocol/client";

export type ServerText =
  | { t: "hello"; version: string }
  | { t: "amp"; amp: AmpStatus }
  | { t: "update"; update: UpdateInfo }
  | { t: "error"; code: string; func: number | null; detail?: string }
  | { t: "pong" }
  | { t: "busy" }
  | { t: "replaced" }
  | { t: "taken" }
  | { t: "watching" }
  | { t: "free" }
  /** Frames for this page were dropped (it was too slow): its model may be stale. */
  | { t: "resync" };

export type ConnState =
  | { kind: "idle" }
  | { kind: "connecting" }
  /** "hello" received; only now frames may be sent. */
  | { kind: "open" }
  | { kind: "waiting"; retryInMs: number }
  /** Another tab or window of this session took over; no automatic reconnect until retry(). */
  | { kind: "replaced" }
  /**
   * All controller slots are taken (busy) or a takeover dropped us. The socket watches the traffic
   * read-only (?watch=1) and becomes a controller again once the server says "free".
   */
  | { kind: "watching" };

/** The subset of the browser WebSocket used here (injectable for tests). */
export interface SocketLike {
  binaryType: BinaryType;
  send(data: string | ArrayBufferView): void;
  close(code?: number, reason?: string): void;
  onopen: ((ev: unknown) => void) | null;
  onmessage: ((ev: { data: unknown }) => void) | null;
  onclose: ((ev: { code: number }) => void) | null;
  onerror: ((ev: unknown) => void) | null;
}

export interface WsOptions {
  url?: () => string;
  createSocket?: (url: string) => SocketLike;
  setTimeout?: typeof setTimeout;
  clearTimeout?: typeof clearTimeout;
  setInterval?: typeof setInterval;
  clearInterval?: typeof clearInterval;
}

export const BUSY_CLOSE_CODE = 4001;
export const REPLACED_CLOSE_CODE = 4002;
export const TAKEN_CLOSE_CODE = 4003;
export const FREE_CLOSE_CODE = 4004;
/** Pause before (re)entering watch mode, so a lost race for a just-freed slot cannot spin. */
export const WATCH_DELAY_MS = 300;
export const PING_INTERVAL_MS = 10_000;
const KIND_NORMAL = 0x00;
const KIND_SAVE = 0x01;
const KIND_ECHO = 0x02;

/** Reconnect delay after `failures` consecutive failed attempts (1 = first): 1, 2, 4, 8, 10, 10 ... s. */
export function backoffMs(failures: number): number {
  return Math.min(10_000, 1000 * 2 ** Math.max(0, failures - 1));
}

function defaultUrl(): string {
  return `${location.protocol === "https:" ? "wss" : "ws"}://${location.host}/ws`;
}

type Timer = ReturnType<typeof setTimeout>;
type Interval = ReturnType<typeof setInterval>;

export class WsTransport implements Transport {
  private sock: SocketLike | null = null;
  private state: ConnState = { kind: "idle" };
  private failures = 0;
  private busySeen = false;
  private replacedSeen = false;
  private takenSeen = false;
  private freeSeen = false;
  /** Connect with ?watch=1 (another controller holds the slot). */
  private watch = false;
  private reconnectTimer: Timer | null = null;
  private pingTimer: Interval | null = null;
  private readonly frameCbs: ((f: Uint8Array) => void)[] = [];
  private readonly echoCbs: ((f: Uint8Array) => void)[] = [];
  private readonly errorCbs: ((e: { code: string; func: number | null }) => void)[] = [];
  private readonly textCbs: ((m: ServerText) => void)[] = [];
  private readonly stateCbs: ((s: ConnState) => void)[] = [];

  constructor(private readonly opts: WsOptions = {}) {}

  get connState(): ConnState {
    return this.state;
  }

  onFrame(cb: (f: Uint8Array) => void): void {
    this.frameCbs.push(cb);
  }

  /** Frames another controller (tab, browser, MCP server) sent to the amp. */
  onEcho(cb: (f: Uint8Array) => void): void {
    this.echoCbs.push(cb);
  }

  onError(cb: (e: { code: string; func: number | null }) => void): void {
    this.errorCbs.push(cb);
  }

  onText(cb: (m: ServerText) => void): void {
    this.textCbs.push(cb);
  }

  onState(cb: (s: ConnState) => void): void {
    this.stateCbs.push(cb);
  }

  /** Connects unless already connected or connecting. */
  start(): void {
    if (this.state.kind === "idle") this.open();
  }

  /** Connects now, resetting the backoff. Also leaves the replaced state. */
  retry(): void {
    if (this.state.kind === "open" || this.state.kind === "connecting" || this.state.kind === "watching") return;
    this.clearReconnect();
    this.failures = 0;
    this.open();
  }

  /** Closes for good (logout); no reconnect. */
  stop(): void {
    this.clearReconnect();
    this.watch = false;
    const s = this.sock;
    this.detach();
    s?.close(1000, "bye");
    this.setState({ kind: "idle" });
  }

  send(frame: Uint8Array, save: boolean): void {
    if (this.state.kind !== "open" || !this.sock) throw new Error("not connected");
    const msg = new Uint8Array(frame.length + 1);
    msg[0] = save ? KIND_SAVE : KIND_NORMAL;
    msg.set(frame, 1);
    this.sock.send(msg);
  }

  private open(): void {
    this.busySeen = false;
    this.replacedSeen = false;
    this.takenSeen = false;
    this.freeSeen = false;
    // While watching the page keeps showing the amp, so there is no "connecting" in between.
    if (!this.watch) this.setState({ kind: "connecting" });
    const create = this.opts.createSocket ?? ((url: string) => new WebSocket(url) as unknown as SocketLike);
    let s: SocketLike;
    try {
      s = create((this.opts.url ?? defaultUrl)() + (this.watch ? "?watch=1" : ""));
    } catch {
      this.scheduleReconnect();
      return;
    }
    s.binaryType = "arraybuffer";
    this.sock = s;
    s.onmessage = (ev) => {
      if (this.sock === s) this.onMessage(ev.data);
    };
    s.onclose = (ev) => {
      if (this.sock === s) this.onClose(ev.code);
    };
    s.onerror = () => {}; // A close event always follows.
  }

  private onMessage(data: unknown): void {
    if (data instanceof ArrayBuffer) {
      const b = new Uint8Array(data);
      if (b.length < 2) return;
      if (b[0] === KIND_NORMAL) for (const cb of this.frameCbs) cb(b.slice(1));
      else if (b[0] === KIND_ECHO) for (const cb of this.echoCbs) cb(b.slice(1));
      return;
    }
    if (typeof data !== "string") return;
    let m: ServerText;
    try {
      m = JSON.parse(data) as ServerText;
    } catch {
      return;
    }
    if (!m || typeof m !== "object" || typeof m.t !== "string") return;
    switch (m.t) {
      case "hello":
        this.failures = 0;
        // A watcher gets "watching" right after hello.
        if (!this.watch) this.setState({ kind: "open" });
        this.startPing();
        break;
      case "watching":
        this.setState({ kind: "watching" });
        break;
      case "free":
        this.freeSeen = true;
        break;
      case "busy":
        this.busySeen = true;
        break;
      case "replaced":
        this.replacedSeen = true;
        break;
      case "taken":
        this.takenSeen = true;
        break;
      case "error":
        for (const cb of this.errorCbs) cb({ code: m.code, func: m.func ?? null });
        break;
    }
    for (const cb of this.textCbs) cb(m);
  }

  private onClose(code: number): void {
    this.detach();
    // The slot is free again: try to become the controller.
    if (this.watch && (this.freeSeen || code === FREE_CLOSE_CODE)) {
      this.watch = false;
      this.open();
      return;
    }
    // Someone else controls the amp (another browser, or the MCP server took over): watch it.
    if (this.busySeen || code === BUSY_CLOSE_CODE || this.takenSeen || code === TAKEN_CLOSE_CODE) {
      this.watch = true;
      this.setState({ kind: "watching" });
      this.reconnectTimer = (this.opts.setTimeout ?? setTimeout)(() => {
        this.reconnectTimer = null;
        this.open();
      }, WATCH_DELAY_MS);
      return;
    }
    // Reconnecting here would take the slot back from the other tab, and it from us, forever.
    if (this.replacedSeen || code === REPLACED_CLOSE_CODE) {
      this.setState({ kind: "replaced" });
      return;
    }
    this.scheduleReconnect();
  }

  private scheduleReconnect(): void {
    this.failures++;
    const delay = backoffMs(this.failures);
    this.setState({ kind: "waiting", retryInMs: delay });
    this.reconnectTimer = (this.opts.setTimeout ?? setTimeout)(() => {
      this.reconnectTimer = null;
      this.open();
    }, delay);
  }

  private startPing(): void {
    this.stopPing();
    this.pingTimer = (this.opts.setInterval ?? setInterval)(() => {
      try {
        this.sock?.send(JSON.stringify({ t: "ping" }));
      } catch {
        // The close handler takes care of a dead socket.
      }
    }, PING_INTERVAL_MS);
  }

  private stopPing(): void {
    if (this.pingTimer !== null) (this.opts.clearInterval ?? clearInterval)(this.pingTimer);
    this.pingTimer = null;
  }

  private clearReconnect(): void {
    if (this.reconnectTimer !== null) (this.opts.clearTimeout ?? clearTimeout)(this.reconnectTimer);
    this.reconnectTimer = null;
  }

  private detach(): void {
    this.stopPing();
    if (this.sock) {
      this.sock.onmessage = null;
      this.sock.onclose = null;
      this.sock.onerror = null;
    }
    this.sock = null;
  }

  private setState(s: ConnState): void {
    this.state = s;
    for (const cb of this.stateCbs) cb(s);
  }
}
