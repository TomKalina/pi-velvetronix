// Pure view-model decisions (no DOM): when controls are enabled, input validation, update polling.

import type { AmpConnState, UpdateInfo } from "../api";
import type { EffectSlot } from "../protocol/program";
import { EFFECT_PARAMS, type EffectParam } from "../protocol/tables";
import type { ConnState } from "../ws";
import { BUSY_TEXT, REPLACED_TEXT } from "./format";

export interface GateInput {
  ws: ConnState["kind"];
  /** null until the first status/amp message arrived. */
  amp: AmpConnState | null;
  updating: boolean;
  /** readAll() completed for the current connection. */
  ready: boolean;
  reading: boolean;
  readError: string | null;
}

export type Gate = { enabled: true } | { enabled: false; reason: string; canRetryRead: boolean; canRetryWs: boolean };

const off = (reason: string, canRetryRead = false, canRetryWs = false): Gate => ({ enabled: false, reason, canRetryRead, canRetryWs });

/** Controls are enabled only after readAll() succeeded and the amp is connected (design §9.2). */
export function controlGate(g: GateInput): Gate {
  if (g.updating) return off("Probíhá aktualizace, ovládání je pozastavené.");
  switch (g.ws) {
    case "busy": return off(`${BUSY_TEXT}. Zavřete ho tam a pak klikněte na Zkusit znovu.`, false, true);
    case "replaced": return off(`${REPLACED_TEXT}.`, false, true);
    case "idle":
    case "connecting": return off("Připojuji se k ovladači…");
    case "waiting": return off("Spojení s ovladačem je přerušené.", false, true);
    case "open": break;
  }
  switch (g.amp) {
    case null: return off("Zjišťuji stav komba…");
    case "disconnected": return off("Kombo není připojené přes USB. Zapněte ho a připojte kabel.");
    case "detected": return off("Kombo nalezeno, ověřuji, že jde o VOX Valvetronix X…");
    case "busy": return off("USB port komba drží jiný program.");
    case "error": return off("Chyba USB spojení s kombem.");
    case "connected": break;
  }
  if (g.ready) return { enabled: true };
  if (g.reading) return off("Načítám nastavení z komba…");
  if (g.readError) return off(`Načtení nastavení selhalo: ${g.readError}`, true);
  return off("Čekám na načtení nastavení…", true);
}

export function isUpdating(u: UpdateInfo | null): boolean {
  return u !== null && (u.state === "staged" || u.state === "applying");
}

export const UPDATE_MAX_BYTES = 40 * 1024 * 1024;

/** Client-side check of an update file before upload. Returns a Czech error or null. */
export function precheckUpdate(name: string, size: number, head: Uint8Array): string | null {
  const lower = name.toLowerCase();
  if (size === 0) return "Soubor je prázdný.";
  if (size > UPDATE_MAX_BYTES) return "Soubor je příliš velký (nejvýš 40 MiB). Obraz disku se sem nenahrává.";
  const isGzip = head.length >= 2 && head[0] === 0x1f && head[1] === 0x8b;
  if (!isGzip) {
    if (head[0] === 0xfd && head[1] === 0x37 && head[2] === 0x7a && head[3] === 0x58) {
      return "Toto je soubor .xz (např. obraz systému). Nahrajte balíček pi-velvetronix-<verze>-linux-arm64.tar.gz.";
    }
    if (head[0] === 0x50 && head[1] === 0x4b) return "Toto je soubor .zip. Nahrajte balíček .tar.gz.";
    if (head[0] === 0x7f && head[1] === 0x45 && head[2] === 0x4c && head[3] === 0x46) {
      return "Toto je samotný program. Nahrajte celý balíček .tar.gz.";
    }
    if (lower.endsWith(".img")) return "Toto je obraz disku (.img). Nahrajte balíček pi-velvetronix-<verze>-linux-arm64.tar.gz.";
    return "Soubor není balíček .tar.gz.";
  }
  if (lower.endsWith(".img.gz")) return "Toto je komprimovaný obraz disku. Nahrajte balíček pi-velvetronix-<verze>-linux-arm64.tar.gz.";
  return null;
}

export const PROGRAM_NAME_MAX = 16;

/** Program names are up to 16 printable ASCII chars (P§5.1). Returns a Czech error or null. */
export function validateProgramName(name: string): string | null {
  if (name.length > PROGRAM_NAME_MAX) return `Název smí mít nejvýš ${PROGRAM_NAME_MAX} znaků.`;
  for (const ch of name) {
    const c = ch.codePointAt(0) ?? 0;
    if (c < 0x20 || c > 0x7e) return `Znak „${ch}“ kombo neumí zobrazit. Použijte jen písmena bez diakritiky, číslice a běžné symboly.`;
  }
  return null;
}

/** Returns a Czech error or null. WPA-PSK: 8..63 chars, or 64 hex digits. */
export function validateWifiForm(ssid: string, password: string): string | null {
  const ssidBytes = new TextEncoder().encode(ssid).length;
  if (ssidBytes === 0) return "Zadejte název sítě (SSID).";
  if (ssidBytes > 32) return "Název sítě je příliš dlouhý (nejvýš 32 bajtů).";
  if (password.length === 64 && /^[0-9a-fA-F]+$/.test(password)) return null;
  if (password.length < 8 || password.length > 63) return "Heslo k Wi-Fi musí mít 8 až 63 znaků.";
  return null;
}

export function validateHotspotMinutes(v: number): string | null {
  return Number.isInteger(v) && v >= 1 && v <= 30 ? null : "Zadejte 1 až 30 minut.";
}

/** Max wait while the device reports nothing in progress (or is unreachable). */
export const UPDATE_POLL_LIMIT_MS = 120_000;
/** Overall cap: a never-READY release rolls back after about 210 s (4 x TimeoutStartSec 60 s + backoff). */
export const UPDATE_POLL_CAP_MS = 330_000;

export type PollStep = "wait" | "done" | "timeout";

/** The device still reports an update in progress (staged/applying, or a release not yet confirmed). */
export function updateInProgress(u: UpdateInfo | null): boolean {
  return u !== null && (u.pending || isUpdating(u));
}

/**
 * After a 202 upload: keep polling until the version changes or a new final result appears.
 * quietMs = time since the last poll that reported an update in progress. Times out after
 * UPDATE_POLL_LIMIT_MS without such a report, and after UPDATE_POLL_CAP_MS in any case.
 */
export function updatePollStep(before: UpdateInfo, now: UpdateInfo | null, elapsedMs: number, quietMs = elapsedMs): PollStep {
  if (now) {
    if (now.current_version !== before.current_version && now.state === "idle" && !now.pending) return "done";
    if (now.state === "idle" && !now.pending && now.last_result !== before.last_result) return "done";
  }
  if (elapsedMs >= UPDATE_POLL_CAP_MS) return "timeout";
  if (updateInProgress(now)) return "wait";
  return quietMs >= UPDATE_POLL_LIMIT_MS ? "timeout" : "wait";
}

export interface VisibleParam {
  n: number;
  spec: EffectParam;
}

/** Params used by this effect type; unused indices are never shown or sent. */
export function visibleEffectParams(slot: EffectSlot, type: number): VisibleParam[] {
  const row = EFFECT_PARAMS[slot][type] ?? [];
  const out: VisibleParam[] = [];
  row.forEach((spec, n) => {
    if (spec) out.push({ n, spec });
  });
  return out;
}

/** Skip writing amp state into a control the user touched this recently (an older ACK would snap it back). */
export const EDIT_GUARD_MS = 1500;

export function editGuardActive(lastEditAt: number | undefined, now: number): boolean {
  return lastEditAt !== undefined && now - lastEditAt < EDIT_GUARD_MS;
}

/** Song title -> amp program name: diacritics removed, printable ASCII only, max 16 chars. */
export function presetName(title: string): string {
  const ascii = title
    .normalize("NFD")
    .replace(/[̀-ͯ]/g, "")
    .replace(/[‘’]/g, "'")
    .replace(/[“”„]/g, '"')
    .replace(/[^\x20-\x7e]/g, "")
    .replace(/\s+/g, " ")
    .trim();
  return ascii.slice(0, PROGRAM_NAME_MAX).trimEnd();
}

/** Lowercase without diacritics, for search. */
export function searchKey(s: string): string {
  return s.normalize("NFD").replace(/[̀-ͯ]/g, "").toLowerCase();
}

/** Songs matching every word of the query (title, artist, year) and the genre (null = all). */
export function filterSongs<T extends { title: string; artist: string; genre: string; year: number | null }>(
  songs: readonly T[], query: string, genre: string | null,
): T[] {
  const words = searchKey(query).split(/\s+/).filter(Boolean);
  return songs.filter((s) => {
    if (genre !== null && s.genre !== genre) return false;
    const hay = searchKey(`${s.title} ${s.artist} ${s.year ?? ""}`);
    return words.every((w) => hay.includes(w));
  });
}
