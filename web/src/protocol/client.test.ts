import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { AuditionSettings } from "./audition";
import { VoxClient, VoxError, type AmpState, type Transport } from "./client";
import { pack7, unpack7 } from "./encoding";
import { funcOf, msg } from "./messages";
import { decodeProgram } from "./program";
import { currentFrame, hex, programFrame, toHex, VECTOR_A_STRUCT } from "./testutil";

const korg = (data: string) => hex(`F0 42 30 00 01 34 ${data} F7`);
const ACK = korg("23");
const NAK = korg("24");

function slotStruct(slot: number): Uint8Array {
  const s = Uint8Array.from(VECTOR_A_STRUCT);
  s.set(Array.from(`SLOT ${slot}`.padEnd(16, " "), (c) => c.charCodeAt(0)), 0);
  return s;
}

type Sent = { frame: Uint8Array; save: boolean; at: number };

/** Simulated amp behind the transport. Replies after `latency` ms (fake timers). */
class FakeAmp implements Transport {
  sent: Sent[] = [];
  latency = 5;
  mode = 0;
  program = 3;
  slots: Uint8Array[] = Array.from({ length: 8 }, (_, i) => slotStruct(i));
  current: Uint8Array = Uint8Array.from(this.slots[3]);
  /** Function bytes (null = device inquiry) the amp ignores, with remaining count (Infinity = always). */
  silent = new Map<number | null, number>();
  /** Function bytes answered with NAK. */
  nak = new Set<number>();
  /** Single frames answered with NAK. */
  nakIf: ((frame: Uint8Array) => boolean) | null = null;
  /** Function bytes answered with a server error instead of reaching the amp. */
  serverError = new Map<number, string>();
  /** Amp stores 4C data but does not ACK it. */
  silentWriteAck = false;
  corruptWrite = false;
  echoSelect = false;
  /** Number of default-value dials sent after an effect type ACK (null = the real count). */
  burstCount: number | null = null;
  burstSpacing = 10;
  private frameCb: (f: Uint8Array) => void = () => {};
  private errorCb: (e: { code: string; func: number | null }) => void = () => {};

  onFrame(cb: (f: Uint8Array) => void) { this.frameCb = cb; }
  onError(cb: (e: { code: string; func: number | null }) => void) { this.errorCb = cb; }

  push(frame: Uint8Array, delay = 0) { setTimeout(() => this.frameCb(frame), delay); }

  error(code: string, func: number | null, delay = 0) { setTimeout(() => this.errorCb({ code, func }), delay); }

  funcs() { return this.sent.map((s) => toHex(s.frame.subarray(funcOf(s.frame) === null ? 1 : 6, -1))); }

  send(frame: Uint8Array, save: boolean) {
    this.sent.push({ frame, save, at: Date.now() });
    const fn = funcOf(frame);
    const left = this.silent.get(fn) ?? 0;
    if (left > 0) {
      this.silent.set(fn, left - 1);
      return;
    }
    if (fn !== null && this.serverError.has(fn)) {
      return this.error(this.serverError.get(fn)!, fn, this.latency);
    }
    if ((fn !== null && this.nak.has(fn)) || this.nakIf?.(frame)) return this.push(NAK, this.latency);
    const d = this.latency;
    switch (fn) {
      case null: return this.push(hex("F0 7E 00 06 02 42 34 01 00 00 02 00 01 00 F7"), d);
      case 0x12: return this.push(korg(`42 0${this.mode} 0${this.program}`), d);
      case 0x10: return this.push(currentFrame(pack7(this.current)), d);
      case 0x1c: return this.push(programFrame(frame[8], pack7(this.slots[frame[8]])), d);
      case 0x4c: {
        if (!save) return this.error("save_flag_required", 0x4c, d);
        const s = unpack7(frame.subarray(9, -1));
        if (this.corruptWrite) s[0x13] ^= 1;
        this.slots[frame[8]] = s;
        if (!this.silentWriteAck) this.push(ACK, d);
        return;
      }
      case 0x4e:
        this.program = frame[8];
        this.current = Uint8Array.from(this.slots[frame[8]]);
        this.push(ACK, d);
        if (this.echoSelect) this.push(korg(`4E 00 0${frame[8]}`), d + 2);
        return;
      case 0x41: {
        this.push(ACK, d);
        if (frame[7] === 0x03 && frame[8] !== 0x00) {
          const id = { 1: 0x05, 2: 0x06, 4: 0x08 }[frame[8]]!;
          const n = this.burstCount ?? (frame[8] === 4 ? 5 : 6);
          for (let i = 0; i < n; i++) {
            this.push(Uint8Array.from([0xf0, 0x42, 0x30, 0x00, 0x01, 0x34, 0x41, id, i, 0, 0, 0xf7]), d + (i + 1) * this.burstSpacing);
          }
        }
        return;
      }
    }
  }
}

let amp: FakeAmp;
let client: VoxClient;
let states: AmpState[];

beforeEach(() => {
  vi.useFakeTimers();
  amp = new FakeAmp();
  client = new VoxClient(amp);
  states = [];
  client.onState((s) => states.push(s));
});

afterEach(() => {
  vi.useRealTimers();
});

async function connected(): Promise<void> {
  const p = client.readAll();
  await vi.advanceTimersByTimeAsync(1000);
  await p;
  amp.sent = [];
}

const lastState = () => states[states.length - 1];

describe("readAll", () => {
  it("runs inquiry, 12, 10, 1C x 8 and reports what the amp holds", async () => {
    const p = client.readAll();
    await vi.advanceTimersByTimeAsync(200);
    const s = await p;
    expect(amp.funcs()).toEqual([
      "7E 7F 06 01", "12", "10",
      ...Array.from({ length: 8 }, (_, i) => `1C 00 0${i}`),
    ]);
    expect(amp.sent.every((x) => !x.save)).toBe(true);
    expect(s.ready).toBe(true);
    expect(s.mode).toBe("user");
    expect(s.program).toBe(3);
    expect(s.current?.name).toBe("SLOT 3");
    expect(s.slots.map((x) => x?.name)).toEqual(Array.from({ length: 8 }, (_, i) => `SLOT ${i}`));
    expect(s.slots[5]?.raw).toEqual(slotStruct(5));
    expect(lastState()).toEqual(s);
    expect(states.length).toBeGreaterThan(1);
  });

  it("ignores a missing inquiry reply after 1 s", async () => {
    amp.silent.set(null, Infinity);
    const p = client.readAll();
    await vi.advanceTimersByTimeAsync(999);
    expect(amp.funcs()).toEqual(["7E 7F 06 01"]);
    await vi.advanceTimersByTimeAsync(200);
    expect((await p).ready).toBe(true);
    expect(amp.funcs().filter((f) => f.startsWith("7E"))).toHaveLength(1);
  });

  it("reports manual mode with no program", async () => {
    amp.mode = 2;
    amp.program = 0;
    const p = client.readAll();
    await vi.advanceTimersByTimeAsync(200);
    const s = await p;
    expect(s.mode).toBe("manual");
    expect(s.program).toBeNull();
  });
});

describe("timeouts and retries", () => {
  it("retries a read once after 1500 ms", async () => {
    amp.silent.set(0x10, 1);
    const p = client.readAll();
    await vi.advanceTimersByTimeAsync(20);
    expect(amp.funcs().filter((f) => f === "10")).toHaveLength(1);
    await vi.advanceTimersByTimeAsync(1500);
    expect(amp.funcs().filter((f) => f === "10")).toHaveLength(2);
    await vi.advanceTimersByTimeAsync(200);
    expect((await p).current?.name).toBe("SLOT 3");
  });

  it("fails a read after the retry also times out", async () => {
    amp.silent.set(0x12, Infinity);
    const p = client.readAll();
    const assertion = expect(p).rejects.toMatchObject({ code: "timeout" });
    await vi.advanceTimersByTimeAsync(3100);
    await assertion;
    expect(amp.funcs().filter((f) => f === "12")).toHaveLength(2);
  });

  it("a timeout frees the queue for the next request", async () => {
    await connected();
    amp.silent.set(0x4e, Infinity);
    const sel = client.select(2);
    const assertion = expect(sel).rejects.toBeInstanceOf(VoxError);
    client.setParam(4, 0, 50);
    await vi.advanceTimersByTimeAsync(999);
    expect(amp.funcs()).toEqual(["4E 00 02"]);
    await vi.advanceTimersByTimeAsync(2);
    expect(amp.funcs()).toEqual(["4E 00 02", "41 04 00 32 00"]);
    await assertion;
    await vi.advanceTimersByTimeAsync(10);
    expect(lastState().current?.amp[0]).toBe(50);
  });

  it("never retries a live edit or select", async () => {
    await connected();
    amp.silent.set(0x41, Infinity);
    amp.silent.set(0x4e, Infinity);
    client.setParam(4, 0, 10);
    const sel = client.select(1);
    const assertion = expect(sel).rejects.toMatchObject({ code: "timeout" });
    await vi.advanceTimersByTimeAsync(5000);
    await assertion;
    expect(amp.funcs()).toEqual(["41 04 00 0A 00", "4E 00 01"]);
  });

  it("never retries 4C: on a timeout it verifies via 1C without reselecting", async () => {
    await connected();
    amp.silentWriteAck = true;
    const p = decodeProgram(slotStruct(6));
    const res = client.save(1, { ...p, name: "NEW" });
    await vi.advanceTimersByTimeAsync(5000);
    // Stored and verified, but no 4E was sent, so the slot is not the active program.
    expect(await res).toBe("verified-inactive");
    expect(amp.funcs()).toEqual([toHex(msg.writeProgram(1, { ...p, name: "NEW" }).subarray(6, -1)), "1C 00 01"]);
  });

  it("drops late replies that arrive with no request pending", async () => {
    await connected();
    amp.push(ACK);
    amp.push(NAK);
    amp.push(korg("26"));
    await vi.advanceTimersByTimeAsync(10);
    expect(amp.sent).toHaveLength(0);
  });
});

describe("matching and notifications", () => {
  it("unsolicited 41/4E/21 never complete a pending request and are routed to the model", async () => {
    await connected();
    amp.silent.set(0x41, 1); // we answer the first 41 by hand
    client.setParam(4, 0, 70);
    client.setParam(4, 1, 33);
    await vi.advanceTimersByTimeAsync(1);
    expect(amp.funcs()).toEqual(["41 04 00 46 00"]);

    amp.push(korg("41 04 02 11 00")); // panel: middle
    amp.push(korg("4E 00 05")); // panel: program B2
    amp.push(korg("21 00 02")); // panel: stored A3
    await vi.advanceTimersByTimeAsync(1);
    // Still waiting for the ACK of the first 41: nothing else sent yet.
    expect(amp.funcs()).toEqual(["41 04 00 46 00"]);
    expect(lastState().current?.amp[2]).toBe(0x11);
    expect(lastState().current?.amp[0]).not.toBe(70);
    expect(lastState().program).toBe(5);

    amp.push(ACK);
    await vi.advanceTimersByTimeAsync(100);
    expect(amp.funcs()).toEqual(["41 04 00 46 00", "41 04 01 21 00", "10", "1C 00 02"]);
    const s = lastState();
    expect(s.current?.name).toBe("SLOT 3"); // fake amp's buffer did not change
    expect(s.mode).toBe("user");
  });

  it("4E notification refreshes the Current Buffer via 10", async () => {
    await connected();
    amp.current = slotStruct(6);
    amp.push(korg("4E 00 06"));
    await vi.advanceTimersByTimeAsync(20);
    expect(amp.funcs()).toEqual(["10"]);
    expect(lastState()).toMatchObject({ mode: "user", program: 6 });
    expect(lastState().current?.name).toBe("SLOT 6");
  });

  it("21 notification re-reads that slot", async () => {
    await connected();
    amp.slots[4] = slotStruct(7);
    amp.push(korg("21 00 04"));
    await vi.advanceTimersByTimeAsync(20);
    expect(amp.funcs()).toEqual(["1C 00 04"]);
    expect(lastState().slots[4]?.name).toBe("SLOT 7");
  });

  it("a 4C reply for another slot does not complete a 1C request", async () => {
    await connected();
    amp.silent.set(0x1c, 1);
    amp.push(korg("21 00 02"));
    await vi.advanceTimersByTimeAsync(1);
    expect(amp.funcs()).toEqual(["1C 00 02"]);
    amp.push(programFrame(5, pack7(slotStruct(7))));
    amp.push(programFrame(2, pack7(slotStruct(7)), 1)); // preset bank
    await vi.advanceTimersByTimeAsync(10);
    expect(lastState().slots[2]?.name).toBe("SLOT 2");
    amp.push(programFrame(2, pack7(slotStruct(6))));
    await vi.advanceTimersByTimeAsync(1);
    expect(lastState().slots[2]?.name).toBe("SLOT 6");
    expect(lastState().slots[5]?.name).toBe("SLOT 5");
  });

  it("select sends 4E, waits for ACK, re-reads 10, and treats an echo idempotently", async () => {
    await connected();
    amp.echoSelect = true;
    const sel = client.select(1);
    await vi.advanceTimersByTimeAsync(50);
    await sel;
    expect(amp.funcs()).toEqual(["4E 00 01", "10"]);
    expect(lastState()).toMatchObject({ mode: "user", program: 1 });
    expect(lastState().current?.name).toBe("SLOT 1");
  });

  it("a failed 10 after a select ACK clears the stale Current Buffer", async () => {
    await connected();
    amp.silent.set(0x10, 2);
    const sel = client.select(1);
    const assertion = expect(sel).rejects.toMatchObject({ code: "timeout" });
    await vi.advanceTimersByTimeAsync(5000);
    await assertion;
    expect(amp.funcs()).toEqual(["4E 00 01", "10", "10"]);
    expect(lastState()).toMatchObject({ mode: "user", program: 1, current: null });

    // A later successful read restores it.
    const again = client.select(1);
    await vi.advanceTimersByTimeAsync(50);
    await again;
    expect(lastState().current?.name).toBe("SLOT 1");
  });

  it("a failed 10 after a 4E notification clears the stale Current Buffer", async () => {
    await connected();
    amp.silent.set(0x10, 2);
    amp.push(korg("4E 00 06"));
    await vi.advanceTimersByTimeAsync(1);
    expect(lastState().current?.name).toBe("SLOT 3");
    await vi.advanceTimersByTimeAsync(5000);
    expect(amp.funcs()).toEqual(["10", "10"]);
    expect(lastState()).toMatchObject({ mode: "user", program: 6, current: null });
  });

  it("a failed resync after a live-edit NAK keeps the Current Buffer", async () => {
    await connected();
    amp.nak.add(0x41);
    amp.silent.set(0x10, 2);
    client.setParam(6, 4, 1);
    await vi.advanceTimersByTimeAsync(5000);
    expect(amp.funcs()).toEqual(["41 06 04 01 00", "10", "10"]);
    expect(lastState().current?.name).toBe("SLOT 3");
  });

  it("a NAK fails select and a live-edit NAK resyncs with 10", async () => {
    await connected();
    amp.nak.add(0x4e);
    const sel = client.select(1);
    const assertion = expect(sel).rejects.toMatchObject({ code: "nak" });
    await vi.advanceTimersByTimeAsync(20);
    await assertion;
    amp.nak.add(0x41);
    amp.sent = [];
    client.setParam(6, 4, 1);
    await vi.advanceTimersByTimeAsync(50);
    expect(amp.funcs()).toEqual(["41 06 04 01 00", "10"]);
  });
});

describe("coalescing", () => {
  it("coalesces queued edits per (id, sub), last value wins, applied on ACK", async () => {
    await connected();
    const gain0 = lastState().current!.amp[0];
    client.setParam(4, 0, 10); // in flight
    client.setParam(4, 0, 20);
    client.setParam(4, 0, 30);
    client.setParam(4, 1, 40);
    client.setParam(4, 0, 50);
    expect(lastState().current?.amp[0]).toBe(gain0);
    await vi.advanceTimersByTimeAsync(100);
    expect(amp.funcs()).toEqual(["41 04 00 0A 00", "41 04 00 32 00", "41 04 01 28 00"]);
    expect(lastState().current?.amp[0]).toBe(50);
    expect(lastState().current?.amp[1]).toBe(40);
  });

  it("does not apply a value before its ACK", async () => {
    await connected();
    amp.latency = 50;
    const gain0 = lastState().current!.amp[0];
    client.setParam(4, 0, 77);
    await vi.advanceTimersByTimeAsync(49);
    expect(lastState().current?.amp[0]).toBe(gain0);
    await vi.advanceTimersByTimeAsync(2);
    expect(lastState().current?.amp[0]).toBe(77);
  });
});

describe("effect type burst rule", () => {
  it("holds the queue until nDials dials arrived, so host values are not overwritten", async () => {
    await connected();
    const done = client.setEffectType("pedal1", 6);
    client.setParam(5, 1, 77);
    await vi.advanceTimersByTimeAsync(5);
    await done;
    expect(amp.funcs()).toEqual(["41 03 01 06 00"]);
    expect(lastState().current?.pedal1Type).toBe(6);
    await vi.advanceTimersByTimeAsync(50); // 5 of 6 dials
    expect(amp.funcs()).toEqual(["41 03 01 06 00"]);
    await vi.advanceTimersByTimeAsync(10); // 6th dial
    expect(amp.sent.at(-1)?.at).toBe(amp.sent[0].at + 65);
    expect(amp.funcs()).toEqual(["41 03 01 06 00", "41 05 01 4D 00"]);
    await vi.advanceTimersByTimeAsync(10);
    expect(lastState().current?.pedal1).toEqual([0, 77, 0, 0, 0, 0]);
  });

  it("releases the hold after 200 ms when fewer dials arrive", async () => {
    await connected();
    amp.burstCount = 2;
    const done = client.setEffectType("reverb", 1);
    client.setParam(8, 0, 30);
    await vi.advanceTimersByTimeAsync(5);
    await done;
    await vi.advanceTimersByTimeAsync(199);
    expect(amp.funcs()).toEqual(["41 03 04 01 00"]);
    await vi.advanceTimersByTimeAsync(1);
    expect(amp.funcs()).toEqual(["41 03 04 01 00", "41 08 00 1E 00"]);
  });

  it("also holds after a type change sent through setParam", async () => {
    await connected();
    amp.burstCount = 0;
    client.setParam(3, 2, 4);
    client.setParam(6, 1, 20);
    await vi.advanceTimersByTimeAsync(100);
    expect(amp.funcs()).toEqual(["41 03 02 04 00"]);
    await vi.advanceTimersByTimeAsync(110);
    expect(amp.funcs()).toEqual(["41 03 02 04 00", "41 06 01 14 00"]);
  });
});

describe("save", () => {
  it("4C with save flag -> 4E -> 1C, byte compare verified", async () => {
    await connected();
    const p = { ...decodeProgram(slotStruct(6)), name: "SAVED", nr: 10 };
    const res = client.save(2, p);
    await vi.advanceTimersByTimeAsync(100);
    expect(await res).toBe("verified");
    expect(amp.funcs()[0].startsWith("4C 00 02 ")).toBe(true);
    expect(amp.funcs().slice(1)).toEqual(["4E 00 02", "1C 00 02", "10"]);
    expect(amp.sent.map((s) => s.save)).toEqual([true, false, false, false]);
    const s = lastState();
    expect(s).toMatchObject({ mode: "user", program: 2 });
    expect(s.slots[2]?.name).toBe("SAVED");
    expect(s.current?.name).toBe("SAVED");
  });

  it("reports mismatch when the read-back differs", async () => {
    await connected();
    amp.corruptWrite = true;
    const res = client.save(0, decodeProgram(slotStruct(6)));
    await vi.advanceTimersByTimeAsync(100);
    expect(await res).toBe("mismatch");
  });

  it("a NAK on 4E after an ACKed 4C still verifies and reports the slot as not activated", async () => {
    await connected();
    amp.nak.add(0x4e);
    const p = { ...decodeProgram(slotStruct(6)), name: "SAVED" };
    const res = client.save(2, p);
    await vi.advanceTimersByTimeAsync(100);
    expect(await res).toBe("verified-inactive");
    expect(amp.funcs().slice(1)).toEqual(["4E 00 02", "1C 00 02"]);
    const s = lastState();
    expect(s).toMatchObject({ mode: "user", program: 3 });
    expect(s.slots[2]?.name).toBe("SAVED");
    expect(s.current?.name).toBe("SLOT 3");
  });

  it("a 4E timeout after an ACKed 4C is not a failed save", async () => {
    await connected();
    amp.silent.set(0x4e, Infinity);
    amp.corruptWrite = true;
    const res = client.save(2, decodeProgram(slotStruct(6)));
    await vi.advanceTimersByTimeAsync(5000);
    expect(await res).toBe("mismatch-inactive");
    expect(amp.funcs().slice(1)).toEqual(["4E 00 02", "1C 00 02"]);
  });

  it("a failed read-back after an ACKed 4C reports unverified", async () => {
    await connected();
    amp.silent.set(0x1c, Infinity);
    const res = client.save(2, decodeProgram(slotStruct(6)));
    await vi.advanceTimersByTimeAsync(5000);
    expect(await res).toBe("unverified");
    expect(amp.funcs().slice(1)).toEqual(["4E 00 02", "1C 00 02", "1C 00 02", "10"]);
    expect(lastState()).toMatchObject({ mode: "user", program: 2 });
  });

  it("a failed read-back after a 4C timeout fails the save", async () => {
    await connected();
    amp.silentWriteAck = true;
    amp.silent.set(0x1c, Infinity);
    const res = client.save(2, decodeProgram(slotStruct(6)));
    const assertion = expect(res).rejects.toMatchObject({ code: "timeout" });
    await vi.advanceTimersByTimeAsync(5000);
    await assertion;
    expect(amp.funcs().slice(1)).toEqual(["1C 00 02", "1C 00 02"]);
  });

  it("rejects an invalid program without sending anything", async () => {
    await connected();
    const res = client.save(0, { ...decodeProgram(slotStruct(6)), ampModel: 0x20 });
    await expect(res).rejects.toBeInstanceOf(RangeError);
    expect(amp.sent).toHaveLength(0);
  });

  it("a NAK on 4C fails the save", async () => {
    await connected();
    amp.nak.add(0x4c);
    const res = client.save(0, decodeProgram(slotStruct(6)));
    const assertion = expect(res).rejects.toMatchObject({ code: "nak" });
    await vi.advanceTimersByTimeAsync(100);
    await assertion;
    expect(amp.sent).toHaveLength(1);
  });
});

describe("server errors", () => {
  it("fail the in-flight request with the server code and free the queue", async () => {
    await connected();
    amp.serverError.set(0x41, "amp_disconnected");
    const res = client.setEffectType("pedal2", 1);
    const assertion = expect(res).rejects.toMatchObject({ code: "amp_disconnected" });
    const sel = client.select(4);
    await vi.advanceTimersByTimeAsync(10);
    await assertion;
    await vi.advanceTimersByTimeAsync(50);
    await sel;
    expect(amp.funcs()).toEqual(["41 03 02 01 00", "4E 00 04", "10"]);
  });

  it("ignore errors whose func does not match the in-flight frame", async () => {
    await connected();
    amp.latency = 100;
    const sel = client.select(4);
    amp.push(new Uint8Array(0), 0); // unknown frame: dropped
    await vi.advanceTimersByTimeAsync(1);
    amp.error("forbidden", 0x41); // stray error for another function
    await vi.advanceTimersByTimeAsync(300);
    await expect(sel).resolves.toBeUndefined();
  });

  it("an inquiry rejected by the server (func null) is ignored by readAll", async () => {
    amp.silent.set(null, 1);
    const p = client.readAll();
    await vi.advanceTimersByTimeAsync(1);
    amp.error("forbidden", null);
    await vi.advanceTimersByTimeAsync(200);
    expect((await p).ready).toBe(true);
    expect(amp.sent[1].at - amp.sent[0].at).toBeLessThan(10);
  });
});

describe("reset", () => {
  it("drops the queue and the state", async () => {
    await connected();
    amp.silent.set(0x4e, Infinity);
    const a = client.select(1);
    const b = client.select(2);
    const ra = expect(a).rejects.toMatchObject({ code: "reset" });
    const rb = expect(b).rejects.toMatchObject({ code: "reset" });
    client.reset();
    await ra;
    await rb;
    expect(lastState()).toEqual({ mode: null, program: null, current: null, slots: Array(8).fill(null), ready: false });
    await vi.advanceTimersByTimeAsync(5000);
    expect(amp.funcs()).toEqual(["4E 00 01"]);
  });
});

describe("injected timers", () => {
  it("uses the injected setTimeout/clearTimeout", async () => {
    const pending: { cb: () => void; ms: number }[] = [];
    const amp2 = new FakeAmp();
    amp2.silent.set(null, Infinity);
    const fakeSet = ((cb: () => void, ms: number) => (pending.push({ cb, ms }), pending.length)) as unknown as typeof setTimeout;
    const fakeClear = (() => {}) as unknown as typeof clearTimeout;
    const c = new VoxClient(amp2, { setTimeout: fakeSet, clearTimeout: fakeClear });
    const p = c.readAll();
    expect(pending).toHaveLength(1);
    expect(pending[0].ms).toBe(1000);
    amp2.silent.set(0x12, Infinity);
    pending[0].cb();
    await Promise.resolve();
    await Promise.resolve();
    expect(amp2.funcs()).toEqual(["7E 7F 06 01", "12"]);
    expect(pending[1].ms).toBe(1500);
    p.catch(() => {});
  });
});

const SOUND: AuditionSettings = {
  amp: {
    model: 1, // DELUXE CL NORMAL: no BRIGHT CAP
    gain: 40, treble: 60, middle: 50, bass: 45, presence: 55, resonance: 30,
    brightCap: 0, lowCut: 1, midBoost: 0, bias: 2, ampClass: 1,
  },
  nr: 20,
  pedal1: { on: true, type: 6, params: [70, 50, 48, 55, 40, 60] },
  pedal2: { on: false, type: 1, params: [400, 30, 50, 60, 0, 0] }, // phaser: indices 4, 5 unused
  reverb: { on: true, type: 1, params: [25, 30, 10, 50, 50] },
};

describe("audition", () => {
  async function run(s: AuditionSettings = SOUND, ms = 3000) {
    const p = client.audition(s);
    await vi.advanceTimersByTimeAsync(ms);
    return p;
  }

  it("sends model first, then amp params without VOLUME, NR, types with their params, then on/off", async () => {
    await connected();
    const r = await run();
    expect(r).toEqual({ total: 33, done: 33, failed: [], aborted: null, volume: null });
    expect(amp.funcs()).toEqual([
      "41 03 00 01 00",
      "41 04 00 28 00", "41 04 01 3C 00", "41 04 02 32 00", "41 04 03 2D 00", "41 04 05 37 00", "41 04 06 1E 00",
      // BRIGHT CAP (04 07) is skipped: DELUXE CL NORMAL has none.
      "41 04 08 01 00", "41 04 09 00 00", "41 04 0A 02 00", "41 04 0B 01 00",
      "41 01 00 14 00",
      "41 03 01 06 00", "41 05 00 46 00", "41 05 01 32 00", "41 05 02 30 00", "41 05 03 37 00", "41 05 04 28 00", "41 05 05 3C 00",
      "41 03 02 01 00", "41 06 00 10 03", "41 06 01 1E 00", "41 06 02 32 00", "41 06 03 3C 00",
      "41 03 04 01 00", "41 08 00 19 00", "41 08 01 1E 00", "41 08 02 0A 00", "41 08 03 32 00", "41 08 04 32 00",
      "41 02 01 01 00", "41 02 02 00 00", "41 02 04 01 00",
    ]);
    expect(amp.sent).toHaveLength(r.total);
    // Never a save, a program write, a buffer load or VOLUME.
    expect(amp.sent.every((x) => !x.save)).toBe(true);
    expect(amp.sent.some((x) => [0x4c, 0x11, 0x40].includes(funcOf(x.frame)!))).toBe(false);
    expect(amp.funcs().some((f) => f.startsWith("41 04 04"))).toBe(false);
    const cur = lastState().current!;
    expect(cur.ampModel).toBe(1);
    expect(cur.amp[4]).toBe(VECTOR_A_STRUCT[0x17]); // VOLUME untouched
    expect(cur.amp[0]).toBe(40);
    expect(cur.pedal1Type).toBe(6);
    expect(cur.pedal1).toEqual([70, 50, 48, 55, 40, 60]);
    expect(cur.pedal2.slice(0, 4)).toEqual([400, 30, 50, 60]);
    expect([cur.pedal1On, cur.pedal2On, cur.reverbOn]).toEqual([true, false, true]);
  });

  it("sends BRIGHT CAP for models that have it", async () => {
    await connected();
    await run({ ...SOUND, amp: { ...SOUND.amp, model: 7, brightCap: 1 } });
    expect(amp.funcs()).toContain("41 04 07 01 00");
  });

  it("holds after the model ACK (200 ms without a burst) and after each type until its burst arrived", async () => {
    await connected();
    await run();
    const at = (f: string) => amp.sent[amp.funcs().indexOf(f)].at;
    // Model: no amp-param burst from the fake, so the 200 ms hold runs out.
    expect(at("41 04 00 28 00") - at("41 03 00 01 00")).toBe(5 + 200);
    // Pedal 1: ACK after 5 ms, 6 dials 10 ms apart; the first param goes out with the 6th dial.
    expect(at("41 05 00 46 00") - at("41 03 01 06 00")).toBe(5 + 60);
    // Reverb: 5 dials.
    expect(at("41 08 00 19 00") - at("41 03 04 01 00")).toBe(5 + 50);
  });

  it("a burst after the model ACK releases the hold early", async () => {
    await connected();
    const p = client.audition(SOUND);
    await vi.advanceTimersByTimeAsync(5);
    for (let i = 0; i < 12; i++) amp.push(korg(`41 04 ${i.toString(16).padStart(2, "0")} 00 00`), i + 1);
    await vi.advanceTimersByTimeAsync(3000);
    await p;
    expect(amp.sent[1].at - amp.sent[0].at).toBe(5 + 12);
  });

  it("reports NAKed steps and keeps going", async () => {
    await connected();
    amp.nakIf = (f) => f[6] === 0x41 && f[7] === 0x06 && f[8] === 0x00;
    const r = await run();
    expect(r.failed).toEqual([{ step: "pedal2 SPEED", code: "nak" }]);
    expect(r.aborted).toBeNull();
    expect(r.done).toBe(r.total);
    expect(amp.funcs().at(-1)).toBe("41 02 04 01 00");
  });

  it("stops on a timeout and reports where", async () => {
    await connected();
    amp.silent.set(0x41, 1);
    const r = await run();
    expect(r).toEqual({ total: 33, done: 0, failed: [{ step: "amp model", code: "timeout" }], aborted: "timeout", volume: null });
    expect(amp.sent).toHaveLength(1);
  });

  it("stops on a server error", async () => {
    await connected();
    amp.serverError.set(0x41, "amp_disconnected");
    const r = await run();
    expect(r.aborted).toBe("amp_disconnected");
    expect(amp.sent).toHaveLength(1);
  });

  it("refuses to start while another audition runs", async () => {
    await connected();
    const first = client.audition(SOUND);
    await expect(client.audition(SOUND)).rejects.toMatchObject({ code: "busy" });
    await vi.advanceTimersByTimeAsync(3000);
    expect((await first).failed).toEqual([]);
    // Free again afterwards.
    const again = client.audition(SOUND);
    await vi.advanceTimersByTimeAsync(3000);
    expect((await again).done).toBe(33);
  });

  it("skips the params of an effect whose type change was refused", async () => {
    await connected();
    amp.nakIf = (f) => f[6] === 0x41 && f[7] === 0x03 && f[8] === 0x01; // pedal1 type
    const r = await run();
    expect(r.failed).toEqual([{ step: "pedal1 type", code: "nak" }]);
    expect(r.done).toBe(r.total);
    expect(amp.funcs().some((f) => f.startsWith("41 05"))).toBe(false);
    // The other effects still get their params.
    expect(amp.funcs()).toContain("41 06 00 10 03");
    expect(amp.funcs().at(-1)).toBe("41 02 04 01 00");
  });

  it("locks select, save, type changes and live edits while it runs", async () => {
    await connected();
    const p = client.audition(SOUND);
    expect(client.busy).toBe(true);
    await expect(client.select(1)).rejects.toMatchObject({ code: "busy" });
    await expect(client.save(1, lastState().current!)).rejects.toMatchObject({ code: "busy" });
    await expect(client.setEffectType("pedal1", 2)).rejects.toMatchObject({ code: "busy" });
    client.setParam(0x04, 0x04, 99); // VOLUME: must never go out
    await vi.advanceTimersByTimeAsync(3000);
    const r = await p;
    expect(r.failed).toEqual([]);
    expect(client.busy).toBe(false);
    expect(amp.sent).toHaveLength(r.total);
    expect(amp.funcs().some((f) => /^(4C|4E|11|40) /.test(f) || f.startsWith("41 04 04"))).toBe(false);
  });

  it("reports a VOLUME change made by the amp itself", async () => {
    await connected();
    const before = lastState().current!.amp[4];
    const p = client.audition(SOUND);
    await vi.advanceTimersByTimeAsync(5);
    amp.push(korg(`41 04 04 ${(before === 77 ? 66 : 77).toString(16).toUpperCase()} 00`), 1); // burst after the model ACK
    await vi.advanceTimersByTimeAsync(3000);
    expect((await p).volume).toEqual({ from: before, to: before === 77 ? 66 : 77 });
  });

  it("rejects invalid settings before sending anything", async () => {
    await connected();
    const bad = { ...SOUND, pedal2: { ...SOUND.pedal2, type: 5, params: [10, 0, 0, 0, 0, 0] } }; // delay TIME < 30
    await expect(client.audition(bad)).rejects.toThrow(RangeError);
    await expect(client.audition({ ...SOUND, reverb: { ...SOUND.reverb, params: [1, 2, 3] } })).rejects.toThrow(RangeError);
    await expect(client.audition({ ...SOUND, amp: { ...SOUND.amp, gain: 101 } })).rejects.toThrow(RangeError);
    expect(amp.sent).toHaveLength(0);
    // A rejected call does not leave the client busy.
    const ok = client.audition(SOUND);
    await vi.advanceTimersByTimeAsync(3000);
    expect((await ok).failed).toEqual([]);
  });
});
