// Per-browser view preferences (display mode, name style). localStorage may be missing or throw
// (private mode, blocked storage): then the defaults apply and nothing is remembered.

import type { NameMode } from "./names";

export type DisplayMode = "graphic" | "technical";

export interface Prefs {
  display: DisplayMode;
  names: NameMode;
}

const KEY = "vtx.prefs.v1";
export const DEFAULT_PREFS: Prefs = { display: "graphic", names: "original" };

/** Pure: stored JSON -> prefs; anything unknown falls back to the default. */
export function parsePrefs(raw: string | null): Prefs {
  let v: Partial<Record<keyof Prefs, unknown>> = {};
  try {
    const parsed: unknown = raw ? JSON.parse(raw) : {};
    if (parsed && typeof parsed === "object") v = parsed as typeof v;
  } catch {
    // Corrupt value: defaults.
  }
  return {
    display: v.display === "technical" ? "technical" : "graphic",
    names: v.names === "code" ? "code" : "original",
  };
}

function read(): string | null {
  try {
    return localStorage.getItem(KEY);
  } catch {
    return null;
  }
}

/** Current preferences; change them only through setPref. */
export const prefs: Prefs = parsePrefs(read());

export function setPref<K extends keyof Prefs>(key: K, value: Prefs[K]): void {
  prefs[key] = value;
  try {
    localStorage.setItem(KEY, JSON.stringify(prefs));
  } catch {
    // Not remembered; still applies to this page.
  }
}
