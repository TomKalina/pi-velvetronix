# VOX Valvetronix X (VT20X/40X/100X) USB-MIDI SysEx protocol: authoritative implementation spec

For pi-velvetronix (TypeScript implementer; Go backend only bridges raw SysEx between WebSocket and ALSA rawmidi).
Date: 2026-10-19. Hex everywhere unless marked `dec`.

---

## 0. Sources, precedence, citation keys

### 0.1 Precedence (when sources disagree)

1. **MI**: official VOX "Valvetronix X MIDI IMPLEMENTATION Version 1.0 (31 Jul 2015)".
2. **VPC hardware notes**: third-party observations on a real VT40X (vox-preset-cli README/code comments).
3. **K**: vox-amp-librarian Kotlin code (tested by its author on a VT20X).
4. **D / F**: vox-amp-librarian protocol doc and VTXPROG doc.

The two input reports (`protocol-doc.md`, `protocol-code.md`) did not use MI. MI settles almost every open question
from those reports (see "Discrepancies resolved").

### 0.2 Citation keys

| Key | What | Location |
|---|---|---|
| MI:Lnn (p.N) | MI text extraction, line nn; p.N = PDF page from the `=== PAGE N ===` markers | local text extraction of the official PDF (not included) |
| (MI PDF) | Original PDF, https://voxamps.com/wp-content/uploads/2019/07/VTX_Midi_Implementation.pdf | Re-downloaded today into `.../research-pi/src/midi-impl-verify/VTX_Midi_Implementation.pdf`: HTTP 200, 75452 bytes, sha256 `500a089bdcd835fcefed8bde30ac7e2b47486b3f1c5ae35e3d9245389737d73a`. This is byte-identical to `.../research-pi/src/manual-midi/VTX_Midi_Implementation.pdf` |
| (MI alt) | Second, independent text extraction of the same PDF. Spot-checked: function list, NOTE 4 and all parameter IDs agree with `midi.txt`. For example, the phaser IDs are at alt:618-625 | `.../research-pi/src/pdfs/VTX_Midi_Impl.txt` |
| D#Lnn | `doc/protocols/vt_20_40_100_x.md` | clone `.../scratchpad/research/src/librarian-code/`, HEAD `c9799506ca2a9af934dd81d4eddd49892618e167`. Permalink pattern: `https://github.com/tmarsteel/vox-amp-librarian/blob/c9799506ca2a9af934dd81d4eddd49892618e167/<path>#L<n>` |
| F#Lnn | `doc/fileformats/vtxprog.md` | same clone |
| K/… | `src/main/kotlin/com/github/tmarsteel/voxamplibrarian/…` | same clone |
| KT/… | `src/test/kotlin/com/github/tmarsteel/voxamplibrarian/…` | same clone |
| VPC/… | vox-preset-cli, https://github.com/fran2007-k/vox-preset-cli, HEAD `37551f03100556f0b6c9179a40716b9cb125195d` (2026-09-22). Code and README were written by an AI agent; the README says hardware tests were "witnessed" by the owner on a VT40X (VPC/README.md:79-81) | `.../research-pi/src/vox-preset-cli-ro/` |
| OM:Lnn | VT100X owner's manual text | `.../research-pi/tools/manual.txt` |
| PD:Lnn / PC:Lnn | The two input reports | `.../research-pi/protocol-doc.md`, `.../research/protocol-code.md` |
| VEC | My own verification script, run as `python3 -I vectors.py`. It is not derived from any downloaded code | `.../research-pi/tools-protocol/vectors.py` |

Tags:

- **[MI]**: official doc.
- **[HW-VPC]**: third-party hardware observation (VT40X).
- **[HW-D]**: the librarian author's capture on a VT20X.
- **[K]**: code behaviour.
- **[INFER]**: my derivation, checked arithmetically.
- **UNVERIFIED**: nobody has confirmed it on a VT100X.

Nothing here has been tested on a VT100X.

---

## 1. Transport and framing

| Item | Value | Source |
|---|---|---|
| Identity | KORG manufacturer ID `42`. VOX Valvetronix X family ID `34 01` (LSB first) = 0x0134. Member `00 00` | MI:L11-31 (p.1) |
| Device inquiry, request | `F0 7E nn 06 01 F7`. nn = `00` (channel) or `7F` (any) | MI:L61-75 (p.2) |
| Device inquiry, reply | `F0 7E 00 06 02 42 34 01 00 00 vv 00 vv 00 F7`. The first vv is the minor version (from 00), the second is the major version (from 01) | MI:L11-31 |
| KORG SysEx header | `F0 42 30 00 01 34 <func> <data…> F7`. `30` = "Format ID"; `00 01 34` = "VOX Valvetronix X ID" | MI:L36-50, L78-92 |
| Data bytes | All bytes between F0 and F7 are 0x00-0x7F. Values wider than 7 bits use 14-bit split (live) or 7-to-8 packing (dumps) | MI:L286-289, L369-383 |
| Length field | None. A message ends at `F7` | MI:L36-50 |
| Channel messages | None are documented. MI covers SysEx only | MI whole doc; `.../research-pi/vt100x.md` §8.3 |
| Port names | Windows: manufacturer "KORG, Inc." and port "Valvetronix X". Linux/ALSA (Chrome WebMIDI): manufacturer "Vox Amplification Ltd." and name containing "valvetronix x" | K/webmidi.kt:180-184 |
| VID/PID | Not in any source here. **UNVERIFIED** | `.../research-pi/vt100x.md` §8.1 |

Other transport notes:

- Only one client can own the amp at a time (README of librarian; VPC/README.md:123-125).
- On the Pi, ALSA rawmidi delivers a byte stream. Reassemble SysEx from `F0` to `F7`. Drop bytes F8-FF if any are
  interleaved (general MIDI rule, not stated in these sources).

TypeScript constants:

```ts
export const SYSEX_START = 0xF0, SYSEX_END = 0xF7;
export const KORG = 0x42;
export const HEADER = [0xF0, 0x42, 0x30, 0x00, 0x01, 0x34] as const; // MI:L143
export const PAYLOAD_PREFIX = [0x30, 0x00, 0x01, 0x34] as const;    // payload = bytes after F0 42, before F7
export const DEVICE_INQUIRY = [0xF0, 0x7E, 0x7F, 0x06, 0x01, 0xF7] as const;
export const FN = {
  // host -> amp requests
  MODE_REQUEST: 0x12, CURRENT_PROGRAM_DUMP_REQUEST: 0x10, PROGRAM_DUMP_REQUEST: 0x1C,
  CUSTOM_DUMP_REQUEST: 0x31, PROGRAM_WRITE_REQUEST: 0x11,
  // data (some both directions)
  MODE_DATA: 0x42, CURRENT_PROGRAM_DUMP: 0x40, PROGRAM_DUMP: 0x4C, CUSTOM_DUMP: 0x65,
  MODE_CHANGE: 0x4E, PARAMETER_CHANGE: 0x41,
  // status
  DATA_FORMAT_ERROR: 0x26, ACK: 0x23, NAK: 0x24, WRITE_COMPLETED: 0x21, WRITE_ERROR: 0x22,
} as const; // MI:L100-127 (p.2-3)
export const PROGRAM_STRUCT_LEN = 62;  // 0x3E. MI:L359, F#L16
export const PROGRAM_PACKED_LEN = 71;  // MI:L359
export const USER_SLOTS = 8;           // OM:L680
```

---

## 2. Value encodings

### 2.1 Live PARAMETER CHANGE values: 14-bit, LSB first [MI]

The format is `41 ID SUB vLSB vMSB`:

- vLSB holds bits 6-0.
- vMSB holds bits 13-7 (MI:L286-289, p.6).
- Single-byte parameters use vMSB = `00`.

```ts
export const enc14 = (v: number) => { if (!Number.isInteger(v) || v < 0 || v > 0x3FFF) throw RangeError(); return [v & 0x7F, (v >> 7) & 0x7F]; };
export const dec14 = (lo: number, hi: number) => (lo & 0x7F) | ((hi & 0x7F) << 7);
```

- The librarian doc calls this a "quirk" that "jumps by 129 after 127" (D#L94-131).
- Its formula `v = p - floor(p/0x100)*0x80`, where `p = b0 + 256*b1`, is algebraically `b0 + 128*b1`. That is exactly
  dec14.
- K/protocol/TwoByteDial.kt:22-35 implements the same thing.
- There is no quirk.

### 2.2 Program dumps: 62-byte struct, KORG 7-to-8 packing [MI]

- MI NOTE 1 (MI:L358-360, p.7): "62 bytes = 7*8+6 -> 8*8+(1+6) => 71 bytes".
- NOTE 2 (MI:L362-363): the same format applies to program data in internal memory.
- NOTE 4 (MI:L369-383): each set of 7 data bytes becomes 8 MIDI bytes. The **first** MIDI byte of the set carries the
  b7 bits.
  - Its bit j (j = 0..6) is b7 of data byte 7n+j.
  - It is followed by the 7 data bytes with b7 cleared.
- The last set holds 6 data bytes: 1 MSB byte + 6 bytes.

Within the 62-byte struct, multi-byte values are **little-endian 16-bit, plain binary**:

- MI "*4 : Byte Order = little endian" (MI:L805, p.15).
- Ranges are given as plain hex. For example, chorus speed is `64~2710` = 100..10000 mHz (MI:L572).
- F#L49 and F#L56 agree: "encoded without any quirks".

```ts
export function pack7(data: Uint8Array): Uint8Array {          // 62 -> 71
  const out: number[] = [];
  for (let i = 0; i < data.length; i += 7) {
    const g = data.subarray(i, i + 7); let msb = 0;
    g.forEach((b, j) => { if (b & 0x80) msb |= 1 << j; });
    out.push(msb, ...Array.from(g, b => b & 0x7F));
  }
  return Uint8Array.from(out);
}
export function unpack7(p: Uint8Array): Uint8Array {           // 71 -> 62
  const out: number[] = [];
  for (let i = 0; i < p.length; i += 8) {
    const msb = p[i], n = Math.min(7, p.length - i - 1);
    if (msb & 0x80 || (msb >> n) !== 0) throw Error(`bad MSB byte ${msb.toString(16)} at ${i}`);
    for (let j = 0; j < n; j++) { const b = p[i + 1 + j]; if (b & 0x80) throw Error('8-bit byte in dump'); out.push(b | (((msb >> j) & 1) << 7)); }
  }
  return Uint8Array.from(out);
}
```

#### Index arithmetic

- Struct offset s maps to packed index k = 8·⌊s/7⌋ + 1 + (s mod 7).
- The MSB byte for s is at packed index 8·⌊s/7⌋, bit (s mod 7).
- The librarian doc numbers its "program" from packed index 1. It treats packed index 0 as part of its header strings:
  - the trailing `00` in `4C 00 XX 00` (D#L371, L407);
  - the `00` in `40 00` (D#L383).
- So the **librarian-doc offset = k − 1**. This off-by-one is the main trap when reading D or K.

#### Worked example (VEC output, verified)

| Case | Struct bytes | Packed |
|---|---|---|
| Pedal-1 param0 = 128 mHz | 0x20/0x21 = `80 00` | Struct 0x20 is set 4, j=4. MSB byte at packed 32 (doc offset 0x1F) = `10`. Packed 37/38 (doc 0x24/0x25) = `00 00` |
| Pedal-2 param0 = 254 ms | 0x28/0x29 = `FE 00` | Struct 0x28 is set 5, j=5. MSB byte at packed 40 (doc 0x27) = `20`. Packed 46/47 (doc 0x2D/0x2E) = `7E 00` |
| Chorus 800 mHz | 0x20/0x21 = `20 03` | Packed `20 03`, MSB bit clear. Plain LE16 = 800. The librarian's TwoByteDial decode gives 0x20 + 3·128 = **416**, which is wrong |

How this matches each source:

- **Row 1** matches D#L164 "1F … (00, sometimes 10, 8th byte)": the librarian author saw `10` there in a real capture
  [HW-D]. K/protocol/Program.kt:143 also accepts `00`/`10`.
- **Row 2** is exactly D#L196-212's "byte at 27 == 20 → add 0x80". VEC decodes all four D#L207-212 rows through generic
  unpack7 to 256, 4156, 2196 and 254, matching the doc.
- **Row 3** is what VPC saw on real hardware: "Chorus Speed reads back wrong via Request Current Program; raw wire bytes
  were the plain semantic value" [HW-VPC] (VPC/lib/protocol.js:458-472, VPC/README.md:231-240).
- Pedal-2 param0 "round-tripped correctly" in VPC because VPC and K both implement pedal-2 param0 as packing-equivalent
  (K/protocol/Program.kt:299-321).

Round trip `pack7(unpack7(x)) == x` holds for both test frames in §11 (VEC).

---

## 3. Enumerations [MI, matching K]

| Enum | Values | Source |
|---|---|---|
| Mode (in `42`, `4E`) | `00` User, `01` Preset, `02` Manual | MI:L207, L270 |
| Dump bank m (in `1C`, `4C`) | `00` User (R,T), `01` Preset (T only) | MI:L167, L236 |
| User program | `00`-`07` = A1 A2 A3 A4 B1 B2 B3 B4 (bank A green, bank B red) | MI:L195 (`0000 0ppp`), D#L29-38, K/protocol/ProgramSlot.kt:8-17 |
| Preset program | `00pp pppp`. The amp has 33 presets, 60 with editor software. The number→name mapping is unknown | MI:L168; OM:L678-679; D#L424-425 |
| Amp model (struct 0x12, `41 03 00`) | 00 DELUXE CL VIBRATO, 01 DELUXE CL NORMAL, 02 TWEED 4x10 BRIGHT, 03 TWEED 4x10 NORMAL, 04 BOUTIQUE CL, 05 BOUTIQUE OD, 06 VOX AC30, 07 VOX AC30TB, 08 BRIT 1959 TREBLE, 09 BRIT 1959 NORMAL, 0A BRIT 800, 0B BRIT VM, 0C SL-OD, 0D DOUBLE REC, 0E CALI ELATION, 0F ERUPT III CH2, 10 ERUPT III CH3, 11 BOUTIQUE METAL, 12 BRIT OR MKII, 13 ORIGINAL CL | MI:L502-529 (p.10); K/protocol/AmpModel.kt:8-28 |
| Label of amp param 5 | "TONE" for 06 and 07, "PRESENCE" for the rest | MI:L509-528 |
| BRIGHT CAP available | All models except 01, 03, 09, 0F, 11 | MI:L509-528 |
| Pedal 1 type (0x1F) | 00 COMP, 01 CHORUS, 02 TUBE OD, 03 GOLD DRIVE, 04 TREBLE BOOST, 05 RC TURBO, 06 ORANGE DIST, 07 FAT DIST, 08 BRIT LEAD, 09 FUZZ | MI:L545-627; K/protocol/PedalType.kt:37-48 |
| Pedal 2 type (0x27) | 00 FLANGER, 01 BLACK PHASER, 02 ORANGE PHASER 1, 03 ORANGE PHASER 2, 04 TREMOLO, 05 TAPE ECHO, 06 ANALOG DELAY | MI:L664-738; K/protocol/PedalType.kt:62-70 |
| Reverb type (0x37) | 00 ROOM, 01 SPRING, 02 HALL, 03 PLATE | MI:L775-782 |
| Bias shift (0x1D) | 00 Off, 01 COLD, 02 HOT | MI:L453 |
| Class (0x1E) | 00 A, 01 A/B | MI:L455 |
| COMP VOICE | 00, 01, 02 (UI "1/2/3") | MI:L557; K/appmodel/slot1pedals.kt:66-70 |
| Effect slot SUB (in `41 02`, `41 03`) | 01 PEDAL1, 02 PEDAL2, 04 REVERB. SUB = bit number in struct 0x11 | MI:L416-423, L459-488 |
| Effect param ID (in `41 ID n`) | 05 PEDAL1, 06 PEDAL2, 08 REVERB | MI:L463-496 |

The doc says amp 06 VOX AC30 has no panel position (D#L53). The VT100X manual lists it on the knob. This does not
matter for the protocol (`.../research-pi/vt100x.md` §8.4).

---

## 4. Message catalogue

- Payloads are shown in full frames.
- `H→A` = host to amp; `A→H` = amp to host.
- "Len" = full frame length including `F0 … F7`.

| # | Name | Request (H→A) | Expected response(s) | Source |
|---|---|---|---|---|
| 1 | Device inquiry | `F0 7E 7F 06 01 F7` | `F0 7E 00 06 02 42 34 01 00 00 vv 00 vv 00 F7` (15 B) | MI:L11-33, L61-75 |
| 2 | MODE REQUEST | `F0 42 30 00 01 34 12 F7` (8 B) | `F0 42 30 00 01 34 42 mm pp F7` (10 B). In Manual mode pp = `00` (D#L363; K/protocol/message/CurrentModeResponse.kt:30-33) | MI:L139-147, L201-211 |
| 3 | CURRENT PROGRAM DATA DUMP REQUEST | `F0 42 30 00 01 34 10 F7` (8 B) | `F0 42 30 00 01 34 40 <71 packed> F7` (**79 B**), or NAK `…24 F7` | MI:L150-158, L214-227 |
| 4 | PROGRAM DATA DUMP REQUEST | `F0 42 30 00 01 34 1C 0m pp F7` (10 B). User: m=00, pp 00-07 | `F0 42 30 00 01 34 4C 0m pp <71 packed> F7` (**81 B**), or NAK | MI:L161-172, L230-244; D#L365-375 |
| 5 | CUSTOM AMP/FX DATA DUMP REQUEST | `F0 42 30 00 01 34 31 00 pp F7`. pp = custom data no. 00-03 | `F0 42 30 00 01 34 65 00 pp <71 packed> F7` (81 B), or NAK. Struct = MI TABLE 2 (§5.3) | MI:L175-185, L247-261, L809-938; D#L389-403 |
| 6 | PROGRAM WRITE REQUEST (store Current Buffer into a user slot) | `F0 42 30 00 01 34 11 00 0p F7` (10 B) | `F0 42 30 00 01 34 21 00 0p F7` WRITE COMPLETED, or `…22 00 0p F7` WRITE ERROR | MI:L188-198, L330-354. Used by no known implementation: **UNVERIFIED** |
| 7 | CURRENT PROGRAM DATA DUMP (load into Current Buffer = live, not saved) | `F0 42 30 00 01 34 40 <71 packed> F7` (79 B) | ACK `…23 F7`, or NAK `…24 F7` | MI:L214-227 ("saves them to Current Buffer"). Receive direction used by no known implementation: **UNVERIFIED** |
| 8 | PROGRAM DATA DUMP (write into internal memory) | `F0 42 30 00 01 34 4C 00 pp <71 packed> F7` (81 B) | ACK or NAK | MI:L230-244 ("saves them to Internal Memory"); K/protocol/message/WriteUserProgramMessage.kt:11-17 [HW-D]; VPC/lib/protocol.js:436-439 [HW-VPC] |
| 9 | MODE CHANGE | `F0 42 30 00 01 34 4E mm pp F7` (10 B) | ACK or NAK. The amp also sends this unsolicited when the mode or program changes on the panel (§7) | MI:L264-276; K/protocol/message/ProgramSlotChangedMessage.kt:11-22 (mm=00) |
| 10 | PARAMETER CHANGE | `F0 42 30 00 01 34 41 ID SUB lo hi F7` (12 B) | ACK or NAK. For a pedal-type change, see §6.2. The amp also sends `41` unsolicited when a knob or switch moves | MI:L279-294 |
| 11 | DATA FORMAT ERROR | n/a | `F0 42 30 00 01 34 26 F7`: "error in the MIDI IN message" | MI:L297-305; K/protocol/message/ErrorMessage.kt:11,25 |
| 12 | ACK | n/a | `F0 42 30 00 01 34 23 F7`: "DATA LOAD, PROCESSING have been completed" | MI:L308-316 |
| 13 | NAK | n/a | `F0 42 30 00 01 34 24 F7`: "have not been completed". K names it InvalidValue (out of range) | MI:L319-327; K/protocol/message/ErrorMessage.kt:13-14,26 |
| 14 | WRITE COMPLETED | n/a | `F0 42 30 00 01 34 21 00 0p F7`. Reply to #6, and also unsolicited when the user stores a program with the panel switches ("W") | MI:L125, L136, L330-341 |
| 15 | WRITE ERROR | n/a | `F0 42 30 00 01 34 22 00 0p F7` | MI:L344-354 |

Notes:

- **The byte after `4C`, and the byte after `1C`, is m (bank), not a constant.**
  - For user slots it is `00`, so the librarian's constant strings `1C 00 XX` and `4C 00 XX 00` are byte-correct.
  - Their trailing `00` is the first packing MSB byte (§2.2).
- `1C 01 pp` (read a preset program) is official (MI:L167) but has not been tried by anyone. **UNVERIFIED**.
- `4E 01 pp` (select a preset) and `4E 02 00` (switch to manual) sent **by the host** are official (MI:L270-275) but
  untested. **UNVERIFIED**.
- Response routing key = the function byte at payload index 4 (frame index 6), plus mm/pp where present. Never route by
  position (§8).

### 4.1 Request/response map (for the router)

| Sent func | Completes on | Fails on |
|---|---|---|
| `12` | `42` | `26`, timeout |
| `10` | `40` | `24`, `26`, timeout |
| `1C m p` | `4C m p` (m and p must match) | `24`, `26`, timeout |
| `31 00 p` | `65 00 p` | `24`, `26`, timeout |
| `11 00 p` | `21 00 p` | `22 00 p`, `26`, timeout |
| `40 …`, `4C …`, `4E …`, `41 …` | `23` | `24`, `26`, timeout |

Unsolicited messages (`41`, `4E`, `21`) can arrive at any time, including while a request is pending. They never
complete a pending request; they go to the notification handler (§7). VPC saw this on hardware: "the amp can
spontaneously emit unrelated status/echo messages" before the ACK (VPC/lib/midi.js:77-104; VPC/README.md:66-70)
[HW-VPC].

---

## 5. Program layout (62-byte struct = VTXPROG record = MI TABLE 1)

Columns:

- **S** = struct / VTXPROG offset (MI "No.").
- **k** = packed index in the 71-byte block (0 = first MSB byte). Frame index = k + 9 for `4C`/`1C` frames and k + 7
  for `40` frames.
- **Dk** = librarian-doc / K offset (= k − 1).
- **ID** = PARAMETER CHANGE ID,SUB (MI:L387-497).

### 5.1 Fixed part

| S | k | Dk | Size | Field | Encoding / range | ID,SUB | Source |
|---|---|---|---|---|---|---|---|
| — | 0 | (hdr `00`) | 1 | MSB byte, set 0 | bits for S 00-06 (always 0, ASCII) | — | MI:L369-383 |
| 00-06 | 1-7 | 00-06 | 7 | Name chars 1-7 | ASCII 20-7E, space padded | 00,00-00,06 | MI:L400-405 |
| — | 8 | 07 | 1 | MSB byte, set 1 | — | — | D#L140 |
| 07-0D | 9-15 | 08-0E | 7 | Name chars 8-14 | ASCII 20-7E | 00,07-00,0D | |
| — | 16 | 0F | 1 | MSB byte, set 2 | — | — | D#L142 |
| 0E-0F | 17-18 | 10-11 | 2 | Name chars 15-16 | ASCII 20-7E | 00,0E-00,0F | |
| 10 | 19 | 12 | 1 | NR SENS | 00-64 (0.0-10.0) | 01,00 | MI:L408 |
| 11 | 20 | 13 | 1 | EFFECT STATUS | b1 PEDAL1, b2 PEDAL2, b4 REVERB. b0, b3, b5-7 reserved: preserve them | 02,01 / 02,02 / 02,04 | MI:L412-425 |
| 12 | 21 | 14 | 1 | AMP model | 00-13 | 03,00 | MI:L428 |
| 13 | 22 | 15 | 1 | GAIN | 00-64 | 04,00 | MI:L432 |
| 14 | 23 | 16 | 1 | TREBLE | 00-64 | 04,01 | MI:L434 |
| — | 24 | 17 | 1 | MSB byte, set 3 | — | — | D#L156 |
| 15 | 25 | 18 | 1 | MIDDLE | 00-64 | 04,02 | MI:L436 |
| 16 | 26 | 19 | 1 | BASS | 00-64 | 04,03 | MI:L438 |
| 17 | 27 | 1A | 1 | VOLUME (program volume, not Power Level) | 00-64 | 04,04 | MI:L440; D#L19 |
| 18 | 28 | 1B | 1 | PRESENCE/TONE | 00-64 | 04,05 | MI:L442 |
| 19 | 29 | 1C | 1 | RESONANCE | 00-64 | 04,06 | MI:L444 |
| 1A | 30 | 1D | 1 | BRIGHT CAP | 00/01 | 04,07 | MI:L446 |
| 1B | 31 | 1E | 1 | LOW CUT | 00/01 | 04,08 | MI:L449 |
| — | 32 | 1F | 1 | MSB byte, set 4 | bit4 (`10`) = b7 of S 0x20 | — | D#L164 |
| 1C | 33 | 20 | 1 | MID BOOST | 00/01 | 04,09 | MI:L451 |
| 1D | 34 | 21 | 1 | BIAS SHIFT | 00-02 | 04,0A | MI:L453 |
| 1E | 35 | 22 | 1 | CLASS | 00-01 | 04,0B | MI:L455 |
| 1F | 36 | 23 | 1 | PEDAL1 type | 00-09 | 03,01 | MI:L459 |
| 20-21 | 37-38 | 24-25 | 2 | PEDAL1 param 0 | **LE16 plain** (see 5.2) | 05,00 | MI:L463-468, L805 |
| 22 | 39 | 26 | 1 | PEDAL1 param 1 | see 5.2 | 05,01 | |
| — | 40 | 27 | 1 | MSB byte, set 5 | bit5 (`20`) = b7 of S 0x28 | — | D#L171, L194-212 |
| 23-26 | 41-44 | 28-2B | 4 | PEDAL1 params 2-5 | see 5.2 | 05,02-05,05 | |
| 27 | 45 | 2C | 1 | PEDAL2 type | 00-06 | 03,02 | MI:L471 |
| 28-29 | 46-47 | 2D-2E | 2 | PEDAL2 param 0 | **LE16 plain** | 06,00 | MI:L475-480 |
| — | 48 | 2F | 1 | MSB byte, set 6 | — | — | D#L178 |
| 2A-2E | 49-53 | 30-34 | 5 | PEDAL2 params 1-5 | see 5.2 | 06,01-06,05 | |
| 2F-30 | 54-55 | 35-36 | 2 | reserved | preserve; write 00 | — | MI:L483 |
| — | 56 | 37 | 1 | MSB byte, set 7 | — | — | |
| 31-36 | 57-62 | 38-3D | 6 | reserved | preserve; write 00 | — | MI:L483 |
| 37 | 63 | 3E | 1 | REVERB type | 00-03 | 03,04 | MI:L487 |
| — | 64 | 3F | 1 | MSB byte, set 8 (last set: 6 data bytes, so bit 6 must be 0) | — | — | D#L186 |
| 38-3C | 65-69 | 40-44 | 5 | REVERB params 0-4 | see 5.2 | 08,00-08,04 | MI:L491-496 |
| 3D | 70 | 45 | 1 | reverb area, unused by any type | preserve; write 00 | (08,05 not defined) | MI:L491-496, L788-796 |

- D#L184 says the reserved run at 0x35 is "Length A". It is really 9 packed bytes, 0x35-0x3D (7 data + 1 MSB + 1
  data). K skips exactly 9 (K/protocol/Program.kt:101-105, L163).
- The struct view of the same region is 8 reserved bytes at S 2F-36 (MI:L483; F#L62).

### 5.2 Effect parameters by type (param index n = live SUB = struct position)

Mapping from param index n to struct offset:

- PEDAL1: n=0 → S 0x20-0x21 (LE16); n=1..5 → S 0x21+n.
- PEDAL2: n=0 → S 0x28-0x29 (LE16); n=1..5 → S 0x29+n.
- REVERB: n=0..4 → S 0x38+n, single bytes.

Units:

- "0-100" means 00-64, shown as 0.0-10.0.
- Hz values are in mHz; times are in ms.

Defaults are librarian UI defaults (not amp defaults).

| Slot (ID) | Type | n=0 | n=1 | n=2 | n=3 | n=4 | n=5 | Source |
|---|---|---|---|---|---|---|---|---|
| P1 (05) | 00 COMP | SENS 0-100 | LEVEL 0-100 | ATTACK 0-100 | VOICE 0-2 | (unused) | (unused) | MI:L545-558 |
| P1 (05) | 01 CHORUS | SPEED 100-10000 mHz (`64~2710`) | DEPTH 0-100 | MANUAL 0-100 | MIX 0-100 | LOW CUT 0/1 | HIGH CUT 0/1 | MI:L566-583 |
| P1 (05) | 02-05 OVERDRIVE, 06-09 DISTORTION | DRIVE 0-100 | TONE 0-100 | LEVEL 0-100 | TREBLE 0-100 | MIDDLE 0-100 | BASS 0-100 | MI:L590-644 |
| P2 (06) | 00 FLANGER | SPEED 100-5000 mHz (`64~1388`) | DEPTH | MANUAL | LOW CUT 0/1 | HIGH CUT 0/1 | RESONANCE | MI:L664-682 |
| P2 (06) | 01-03 PHASER | SPEED 100-10000 mHz | RESONANCE | **MANUAL** | **DEPTH** | (unused) | (unused) | MI:L689-706 (alt:618-625) |
| P2 (06) | 04 TREMOLO | SPEED 1650-10000 mHz (`672~2710`) | DEPTH | DUTY | SHAPE | LEVEL | (unused) | MI:L713-729 |
| P2 (06) | 05-06 DELAY | TIME 30-1200 ms (`1E~4B0`) | LEVEL | FEEDBACK | TONE | MOD SPEED | **MOD DEPTH** | MI:L736-755 |
| REV (08) | 00-03 all | MIX 0-100 | TIME 0-100 | PRE DELAY **0-70 ms (00-46)** | LOW DAMP 0-100 | HIGH DAMP 0-100 | n/a | MI:L775-796 |

Details:

- Param 0 of every pedal type is stored LE16 in the struct, including COMP SENS and DRIVE. The MI rows marked "00~01"
  with (*4) are at MI:L551, L602, L633.
- For 0-100 values the high byte is 00.
- Librarian UI defaults: K/appmodel/slot1pedals.kt:44-156, K/appmodel/slot2pedals.kt:42-196,
  K/appmodel/reverb_pedals.kt:26-52, K/appmodel/amps.kt:32-103.
- Unused parameter bytes: write 00, and keep whatever the amp returned when you round-trip.
  - The amp reports 0 for them after a type change (K/appmodel/SimulationConfiguration.kt:67-72) [HW-D].

### 5.3 CUSTOM AMP/FX record (func 31/65), official only, **UNVERIFIED**

This is the same 62-byte packed format (MI:L365-367). TABLE 2 (MI:L809-938, p.15-17):

| Offset | Content |
|---|---|
| 00-0F | reserved |
| 10 | NR SENS |
| 11 | Data-enable bits: b0 AMP+NR, b1 PEDAL1, b2 PEDAL2, b4 REVERB |
| 12 | Amp model for the custom amp positions (CDN 0/1/2 = USER A/B/C, which are positions on the amp knob, OM:L514-520) |
| 13-17 | reserved |
| 18-1E | Amp params (PRESENCE … CLASS) |
| 1F | PEDAL1 type, with CDN 0 COMP / 1 CHORUS / 2 OVERDRIVE (02-05) / 3 DISTORTION (06-09) |
| 20-26 | PEDAL1 params |
| 27 | PEDAL2 type, with CDN 0 FLANGER / 1 PHASER (01-03) / 2 TREMOLO / 3 DELAY (05-06) |
| 28-2E | PEDAL2 params |
| 2F-36 | reserved |
| 37 | Reverb type, with CDN 0-3 = ROOM / SPRING / HALL / PLATE |
| 38-3D | Reverb params |

- **Interpretation [INFER]**: record CDN p holds what each effect-knob position p (and USER A/B/C for amp) currently
  maps to.
- This is not needed for an MVP. Read-only support is safe; writing back via `65` is risky until tested.

---

## 6. Live-change messages (`41 ID SUB lo hi`) [MI]

### 6.1 Full ID table

| Target | Frame | Value | Struct effect |
|---|---|---|---|
| Name char i (i=0..15) | `F0 42 30 00 01 34 41 00 0i cc 00 F7` | cc = ASCII 20-7E | S i. Official (MI:L400-405); no implementation uses it. **UNVERIFIED** |
| NR SENS | `… 41 01 00 vv 00 F7` | 00-64 | S 10 (K/protocol/message/NoiseReductionSensitivityChangedMessage.kt:13-29) |
| Effect on/off | `… 41 02 ss vv 00 F7` | ss 01/02/04; vv 00/01 | S 11 bit ss (K/protocol/message/PedalActiveStateChangedMessage.kt:10-29) |
| Amp model | `… 41 03 00 mm 00 F7` | 00-13 | S 12 (K/protocol/message/SimulatedAmpModelChangedMessage.kt:13-29) |
| Pedal/reverb type | `… 41 03 ss tt 00 F7` | ss 01/02/04 | S 1F / 27 / 37 (K/protocol/message/EffectPedalTypeChangedMessage.kt:17-58) |
| Amp param | `… 41 04 nn vv 00 F7` | nn 00-0B. Ranges per §5.1 (switches 0/1, bias 0-2) | S 13+nn (K/protocol/message/AmpDialTurnedMessage.kt:12-28; D#L221-241) |
| Pedal1 param | `… 41 05 nn lo hi F7` | n=0 is 14-bit; others have hi=00 | §5.2 |
| Pedal2 param | `… 41 06 nn lo hi F7` | same | §5.2 |
| Reverb param | `… 41 08 nn vv 00 F7` | nn 00-04 | §5.2 |

### 6.2 Behaviour

- Each host-sent `41` gets ACK `23` or NAK `24` (MI:L292-293).

**Pedal/reverb type change from the host** [K, HW-D]:

- The code expects ACK first, then exactly nDials `41 <05|06|08> nn lo hi` messages with the new type's default values
  (K/protocol/message/EffectPedalTypeChangedMessage.kt:9-15). nDials is 6/6/5 (K/protocol/PedalType.kt:15-17).
- Unused dials arrive with value 0 (K/appmodel/SimulationConfiguration.kt:67-72).
- Implementation: do not "expect" these. Treat every A→H `41` as a state update (§7). Complete the request on `23`
  alone.
- **Burst race rule.** After the ACK of a `41 03 <01|02|04> tt 00`, hold the send queue until nDials (6/6/5)
  `41 <05|06|08>` messages have arrived, or about 200 ms have passed, whichever comes first. The 200 ms is an estimate,
  **UNVERIFIED**.
  - Otherwise the amp's default-value burst can interleave with, and overwrite, the parameter values the host sends
    right after the type change.
  - VPC saw such interleaving on hardware (VPC/lib/midi.js:77-84). K avoids it by consuming nDials messages inside the
    exchange.
  - Apply host-set values to the model when their ACK arrives, not when they are sent.

**Amp model change on the panel knob** [HW-D]:

- The amp sends `41 03 00 mm 00`, then amp-param messages for all IDs 04,00-04,0B (D#L311-314).
- In Manual mode it sends the model message, then NR (D#L434-437).
- Whether the same burst follows a **host-sent** model change is **UNVERIFIED**. K does not wait for one
  (K/protocol/message/SimulatedAmpModelChangedMessage.kt:12, CommandWithoutResponse).

**Other observations**:

- The amp does not send effect-knob changes when that effect is not selected on the panel (D#L20) [HW-D].
- **Invalid IDs and indices are rejected** [HW-VPC]. VPC sent phaser DEPTH as `41 06 04` and delay MOD DEPTH as
  `41 06 06` (indices copied from D#L278-290). The amp "never ACKs" them (VPC/README.md:242-249;
  VPC/gui/server.js:268-282).
  - Phaser MANUAL sent to `06 03` "read back a different value" (VPC/README.md:251-259).
  - That is exactly what the MI indices predict: 04 and 06 do not exist for these types, and 03 is DEPTH.
  - Use MI indices. Expect NAK `24` (or `26`) for non-existent IDs. VPC's waiter ignored non-ACK replies
    (VPC/lib/midi.js:92-100), so it only saw timeouts.

**Rate**:

- No documented limit. Keep one message in flight and wait for `23`/`24` before the next.
- Coalesce slider drags per (ID,SUB): last value wins. K coalesces queued states (K/appmodel/hardware_integration/VoxVtxAmpConnection.kt:65-71).

**Cost of a full "audition"**:

- About 25+ single messages (VPC/lib/protocol.js:596-645), or one `40 <71>` (§4 #7, **UNVERIFIED**).

**Stale readback**:

- After live tube-bias/class changes, `10` may report the value from before the last change. Saving with the amp
  buttons fixes it (D#L386-387) [HW-D].
- Keep the UI model from your own sent values plus notifications. Do not "correct" it from `10` right after a change.

---

## 7. Unsolicited amp→host messages (notification handling)

| Message | Meaning | Recommended handling | Source |
|---|---|---|---|
| `41 ID SUB lo hi` | A panel knob or switch changed one parameter. It also arrives as bursts after model or type changes | Apply to the Current Buffer model (§6.1 map). Push to UI. Never treat it as a reply | MI:L294, L134-135; D#L214-219 |
| `4E 00 pp` | User program pp selected on the panel; also sent when leaving Manual mode | Set mode=User, slot=pp. Then request `10` (Current Buffer changed). Slots unchanged | MI:L276; D#L298-303 |
| `4E 01 pp` | Preset mode / preset pp selected | Set mode=Preset, preset=pp. Request `10`. K has no parser for this (it throws) | MI:L270; D#L420-425; PC:L109 |
| `4E 02 00` | Manual mode | Set mode=Manual. Request `10`; ToneRoom does this and gets a program named `MANUAL` + 10 spaces (D#L431-432). K has no parser | D#L427-432 |
| `21 00 0p` | User stored a program to slot p with the panel WRITE operation | Re-read slot p with `1C 00 0p`. K has no parser | MI:L125, L136, L330-341 |
| `23`/`24`/`26` with no request pending | Late reply after a timeout | Log and drop | [INFER] |
| Anything else (or a non-KORG manufacturer) | Unknown | Log and drop. Never throw. K throws on unknown messages (K/protocol/VoxVtxAmplifierClient.kt:83-102) and only logs a manufacturer mismatch, without returning (L40-44) | |

K's handling of `4E 00`: it refetches the full state on ProgramSlotChanged
(K/appmodel/hardware_integration/protocol_to_ui.kt:18-27; K/appmodel/hardware_integration/VoxVtxAmpConnection.kt:30-35).

---

## 8. Procedures

### 8.1 Connect / state read

1. Optional: send the device inquiry. Check `42 34 01`. Log the version. Timeout 1000 ms; ignore failure.
   - The amp is required to answer per MI:L33. Its response on a VT100X is **UNVERIFIED**.
2. `12` → `42 mm pp`.
3. `10` → `40 <71>` → unpack7 → struct = Current Buffer.
4. `1C 00 00` … `1C 00 07` → 8 × `4C 00 pp <71>`. Strictly one at a time.

Sequence order:

- This order is K's (K/appmodel/hardware_integration/VoxVtxAmpConnection.kt:110-125).
- ToneRoom reads the slots first, then `10` (D#L379). Either order works.
- Keep raw 62-byte structs so reserved bits round-trip.
- K drops them (K/protocol/Program.kt:50-60, L101-105). That is lossy; do not copy it.

### 8.2 Select a program

- Send `4E 00 pp`, wait for `23`, then request `10` to refresh.
- K sends the same select and assumes the Current Buffer = the stored slot (K/appmodel/hardware_integration/VoxVtxAmpConnection.kt:36-45).
- Whether the amp echoes `4E` back is not stated in MI (MI:L276 says only switch-initiated changes are transmitted).
  Handle an echo idempotently.

### 8.3 Save to user slot (recommended = proven path)

1. Build the 62-byte struct (name padded with spaces to 16, ASCII 20-7E). Validate every field against §5.
2. `pack7` → 71 bytes. Send `F0 42 30 00 01 34 4C 00 pp <71> F7`. Wait for `23`; a 2000 ms timeout is recommended.
   - MI: received 4C data is saved "to Internal Memory" (MI:L242-243).
3. Optional, but this is what ToneRoom, K and VPC do: send `4E 00 pp` and wait for `23`.
   - This is a MODE CHANGE: it makes slot pp the active program and reloads the Current Buffer from it [INFER from
     MI:L224-225 vs L242-243 and L274].
   - It is **not** a "persist" command. K's "TODO! unclear what this message does" (K/protocol/message/PersistUserProgramMessage.kt:6-10)
     and D#L411-412 are resolved by MI.
   - Skip it if the user must stay on the current program.
   - **Mandatory when pp is the active slot.** MI separates Internal Memory from the Current Buffer (MI:L224-225 vs
     L242-243), and nothing says `4C` refreshes the live buffer.
4. Verify: `1C 00 pp`, then compare the unpacked struct byte-for-byte with what you sent.

Hardware evidence:

- 4C + 4E survives power cycles on a VT40X [HW-VPC] (VPC/README.md:224-226; VPC/apply-preset.js:101-116 sends 4C then 4E).
- Whether 4C **alone** survives a power cycle on a VT100X is **UNVERIFIED**.
- Do not auto-retry `4C`. On a timeout, do step 4 instead.

Alternative official path (**UNVERIFIED**, no known user):

1. `40 <71>` → `23` (load into Current Buffer).
2. `11 00 0p` → `21 00 0p` / `22 00 0p` (MI:L188-198).

K's "edit a non-active slot" path sends `4E 00 pp` **before** `4C`, without waiting in between
(K/appmodel/hardware_integration/ui_to_protocol.kt:158-165; K/protocol/VoxVtxAmplifierClient.kt:129-141). Prefer the
order above.

### 8.4 Audition a whole program without saving

- Proven: the sequence of single `41` messages (VPC/lib/protocol.js:596-645) [HW-VPC].
- Official, **UNVERIFIED**: `40 <71>` → `23`.

### 8.5 After a NAK/format error

- Mark the request failed.
- K re-reads the full state after any error (K/appmodel/hardware_integration/VoxVtxAmpConnection.kt:99-102). For us,
  re-read `10` only, after a live-edit NAK.

---

## 9. Timeouts, retries, concurrency

### 9.1 Concurrency

- Exactly one request in flight. FIFO queue. Live-edit messages are coalesced per (ID,SUB) inside the queue.
- K also serialises exchanges (K/protocol/VoxVtxAmplifierClient.kt:108-110, L173-191).
- K's 1 s timeout cannot interrupt (`withTimeout { suspendCoroutine {} }`, K/protocol/VoxVtxAmplifierClient.kt:53-58;
  `cancel()` has no caller). Implement a real timer that frees the queue.

### 9.2 Timeouts

| Message | Timeout | Basis |
|---|---|---|
| `12`, `10`, `1C`, `31` | 1500 ms | VPC default 1500 ms (VPC/lib/midi.js:59, L85); K 1000 ms (K/protocol/VoxVtxAmplifierClient.kt:200) |
| `41`, `4E` | 1000 ms | VPC uses 800 ms for live messages on hardware (VPC/apply-preset.js:75; VPC/gui/server.js:91) |
| `4C`, `40` | 2000 ms | **UNVERIFIED** flash write time. VPC's 1500 ms worked (VPC/apply-preset.js:115) |
| `11` | 3000 ms | **UNVERIFIED** |

### 9.3 Retries

- Reads (`12`, `10`, `1C`, `31`): one retry, then report "amp not responding" and run full resync on recovery.
- Writes (`4C`, `11`, `40`): never auto-retry. Read back to verify.
- Live `41`: no retry. A newer value supersedes it. After a NAK, resync with `10`.
- Matching: by function byte plus echoed m/p (§4.1). Discard replies whose m/p differ.

### 9.4 Disconnect

- No reply to 2 consecutive reads: consider the amp gone.
- Re-run §8.1 when the Go bridge reports the ALSA port again.

---

## 10. VTXPROG conversion (`.vtxprog`)

### 10.1 File layout

- Prelude, 0x20 bytes: `56 54 58 50 52 4F 47 31 30 30 30 20` ("VTXPROG1000 ") followed by 20 × `00` (F#L5-10).
  K requires an exact match (K/vtxprog/VtxProgFile.kt:74-87).
- Then N programs × 0x3E bytes.
- ToneRoom always writes 11 (F#L12-17), so a full file is 0x20 + 11·0x3E = **714 bytes** (VEC).
- K reads any whole N (K/vtxprog/VtxProgFile.kt:89-96) and writes as many as the group holds
  (K/reactapp/components/sidebar/SidebarComponent.kt:290-292).

### 10.2 Record layout

- **Each record is byte-identical to the 62-byte struct of §5.**
- F#L23-69 matches MI TABLE 1 offset for offset: name 00-0F, NR 10, flags 11 … reverb 38-3C, then 3D.
- F#L69 says "3e"; that is a typo for 3D. F#L62 marks 2F-36 as 8 unused bytes.
- Param 0 of each pedal is plain LE16 (F#L49, L56; K/vtxprog/VtxProgFile.kt:47, L54, L122, L129; K/BinaryInput.kt:14-18).

### 10.3 Conversions

| Direction | Steps |
|---|---|
| File → amp slot p | Take record i (bytes `0x20+0x3E·i … +0x3E`) → validate → `pack7` → `F0 42 30 00 01 34 4C 00 p <71> F7` |
| Amp → file | `unpack7(71)` → record. Write the prelude, then 8 slot records, then pad to 11 |

- Padding to 11: ToneRoom expects 11. What it does with fewer is **UNVERIFIED**.
- Pad with copies of a neutral program whose name is all spaces, not 00 bytes: MI names are 20-7E (MI:L402).

### 10.4 Import validation

- Reject a wrong prelude, or a length that is not 0x20 + n·0x3E.
- Per field, check the §5 ranges. Report the offending program and offset; never crash.
- A third-party issue says "older" ToneRoom files contain 0x20 in boolean/dial bytes ("Expected boolean (0 or 1), got 32").
  - Source: https://github.com/tmarsteel/vox-amp-librarian/issues/12, 2025-10-08, unanswered, no sample file.
  - Its text also claims "big-endian" multi-byte dials, which contradicts F#L49.
  - **UNVERIFIED**. Recommended: report a validation error and offer "import with clamping". Do not silently
    reinterpret.

### 10.5 Bugs in the reference VTXPROG path

- The CHORUS DEPTH/MANUAL/MIX bug: K stores them in S 2A-2C instead of 22-24 (K/appmodel/slot1pedals.kt:89-103).
  - This affects K's `.vtxprog` export and slot writes.
  - Files and slots written by vox-amp-librarian (or by VPC, which deliberately replicates the bug,
    VPC/lib/protocol.js:380-398) may carry chorus values in pedal-2 bytes.
  - Do not "repair" such data automatically.

---

## 11. Test vectors

### 11.1 Verbatim from sources

From the librarian doc, payloads without `F0 42`/`F7`:

```
30 00 01 34 23                 ACK                                  D#L219
30 00 01 34 12                 mode request                         D#L352
30 00 01 34 42 XX YY           mode response                        D#L354
30 00 01 34 1C 00 XX           request user program                 D#L367
30 00 01 34 4c 00 XX 00 ...    user program dump / write prefix     D#L371, D#L407
30 00 01 34 10                 request current program              D#L381
30 00 01 34 40 00 ...          current program dump prefix          D#L383
30 00 01 34 4e 00 XX           program select / changed             D#L300, D#L411
30 00 01 34 4e 01 XX           preset selected (A->H)               D#L422
30 00 01 34 4e 02 00           manual mode (A->H)                   D#L429
30 00 01 34 31 00 XX           request custom amp/fx (doc: "user amp preset")  D#L391
```

From the unit test (KT/protocol/AmpDialTurnedMessageTest.kt:7-23):

```
30 00 01 34 41 04 05 40 00   -> ID 04 SUB 05 (PRESENCE/TONE), value 0x40 = 64 (6.4)
30 00 01 34 41 04 0A 02 00   -> ID 04 SUB 0A (BIAS SHIFT), value 2 = HOT
```

POC frames (`control poc.html`, per PC:L430-433):

- set gain: `F0 42 30 00 01 34 41 04 00 <v> 00 F7`
- ACK = the 8-byte `F0 42 30 00 01 34 23 F7`

### 11.2 Live 14-bit values (VEC; first rows are D#L101-115)

| value (dec) | lo hi | value (dec) | lo hi |
|---|---|---|---|
| 120 | `78 00` | 100 (min chorus/flanger/phaser) | `64 00` |
| 127 | `7F 00` | 1650 (min tremolo) | `72 0C` |
| 128 (doc "256 → 0.128 Hz") | `00 01` | 5000 (max flanger) | `08 27` |
| 129 | `01 01` | 10000 (max chorus/phaser/tremolo) | `10 4E` |
| 132 | `04 01` | 30 / 1200 ms (delay min/max) | `1E 00` / `30 09` |

### 11.3 Derived full frames

All built from MI; checked with VEC where marked.

```
F0 7E 7F 06 01 F7                    device inquiry
F0 42 30 00 01 34 12 F7              mode request          -> F0 42 30 00 01 34 42 00 03 F7 = User A4
F0 42 30 00 01 34 10 F7              current program req   -> 79-byte 40 frame
F0 42 30 00 01 34 1C 00 07 F7        read user B4          -> 81-byte 4C 00 07 frame
F0 42 30 00 01 34 4E 00 04 F7        select B1             -> F0 42 30 00 01 34 23 F7
F0 42 30 00 01 34 11 00 02 F7        store buffer to A3    -> F0 42 30 00 01 34 21 00 02 F7 (UNVERIFIED)
F0 42 30 00 01 34 41 04 00 32 00 F7  gain 5.0
F0 42 30 00 01 34 41 04 0A 01 00 F7  bias COLD
F0 42 30 00 01 34 41 03 00 07 00 F7  amp model VOX AC30TB
F0 42 30 00 01 34 41 01 00 64 00 F7  NR 10.0
F0 42 30 00 01 34 41 02 02 01 00 F7  pedal 2 on
F0 42 30 00 01 34 41 02 04 00 00 F7  reverb off
F0 42 30 00 01 34 41 03 01 06 00 F7  pedal1 = ORANGE DIST  -> ACK, then 6 x "41 05 nn lo hi"
F0 42 30 00 01 34 41 03 04 01 00 F7  reverb = SPRING       -> ACK, then 5 x "41 08 nn lo hi"
F0 42 30 00 01 34 41 05 00 10 4E F7  chorus SPEED 10.000 Hz
F0 42 30 00 01 34 41 05 00 00 01 F7  chorus SPEED 0.128 Hz
F0 42 30 00 01 34 41 06 00 72 0C F7  tremolo SPEED 1.650 Hz
F0 42 30 00 01 34 41 06 00 30 09 F7  delay TIME 1200 ms
F0 42 30 00 01 34 41 06 02 4D 00 F7  phaser MANUAL 7.7   (MI index 02)
F0 42 30 00 01 34 41 06 03 32 00 F7  phaser DEPTH 5.0    (MI index 03)
F0 42 30 00 01 34 41 06 05 0A 00 F7  delay MOD DEPTH 1.0 (MI index 05)
F0 42 30 00 01 34 41 08 02 46 00 F7  reverb PRE DELAY 70 ms (max)
```

### 11.4 Program vector A: write to A4

Regenerated by VEC; identical to PD:L765.

Struct (62 bytes):

```
50 49 20 54 45 53 54 20 20 20 20 20 20 20 20 20 32 12 07 32 3C 28 46 50 1E 14 01 00 01 02 01 01
80 00 32 32 32 00 01 05 FE 00 32 28 32 0A 00 00 00 00 00 00 00 00 00 01 32 32 14 32 32 00
```

Meaning:

- Name "PI TEST", NR 5.0, status 0x12 (P1 on, P2 off, REV on).
- Amp: AC30TB; gain 5.0, treble 6.0, middle 4.0, bass 7.0, volume 8.0, tone 3.0, resonance 2.0; bright on, low cut off,
  mid boost on, bias HOT, class A/B.
- Pedal 1: CHORUS, speed 128 mHz, depth/manual/mix 5.0, low cut off, high cut on.
- Pedal 2: TAPE ECHO, 254 ms, level 5.0, feedback 4.0, tone 5.0, mod speed 1.0, mod depth 0.
- Reverb: SPRING, mix 5.0, time 5.0, pre-delay 20 ms, low damp 5.0, high damp 5.0.

Write frame (81 B):

```
F0 42 30 00 01 34 4C 00 03 00 50 49 20 54 45 53 54 00 20 20 20 20 20 20 20 00 20 20 32 12 07 32 3C 00 28 46 50 1E 14 01 00 10 01 02 01 01 00 00 32 20 32 32 00 01 05 7E 00 00 32 28 32 0A 00 00 00 00 00 00 00 00 00 00 01 00 32 32 14 32 32 00 F7
```

- The MSB bytes at packed indices 0, 8, 16 … 64 are `00 00 00 00 10 20 00 00 00`.
- The same 71 bytes after `F0 42 30 00 01 34 40` form the 79-byte Current Buffer frame.

### 11.5 Program vector B: "DELAY MAX"

Struct: like A, with name "DELAY MAX", chorus 10000 mHz / 6.7 / 5.7 / 1.0 / 0 / 0, delay 1200 ms / 5.0 / 7.7 / 5.0 / 0.1 / 0.

```
struct: 44 45 4C 41 59 20 4D 41 58 20 20 20 20 20 20 20 32 12 07 32 3C 28 46 50 1E 14 01 00 01 02 01 01
        10 27 43 39 0A 00 00 05 B0 04 32 4D 32 01 00 00 00 00 00 00 00 00 00 01 32 32 14 32 32 00
packed: 00 44 45 4C 41 59 20 4D 00 41 58 20 20 20 20 20 00 20 20 32 12 07 32 3C 00 28 46 50 1E 14 01 00
        00 01 02 01 01 10 27 43 20 39 0A 00 00 05 30 04 00 32 4D 32 01 00 00 00 00 00 00 00 00 00 00 01
        00 32 32 14 32 32 00
```

- Delay 1200 = 0x04B0. The struct holds `B0 04`; packed it becomes `30 04` with bit5 set in the set-5 MSB byte
  (packed 40 = `20`).
- Chorus 10000 = 0x2710 = `10 27`, no MSB bit.
- The librarian's decoder would read chorus as 0x10 + 0x27·128 = 5008: wrong.

### 11.6 Pedal-2 param0 vectors (D#L207-212 through generic unpack, VEC)

| Doc 27 | Doc 2D | Doc 2E | Decoded |
|---|---|---|---|
| `00` | `00` | `01` | 256 |
| `00` | `3C` | `10` | 4156 |
| `20` | `14` | `08` | 2196 |
| `20` | `7E` | `00` | 254 |

### 11.7 Invalid-input cases (the parser/encoder must reject them, or the amp NAKs)

| Input | Expected |
|---|---|
| Frame not starting `F0` or not ending `F7`; byte ≥ 0x80 inside | Drop (K drops, K/webmidi.kt:141-149) |
| Manufacturer ≠ `42`, or prefix ≠ `30 00 01 34` | Drop and log (K bug: it continues, K/protocol/VoxVtxAmplifierClient.kt:40-44) |
| `40` with ≠ 71 data bytes; `4C` with ≠ 3+71 | Reject (K requires EOF, K/protocol/message/CurrentProgramResponse.kt:17-18, K/protocol/message/UserProgramResponse.kt:22-24) |
| MSB byte with bits for absent bytes (last set: bit 6 set) | Reject (VEC "invalid MSB rejected") |
| MSB bit set on an ASCII or 0-100 field (e.g. set-0 MSB ≠ 00) | Structurally valid, semantically invalid → range error |
| `4C`/`1C` reply whose m/p differ from the request | Not a match; keep waiting |
| Host: slot ≥ 08 in `1C 00`, `4C 00`, `4E 00`, `11 00` | Encoder error (MI `0ppp`). The amp would answer 24/26 (**UNVERIFIED** which) |
| Model > 13, P1 type > 09, P2 type > 06, reverb type > 03, bias > 02, switch > 01 | Encoder error, or amp NAK `24` |
| 0-100 param > 0x64; reverb pre-delay > 0x46; chorus/phaser speed outside 100-10000; flanger outside 100-5000; tremolo outside 1650-10000; delay outside 30-1200 | Encoder error (MI ranges) |
| Live value > 0x3FFF | Encoder error |
| Name longer than 16 or containing chars outside 0x20-0x7E | Encoder error (MI:L402; stricter than K's 0x00-0x7F, K/protocol/ProgramName.kt:5-10) |
| `41 06 04` with a phaser, `41 06 06` with a delay, `41 05 04/05` with COMP | Not sent by a correct encoder. On hardware: no ACK [HW-VPC] |
| VTXPROG: wrong prelude, or size not 0x20 + n·0x3E | Reject (K/vtxprog/VtxProgFile.kt:83-96) |

---

## 12. Reference-implementation bugs (do not port)

All in librarian K unless noted.

1. Pedal-1 param0 is read and written with the 14-bit live formula inside the packed dump
   (K/protocol/Program.kt:86, L148), and set-4 MSB bit `10` is ignored (L143).
   - This is wrong for values ≥ 128: chorus speed.
   - VPC has the same bug (VPC/lib/protocol.js:369, L682-683).
2. CHORUS DEPTH/MANUAL/MIX are bound to `pedal2Dial2..4` (K/appmodel/slot1pedals.kt:89-103). MI puts them at pedal-1
   offsets 02-04 = S 22-24 (MI:L574-579).
   - Live messages are unaffected (they are sent by index).
   - VPC's README claims hardware "confirmation" (VPC/README.md:91-98, L358-384). That only shows the stored bytes were
     what the buggy app wrote; it does not show the amp reads chorus from 2A-2C.
3. Hand-written MSB bytes: only set 5 bit 5 is handled (K/protocol/Program.kt:299-321). Use generic pack7/unpack7.
4. All reserved bits and bytes are dropped on read and zeroed on write (K/protocol/Program.kt:50-60, L101-105).
5. Positional response correlation: an unsolicited knob message during an exchange is fed to the exchange's parser
   (K/protocol/VoxVtxAmplifierClient.kt:143-171, K/protocol/message/SingleMessageResponseHandler.kt:7-8).
6. No handlers for `4E 01`, `4E 02`, `21`. Unknown messages throw (K/protocol/VoxVtxAmplifierClient.kt:100-102).
7. The timeout cannot fire (§9).
8. VPC's phaser/delay indices are copied from the wrong doc table (VPC/lib/protocol.js:141-149, L172-182).

---

## Discrepancies resolved

| # | Topic | protocol-doc.md says | protocol-code.md says | Resolution | Evidence |
|---|---|---|---|---|---|
| 1 | 70- vs 62-byte program; "8th bytes" | KORG packing, inferred (PD:L212-252) | same, inferred (PC:L330-352) | **Confirmed official**: 62 data bytes → 71 MIDI bytes. The 70-byte "program" of D/K = packed bytes 1-70; the `00` before it is set-0's MSB | MI:L358-383; VEC worked example; frame identical to PD:L765 |
| 2 | Pedal-1 param0 encoding | plain LE16 in struct, K wrong; needs HW test (PD:L270-281) | same (PC:L344-352) | **Plain LE16 inside the struct** (packed). The 14-bit split applies only to live `41` | MI:L572-573, L805, L288-289; F#L49; [HW-D] D#L164 saw 1F=`10`; [HW-VPC] chorus readback anomaly (VPC/lib/protocol.js:458-472) |
| 3 | Phaser MANUAL/DEPTH index | doc 03/04 vs code 02/03, UNVERIFIED (PD:L366-367) | same (PC:L273-275) | **MANUAL = 06,02; DEPTH = 06,03** (code right, D#L278-279 wrong) | MI:L699-706 (alt:618-625); [HW-VPC] index 04 never ACKs (VPC/README.md:242-259) |
| 4 | Delay MOD DEPTH index | doc 06 vs code 05 (PD:L372) | same | **06,05** | MI:L754-755; D#L248 "00 to 05"; [HW-VPC] modDepth at 06 rejected (VPC/gui/server.js:268-282) |
| 5 | Chorus field mapping | code binds DEPTH/MANUAL/MIX to pedal2Dial2-4 (PD:L357) | "bug" (PC:L277) | **Bug.** CHORUS DEPTH/MANUAL/MIX = pedal-1 offsets 02/03/04 = S 22/23/24 (wire Dk 26/28/29) | MI:L574-579 |
| 6 | Reverb PRE DELAY range | doc `0000-0064` vs code 0-70 ms (PD:L374) | 0-70 ms (PC:L269) | **00-46 = 0-70 ms**, single byte | MI:L792 |
| 7 | `4E 00 pp` after `4C`: persist? | UNVERIFIED (PD:L451-454) | UNVERIFIED (PC:L447) | `4E` = MODE CHANGE (select). `4C` itself saves to internal memory. The follow-up `4E` only switches to and reloads that slot | MI:L242-243, L264-276; [HW-VPC] power-cycle survival with 4C+4E (VPC/README.md:224-226). 4C-alone persistence on VT100X still UNVERIFIED |
| 8 | Unsolicited `4E 01`/`4E 02` | not parsed by code (PD:L396-405) | unhandled (PC:L109) | Official MODE CHANGE notifications, mm = 1 preset / 2 manual. Also host-sendable | MI:L119, L134, L264-276 |
| 9 | ACK/error codes | 23 ACK; 24/26 from code; KORG names UNVERIFIED (PD:L290-299) | 23/24/26 (PC:L129-131) | **23 DATA LOAD COMPLETED, 24 DATA LOAD ERROR (NAK), 26 DATA FORMAT ERROR**, plus **21 WRITE COMPLETED / 22 WRITE ERROR** (`21/22 00 0p`), which both reports missed | MI:L122-126, L297-354 |
| 10 | Byte after `1C`/`4C` | constant `00` | constant `00` | It is **m** (00 user / 01 preset). `1C 01 pp` reads presets (UNVERIFIED on HW) | MI:L167, L236 |
| 11 | `31`/`65` format | "undocumented" (PD:L414, L438) | not implemented | Documented: 62-byte packed record, TABLE 2 (§5.3) | MI:L175-185, L247-261, L809-938 |
| 12 | Missing functions | — | — | `11` PROGRAM WRITE REQUEST and receivable `40` (load Current Buffer) exist | MI:L110, L115, L188-198, L214-225 |
| 13 | Reserved run at doc 0x35 "Length A" | read as 9 (PD:L513-514) | 9 (PC:L318) | 9 packed bytes = struct 2F-36 (8 bytes) + set-7 MSB | MI:L483; VEC mapping |
| 14 | Doc offset 0x45 / VTXPROG "3e" | struct 3D unused | same | S 3D is inside the reverb parameter area (MI:L491-496), but no reverb type defines a 6th param; preserve it | MI:L788-796; F#L69 typo |
| 15 | Program name charset | ASCII ≤ 0x7F (K) | same | Official 0x20-0x7E | MI:L402 |
| 16 | Amp dial / effect-status IDs | from doc | from code | Unified `41 ID SUB lo hi` scheme; IDs per MI TABLE 1. NR = 01,00; status = 02,{01,02,04}; types = 03,{00,01,02,04}; amp = 04,00-0B; P1 = 05; P2 = 06; REV = 08; name = 00,00-0F | MI:L400-496 |

Both input reports agree with each other (and with D/K) on everything else: enums, slots, amp IDs, frame sizes 79/81,
flags masks 02/04/10, VTXPROG prelude and size.

---

## Still unverified (needs hardware test)

All items below need a test on the VT100X itself.

1. USB VID/PID, ALSA card and port names, and the device-inquiry reply.
   - Run `lsusb -v -d 0944:`, `amidi -l`, and `amidi -S "F0 7E 7F 06 01 F7"`.
2. `4C` alone, without `4E`, persists across a power cycle. Whether `4C` to the **active** slot changes the live sound
   before reselect.
3. `11 00 0p` → `21`/`22`. Host-sent `40 <71>` → `23` and immediate sound change.
4. Host-sent `4E 01 pp` / `4E 02 00`. Whether the amp echoes `4E` after a host select.
5. `1C 01 pp` preset dumps: valid pp range (33 or 60?) and the preset numbering vs names.
6. `31 00 pp` / `65` custom records on the VT100X, and the meaning of the CDN.
7. Name change via `41 00 0i cc 00`.
8. After a host-sent `41 03 00 mm 00`: does the amp send the 12 amp-param messages (as for the panel knob, D#L311-314)?
   After `41 03 ss tt 00`: ACK-then-dials order and count on the VT100X.
9. Exact error code for invalid IDs/indices (`24` vs `26`), e.g. `41 06 04` with a phaser.
10. Chorus speed with low byte ≥ 0x80 read via `1C` and `10` on the VT100X.
    - Set 0.200 Hz (0x00C8) and expect set-4 MSB = `10`, Dk 0x24/0x25 = `48 00`.
11. Amp-side placement of CHORUS DEPTH/MANUAL/MIX: set them live via `41 05 01..03`, read `10`, expect S 22-24
    (Dk 26/28/29). Ideally confirm by ear.
12. Meaning of reserved status bits (S 11 b0/b3/b5-7) and S 2F-36/3D. Preserve them until known.
13. Stale tube-bias/class in `10` (D#L386-387) on the VT100X.
14. Maximum safe live message rate and the write latency of `4C` (timeouts in §9 are estimates).
15. Third-party claim that old ToneRoom `.vtxprog` files contain 0x20 in boolean bytes (librarian issue #12).
