import { describe, expect, it } from "vitest";
import { AMP_MODELS, PEDAL1_TYPES, PEDAL2_TYPES, REVERB_TYPES } from "../protocol/tables";
import { AMP_NAMES, NAMES_DISCLAIMER, nameEntry, PEDAL1_NAMES, PEDAL2_NAMES, REVERB_NAMES, shownName } from "./names";

describe("original names", () => {
  it("cover every model and effect type, in table order", () => {
    expect(AMP_NAMES.map((n) => n.code)).toEqual(AMP_MODELS.map((m) => m.name));
    expect(PEDAL1_NAMES.map((n) => n.code)).toEqual(PEDAL1_TYPES.map((t) => t.name));
    expect(PEDAL2_NAMES.map((n) => n.code)).toEqual(PEDAL2_TYPES.map((t) => t.name));
    expect(REVERB_NAMES.map((n) => n.code)).toEqual(REVERB_TYPES.map((t) => t.name));
  });

  it("keep channel variants of the same amp distinct", () => {
    const shorts = AMP_NAMES.map((n) => n.short);
    expect(new Set(shorts).size).toBe(shorts.length);
    for (const [a, b] of [[0, 1], [2, 3], [8, 9], [15, 16], [4, 5]]) expect(shorts[a]).not.toBe(shorts[b]);
  });

  it("show the original by default and the code on request", () => {
    expect(shownName("amp", 10, "original")).toMatchObject({ text: "Marshall JCM800", modest: false });
    expect(shownName("amp", 10, "code").text).toBe("BRIT 800");
    expect(shownName("amp", 10, "code").title).toContain("Marshall JCM800 (2203, 1983)");
  });

  it("mark guesses with ~ and as modest", () => {
    const tube = shownName("pedal1", 2, "original");
    expect(tube).toMatchObject({ text: "~Ibanez Tube Screamer", modest: true });
    expect(tube.title).toContain("odhad");
    for (const kind of ["pedal1", "pedal2", "reverb"] as const) {
      const rows = kind === "pedal1" ? PEDAL1_NAMES : kind === "pedal2" ? PEDAL2_NAMES : REVERB_NAMES;
      rows.forEach((n, i) => {
        const s = shownName(kind, i, "original");
        if (n.confidence === "inferred" && n.short !== null) expect(s.text.startsWith("~")).toBe(true);
        else expect(s.text.startsWith("~")).toBe(false);
      });
    }
  });

  it("fall back to the code name when there is no specific original", () => {
    expect(shownName("pedal1", 0, "original")).toMatchObject({ text: "COMP", modest: false });
    expect(shownName("reverb", 1, "original").text).toBe("SPRING");
    expect(shownName("amp", 99, "original").text).toBe("#99");
    expect(nameEntry("pedal2", 5)?.code).toBe("TAPE ECHO");
  });

  it("only VOX's own amps are official", () => {
    expect(AMP_NAMES.filter((n) => n.confidence === "official").map((n) => n.code)).toEqual(["VOX AC30", "VOX AC30TB"]);
  });

  it("carry the manual's trademark notice verbatim", () => {
    expect(NAMES_DISCLAIMER.text).toBe("All product names and company names are the trademarks or registered trademarks of their respective owners.");
    expect(NAMES_DISCLAIMER.cs.length).toBeGreaterThan(20);
  });
});
