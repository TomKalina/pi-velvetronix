// Message plan for trying a whole sound live (docs/protocol.md §8.4, proven path: single `41` messages).
// Only the Current Buffer changes; nothing here can write amp memory (no 4C, no 11, no 40).

import { msg } from "./messages";
import { SLOT_PARAM_ID, type EffectSlot } from "./program";
import { AMP_MODELS, AMP_PARAMS, EFFECT_PARAMS, type EffectParam } from "./tables";

export interface AuditionEffect {
  on: boolean;
  type: number;
  /** All 6 (pedals) or 5 (reverb) values; indices the type does not use are not sent. */
  params: readonly number[];
}

/**
 * A full sound. There is deliberately no VOLUME: an audition never changes the program volume.
 * Same shape as a song preset, so presets can be passed directly.
 */
export interface AuditionSettings {
  amp: {
    model: number;
    gain: number; treble: number; middle: number; bass: number; presence: number; resonance: number;
    brightCap: number; lowCut: number; midBoost: number; bias: number; ampClass: number;
  };
  nr: number;
  pedal1: AuditionEffect;
  pedal2: AuditionEffect;
  reverb: AuditionEffect;
}

export interface AuditionStep {
  /** Short English label for failure reports, e.g. "amp GAIN", "pedal2 type". */
  label: string;
  frame: Uint8Array;
  /** Hold the queue after the ACK for an amp burst of `count` `41 <id>` messages (see auditionPlan). */
  hold?: { id: number; count: number };
  /** Effect slot of a type or param step; its params are skipped when its type step fails. */
  slot?: EffectSlot;
  isType?: boolean;
}

type AmpKey = Exclude<keyof AuditionSettings["amp"], "model">;

/** Program.amp index of each amp setting. Index 4 (VOLUME) is absent on purpose. */
const AMP_INDEX: Record<AmpKey, number> = {
  gain: 0, treble: 1, middle: 2, bass: 3, presence: 5, resonance: 6,
  brightCap: 7, lowCut: 8, midBoost: 9, bias: 10, ampClass: 11,
};
const BRIGHT_CAP = 7;
const SLOTS: readonly EffectSlot[] = ["pedal1", "pedal2", "reverb"];

function checkValue(what: string, v: number, spec: Pick<EffectParam, "min" | "max">): void {
  if (!Number.isInteger(v) || v < spec.min || v > spec.max) throw new RangeError(`${what} out of range ${spec.min}..${spec.max}: ${v}`);
}

/**
 * Order: amp model, amp params (no VOLUME), NR, then per effect its type followed by its params,
 * then the three on/off switches. Throws RangeError before anything is sent if a value is invalid.
 *
 * - After the model ACK the queue is held for an amp-param burst (12 x `41 04`, max 200 ms). The panel
 *   knob causes such a burst (P§6.2); whether a host-sent model change does is UNVERIFIED, so this is a
 *   precaution that costs at most 200 ms.
 * - Effect type changes get the regular burst hold of the client (P§6.2), so their params are sent after it.
 * - BRIGHT CAP is skipped for models that do not have it; unused effect indices are never sent (the amp
 *   rejects them, P§6.2).
 */
export function auditionPlan(s: AuditionSettings): AuditionStep[] {
  const model = AMP_MODELS[s.amp.model];
  if (!model || !Number.isInteger(s.amp.model)) throw new RangeError(`amp model out of range: ${s.amp.model}`);
  const steps: AuditionStep[] = [{ label: "amp model", frame: msg.ampModel(s.amp.model), hold: { id: 0x04, count: AMP_PARAMS.length } }];

  for (const [key, index] of Object.entries(AMP_INDEX) as [AmpKey, number][]) {
    const v = s.amp[key];
    const spec = AMP_PARAMS[index];
    checkValue(`amp ${spec.name}`, v, spec);
    if (index === BRIGHT_CAP && !model.brightCap) continue;
    steps.push({ label: `amp ${spec.name}`, frame: msg.ampParam(index, v) });
  }

  checkValue("nr", s.nr, { min: 0, max: 100 });
  steps.push({ label: "nr", frame: msg.noiseReduction(s.nr) });

  for (const slot of SLOTS) {
    const fx = s[slot];
    const row = EFFECT_PARAMS[slot][fx.type];
    if (!row || !Number.isInteger(fx.type)) throw new RangeError(`${slot} type out of range: ${fx.type}`);
    if (fx.params.length !== row.length) throw new RangeError(`${slot} needs ${row.length} params, got ${fx.params.length}`);
    if (typeof fx.on !== "boolean") throw new RangeError(`${slot} on must be boolean`);
    steps.push({ label: `${slot} type`, frame: msg.effectType(slot, fx.type), slot, isType: true });
    row.forEach((spec, n) => {
      if (!spec) return;
      checkValue(`${slot} ${spec.name}`, fx.params[n], spec);
      steps.push({ label: `${slot} ${spec.name}`, frame: msg.param(SLOT_PARAM_ID[slot], n, fx.params[n]), slot });
    });
  }

  for (const slot of SLOTS) steps.push({ label: `${slot} ${s[slot].on ? "on" : "off"}`, frame: msg.effectOn(slot, s[slot].on) });
  return steps;
}
