import { describe, expect, it } from "vitest";
import { dec14, enc14, pack7, unpack7 } from "./encoding";
import { hex, VECTOR_A_PACKED, VECTOR_A_STRUCT, VECTOR_B_PACKED, VECTOR_B_STRUCT } from "./testutil";

describe("enc14/dec14 (§11.2)", () => {
  const table: [number, string][] = [
    [120, "78 00"], [127, "7F 00"], [128, "00 01"], [129, "01 01"], [132, "04 01"],
    [100, "64 00"], [1650, "72 0C"], [5000, "08 27"], [10000, "10 4E"], [30, "1E 00"], [1200, "30 09"],
  ];
  it.each(table)("%i <-> %s", (v, bytes) => {
    const [lo, hi] = Array.from(hex(bytes));
    expect(enc14(v)).toEqual([lo, hi]);
    expect(dec14(lo, hi)).toBe(v);
  });

  it("round-trips the whole 14-bit range", () => {
    for (let v = 0; v <= 0x3fff; v++) expect(dec14(...enc14(v))).toBe(v);
  });

  it("rejects values > 0x3FFF, negatives and non-integers (§11.7)", () => {
    expect(() => enc14(0x4000)).toThrow(RangeError);
    expect(() => enc14(-1)).toThrow(RangeError);
    expect(() => enc14(1.5)).toThrow(RangeError);
  });
});

describe("pack7/unpack7 (§2.2)", () => {
  it("packs vector A and B to the documented 71 bytes", () => {
    expect(pack7(VECTOR_A_STRUCT)).toEqual(Uint8Array.from(VECTOR_A_PACKED));
    expect(pack7(VECTOR_B_STRUCT)).toEqual(VECTOR_B_PACKED);
  });

  it("unpacks vector A and B to the documented 62 bytes", () => {
    expect(unpack7(VECTOR_A_PACKED)).toEqual(VECTOR_A_STRUCT);
    expect(unpack7(VECTOR_B_PACKED)).toEqual(VECTOR_B_STRUCT);
  });

  it("puts the MSB bytes of vector A at packed 0, 8, ... 64", () => {
    const msbs = [0, 8, 16, 24, 32, 40, 48, 56, 64].map((i) => VECTOR_A_PACKED[i]);
    expect(msbs).toEqual([0x00, 0x00, 0x00, 0x00, 0x10, 0x20, 0x00, 0x00, 0x00]);
  });

  it("round-trips pack7(unpack7(x)) and unpack7(pack7(x))", () => {
    expect(pack7(unpack7(VECTOR_A_PACKED))).toEqual(Uint8Array.from(VECTOR_A_PACKED));
    expect(pack7(unpack7(VECTOR_B_PACKED))).toEqual(VECTOR_B_PACKED);
    const all = Uint8Array.from({ length: 62 }, (_, i) => (i * 37 + 0x80) & 0xff);
    expect(unpack7(pack7(all))).toEqual(all);
  });

  it("rejects an MSB bit for an absent byte in the last set (§11.7)", () => {
    const bad = Uint8Array.from(VECTOR_A_PACKED);
    bad[64] = 0x40;
    expect(() => unpack7(bad)).toThrow();
  });

  it("rejects bytes >= 0x80 and a lone trailing MSB byte", () => {
    const bad = Uint8Array.from(VECTOR_A_PACKED);
    bad[10] = 0x80;
    expect(() => unpack7(bad)).toThrow();
    const msbHigh = Uint8Array.from(VECTOR_A_PACKED);
    msbHigh[0] = 0x80;
    expect(() => unpack7(msbHigh)).toThrow();
    expect(() => unpack7(Uint8Array.from([...VECTOR_A_PACKED.subarray(0, 64), 0x00]))).toThrow();
  });
});
