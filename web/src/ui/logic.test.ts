import { describe, expect, it } from "vitest";
import type { UpdateInfo } from "../api";
import {
  controlGate, editGuardActive, isUpdating, precheckUpdate, UPDATE_MAX_BYTES, updatePollStep, validateHotspotMinutes,
  validateProgramName, validateWifiForm, visibleEffectParams, type GateInput,
} from "./logic";

const ok: GateInput = { ws: "open", amp: "connected", updating: false, ready: true, reading: false, readError: null };

describe("controlGate", () => {
  it("enables controls only when ws open, amp connected and readAll done", () => {
    expect(controlGate(ok)).toEqual({ enabled: true });
    expect(controlGate({ ...ok, ready: false, reading: true }).enabled).toBe(false);
    for (const amp of ["disconnected", "detected", "busy", "error", null] as const) {
      expect(controlGate({ ...ok, amp }).enabled, String(amp)).toBe(false);
    }
    for (const ws of ["idle", "connecting", "waiting", "busy", "replaced"] as const) {
      expect(controlGate({ ...ok, ws }).enabled, ws).toBe(false);
    }
    expect(controlGate({ ...ok, updating: true }).enabled).toBe(false);
  });

  it("does not trust a stale ready flag once the amp is gone", () => {
    expect(controlGate({ ...ok, amp: "disconnected", ready: true }).enabled).toBe(false);
  });

  it("offers the right retry", () => {
    const busy = controlGate({ ...ok, ws: "busy" });
    expect(busy).toMatchObject({ enabled: false, canRetryWs: true, canRetryRead: false });
    if (!busy.enabled) expect(busy.reason).toContain("Ovládání je otevřené v jiném prohlížeči");
    const replaced = controlGate({ ...ok, ws: "replaced" });
    expect(replaced).toMatchObject({ enabled: false, canRetryWs: true, canRetryRead: false });
    if (!replaced.enabled) expect(replaced.reason).toBe("Ovládání převzala jiná karta nebo okno tohoto prohlížeče.");
    expect(controlGate({ ...ok, ready: false, readError: "Kombo neodpovídá." })).toMatchObject({ canRetryRead: true });
  });
});

describe("isUpdating", () => {
  it("is true for staged and applying", () => {
    const u = (state: UpdateInfo["state"]): UpdateInfo => ({ state, current_version: "1", previous_version: null, pending: false, last_result: null });
    expect([isUpdating(null), isUpdating(u("idle")), isUpdating(u("staged")), isUpdating(u("applying"))]).toEqual([false, false, true, true]);
  });
});

describe("precheckUpdate", () => {
  const gz = Uint8Array.from([0x1f, 0x8b, 0x08, 0x00]);
  it("accepts a gzip file within the limit", () => {
    expect(precheckUpdate("pi-velvetronix-0.1.1-linux-arm64.tar.gz", 5_000_000, gz)).toBeNull();
    expect(precheckUpdate("x.tar.gz", UPDATE_MAX_BYTES, gz)).toBeNull();
  });

  it("rejects size, empty files and wrong formats with specific messages", () => {
    expect(precheckUpdate("x.tar.gz", UPDATE_MAX_BYTES + 1, gz)).toMatch(/příliš velký/);
    expect(precheckUpdate("x.tar.gz", 0, new Uint8Array())).toMatch(/prázdný/);
    expect(precheckUpdate("os.img.xz", 1000, Uint8Array.from([0xfd, 0x37, 0x7a, 0x58, 0x5a, 0x00]))).toMatch(/\.xz/);
    expect(precheckUpdate("pkg.zip", 1000, Uint8Array.from([0x50, 0x4b, 0x03, 0x04]))).toMatch(/\.zip/);
    expect(precheckUpdate("pi-velvetronix", 1000, Uint8Array.from([0x7f, 0x45, 0x4c, 0x46]))).toMatch(/samotný program/);
    expect(precheckUpdate("disk.img", 1000, Uint8Array.from([0xeb, 0x3c, 0x90, 0x00]))).toMatch(/\.img/);
    expect(precheckUpdate("disk.IMG.gz", 1000, gz)).toMatch(/obraz disku/);
    expect(precheckUpdate("notes.txt", 10, Uint8Array.from([0x68, 0x69]))).toMatch(/není balíček/);
  });
});

describe("validateProgramName", () => {
  it("accepts up to 16 printable ASCII chars", () => {
    expect(validateProgramName("")).toBeNull();
    expect(validateProgramName("CLEAN ~!@#$%^&*(")).toBeNull();
    expect(validateProgramName("x".repeat(17))).toMatch(/16/);
    expect(validateProgramName("Čistý")).toMatch(/„Č“/);
    expect(validateProgramName("tab\there")).not.toBeNull();
    expect(validateProgramName("a\u007f")).not.toBeNull();
  });
});

describe("Wi-Fi validation", () => {
  it("checks SSID and WPA passphrase", () => {
    expect(validateWifiForm("", "password1")).toMatch(/SSID/);
    expect(validateWifiForm("x".repeat(33), "password1")).toMatch(/32/);
    expect(validateWifiForm("HomeNet", "short")).toMatch(/8 až 63/);
    expect(validateWifiForm("HomeNet", "x".repeat(64))).toMatch(/8 až 63/);
    expect(validateWifiForm("HomeNet", "a".repeat(64))).toBeNull();
    expect(validateWifiForm("HomeNet", "password1")).toBeNull();
  });

  it("checks hotspot minutes 1..30", () => {
    expect([0, 1, 30, 31, 2.5, Number.NaN].map((v) => validateHotspotMinutes(v) === null)).toEqual([false, true, true, false, false, false]);
  });
});

describe("updatePollStep", () => {
  const before: UpdateInfo = { state: "idle", current_version: "0.1.0", previous_version: null, pending: false, last_result: "installed 0.1.0" };

  it("waits while the daemon is down or still on the old version", () => {
    expect(updatePollStep(before, null, 5000)).toBe("wait");
    expect(updatePollStep(before, { ...before, state: "applying" }, 5000)).toBe("wait");
    expect(updatePollStep(before, before, 5000)).toBe("wait");
  });

  it("is done only when the new version runs and confirmed itself", () => {
    expect(updatePollStep(before, { ...before, current_version: "0.1.1", state: "applying", pending: true }, 30_000)).toBe("wait");
    expect(updatePollStep(before, { ...before, current_version: "0.1.1" }, 30_000)).toBe("done");
  });

  it("is done when a new final result appeared on the old version (rollback / failure)", () => {
    expect(updatePollStep(before, { ...before, last_result: "rolled-back 0.1.1 -> releases/0.1.0" }, 60_000)).toBe("done");
    expect(updatePollStep(before, { ...before, state: "staged", last_result: "failed sha256" }, 60_000)).toBe("wait");
  });

  it("times out after 120 s", () => {
    expect(updatePollStep(before, null, 120_000)).toBe("timeout");
    expect(updatePollStep(before, before, 119_999)).toBe("wait");
  });

  it("keeps waiting while the device reports an update in progress, up to 330 s", () => {
    const pending = { ...before, current_version: "0.1.1", pending: true };
    expect(updatePollStep(before, pending, 200_000, 0)).toBe("wait");
    expect(updatePollStep(before, { ...before, state: "applying" }, 200_000, 0)).toBe("wait");
    expect(updatePollStep(before, { ...before, state: "staged" }, 200_000, 0)).toBe("wait");
    expect(updatePollStep(before, pending, 329_999, 0)).toBe("wait");
    expect(updatePollStep(before, pending, 330_000, 0)).toBe("timeout");
  });

  it("counts the 120 s from the last in-progress report (restart gaps during a rollback)", () => {
    expect(updatePollStep(before, null, 210_000, 8_000)).toBe("wait");
    expect(updatePollStep(before, before, 210_000, 119_999)).toBe("wait");
    expect(updatePollStep(before, null, 210_000, 120_000)).toBe("timeout");
    // The rollback result still ends the wait.
    expect(updatePollStep(before, { ...before, last_result: "rolled-back 0.1.1 -> releases/0.1.0" }, 215_000, 4_000)).toBe("done");
  });
});

describe("visibleEffectParams", () => {
  it("hides unused indices", () => {
    expect(visibleEffectParams("pedal1", 0).map((p) => p.n)).toEqual([0, 1, 2, 3]); // COMP
    expect(visibleEffectParams("pedal2", 1).map((p) => p.spec.name)).toEqual(["SPEED", "RESONANCE", "MANUAL", "DEPTH"]); // phaser
    expect(visibleEffectParams("pedal2", 4).map((p) => p.n)).toEqual([0, 1, 2, 3, 4]); // tremolo
    expect(visibleEffectParams("reverb", 2)).toHaveLength(5);
    expect(visibleEffectParams("pedal1", 99)).toEqual([]);
  });
});

describe("editGuardActive", () => {
  it("is active for 1.5 s after the last edit", () => {
    expect(editGuardActive(undefined, 1000)).toBe(false);
    expect(editGuardActive(1000, 2499)).toBe(true);
    expect(editGuardActive(1000, 2500)).toBe(false);
  });
});
