import { describe, expect, it } from "vitest";
import { msg, parse, type Parsed } from "./messages";
import { decodeProgram } from "./program";
import {
  currentFrame, hex, payloadFrame, programFrame, VECTOR_A_FRAME, VECTOR_A_PACKED, VECTOR_A_STRUCT,
} from "./testutil";

describe("§11.1 payloads", () => {
  it("ACK", () => expect(parse(payloadFrame("30 00 01 34 23"))).toEqual({ kind: "ack" }));
  it("mode request", () => expect(msg.modeRequest()).toEqual(payloadFrame("30 00 01 34 12")));
  it("mode response", () => {
    expect(parse(payloadFrame("30 00 01 34 42 00 03"))).toEqual({ kind: "mode", mode: "user", program: 3 });
    expect(parse(payloadFrame("30 00 01 34 42 01 20"))).toEqual({ kind: "mode", mode: "preset", program: 0x20 });
    expect(parse(payloadFrame("30 00 01 34 42 02 00"))).toEqual({ kind: "mode", mode: "manual", program: 0 });
  });
  it("request user program", () => {
    for (let xx = 0; xx < 8; xx++) expect(msg.programRequest(xx)).toEqual(payloadFrame(`30 00 01 34 1C 00 0${xx}`));
  });
  it("user program dump / write prefix 4c 00 XX 00", () => {
    const f = msg.writeProgram(5, decodeProgram(VECTOR_A_STRUCT));
    expect(Array.from(f.subarray(0, 10))).toEqual(Array.from(hex("F0 42 30 00 01 34 4C 00 05 00")));
  });
  it("request current program", () => expect(msg.currentProgramRequest()).toEqual(payloadFrame("30 00 01 34 10")));
  it("current program dump prefix 40 00", () => {
    const f = currentFrame(VECTOR_A_PACKED);
    expect(Array.from(f.subarray(2, 8))).toEqual(Array.from(hex("30 00 01 34 40 00")));
    const p = parse(f);
    expect(p.kind).toBe("current_program");
    if (p.kind === "current_program") expect(p.program.raw).toEqual(VECTOR_A_STRUCT);
  });
  it("program select / changed 4e 00 XX", () => {
    expect(msg.selectProgram(6)).toEqual(payloadFrame("30 00 01 34 4E 00 06"));
    expect(parse(payloadFrame("30 00 01 34 4e 00 06"))).toEqual({ kind: "mode_change", mode: "user", program: 6 });
  });
  it("preset selected 4e 01 XX", () => {
    expect(parse(payloadFrame("30 00 01 34 4e 01 11"))).toEqual({ kind: "mode_change", mode: "preset", program: 0x11 });
  });
  it("manual mode 4e 02 00", () => {
    expect(parse(payloadFrame("30 00 01 34 4e 02 00"))).toEqual({ kind: "mode_change", mode: "manual", program: 0 });
  });
  it("custom amp/fx request 31 00 XX is host-only and has no builder: parses as unknown", () => {
    expect(parse(payloadFrame("30 00 01 34 31 00 02")).kind).toBe("unknown");
  });
  it("AmpDialTurnedMessageTest vectors", () => {
    expect(parse(payloadFrame("30 00 01 34 41 04 05 40 00"))).toEqual({ kind: "param", id: 4, sub: 5, value: 64 });
    expect(parse(payloadFrame("30 00 01 34 41 04 0A 02 00"))).toEqual({ kind: "param", id: 4, sub: 10, value: 2 });
    expect(msg.ampParam(5, 0x40)).toEqual(payloadFrame("30 00 01 34 41 04 05 40 00"));
    expect(msg.ampParam(10, 2)).toEqual(payloadFrame("30 00 01 34 41 04 0A 02 00"));
  });
  it("POC set gain and ACK", () => {
    expect(msg.ampParam(0, 0x2a)).toEqual(hex("F0 42 30 00 01 34 41 04 00 2A 00 F7"));
    expect(parse(hex("F0 42 30 00 01 34 23 F7"))).toEqual({ kind: "ack" });
  });
});

describe("§11.3 frames", () => {
  const builders: [string, () => Uint8Array, string][] = [
    ["device inquiry", () => msg.deviceInquiry(), "F0 7E 7F 06 01 F7"],
    ["mode request", () => msg.modeRequest(), "F0 42 30 00 01 34 12 F7"],
    ["current program request", () => msg.currentProgramRequest(), "F0 42 30 00 01 34 10 F7"],
    ["read user B4", () => msg.programRequest(7), "F0 42 30 00 01 34 1C 00 07 F7"],
    ["select B1", () => msg.selectProgram(4), "F0 42 30 00 01 34 4E 00 04 F7"],
    ["gain 5.0", () => msg.ampParam(0, 50), "F0 42 30 00 01 34 41 04 00 32 00 F7"],
    ["bias COLD", () => msg.ampParam(10, 1), "F0 42 30 00 01 34 41 04 0A 01 00 F7"],
    ["amp model VOX AC30TB", () => msg.ampModel(7), "F0 42 30 00 01 34 41 03 00 07 00 F7"],
    ["NR 10.0", () => msg.noiseReduction(100), "F0 42 30 00 01 34 41 01 00 64 00 F7"],
    ["pedal 2 on", () => msg.effectOn("pedal2", true), "F0 42 30 00 01 34 41 02 02 01 00 F7"],
    ["reverb off", () => msg.effectOn("reverb", false), "F0 42 30 00 01 34 41 02 04 00 00 F7"],
    ["pedal1 = ORANGE DIST", () => msg.effectType("pedal1", 6), "F0 42 30 00 01 34 41 03 01 06 00 F7"],
    ["reverb = SPRING", () => msg.effectType("reverb", 1), "F0 42 30 00 01 34 41 03 04 01 00 F7"],
    ["chorus SPEED 10.000 Hz", () => msg.effectParam("pedal1", 0, 10000), "F0 42 30 00 01 34 41 05 00 10 4E F7"],
    ["chorus SPEED 0.128 Hz", () => msg.effectParam("pedal1", 0, 128), "F0 42 30 00 01 34 41 05 00 00 01 F7"],
    ["tremolo SPEED 1.650 Hz", () => msg.effectParam("pedal2", 0, 1650), "F0 42 30 00 01 34 41 06 00 72 0C F7"],
    ["delay TIME 1200 ms", () => msg.effectParam("pedal2", 0, 1200), "F0 42 30 00 01 34 41 06 00 30 09 F7"],
    ["phaser MANUAL 7.7", () => msg.effectParam("pedal2", 2, 77), "F0 42 30 00 01 34 41 06 02 4D 00 F7"],
    ["phaser DEPTH 5.0", () => msg.effectParam("pedal2", 3, 50), "F0 42 30 00 01 34 41 06 03 32 00 F7"],
    ["delay MOD DEPTH 1.0", () => msg.effectParam("pedal2", 5, 10), "F0 42 30 00 01 34 41 06 05 0A 00 F7"],
    ["reverb PRE DELAY 70 ms", () => msg.effectParam("reverb", 2, 70), "F0 42 30 00 01 34 41 08 02 46 00 F7"],
    ["generic param", () => msg.param(4, 0, 50), "F0 42 30 00 01 34 41 04 00 32 00 F7"],
  ];
  it.each(builders)("%s", (_, build, expected) => expect(build()).toEqual(hex(expected)));

  const replies: [string, string, Parsed][] = [
    ["mode User A4", "F0 42 30 00 01 34 42 00 03 F7", { kind: "mode", mode: "user", program: 3 }],
    ["ACK", "F0 42 30 00 01 34 23 F7", { kind: "ack" }],
    ["NAK", "F0 42 30 00 01 34 24 F7", { kind: "nak" }],
    ["format error", "F0 42 30 00 01 34 26 F7", { kind: "format_error" }],
    ["write completed A3", "F0 42 30 00 01 34 21 00 02 F7", { kind: "write_completed", slot: 2 }],
    ["write error A3", "F0 42 30 00 01 34 22 00 02 F7", { kind: "write_error", slot: 2 }],
    ["chorus speed", "F0 42 30 00 01 34 41 05 00 10 4E F7", { kind: "param", id: 5, sub: 0, value: 10000 }],
    ["identity", "F0 7E 00 06 02 42 34 01 00 00 03 00 01 00 F7", { kind: "identity", major: 1, minor: 3 }],
  ];
  it.each(replies)("parses %s", (_, frame, expected) => expect(parse(hex(frame))).toEqual(expected));

  it("the 1C 00 07 request is answered by an 81-byte 4C 00 07 frame", () => {
    const f = programFrame(7, VECTOR_A_PACKED);
    expect(f.length).toBe(81);
    const p = parse(f);
    expect(p).toMatchObject({ kind: "program", bank: 0, slot: 7 });
  });

  it("the current program frame is 79 bytes", () => expect(currentFrame(VECTOR_A_PACKED).length).toBe(79));
});

describe("§11.4 vector A frame", () => {
  it("writeProgram(3) produces the documented 81-byte frame", () => {
    const f = msg.writeProgram(3, decodeProgram(VECTOR_A_STRUCT));
    expect(f.length).toBe(81);
    expect(f).toEqual(VECTOR_A_FRAME);
  });

  it("parses the 81-byte frame back to the struct", () => {
    const p = parse(VECTOR_A_FRAME);
    expect(p.kind).toBe("program");
    if (p.kind !== "program") return;
    expect(p.bank).toBe(0);
    expect(p.slot).toBe(3);
    expect(p.program.raw).toEqual(VECTOR_A_STRUCT);
    expect(p.program.name).toBe("PI TEST");
    expect(p.program.pedal1[0]).toBe(128);
    expect(p.program.pedal2[0]).toBe(254);
  });
});

describe("parse rejects malformed frames (§11.7)", () => {
  const unknown: [string, Uint8Array][] = [
    ["empty", new Uint8Array(0)],
    ["not starting F0", hex("00 42 30 00 01 34 23 F7")],
    ["not ending F7", hex("F0 42 30 00 01 34 23 00")],
    ["byte >= 0x80 inside", hex("F0 42 30 00 01 34 23 80 F7")],
    ["realtime byte inside", hex("F0 42 30 00 01 F8 34 23 F7")],
    ["manufacturer != 42", hex("F0 41 30 00 01 34 23 F7")],
    ["format id != 30", hex("F0 42 31 00 01 34 23 F7")],
    ["family != 00 01 34", hex("F0 42 30 00 01 35 23 F7")],
    ["ACK too long", hex("F0 42 30 00 01 34 23 00 F7")],
    ["mode too short", hex("F0 42 30 00 01 34 42 00 F7")],
    ["mode with unknown mm", hex("F0 42 30 00 01 34 42 03 00 F7")],
    ["4E with unknown mm", hex("F0 42 30 00 01 34 4E 05 00 F7")],
    ["param too short", hex("F0 42 30 00 01 34 41 04 00 32 F7")],
    ["write completed with slot >= 8", hex("F0 42 30 00 01 34 21 00 08 F7")],
    ["40 with 70 data bytes", currentFrame(VECTOR_A_PACKED.subarray(0, 70))],
    ["40 with 72 data bytes", currentFrame(Uint8Array.from([...VECTOR_A_PACKED, 0]))],
    ["4C with 3+70", programFrame(3, VECTOR_A_PACKED.subarray(0, 70))],
    ["4C with 3+72", programFrame(3, Uint8Array.from([...VECTOR_A_PACKED, 0]))],
    ["unknown function", hex("F0 42 30 00 01 34 7F F7")],
    ["identity wrong family", hex("F0 7E 00 06 02 42 35 01 00 00 03 00 01 00 F7")],
    ["identity wrong length", hex("F0 7E 00 06 02 42 34 01 00 00 03 00 01 F7")],
    ["host inquiry", hex("F0 7E 7F 06 01 F7")],
  ];
  it.each(unknown)("%s -> unknown", (_, f) => {
    expect(parse(f)).toEqual({ kind: "unknown", frame: f });
  });

  it("rejects a dump whose last MSB byte has bit 6 set", () => {
    const packed = Uint8Array.from(VECTOR_A_PACKED);
    packed[64] = 0x40;
    expect(parse(programFrame(3, packed)).kind).toBe("unknown");
    expect(parse(currentFrame(packed)).kind).toBe("unknown");
  });

  it("never throws on random input", () => {
    let seed = 1;
    const rnd = () => ((seed = (seed * 1103515245 + 12345) & 0x7fffffff) >> 8) & 0xff;
    for (let i = 0; i < 2000; i++) {
      const len = rnd() % 90;
      const f = Uint8Array.from({ length: len }, () => rnd());
      if (len > 0) f[0] = 0xf0;
      if (len > 1) f[len - 1] = 0xf7;
      expect(() => parse(f)).not.toThrow();
    }
  });
});

describe("builders reject invalid input (§11.7)", () => {
  const bad: [string, () => unknown][] = [
    ["programRequest slot 8", () => msg.programRequest(8)],
    ["programRequest slot -1", () => msg.programRequest(-1)],
    ["selectProgram slot 8", () => msg.selectProgram(8)],
    ["writeProgram slot 8", () => msg.writeProgram(8, decodeProgram(VECTOR_A_STRUCT))],
    ["writeProgram invalid program", () => msg.writeProgram(0, { ...decodeProgram(VECTOR_A_STRUCT), nr: 101 })],
    ["ampModel 0x14", () => msg.ampModel(0x14)],
    ["effectType pedal1 0x0A", () => msg.effectType("pedal1", 0x0a)],
    ["effectType pedal2 7", () => msg.effectType("pedal2", 7)],
    ["effectType reverb 4", () => msg.effectType("reverb", 4)],
    ["ampParam bias 3", () => msg.ampParam(10, 3)],
    ["ampParam switch 2", () => msg.ampParam(7, 2)],
    ["ampParam knob 0x65", () => msg.ampParam(0, 0x65)],
    ["ampParam index 12", () => msg.ampParam(12, 0)],
    ["noiseReduction 0x65", () => msg.noiseReduction(0x65)],
    ["effectParam reverb index 5", () => msg.effectParam("reverb", 5, 0)],
    ["effectParam pedal2 index 6", () => msg.effectParam("pedal2", 6, 0)],
    ["effectParam param 1 > 7 bits", () => msg.effectParam("pedal1", 1, 0x80)],
    ["live value > 0x3FFF", () => msg.effectParam("pedal1", 0, 0x4000)],
    ["param value > 0x3FFF", () => msg.param(5, 0, 0x4000)],
    ["param id > 0x7F", () => msg.param(0x80, 0, 0)],
  ];
  it.each(bad)("%s", (_, build) => expect(build).toThrow(RangeError));
});
