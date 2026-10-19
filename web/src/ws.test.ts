import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { VoxClient } from "./protocol/client";
import { msg } from "./protocol/messages";
import { decodeProgram } from "./protocol/program";
import { hex, VECTOR_A_STRUCT } from "./protocol/testutil";
import { backoffMs, BUSY_CLOSE_CODE, PING_INTERVAL_MS, REPLACED_CLOSE_CODE, WsTransport, type ConnState, type SocketLike } from "./ws";

class FakeSocket implements SocketLike {
  binaryType: BinaryType = "blob";
  sent: (string | Uint8Array)[] = [];
  closed: { code?: number } | null = null;
  onopen: ((ev: unknown) => void) | null = null;
  onmessage: ((ev: { data: unknown }) => void) | null = null;
  onclose: ((ev: { code: number }) => void) | null = null;
  onerror: ((ev: unknown) => void) | null = null;

  constructor(readonly url: string) {}

  send(data: string | ArrayBufferView): void {
    this.sent.push(typeof data === "string" ? data : new Uint8Array(data.buffer, data.byteOffset, data.byteLength).slice());
  }
  close(code?: number): void {
    this.closed = { code };
  }
  // server side
  text(m: unknown): void {
    this.onmessage?.({ data: JSON.stringify(m) });
  }
  binary(bytes: number[] | Uint8Array): void {
    this.onmessage?.({ data: Uint8Array.from(bytes).buffer });
  }
  serverClose(code: number): void {
    this.onclose?.({ code });
  }
}

let sockets: FakeSocket[];
let states: ConnState[];
let t: WsTransport;
const last = () => sockets[sockets.length - 1];

beforeEach(() => {
  vi.useFakeTimers();
  sockets = [];
  states = [];
  t = new WsTransport({
    url: () => "ws://test/ws",
    createSocket: (url) => {
      const s = new FakeSocket(url);
      sockets.push(s);
      return s;
    },
  });
  t.onState((s) => states.push(s));
});

afterEach(() => {
  vi.useRealTimers();
});

function connect(): FakeSocket {
  t.start();
  const s = last();
  s.text({ t: "hello", version: "0.1.0" });
  return s;
}

describe("backoffMs", () => {
  it("doubles from 1 s and caps at 10 s", () => {
    expect([1, 2, 3, 4, 5, 6, 20].map(backoffMs)).toEqual([1000, 2000, 4000, 8000, 10000, 10000, 10000]);
  });
});

describe("WsTransport", () => {
  it("is open only after hello, and refuses to send before", () => {
    t.start();
    expect(last().url).toBe("ws://test/ws");
    expect(last().binaryType).toBe("arraybuffer");
    expect(t.connState.kind).toBe("connecting");
    expect(() => t.send(msg.modeRequest(), false)).toThrow();
    last().text({ t: "hello", version: "x" });
    expect(t.connState.kind).toBe("open");
  });

  it("prefixes frames with kind 0, or 1 for a user-confirmed save", () => {
    const s = connect();
    t.send(Uint8Array.from([0xf0, 0x01, 0xf7]), false);
    t.send(Uint8Array.from([0xf0, 0x02, 0xf7]), true);
    expect(s.sent).toEqual([Uint8Array.from([0, 0xf0, 0x01, 0xf7]), Uint8Array.from([1, 0xf0, 0x02, 0xf7])]);
  });

  it("delivers amp frames without the kind byte and ignores other kinds", () => {
    const s = connect();
    const got: Uint8Array[] = [];
    t.onFrame((f) => got.push(f));
    s.binary([0x00, 0xf0, 0x42, 0xf7]);
    s.binary([0x05, 0xf0, 0x42, 0xf7]);
    s.binary([0x00]);
    expect(got).toEqual([Uint8Array.from([0xf0, 0x42, 0xf7])]);
  });

  it("passes server error messages to every error listener and all text to text listeners", () => {
    const s = connect();
    const errors: unknown[] = [];
    const texts: string[] = [];
    t.onError((e) => errors.push(e));
    t.onError((e) => errors.push(e.code));
    t.onText((m) => texts.push(m.t));
    s.text({ t: "error", code: "forbidden", func: 0x40 });
    s.text({ t: "amp", amp: { state: "connected" } });
    s.onmessage?.({ data: "not json" });
    expect(errors).toEqual([{ code: "forbidden", func: 0x40 }, "forbidden"]);
    expect(texts).toEqual(["error", "amp"]);
  });

  it("reconnects with backoff 1, 2, 4, 8, 10, 10 s", () => {
    t.start();
    const delays: number[] = [];
    for (let i = 0; i < 6; i++) {
      last().serverClose(1006);
      const st = t.connState;
      expect(st.kind).toBe("waiting");
      if (st.kind === "waiting") delays.push(st.retryInMs);
      const before = sockets.length;
      vi.advanceTimersByTime(st.kind === "waiting" ? st.retryInMs - 1 : 0);
      expect(sockets.length).toBe(before);
      vi.advanceTimersByTime(1);
      expect(sockets.length).toBe(before + 1);
    }
    expect(delays).toEqual([1000, 2000, 4000, 8000, 10000, 10000]);
  });

  it("resets the backoff after a successful hello", () => {
    t.start();
    last().serverClose(1006);
    vi.advanceTimersByTime(1000);
    last().serverClose(1006);
    vi.advanceTimersByTime(2000);
    last().text({ t: "hello", version: "x" });
    last().serverClose(1001);
    expect(t.connState).toEqual({ kind: "waiting", retryInMs: 1000 });
  });

  it("stays busy after {t:busy} + close and does not reconnect until retry()", () => {
    t.start();
    last().text({ t: "busy" });
    last().serverClose(BUSY_CLOSE_CODE);
    expect(t.connState.kind).toBe("busy");
    vi.advanceTimersByTime(120_000);
    expect(sockets.length).toBe(1);
    t.retry();
    expect(sockets.length).toBe(2);
    expect(t.connState.kind).toBe("connecting");
  });

  it("treats close code 4001 alone as busy", () => {
    t.start();
    last().serverClose(BUSY_CLOSE_CODE);
    expect(t.connState.kind).toBe("busy");
    vi.advanceTimersByTime(60_000);
    expect(sockets.length).toBe(1);
  });

  it("stays replaced after {t:replaced} + 4002 and does not reconnect until retry()", () => {
    connect();
    last().text({ t: "replaced" });
    last().serverClose(REPLACED_CLOSE_CODE);
    expect(t.connState.kind).toBe("replaced");
    vi.advanceTimersByTime(120_000);
    expect(sockets.length).toBe(1);
    t.retry();
    expect(sockets.length).toBe(2);
    expect(t.connState.kind).toBe("connecting");
    last().text({ t: "hello", version: "x" });
    last().serverClose(1006);
    expect(t.connState.kind).toBe("waiting");
  });

  it("treats {t:replaced} with an abnormal close, or close code 4002 alone, as replaced", () => {
    connect();
    last().text({ t: "replaced" });
    last().serverClose(1006);
    expect(t.connState.kind).toBe("replaced");
    t.retry();
    last().serverClose(REPLACED_CLOSE_CODE);
    expect(t.connState.kind).toBe("replaced");
    vi.advanceTimersByTime(60_000);
    expect(sockets.length).toBe(2);
  });

  it("does not report busy for a later normal disconnect after retry", () => {
    t.start();
    last().serverClose(BUSY_CLOSE_CODE);
    t.retry();
    last().text({ t: "hello", version: "x" });
    last().serverClose(1006);
    expect(t.connState.kind).toBe("waiting");
  });

  it("sends a ping every 10 s while open and stops after close", () => {
    const s = connect();
    vi.advanceTimersByTime(PING_INTERVAL_MS * 3);
    expect(s.sent.filter((m) => m === '{"t":"ping"}')).toHaveLength(3);
    s.serverClose(1006);
    vi.advanceTimersByTime(PING_INTERVAL_MS * 3);
    expect(s.sent).toHaveLength(3);
  });

  it("stop() closes the socket and never reconnects", () => {
    const s = connect();
    t.stop();
    expect(s.closed).not.toBeNull();
    expect(t.connState.kind).toBe("idle");
    s.serverClose(1000); // late event from the old socket
    vi.advanceTimersByTime(60_000);
    expect(sockets.length).toBe(1);
    expect(t.connState.kind).toBe("idle");
  });

  it("retry() while waiting connects immediately", () => {
    t.start();
    last().serverClose(1006);
    vi.advanceTimersByTime(500);
    t.retry();
    expect(sockets.length).toBe(2);
    vi.advanceTimersByTime(10_000);
    expect(sockets.length).toBe(2);
  });

  it("ignores events of a replaced socket", () => {
    t.start();
    const old = last();
    old.serverClose(1006);
    vi.advanceTimersByTime(1000);
    const got: Uint8Array[] = [];
    t.onFrame((f) => got.push(f));
    old.binary([0, 0xf0, 0xf7]);
    expect(got).toHaveLength(0);
  });

  it("records state transitions", () => {
    t.start();
    last().text({ t: "hello", version: "x" });
    last().serverClose(1006);
    expect(states.map((s) => s.kind)).toEqual(["connecting", "open", "waiting"]);
  });
});

describe("WsTransport + VoxClient", () => {
  it("sends the 4C program write with the save flag and everything else without", async () => {
    const s = connect();
    const client = new VoxClient(t);
    const program = decodeProgram(VECTOR_A_STRUCT);
    const done = client.save(3, program);
    const ack = hex("00 F0 42 30 00 01 34 23 F7");
    const nextSent = () => s.sent[s.sent.length - 1] as Uint8Array;

    await vi.advanceTimersByTimeAsync(0);
    expect(nextSent()[0]).toBe(1);
    expect(nextSent()[7]).toBe(0x4c);
    s.binary(ack);
    await vi.advanceTimersByTimeAsync(0);
    expect(Array.from(nextSent())).toEqual([0, ...msg.selectProgram(3)]);
    s.binary(ack);
    await vi.advanceTimersByTimeAsync(0);
    expect(Array.from(nextSent())).toEqual([0, ...msg.programRequest(3)]);
    const packed = (s.sent[0] as Uint8Array).slice(10, 81);
    s.binary([0, 0xf0, 0x42, 0x30, 0x00, 0x01, 0x34, 0x4c, 0x00, 0x03, ...packed, 0xf7]);
    await expect(done).resolves.toBe("verified");
  });

  it("fails a request when the socket is not open (send_failed)", async () => {
    const client = new VoxClient(t);
    await expect(client.select(0)).rejects.toMatchObject({ code: "send_failed" });
  });
});
