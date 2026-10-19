import { describe, expect, it } from "vitest";
import { pack7, unpack7 } from "./encoding";
import { applyParam, decodeProgram, encodeProgram, type Program } from "./program";
import { VECTOR_A_PACKED, VECTOR_A_STRUCT, VECTOR_B_STRUCT } from "./testutil";

/** Vector A described field by field (§11.4 "Meaning"). */
function vectorAFields(): Program {
  return {
    raw: new Uint8Array(62),
    name: "PI TEST",
    nr: 50,
    pedal1On: true, pedal2On: false, reverbOn: true,
    ampModel: 0x07,
    amp: [50, 60, 40, 70, 80, 30, 20, 1, 0, 1, 2, 1],
    pedal1Type: 0x01, pedal1: [128, 50, 50, 50, 0, 1],
    pedal2Type: 0x05, pedal2: [254, 50, 40, 50, 10, 0],
    reverbType: 0x01, reverb: [50, 50, 20, 50, 50],
  };
}

describe("vector A (§11.4)", () => {
  it("decodes the struct to the documented fields and keeps raw", () => {
    const p = decodeProgram(VECTOR_A_STRUCT);
    const { raw, ...fields } = p;
    const { raw: _, ...expected } = vectorAFields();
    expect(fields).toEqual(expected);
    expect(raw).toEqual(VECTOR_A_STRUCT);
  });

  it("encodes the fields (on a zero raw) to the documented struct", () => {
    expect(encodeProgram(vectorAFields())).toEqual(VECTOR_A_STRUCT);
  });

  it("round-trips struct -> program -> struct", () => {
    expect(encodeProgram(decodeProgram(VECTOR_A_STRUCT))).toEqual(VECTOR_A_STRUCT);
  });
});

describe("vector B (§11.5)", () => {
  it("decodes chorus 10000 mHz and delay 1200 ms as plain LE16", () => {
    const p = decodeProgram(VECTOR_B_STRUCT);
    expect(p.name).toBe("DELAY MAX");
    expect(p.pedal1Type).toBe(1);
    expect(p.pedal1).toEqual([10000, 67, 57, 10, 0, 0]);
    expect(p.pedal2Type).toBe(5);
    expect(p.pedal2).toEqual([1200, 50, 77, 50, 1, 0]);
    expect(encodeProgram(p)).toEqual(VECTOR_B_STRUCT);
  });

  it("decodes chorus 800 mHz as 800, not the 14-bit 416 (§2.2 row 3)", () => {
    const s = Uint8Array.from(VECTOR_A_STRUCT);
    s[0x20] = 0x20;
    s[0x21] = 0x03;
    const p = decodeProgram(unpack7(pack7(s)));
    expect(p.pedal1[0]).toBe(800);
  });
});

describe("pedal-2 param0 vectors (§11.6)", () => {
  // Doc offsets 27/2D/2E are packed indices 40/46/47.
  const rows: [number, number, number, number][] = [
    [0x00, 0x00, 0x01, 256], [0x00, 0x3c, 0x10, 4156], [0x20, 0x14, 0x08, 2196], [0x20, 0x7e, 0x00, 254],
  ];
  it.each(rows)("doc27=%i doc2D=%i doc2E=%i -> %i", (d27, d2d, d2e, value) => {
    const packed = Uint8Array.from(VECTOR_A_PACKED);
    packed[40] = d27;
    packed[46] = d2d;
    packed[47] = d2e;
    expect(decodeProgram(unpack7(packed)).pedal2[0]).toBe(value);
  });
});

describe("reserved bits", () => {
  it("preserves reserved status bits and reserved/unused bytes from raw", () => {
    const s = Uint8Array.from(VECTOR_A_STRUCT);
    s[0x11] |= 0x01 | 0x08 | 0x20 | 0x40 | 0x80;
    for (let i = 0x2f; i <= 0x36; i++) s[i] = i;
    s[0x3d] = 0x7f;
    const p = decodeProgram(s);
    expect(encodeProgram(p)).toEqual(s);
    const off = encodeProgram({ ...p, pedal1On: false, pedal2On: true, reverbOn: false });
    expect(off[0x11]).toBe((s[0x11] & ~0x16) | 0x04);
    expect(off.subarray(0x2f, 0x37)).toEqual(s.subarray(0x2f, 0x37));
    expect(off[0x3d]).toBe(0x7f);
  });

  it("keeps unused effect bytes the amp returned", () => {
    const s = Uint8Array.from(VECTOR_A_STRUCT);
    s[0x1f] = 0x00; // COMP: params 4 and 5 unused
    s[0x25] = 0xaa;
    s[0x26] = 0x03;
    s[0x20] = 10;
    s[0x21] = 0;
    s[0x24] = 2; // VOICE
    expect(encodeProgram(decodeProgram(s))).toEqual(s);
  });

  it("pads the name with spaces and starts from raw", () => {
    const p = { ...decodeProgram(VECTOR_A_STRUCT), name: "AB" };
    const s = encodeProgram(p);
    expect(String.fromCharCode(...s.subarray(0, 16))).toBe("AB              ");
    expect(s.subarray(16)).toEqual(VECTOR_A_STRUCT.subarray(16));
  });
});

describe("encodeProgram validation (§11.7)", () => {
  const base = () => decodeProgram(VECTOR_A_STRUCT);
  const cases: [string, (p: Program) => Partial<Program>][] = [
    ["model > 0x13", () => ({ ampModel: 0x14 })],
    ["P1 type > 09", () => ({ pedal1Type: 0x0a })],
    ["P2 type > 06", () => ({ pedal2Type: 0x07 })],
    ["reverb type > 03", () => ({ reverbType: 0x04 })],
    ["bias > 02", (p) => ({ amp: p.amp.map((v, i) => (i === 10 ? 3 : v)) })],
    ["switch > 01", (p) => ({ amp: p.amp.map((v, i) => (i === 7 ? 2 : v)) })],
    ["class > 01", (p) => ({ amp: p.amp.map((v, i) => (i === 11 ? 2 : v)) })],
    ["0-100 amp param > 0x64", (p) => ({ amp: p.amp.map((v, i) => (i === 0 ? 0x65 : v)) })],
    ["NR > 0x64", () => ({ nr: 0x65 })],
    ["0-100 effect param > 0x64", (p) => ({ pedal1: p.pedal1.map((v, i) => (i === 1 ? 0x65 : v)) })],
    ["reverb pre-delay > 0x46", (p) => ({ reverb: p.reverb.map((v, i) => (i === 2 ? 0x47 : v)) })],
    ["chorus speed < 100", (p) => ({ pedal1: [99, ...p.pedal1.slice(1)] })],
    ["chorus speed > 10000", (p) => ({ pedal1: [10001, ...p.pedal1.slice(1)] })],
    ["flanger speed > 5000", () => ({ pedal2Type: 0, pedal2: [5001, 0, 0, 0, 0, 0] })],
    ["flanger speed < 100", () => ({ pedal2Type: 0, pedal2: [99, 0, 0, 0, 0, 0] })],
    ["phaser speed > 10000", () => ({ pedal2Type: 1, pedal2: [10001, 0, 0, 0, 0, 0] })],
    ["tremolo speed < 1650", () => ({ pedal2Type: 4, pedal2: [1649, 0, 0, 0, 0, 0] })],
    ["tremolo speed > 10000", () => ({ pedal2Type: 4, pedal2: [10001, 0, 0, 0, 0, 0] })],
    ["delay time < 30", (p) => ({ pedal2: [29, ...p.pedal2.slice(1)] })],
    ["delay time > 1200", (p) => ({ pedal2: [1201, ...p.pedal2.slice(1)] })],
    ["chorus low cut > 1", (p) => ({ pedal1: p.pedal1.map((v, i) => (i === 4 ? 2 : v)) })],
    ["comp voice > 2", () => ({ pedal1Type: 0, pedal1: [0, 0, 0, 3, 0, 0] })],
    ["name longer than 16", () => ({ name: "12345678901234567" })],
    ["name with char < 0x20", () => ({ name: "A\tB" })],
    ["name with char > 0x7E", () => ({ name: "Á" })],
    ["non-integer value", () => ({ nr: 1.5 })],
    ["wrong param count", (p) => ({ reverb: p.reverb.slice(0, 4) })],
    ["raw not 62 bytes", () => ({ raw: new Uint8Array(61) })],
  ];
  it.each(cases)("rejects %s", (_, change) => {
    const p = base();
    expect(() => encodeProgram({ ...p, ...change(p) })).toThrow(RangeError);
  });

  it("accepts the range limits", () => {
    const p = base();
    expect(() => encodeProgram({ ...p, pedal1: [100, ...p.pedal1.slice(1)] })).not.toThrow();
    expect(() => encodeProgram({ ...p, pedal1: [10000, ...p.pedal1.slice(1)] })).not.toThrow();
    expect(() => encodeProgram({ ...p, pedal2: [30, ...p.pedal2.slice(1)] })).not.toThrow();
    expect(() => encodeProgram({ ...p, reverb: [100, 100, 70, 0, 0] })).not.toThrow();
    expect(() => encodeProgram({ ...p, name: "~".repeat(16) })).not.toThrow();
  });

  it("decodes an MSB bit on an ASCII/0-100 field but refuses to encode it", () => {
    const packed = Uint8Array.from(VECTOR_A_PACKED);
    packed[0] = 0x01; // set-0 MSB: bit 7 of name char 0
    packed[24] = 0x01; // set-3 MSB: bit 7 of struct 0x15 (MIDDLE)
    const p = decodeProgram(unpack7(packed));
    expect(p.name.charCodeAt(0)).toBe(0xd0);
    expect(p.amp[2]).toBe(0x28 | 0x80);
    expect(() => encodeProgram(p)).toThrow(RangeError);
  });

  it("decodeProgram rejects a struct that is not 62 bytes", () => {
    expect(() => decodeProgram(new Uint8Array(61))).toThrow(RangeError);
  });
});

describe("applyParam", () => {
  const p = decodeProgram(VECTOR_A_STRUCT);

  it("applies every ID to the right field without mutating the input", () => {
    expect(applyParam(p, 0x01, 0x00, 100).nr).toBe(100);
    expect(applyParam(p, 0x02, 0x01, 0).pedal1On).toBe(false);
    expect(applyParam(p, 0x02, 0x02, 1).pedal2On).toBe(true);
    expect(applyParam(p, 0x02, 0x04, 0).reverbOn).toBe(false);
    expect(applyParam(p, 0x03, 0x00, 0x0b).ampModel).toBe(0x0b);
    expect(applyParam(p, 0x03, 0x01, 6).pedal1Type).toBe(6);
    expect(applyParam(p, 0x03, 0x02, 0).pedal2Type).toBe(0);
    expect(applyParam(p, 0x03, 0x04, 3).reverbType).toBe(3);
    expect(applyParam(p, 0x04, 0x05, 64).amp[5]).toBe(64);
    expect(applyParam(p, 0x04, 0x0a, 2).amp[10]).toBe(2);
    expect(applyParam(p, 0x05, 0x00, 10000).pedal1[0]).toBe(10000);
    expect(applyParam(p, 0x06, 0x05, 10).pedal2[5]).toBe(10);
    expect(applyParam(p, 0x08, 0x02, 70).reverb[2]).toBe(70);
    expect(applyParam(p, 0x00, 0x00, 0x51).name).toBe("QI TEST");
    expect(applyParam(p, 0x00, 0x0f, 0x21).name).toBe("PI TEST        !");
    expect(p).toEqual(decodeProgram(VECTOR_A_STRUCT));
  });

  it("ignores unknown targets", () => {
    expect(applyParam(p, 0x07, 0x00, 1)).toBe(p);
    expect(applyParam(p, 0x04, 0x0c, 1)).toBe(p);
    expect(applyParam(p, 0x08, 0x05, 1)).toBe(p);
    expect(applyParam(p, 0x02, 0x03, 1)).toBe(p);
    expect(applyParam(p, 0x00, 0x10, 0x41)).toBe(p);
  });
});
