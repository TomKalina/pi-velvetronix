import { describe, expect, it, vi } from "vitest";
import { VoxError, type AmpState } from "../protocol/client";
import { AmpSync, type SyncClient } from "./sync";

const STATE: AmpState = { mode: "user", program: 0, current: null, slots: [], ready: true };

function fakeClient() {
  const pending: { resolve(s: AmpState): void; reject(e: unknown): void }[] = [];
  const c: SyncClient & { reads: number; resets: number } = {
    reads: 0,
    resets: 0,
    readAll() {
      c.reads++;
      return new Promise<AmpState>((resolve, reject) => pending.push({ resolve, reject }));
    },
    reset() {
      c.resets++;
      // Like VoxClient.reset(): the in-flight readAll rejects.
      for (const p of pending.splice(0)) p.reject(new VoxError("reset"));
    },
  };
  return { c, pending };
}

describe("AmpSync", () => {
  it("reads once when the link becomes live and is ready after readAll", async () => {
    const { c, pending } = fakeClient();
    const onChange = vi.fn();
    const s = new AmpSync(c, onChange);
    s.update(false);
    expect(c.reads).toBe(0);
    s.update(true);
    s.update(true);
    expect(c.reads).toBe(1);
    expect(s.reading).toBe(true);
    pending[0].resolve(STATE);
    await Promise.resolve();
    expect(s).toMatchObject({ ready: true, reading: false, error: null });
    expect(onChange).toHaveBeenCalled();
  });

  it("resets the client when the link drops, and ignores the rejected stale read", async () => {
    const { c } = fakeClient();
    const s = new AmpSync(c, () => {});
    s.update(true);
    s.update(false);
    await Promise.resolve();
    expect(c.resets).toBe(1);
    expect(s).toMatchObject({ ready: false, reading: false, error: null });
  });

  it("reads again when the amp comes back", async () => {
    const { c, pending } = fakeClient();
    const s = new AmpSync(c, () => {});
    s.update(true);
    pending[0].resolve(STATE);
    await Promise.resolve();
    s.update(false);
    s.update(true);
    expect(c.reads).toBe(2);
    expect(s.ready).toBe(false);
  });

  it("reports a failed read in Czech and retries on request", async () => {
    const { c, pending } = fakeClient();
    const s = new AmpSync(c, () => {});
    s.update(true);
    pending.shift()!.reject(new VoxError("timeout"));
    await Promise.resolve();
    expect(s.error).toBe("Kombo neodpovídá.");
    s.retry();
    expect(c.reads).toBe(2);
    expect(s.error).toBeNull();
    s.retry(); // already reading
    expect(c.reads).toBe(2);
  });

  it("does not retry while the link is down", () => {
    const { c } = fakeClient();
    const s = new AmpSync(c, () => {});
    s.retry();
    expect(c.reads).toBe(0);
  });
});
