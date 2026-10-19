// Pure formatting and Czech labels for the UI (no DOM).

import type { AliasState, AmpConnState, Status, WifiMode } from "../api";
import type { Mode } from "../protocol/messages";
import { USER_SLOT_NAMES, type EffectParam } from "../protocol/tables";
import type { ConnState } from "../ws";

/** Czech decimal comma. */
function decimal(v: number, digits: number): string {
  return v.toFixed(digits).replace(".", ",");
}

/** 0..100 -> "0,0".."10,0". */
export function formatKnob(v: number): string {
  return decimal(v / 10, 1);
}

/** mHz -> Hz with up to 2 decimals: 1650 -> "1,65 Hz", 100 -> "0,1 Hz", 10000 -> "10 Hz". */
export function formatHz(mHz: number): string {
  return `${decimal(mHz / 1000, 2).replace(/,?0+$/, "")} Hz`;
}

export function formatEffectValue(spec: EffectParam, v: number): string {
  switch (spec.unit) {
    case "0-100": return formatKnob(v);
    case "mHz": return formatHz(v);
    case "ms": return `${v} ms`;
    case "bool": return v ? "zap." : "vyp.";
    case "voice": return String(v + 1);
  }
}

export function slotName(slot: number): string {
  return USER_SLOT_NAMES[slot] ?? `#${slot}`;
}

export function modeLabel(mode: Mode | null, program: number | null): string {
  switch (mode) {
    case "user": return program === null ? "uživatelský program" : `uživatelský program ${slotName(program)}`;
    // Preset numbers are shown 1-based; the amp's own numbering is not documented (P§3).
    case "preset": return program === null ? "preset" : `preset č. ${program + 1}`;
    case "manual": return "manuál (ovladače na kombu)";
    case null: return "neznámý";
  }
}

export function wifiModeLabel(mode: WifiMode): string {
  switch (mode) {
    case "sta": return "domácí síť";
    case "ap": return "hotspot";
    case "connecting": return "připojuji…";
    case "off": return "vypnuto";
    case "unknown": return "neznámý stav";
  }
}

export function ampStateLabel(state: AmpConnState): string {
  switch (state) {
    case "disconnected": return "odpojeno";
    case "detected": return "nalezeno (ověřuji)";
    case "connected": return "připojeno";
    case "busy": return "obsazeno jiným programem";
    case "error": return "chyba";
  }
}

export const BUSY_TEXT = "Ovládání je otevřené v jiném prohlížeči";
export const REPLACED_TEXT = "Ovládání převzala jiná karta nebo okno tohoto prohlížeče";

export function wsLabel(s: ConnState): string {
  switch (s.kind) {
    case "idle": return "nepřipojeno";
    case "connecting": return "připojuji…";
    case "open": return "připojeno";
    case "waiting": return `odpojeno, zkusím znovu za ${Math.ceil(s.retryInMs / 1000)} s`;
    case "busy": return BUSY_TEXT;
    case "replaced": return REPLACED_TEXT;
  }
}

export function aliasLabel(state: AliasState): string {
  switch (state) {
    case "established": return "aktivní";
    case "registering": return "registruji…";
    case "collision": return "používá ho jiné zařízení";
    case "failed": return "selhalo";
    case "off": return "vypnuto";
  }
}

/** "velvetronix" -> "velvetronix.local"; names that already carry ".local" stay. */
export function localName(host: string): string {
  if (!host) return "";
  return host.endsWith(".local") ? host : `${host}.local`;
}

export function formatSignal(signal: number | null): string {
  return signal === null ? "" : `${signal} %`;
}

/** 93784 -> "1 d 2 h 3 min"; under an hour also shows seconds. */
export function formatUptime(totalS: number): string {
  const s = Math.max(0, Math.floor(totalS));
  const d = Math.floor(s / 86400);
  const h = Math.floor((s % 86400) / 3600);
  const m = Math.floor((s % 3600) / 60);
  const sec = s % 60;
  if (d > 0) return `${d} d ${h} h ${m} min`;
  if (h > 0) return `${h} h ${m} min`;
  return m > 0 ? `${m} min ${sec} s` : `${sec} s`;
}

export function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${decimal(n / 1024, 0)} KiB`;
  return `${decimal(n / (1024 * 1024), 1)} MiB`;
}

/** One-line Wi-Fi summary for the status bar. */
export function wifiSummary(w: Status["wifi"]): string {
  const parts = [wifiModeLabel(w.mode)];
  if (w.mode === "ap") parts.push(w.ap_ssid, w.ap_ip);
  else {
    if (w.ssid) parts.push(w.ssid);
    if (w.ip) parts.push(w.ip);
    if (w.signal !== null) parts.push(formatSignal(w.signal));
  }
  return parts.join(" · ");
}

/** Hostnames and IPs the device can be reached at. */
export function addressList(st: Status): string[] {
  const out: string[] = [];
  const host = localName(st.mdns.hostname || st.hostname);
  if (host) out.push(host);
  if (st.mdns.alias && st.mdns.alias_state === "established") out.push(localName(st.mdns.alias));
  for (const ip of st.addresses) if (!out.includes(ip)) out.push(ip);
  return out;
}
