// Enumerations and value ranges from the VOX MIDI implementation (docs/protocol.md §3, §5).
// Names are the amp's own (English); Czech UI labels live in the UI.

export interface AmpModel {
  id: number;
  name: string;
  /** BRIGHT CAP switch exists for this model. */
  brightCap: boolean;
  /** Label of amp param 5. */
  toneLabel: "TONE" | "PRESENCE";
}

const NO_BRIGHT_CAP = new Set([0x01, 0x03, 0x09, 0x0f, 0x11]);

export const AMP_MODELS: readonly AmpModel[] = [
  "DELUXE CL VIBRATO", "DELUXE CL NORMAL", "TWEED 4x10 BRIGHT", "TWEED 4x10 NORMAL",
  "BOUTIQUE CL", "BOUTIQUE OD", "VOX AC30", "VOX AC30TB", "BRIT 1959 TREBLE", "BRIT 1959 NORMAL",
  "BRIT 800", "BRIT VM", "SL-OD", "DOUBLE REC", "CALI ELATION", "ERUPT III CH2", "ERUPT III CH3",
  "BOUTIQUE METAL", "BRIT OR MKII", "ORIGINAL CL",
].map((name, id) => ({
  id,
  name,
  brightCap: !NO_BRIGHT_CAP.has(id),
  toneLabel: id === 0x06 || id === 0x07 ? "TONE" : "PRESENCE",
}));

export interface EffectType {
  id: number;
  name: string;
}

const types = (names: string[]): readonly EffectType[] => names.map((name, id) => ({ id, name }));

export const PEDAL1_TYPES = types([
  "COMP", "CHORUS", "TUBE OD", "GOLD DRIVE", "TREBLE BOOST", "RC TURBO",
  "ORANGE DIST", "FAT DIST", "BRIT LEAD", "FUZZ",
]);
export const PEDAL2_TYPES = types([
  "FLANGER", "BLACK PHASER", "ORANGE PHASER 1", "ORANGE PHASER 2", "TREMOLO", "TAPE ECHO", "ANALOG DELAY",
]);
export const REVERB_TYPES = types(["ROOM", "SPRING", "HALL", "PLATE"]);

/**
 * knob: 0..100, shown as 0.0..10.0. switch: 0 off / 1 on.
 * bias: 0 OFF, 1 COLD, 2 HOT. class: 0 A, 1 A/B.
 */
export type AmpParamKind = "knob" | "switch" | "bias" | "class";

export interface AmpParam {
  /** SUB in `41 04 nn`, index into Program.amp. */
  index: number;
  name: string;
  kind: AmpParamKind;
  min: number;
  max: number;
}

const AMP_PARAM_DEFS: [string, AmpParamKind][] = [
  ["GAIN", "knob"], ["TREBLE", "knob"], ["MIDDLE", "knob"], ["BASS", "knob"], ["VOLUME", "knob"],
  // Shown as TONE or PRESENCE depending on the model (AmpModel.toneLabel).
  ["PRESENCE", "knob"], ["RESONANCE", "knob"],
  ["BRIGHT CAP", "switch"], ["LOW CUT", "switch"], ["MID BOOST", "switch"],
  ["BIAS SHIFT", "bias"], ["CLASS", "class"],
];

const KIND_MAX: Record<AmpParamKind, number> = { knob: 100, switch: 1, bias: 2, class: 1 };

export const AMP_PARAMS: readonly AmpParam[] = AMP_PARAM_DEFS.map(([name, kind], index) => ({
  index, name, kind, min: 0, max: KIND_MAX[kind],
}));

export const BIAS_NAMES = ["OFF", "COLD", "HOT"] as const;
export const CLASS_NAMES = ["A", "A/B"] as const;
export const MODE_NAMES = ["user", "preset", "manual"] as const;
export const USER_SLOT_NAMES = ["A1", "A2", "A3", "A4", "B1", "B2", "B3", "B4"] as const;

/**
 * "0-100": 0..100 shown as 0.0..10.0. "mHz": frequency in millihertz. "ms": milliseconds.
 * "bool": 0 off / 1 on. "voice": COMP voice 0..2 (shown as 1..3).
 */
export type EffectParamUnit = "0-100" | "mHz" | "ms" | "bool" | "voice";

export interface EffectParam {
  name: string;
  unit: EffectParamUnit;
  min: number;
  max: number;
}

/** null = index not used by this type (the amp keeps and reports 0 there). */
export type EffectParamRow = readonly (EffectParam | null)[];

const level = (name: string): EffectParam => ({ name, unit: "0-100", min: 0, max: 100 });
const onOff = (name: string): EffectParam => ({ name, unit: "bool", min: 0, max: 1 });
const speed = (min: number, max: number): EffectParam => ({ name: "SPEED", unit: "mHz", min, max });

const COMP: EffectParamRow = [
  level("SENS"), level("LEVEL"), level("ATTACK"), { name: "VOICE", unit: "voice", min: 0, max: 2 }, null, null,
];
const CHORUS: EffectParamRow = [
  speed(100, 10000), level("DEPTH"), level("MANUAL"), level("MIX"), onOff("LOW CUT"), onOff("HIGH CUT"),
];
const DRIVE: EffectParamRow = [
  level("DRIVE"), level("TONE"), level("LEVEL"), level("TREBLE"), level("MIDDLE"), level("BASS"),
];
const FLANGER: EffectParamRow = [
  speed(100, 5000), level("DEPTH"), level("MANUAL"), onOff("LOW CUT"), onOff("HIGH CUT"), level("RESONANCE"),
];
const PHASER: EffectParamRow = [
  speed(100, 10000), level("RESONANCE"), level("MANUAL"), level("DEPTH"), null, null,
];
const TREMOLO: EffectParamRow = [
  speed(1650, 10000), level("DEPTH"), level("DUTY"), level("SHAPE"), level("LEVEL"), null,
];
const DELAY: EffectParamRow = [
  { name: "TIME", unit: "ms", min: 30, max: 1200 },
  level("LEVEL"), level("FEEDBACK"), level("TONE"), level("MOD SPEED"), level("MOD DEPTH"),
];
const REVERB: EffectParamRow = [
  level("MIX"), level("TIME"), { name: "PRE DELAY", unit: "ms", min: 0, max: 70 }, level("LOW DAMP"), level("HIGH DAMP"),
];

/** EFFECT_PARAMS[slot][type][n]: param n (= live SUB) of that effect type. */
export const EFFECT_PARAMS: {
  readonly pedal1: readonly EffectParamRow[];
  readonly pedal2: readonly EffectParamRow[];
  readonly reverb: readonly EffectParamRow[];
} = {
  pedal1: [COMP, CHORUS, DRIVE, DRIVE, DRIVE, DRIVE, DRIVE, DRIVE, DRIVE, DRIVE],
  pedal2: [FLANGER, PHASER, PHASER, PHASER, TREMOLO, DELAY, DELAY],
  reverb: [REVERB, REVERB, REVERB, REVERB],
};
