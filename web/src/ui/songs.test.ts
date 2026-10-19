import { describe, expect, it } from "vitest";
import { auditionPlan } from "../protocol/audition";
import { funcOf } from "../protocol/messages";
import { AMP_MODELS, AMP_PARAMS, EFFECT_PARAMS } from "../protocol/tables";
import { presetName, validateProgramName } from "./logic";
import { GENRES, SONGS } from "./songs";

const PARAM_COUNT = { pedal1: 6, pedal2: 6, reverb: 5 } as const;
const AMP_INDEX: Record<string, number> = {
  gain: 0, treble: 1, middle: 2, bass: 3, presence: 5, resonance: 6, brightCap: 7, lowCut: 8, midBoost: 9, bias: 10, ampClass: 11,
};

function keysDeep(v: unknown, out: string[] = []): string[] {
  if (v && typeof v === "object") for (const [k, x] of Object.entries(v)) { out.push(k); keysDeep(x, out); }
  return out;
}

describe("song presets", () => {
  it("are 40, with unique ids and known genres", () => {
    expect(SONGS).toHaveLength(40);
    expect(new Set(SONGS.map((s) => s.id)).size).toBe(SONGS.length);
    for (const s of SONGS) expect(GENRES).toContain(s.genre);
  });

  it("never carry a volume, anywhere", () => {
    for (const s of SONGS) expect(keysDeep(s).filter((k) => /volume/i.test(k))).toEqual([]);
  });

  it.each(SONGS.map((s) => [s.id, s] as const))("%s: every value is within the amp's ranges", (_id, s) => {
    expect(Number.isInteger(s.amp.model) && AMP_MODELS[s.amp.model]).toBeTruthy();
    const keys = Object.keys(s.amp).filter((k) => k !== "model").sort();
    expect(keys).toEqual(Object.keys(AMP_INDEX).sort());
    for (const [k, idx] of Object.entries(AMP_INDEX)) {
      const v = (s.amp as unknown as Record<string, number>)[k];
      expect(Number.isInteger(v), k).toBe(true);
      expect(v, k).toBeGreaterThanOrEqual(AMP_PARAMS[idx].min);
      expect(v, k).toBeLessThanOrEqual(AMP_PARAMS[idx].max);
    }
    if (!AMP_MODELS[s.amp.model].brightCap) expect(s.amp.brightCap).toBe(0);
    expect(Number.isInteger(s.nr) && s.nr >= 0 && s.nr <= 100).toBe(true);
    for (const slot of ["pedal1", "pedal2", "reverb"] as const) {
      const fx = s[slot];
      expect(typeof fx.on).toBe("boolean");
      const row = EFFECT_PARAMS[slot][fx.type];
      expect(row, `${slot} type ${fx.type}`).toBeDefined();
      expect(fx.params).toHaveLength(PARAM_COUNT[slot]);
      fx.params.forEach((v, n) => {
        const spec = row[n];
        expect(Number.isInteger(v)).toBe(true);
        if (spec) {
          expect(v, `${slot} ${spec.name}`).toBeGreaterThanOrEqual(spec.min);
          expect(v, `${slot} ${spec.name}`).toBeLessThanOrEqual(spec.max);
        } else expect(v, `${slot} unused ${n}`).toBe(0);
      });
    }
    expect(s.year === null || (Number.isInteger(s.year) && s.year > 1950 && s.year < 2030)).toBe(true);
    expect(s.source === null || s.source.startsWith("https://")).toBe(true);
    expect(s.description.length).toBeGreaterThan(20);
  });

  it.each(SONGS.map((s) => [s.id, s] as const))("%s: builds a live-only plan and a valid program name", (_id, s) => {
    const plan = auditionPlan(s);
    expect(plan.every((st) => funcOf(st.frame) === 0x41)).toBe(true);
    expect(plan.some((st) => st.frame[7] === 0x04 && st.frame[8] === 0x04)).toBe(false);
    expect(validateProgramName(presetName(s.title))).toBeNull();
    expect(presetName(s.title).length).toBeGreaterThan(0);
  });
});
