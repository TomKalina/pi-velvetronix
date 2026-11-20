// Tone Room program bank files (.vtxprog): a 32-byte prelude, then 1..11 program structs of 62 bytes,
// the same struct as a program dump (docs/protocol.md §5). Format notes: vox-amp-librarian
// doc/fileformats/vtxprog.md (MIT, Copyright (c) 2023 Tobias Marstaller).

import type { AuditionSettings } from "./audition";
import { decodeProgram, encodeProgram, STRUCT_LEN, type Program } from "./program";

export const VTXPROG_MAGIC = "VTXPROG1000 ";
export const PRELUDE_LEN = 32;
export const MAX_PROGRAMS = 11;

/** code: "not_vtxprog" (no prelude), "bad_length" (not prelude + n x 62 bytes), "too_many" (over 11). */
export class VtxprogError extends Error {
  constructor(readonly code: "not_vtxprog" | "bad_length" | "too_many") {
    super(`invalid .vtxprog file: ${code}`);
    this.name = "VtxprogError";
  }
}

/** One program slot of a file: decoded, or the reason it is unusable (a value out of range). */
export type VtxprogEntry = { index: number; program: Program } | { index: number; error: string };

export function parseVtxprog(file: Uint8Array): VtxprogEntry[] {
  const magic = String.fromCharCode(...file.subarray(0, VTXPROG_MAGIC.length));
  if (magic !== VTXPROG_MAGIC) throw new VtxprogError("not_vtxprog");
  const body = file.length - PRELUDE_LEN;
  if (body < STRUCT_LEN || body % STRUCT_LEN !== 0) throw new VtxprogError("bad_length");
  const n = body / STRUCT_LEN;
  if (n > MAX_PROGRAMS) throw new VtxprogError("too_many");
  const out: VtxprogEntry[] = [];
  for (let i = 0; i < n; i++) {
    const at = PRELUDE_LEN + i * STRUCT_LEN;
    const program = decodeProgram(file.subarray(at, at + STRUCT_LEN));
    try {
      encodeProgram(program); // validates every field (P§11.7)
      out.push({ index: i, program });
    } catch (e) {
      out.push({ index: i, error: e instanceof Error ? e.message : String(e) });
    }
  }
  return out;
}

/** The program as a live sound. Its VOLUME is left out on purpose: an audition never changes it. */
export function programToSettings(p: Program): AuditionSettings {
  const a = p.amp;
  return {
    amp: {
      model: p.ampModel,
      gain: a[0], treble: a[1], middle: a[2], bass: a[3], presence: a[5], resonance: a[6],
      brightCap: a[7], lowCut: a[8], midBoost: a[9], bias: a[10], ampClass: a[11],
    },
    nr: p.nr,
    pedal1: { on: p.pedal1On, type: p.pedal1Type, params: [...p.pedal1] },
    pedal2: { on: p.pedal2On, type: p.pedal2Type, params: [...p.pedal2] },
    reverb: { on: p.reverbOn, type: p.reverbType, params: [...p.reverb] },
  };
}
