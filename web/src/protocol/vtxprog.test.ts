import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
import { auditionPlan } from "./audition";
import { STRUCT_LEN } from "./program";
import { VECTOR_A_STRUCT } from "./testutil";
import { parseVtxprog, PRELUDE_LEN, programToSettings, VTXPROG_MAGIC, VtxprogError } from "./vtxprog";
import { VTX_PRESETS } from "../ui/vtx-presets";

function file(...programs: Uint8Array[]): Uint8Array {
  const out = new Uint8Array(PRELUDE_LEN + programs.length * STRUCT_LEN);
  out.set(Array.from(VTXPROG_MAGIC, (c) => c.charCodeAt(0)), 0);
  programs.forEach((p, i) => out.set(p, PRELUDE_LEN + i * STRUCT_LEN));
  return out;
}

const code = (f: () => unknown) => {
  try {
    f();
  } catch (e) {
    return e instanceof VtxprogError ? e.code : String(e);
  }
  return null;
};

describe(".vtxprog", () => {
  it("reads every program struct after the prelude", () => {
    const entries = parseVtxprog(file(VECTOR_A_STRUCT, VECTOR_A_STRUCT));
    expect(entries).toHaveLength(2);
    const e = entries[1];
    if ("error" in e) throw new Error(e.error);
    expect(e.index).toBe(1);
    expect(e.program.name).toBe("PI TEST");
  });

  it("reads a real Tone Room file", () => {
    const bytes = new Uint8Array(readFileSync(new URL("../../research/vtxprog/marcosmoura/Arctic Monkeys.vtxprog", import.meta.url)));
    const entries = parseVtxprog(bytes);
    expect(entries).toHaveLength(8);
    expect(entries.map((e) => ("program" in e ? e.program.name : e.error))[0]).toBe("Do I Wanna");
  });

  it("rejects files that are not Tone Room banks", () => {
    expect(code(() => parseVtxprog(new Uint8Array(100)))).toBe("not_vtxprog");
    expect(code(() => parseVtxprog(file().subarray(0, PRELUDE_LEN)))).toBe("bad_length");
    expect(code(() => parseVtxprog(file(VECTOR_A_STRUCT).subarray(0, PRELUDE_LEN + 61)))).toBe("bad_length");
    expect(code(() => parseVtxprog(file(...Array(12).fill(VECTOR_A_STRUCT))))).toBe("too_many");
  });

  it("marks a program with a value out of range instead of failing the file", () => {
    const bad = Uint8Array.from(VECTOR_A_STRUCT);
    bad[0x12] = 99; // amp model
    const entries = parseVtxprog(file(VECTOR_A_STRUCT, bad));
    expect("program" in entries[0]).toBe(true);
    expect("error" in entries[1]).toBe(true);
  });

  it("turns a program into a live sound without its volume", () => {
    const e = parseVtxprog(file(VECTOR_A_STRUCT))[0];
    if ("error" in e) throw new Error(e.error);
    const s = programToSettings(e.program);
    expect(s.amp).toMatchObject({ model: 7, gain: 50, treble: 60, middle: 40, bass: 70, presence: 30, resonance: 20 });
    expect(s.amp).not.toHaveProperty("volume");
    expect(s.nr).toBe(e.program.nr);
    expect(() => auditionPlan(s)).not.toThrow();
  });

  it("bundled Tone Room presets are valid, unique and attributed", () => {
    expect(VTX_PRESETS.length).toBeGreaterThan(50);
    expect(new Set(VTX_PRESETS.map((p) => p.id)).size).toBe(VTX_PRESETS.length);
    for (const p of VTX_PRESETS) {
      expect(p.genre).toBe("tone-room");
      expect(p.source).toMatch(/^https:\/\/github\.com\/marcosmoura\/toneroom-presets\//);
      expect(() => auditionPlan(p), p.id).not.toThrow();
    }
  });
});
