// Shared bookkeeping for controls bound to the Current Buffer.

import type { Program } from "../protocol/program";
import type { EffectSlot } from "../protocol/program";
import { editGuardActive } from "./logic";

/** Remembers when the user last touched a control, so an older ACK does not snap it back. */
export interface Edits {
  touched(key: string): void;
  guarded(key: string): boolean;
  forget(key: string): void;
}

export function createEdits(): Edits {
  const last = new Map<string, number>();
  return {
    touched: (key) => void last.set(key, Date.now()),
    guarded: (key) => editGuardActive(last.get(key), Date.now()),
    forget: (key) => void last.delete(key),
  };
}

/** A control that shows one amp value. */
export interface Bound {
  set(p: Program): void;
}

export const ID_NR = 0x01;
export const ID_EFFECT_ON = 0x02;
export const ID_TYPE = 0x03;
export const ID_AMP = 0x04;
export const BRIGHT_CAP = 7;

export function effectOn(p: Program, slot: EffectSlot): boolean {
  return slot === "pedal1" ? p.pedal1On : slot === "pedal2" ? p.pedal2On : p.reverbOn;
}

export function effectType(p: Program, slot: EffectSlot): number {
  return slot === "pedal1" ? p.pedal1Type : slot === "pedal2" ? p.pedal2Type : p.reverbType;
}

export function effectValues(p: Program, slot: EffectSlot): number[] {
  return p[slot];
}
