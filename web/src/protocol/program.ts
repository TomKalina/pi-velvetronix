// 62-byte program struct, docs/protocol.md §5.

import { AMP_MODELS, AMP_PARAMS, EFFECT_PARAMS, PEDAL1_TYPES, PEDAL2_TYPES, REVERB_TYPES } from "./tables";

export type EffectSlot = "pedal1" | "pedal2" | "reverb";

export interface Program {
  /** 62 bytes as read from the amp; encodeProgram starts from it so reserved bits are preserved. */
  raw: Uint8Array;
  /** Up to 16 ASCII chars 0x20..0x7E; trailing spaces are trimmed on decode and padded on encode. */
  name: string;
  nr: number;
  pedal1On: boolean;
  pedal2On: boolean;
  reverbOn: boolean;
  ampModel: number;
  /** 12 values in P§5.1 order: gain, treble, middle, bass, volume, presence/tone, resonance,
   *  bright cap, low cut, mid boost, bias shift, class. */
  amp: number[];
  pedal1Type: number;
  pedal1: number[];
  pedal2Type: number;
  pedal2: number[];
  reverbType: number;
  reverb: number[];
}

export const STRUCT_LEN = 62;
export const NAME_LEN = 16;

const S = {
  nr: 0x10, status: 0x11, ampModel: 0x12, amp: 0x13,
  pedal1Type: 0x1f, pedal1: 0x20, pedal2Type: 0x27, pedal2: 0x28,
  reverbType: 0x37, reverb: 0x38,
} as const;

/** Effect-status bit = effect slot SUB in `41 02 ss` / `41 03 ss`. */
export const SLOT_BIT: Record<EffectSlot, number> = { pedal1: 0x02, pedal2: 0x04, reverb: 0x10 };
export const SLOT_SUB: Record<EffectSlot, number> = { pedal1: 0x01, pedal2: 0x02, reverb: 0x04 };
export const SLOT_PARAM_ID: Record<EffectSlot, number> = { pedal1: 0x05, pedal2: 0x06, reverb: 0x08 };
export const SLOT_PARAM_COUNT: Record<EffectSlot, number> = { pedal1: 6, pedal2: 6, reverb: 5 };
export const SLOT_TYPE_COUNT: Record<EffectSlot, number> = {
  pedal1: PEDAL1_TYPES.length, pedal2: PEDAL2_TYPES.length, reverb: REVERB_TYPES.length,
};

/** Pedal param 0 is LE16 at base; params 1..5 are single bytes at base+1+n. Reverb params are single bytes. */
function pedalParams(s: Uint8Array, base: number): number[] {
  const out = [s[base] | (s[base + 1] << 8)];
  for (let n = 1; n < 6; n++) out.push(s[base + 1 + n]);
  return out;
}

export function decodeProgram(struct62: Uint8Array): Program {
  if (struct62.length !== STRUCT_LEN) throw new RangeError(`program struct must be ${STRUCT_LEN} bytes, got ${struct62.length}`);
  const s = Uint8Array.from(struct62);
  const status = s[S.status];
  return {
    raw: s,
    name: String.fromCharCode(...s.subarray(0, NAME_LEN)).replace(/ +$/, ""),
    nr: s[S.nr],
    pedal1On: (status & SLOT_BIT.pedal1) !== 0,
    pedal2On: (status & SLOT_BIT.pedal2) !== 0,
    reverbOn: (status & SLOT_BIT.reverb) !== 0,
    ampModel: s[S.ampModel],
    amp: Array.from(s.subarray(S.amp, S.amp + 12)),
    pedal1Type: s[S.pedal1Type],
    pedal1: pedalParams(s, S.pedal1),
    pedal2Type: s[S.pedal2Type],
    pedal2: pedalParams(s, S.pedal2),
    reverbType: s[S.reverbType],
    reverb: Array.from(s.subarray(S.reverb, S.reverb + 5)),
  };
}

function check(field: string, v: number, min: number, max: number): void {
  if (!Number.isInteger(v) || v < min || v > max) throw new RangeError(`${field} out of range ${min}..${max}: ${v}`);
}

function checkEffect(slot: EffectSlot, type: number, values: number[]): void {
  check(`${slot} type`, type, 0, SLOT_TYPE_COUNT[slot] - 1);
  if (values.length !== SLOT_PARAM_COUNT[slot]) throw new RangeError(`${slot} needs ${SLOT_PARAM_COUNT[slot]} params`);
  const row = EFFECT_PARAMS[slot][type];
  values.forEach((v, n) => {
    const spec = row[n];
    // Unused indices keep whatever byte the amp returned.
    if (spec) check(`${slot} ${spec.name}`, v, spec.min, spec.max);
    else check(`${slot} param ${n} (unused)`, v, 0, 0xff);
  });
}

/** Validates every field (P§11.7) and returns the 62-byte struct, starting from p.raw. */
export function encodeProgram(p: Program): Uint8Array {
  if (p.raw.length !== STRUCT_LEN) throw new RangeError(`raw must be ${STRUCT_LEN} bytes`);
  if (p.name.length > NAME_LEN) throw new RangeError(`name longer than ${NAME_LEN} chars`);
  for (const ch of p.name) {
    const c = ch.charCodeAt(0);
    if (ch.length !== 1 || c < 0x20 || c > 0x7e) throw new RangeError(`name contains invalid char ${JSON.stringify(ch)}`);
  }
  check("nr", p.nr, 0, 100);
  check("amp model", p.ampModel, 0, AMP_MODELS.length - 1);
  if (p.amp.length !== AMP_PARAMS.length) throw new RangeError(`amp needs ${AMP_PARAMS.length} params`);
  AMP_PARAMS.forEach((spec) => check(`amp ${spec.name}`, p.amp[spec.index], spec.min, spec.max));
  checkEffect("pedal1", p.pedal1Type, p.pedal1);
  checkEffect("pedal2", p.pedal2Type, p.pedal2);
  checkEffect("reverb", p.reverbType, p.reverb);

  const s = Uint8Array.from(p.raw);
  const name = p.name.padEnd(NAME_LEN, " ");
  for (let i = 0; i < NAME_LEN; i++) s[i] = name.charCodeAt(i);
  s[S.nr] = p.nr;
  let status = s[S.status] & ~(SLOT_BIT.pedal1 | SLOT_BIT.pedal2 | SLOT_BIT.reverb);
  if (p.pedal1On) status |= SLOT_BIT.pedal1;
  if (p.pedal2On) status |= SLOT_BIT.pedal2;
  if (p.reverbOn) status |= SLOT_BIT.reverb;
  s[S.status] = status;
  s[S.ampModel] = p.ampModel;
  s.set(p.amp, S.amp);
  for (const [typeOff, base, type, values] of [
    [S.pedal1Type, S.pedal1, p.pedal1Type, p.pedal1],
    [S.pedal2Type, S.pedal2, p.pedal2Type, p.pedal2],
  ] as const) {
    s[typeOff] = type;
    s[base] = values[0] & 0xff;
    s[base + 1] = values[0] >> 8;
    for (let n = 1; n < 6; n++) s[base + 1 + n] = values[n];
  }
  s[S.reverbType] = p.reverbType;
  s.set(p.reverb, S.reverb);
  return s;
}

/** Applies a `41 ID SUB value` change to a copy of p. Unknown targets return p unchanged. Never throws. */
export function applyParam(p: Program, id: number, sub: number, value: number): Program {
  const q: Program = {
    ...p,
    amp: [...p.amp], pedal1: [...p.pedal1], pedal2: [...p.pedal2], reverb: [...p.reverb],
  };
  switch (id) {
    case 0x00:
      if (sub >= NAME_LEN) return p;
      {
        const chars = p.name.padEnd(NAME_LEN, " ").split("");
        chars[sub] = String.fromCharCode(value & 0xff);
        q.name = chars.join("").replace(/ +$/, "");
      }
      return q;
    case 0x01:
      if (sub !== 0) return p;
      q.nr = value;
      return q;
    case 0x02:
      if (sub === SLOT_SUB.pedal1) q.pedal1On = value !== 0;
      else if (sub === SLOT_SUB.pedal2) q.pedal2On = value !== 0;
      else if (sub === SLOT_SUB.reverb) q.reverbOn = value !== 0;
      else return p;
      return q;
    case 0x03:
      if (sub === 0) q.ampModel = value;
      else if (sub === SLOT_SUB.pedal1) q.pedal1Type = value;
      else if (sub === SLOT_SUB.pedal2) q.pedal2Type = value;
      else if (sub === SLOT_SUB.reverb) q.reverbType = value;
      else return p;
      return q;
    case 0x04:
      if (sub >= AMP_PARAMS.length) return p;
      q.amp[sub] = value;
      return q;
    case SLOT_PARAM_ID.pedal1:
      if (sub >= SLOT_PARAM_COUNT.pedal1) return p;
      q.pedal1[sub] = value;
      return q;
    case SLOT_PARAM_ID.pedal2:
      if (sub >= SLOT_PARAM_COUNT.pedal2) return p;
      q.pedal2[sub] = value;
      return q;
    case SLOT_PARAM_ID.reverb:
      if (sub >= SLOT_PARAM_COUNT.reverb) return p;
      q.reverb[sub] = value;
      return q;
    default:
      return p;
  }
}
