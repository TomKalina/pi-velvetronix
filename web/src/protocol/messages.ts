// Frame builders and parser, docs/protocol.md §1, §4, §6.

import { dec14, enc14, pack7, unpack7 } from "./encoding";
import {
  decodeProgram, encodeProgram, SLOT_PARAM_COUNT, SLOT_PARAM_ID, SLOT_SUB, SLOT_TYPE_COUNT,
  type EffectSlot, type Program,
} from "./program";
import { AMP_MODELS, AMP_PARAMS, MODE_NAMES } from "./tables";

export const SYSEX_START = 0xf0;
export const SYSEX_END = 0xf7;
export const KORG = 0x42;
export const HEADER = [0xf0, 0x42, 0x30, 0x00, 0x01, 0x34] as const;
export const PACKED_LEN = 71;
export const USER_SLOTS = 8;

export const FN = {
  MODE_REQUEST: 0x12, CURRENT_PROGRAM_DUMP_REQUEST: 0x10, PROGRAM_DUMP_REQUEST: 0x1c,
  CUSTOM_DUMP_REQUEST: 0x31, PROGRAM_WRITE_REQUEST: 0x11,
  MODE_DATA: 0x42, CURRENT_PROGRAM_DUMP: 0x40, PROGRAM_DUMP: 0x4c, CUSTOM_DUMP: 0x65,
  MODE_CHANGE: 0x4e, PARAMETER_CHANGE: 0x41,
  DATA_FORMAT_ERROR: 0x26, ACK: 0x23, NAK: 0x24, WRITE_COMPLETED: 0x21, WRITE_ERROR: 0x22,
} as const;

/** Frame index of the KORG function byte. */
export const FUNC_INDEX = 6;

function korg(...data: number[]): Uint8Array {
  return Uint8Array.from([...HEADER, ...data, SYSEX_END]);
}

function check(what: string, v: number, min: number, max: number): void {
  if (!Number.isInteger(v) || v < min || v > max) throw new RangeError(`${what} out of range ${min}..${max}: ${v}`);
}

function userSlot(slot: number): number {
  check("user slot", slot, 0, USER_SLOTS - 1);
  return slot;
}

function param(id: number, sub: number, value: number): Uint8Array {
  check("param id", id, 0, 0x7f);
  check("param sub", sub, 0, 0x7f);
  return korg(FN.PARAMETER_CHANGE, id, sub, ...enc14(value));
}

export const msg = {
  deviceInquiry: (): Uint8Array => Uint8Array.from([0xf0, 0x7e, 0x7f, 0x06, 0x01, 0xf7]),
  modeRequest: (): Uint8Array => korg(FN.MODE_REQUEST),
  currentProgramRequest: (): Uint8Array => korg(FN.CURRENT_PROGRAM_DUMP_REQUEST),
  programRequest: (slot: number): Uint8Array => korg(FN.PROGRAM_DUMP_REQUEST, 0x00, userSlot(slot)),
  selectProgram: (slot: number): Uint8Array => korg(FN.MODE_CHANGE, 0x00, userSlot(slot)),
  writeProgram: (slot: number, p: Program): Uint8Array =>
    korg(FN.PROGRAM_DUMP, 0x00, userSlot(slot), ...pack7(encodeProgram(p))),
  param,
  noiseReduction(v: number): Uint8Array {
    check("nr", v, 0, 100);
    return param(0x01, 0x00, v);
  },
  effectOn: (slot: EffectSlot, on: boolean): Uint8Array => param(0x02, SLOT_SUB[slot], on ? 1 : 0),
  ampModel(m: number): Uint8Array {
    check("amp model", m, 0, AMP_MODELS.length - 1);
    return param(0x03, 0x00, m);
  },
  effectType(slot: EffectSlot, t: number): Uint8Array {
    check(`${slot} type`, t, 0, SLOT_TYPE_COUNT[slot] - 1);
    return param(0x03, SLOT_SUB[slot], t);
  },
  ampParam(n: number, v: number): Uint8Array {
    check("amp param", n, 0, AMP_PARAMS.length - 1);
    check(`amp ${AMP_PARAMS[n].name}`, v, AMP_PARAMS[n].min, AMP_PARAMS[n].max);
    return param(0x04, n, v);
  },
  /** Range checks per effect type are the caller's job (EFFECT_PARAMS); the type is not known here. */
  effectParam(slot: EffectSlot, n: number, v: number): Uint8Array {
    check(`${slot} param`, n, 0, SLOT_PARAM_COUNT[slot] - 1);
    // Only pedal param 0 is wider than 7 bits.
    check(`${slot} param ${n} value`, v, 0, n === 0 && slot !== "reverb" ? 0x3fff : 0x7f);
    return param(SLOT_PARAM_ID[slot], n, v);
  },
};

export type Mode = (typeof MODE_NAMES)[number];

export type Parsed =
  | { kind: "ack" } | { kind: "nak" } | { kind: "format_error" }
  | { kind: "write_completed"; slot: number } | { kind: "write_error"; slot: number }
  | { kind: "mode"; mode: Mode; program: number }
  | { kind: "mode_change"; mode: Mode; program: number }
  | { kind: "current_program"; program: Program }
  | { kind: "program"; bank: number; slot: number; program: Program }
  | { kind: "param"; id: number; sub: number; value: number }
  | { kind: "identity"; major: number; minor: number }
  | { kind: "unknown"; frame: Uint8Array };

function sysexValid(f: Uint8Array): boolean {
  if (f.length < 3 || f[0] !== SYSEX_START || f[f.length - 1] !== SYSEX_END) return false;
  for (let i = 1; i < f.length - 1; i++) if (f[i] & 0x80) return false;
  return true;
}

function isKorg(f: Uint8Array): boolean {
  return f.length >= HEADER.length + 2 && HEADER.every((b, i) => f[i] === b);
}

/** KORG function byte of a frame, or null for non-KORG frames (matches the server's `func`). */
export function funcOf(f: Uint8Array): number | null {
  return isKorg(f) ? f[FUNC_INDEX] : null;
}

function program(packed: Uint8Array): Program {
  return decodeProgram(unpack7(packed));
}

function parseKorg(f: Uint8Array): Parsed | null {
  const fn = f[FUNC_INDEX];
  const len = f.length;
  switch (fn) {
    case FN.ACK: return len === 8 ? { kind: "ack" } : null;
    case FN.NAK: return len === 8 ? { kind: "nak" } : null;
    case FN.DATA_FORMAT_ERROR: return len === 8 ? { kind: "format_error" } : null;
    case FN.WRITE_COMPLETED:
    case FN.WRITE_ERROR:
      if (len !== 10 || f[7] !== 0x00 || f[8] >= USER_SLOTS) return null;
      return { kind: fn === FN.WRITE_COMPLETED ? "write_completed" : "write_error", slot: f[8] };
    case FN.MODE_DATA:
    case FN.MODE_CHANGE: {
      const mode = MODE_NAMES[f[7]];
      if (len !== 10 || mode === undefined) return null;
      return { kind: fn === FN.MODE_DATA ? "mode" : "mode_change", mode, program: f[8] };
    }
    case FN.CURRENT_PROGRAM_DUMP:
      return len === 7 + PACKED_LEN + 1 ? { kind: "current_program", program: program(f.subarray(7, len - 1)) } : null;
    case FN.PROGRAM_DUMP:
      if (len !== 9 + PACKED_LEN + 1) return null;
      return { kind: "program", bank: f[7], slot: f[8], program: program(f.subarray(9, len - 1)) };
    case FN.PARAMETER_CHANGE:
      return len === 12 ? { kind: "param", id: f[7], sub: f[8], value: dec14(f[9], f[10]) } : null;
    default:
      return null;
  }
}

/** Parses one frame from the amp. Never throws; anything malformed or unexpected is `unknown`. */
export function parse(frame: Uint8Array): Parsed {
  const unknown: Parsed = { kind: "unknown", frame };
  try {
    if (!sysexValid(frame)) return unknown;
    // Identity reply: F0 7E cc 06 02 42 34 01 00 00 minor 00 major 00 F7
    if (frame[1] === 0x7e) {
      const ok = frame.length === 15 && frame[3] === 0x06 && frame[4] === 0x02 &&
        frame[5] === KORG && frame[6] === 0x34 && frame[7] === 0x01;
      return ok ? { kind: "identity", major: frame[12], minor: frame[10] } : unknown;
    }
    if (!isKorg(frame)) return unknown;
    return parseKorg(frame) ?? unknown;
  } catch {
    // unpack7/decodeProgram reject structurally invalid dumps.
    return unknown;
  }
}
