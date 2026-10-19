import { describe, expect, it } from "vitest";
import type { Status } from "../api";
import { EFFECT_PARAMS } from "../protocol/tables";
import {
  addressList, formatBytes, formatEffectValue, formatHz, formatKnob, formatUptime, localName, modeLabel, wifiSummary, wsLabel,
} from "./format";

const status = (over: Partial<Status> = {}): Status => ({
  version: "0.1.0", hostname: "velvetronix", uptime_s: 10, addresses: ["192.168.1.86"],
  wifi: { mode: "sta", ssid: "HomeNet", ip: "192.168.1.86", signal: 72, ap_ssid: "VOX-Velvetronix", ap_ip: "10.42.0.1", last_error: null },
  mdns: { hostname: "velvetronix", alias: "vox", alias_state: "established" },
  amp: { state: "connected", card: null, name: null, usb_id: null, device: null, identity: null, error: null },
  controller: { active: false },
  update: { state: "idle", current_version: "0.1.0", previous_version: null, pending: false, last_result: null },
  auth: { configured: true },
  ...over,
});

describe("value formatting", () => {
  it("shows 0..100 as 0,0..10,0", () => {
    expect([0, 5, 64, 100].map(formatKnob)).toEqual(["0,0", "0,5", "6,4", "10,0"]);
  });

  it("converts mHz to Hz", () => {
    expect([100, 1650, 800, 5000, 10000, 1234].map(formatHz)).toEqual(["0,1 Hz", "1,65 Hz", "0,8 Hz", "5 Hz", "10 Hz", "1,23 Hz"]);
  });

  it("formats every effect unit", () => {
    const [comp, chorus] = EFFECT_PARAMS.pedal1;
    const delay = EFFECT_PARAMS.pedal2[5];
    expect(formatEffectValue(comp[0]!, 50)).toBe("5,0");
    expect(formatEffectValue(comp[3]!, 0)).toBe("1");
    expect(formatEffectValue(comp[3]!, 2)).toBe("3");
    expect(formatEffectValue(chorus[0]!, 2500)).toBe("2,5 Hz");
    expect(formatEffectValue(chorus[4]!, 1)).toBe("zap.");
    expect(formatEffectValue(chorus[4]!, 0)).toBe("vyp.");
    expect(formatEffectValue(delay[0]!, 350)).toBe("350 ms");
  });

  it("formats uptime and sizes", () => {
    expect(formatUptime(42)).toBe("42 s");
    expect(formatUptime(125)).toBe("2 min 5 s");
    expect(formatUptime(3 * 3600 + 7 * 60)).toBe("3 h 7 min");
    expect(formatUptime(93784)).toBe("1 d 2 h 3 min");
    expect(formatBytes(512)).toBe("512 B");
    expect(formatBytes(40 * 1024 * 1024)).toBe("40,0 MiB");
  });
});

describe("labels", () => {
  it("describes the amp mode", () => {
    expect(modeLabel("user", 5)).toBe("uživatelský program B2");
    expect(modeLabel("preset", 0)).toBe("preset č. 1");
    expect(modeLabel("manual", null)).toMatch(/^manuál/);
    expect(modeLabel(null, null)).toBe("neznámý");
  });

  it("describes the WebSocket state", () => {
    expect(wsLabel({ kind: "open" })).toBe("připojeno");
    expect(wsLabel({ kind: "waiting", retryInMs: 4000 })).toBe("odpojeno, zkusím znovu za 4 s");
    expect(wsLabel({ kind: "busy" })).toBe("Ovládání je otevřené v jiném prohlížeči");
  });

  it("summarises Wi-Fi in home and hotspot mode", () => {
    expect(wifiSummary(status().wifi)).toBe("domácí síť · HomeNet · 192.168.1.86 · 72 %");
    expect(wifiSummary({ ...status().wifi, mode: "ap", ssid: null, ip: null, signal: null })).toBe("hotspot · VOX-Velvetronix · 10.42.0.1");
  });

  it("lists hostnames and IPs; the alias only when established", () => {
    expect(addressList(status())).toEqual(["velvetronix.local", "vox.local", "192.168.1.86"]);
    const collided = status({ mdns: { hostname: "velvetronix.local", alias: "vox.local", alias_state: "collision" } });
    expect(addressList(collided)).toEqual(["velvetronix.local", "192.168.1.86"]);
    expect(localName("")).toBe("");
  });
});
