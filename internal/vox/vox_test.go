package vox

import (
	"bytes"
	"encoding/hex"
	"strings"
	"testing"
)

func hexBytes(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(strings.Join(strings.Fields(s), ""))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// Vector A, docs/protocol.md §11.4 (verbatim).
const vecAStruct = `50 49 20 54 45 53 54 20 20 20 20 20 20 20 20 20 32 12 07 32 3C 28 46 50 1E 14 01 00 01 02 01 01
80 00 32 32 32 00 01 05 FE 00 32 28 32 0A 00 00 00 00 00 00 00 00 00 01 32 32 14 32 32 00`

const vecAFrame = `F0 42 30 00 01 34 4C 00 03 00 50 49 20 54 45 53 54 00 20 20 20 20 20 20 20 00 20 20 32 12 07 32 3C 00 28 46 50 1E 14 01 00 10 01 02 01 01 00 00 32 20 32 32 00 01 05 7E 00 00 32 28 32 0A 00 00 00 00 00 00 00 00 00 00 01 00 32 32 14 32 32 00 F7`

// Vector B, docs/protocol.md §11.5 (verbatim).
const vecBStruct = `44 45 4C 41 59 20 4D 41 58 20 20 20 20 20 20 20 32 12 07 32 3C 28 46 50 1E 14 01 00 01 02 01 01
        10 27 43 39 0A 00 00 05 B0 04 32 4D 32 01 00 00 00 00 00 00 00 00 00 01 32 32 14 32 32 00`

const vecBPacked = `00 44 45 4C 41 59 20 4D 00 41 58 20 20 20 20 20 00 20 20 32 12 07 32 3C 00 28 46 50 1E 14 01 00
        00 01 02 01 01 10 27 43 20 39 0A 00 00 05 30 04 00 32 4D 32 01 00 00 00 00 00 00 00 00 00 00 01
        00 32 32 14 32 32 00`

func TestVectorA(t *testing.T) {
	s := hexBytes(t, vecAStruct)
	frame := hexBytes(t, vecAFrame)
	if len(s) != StructLen || len(frame) != 81 {
		t.Fatalf("lengths: struct %d frame %d", len(s), len(frame))
	}
	packed := Pack7(s)
	if !bytes.Equal(packed, frame[9:80]) {
		t.Fatalf("Pack7 mismatch:\n got %X\nwant %X", packed, frame[9:80])
	}
	if got := Frame(FnProgramDump, append([]byte{0x00, 0x03}, packed...)...); !bytes.Equal(got, frame) {
		t.Fatalf("Frame mismatch:\n got %X\nwant %X", got, frame)
	}
	var msbs []byte
	for k := 0; k < PackedLen; k += 8 {
		msbs = append(msbs, packed[k])
	}
	if want := []byte{0, 0, 0, 0, 0x10, 0x20, 0, 0, 0}; !bytes.Equal(msbs, want) {
		t.Fatalf("MSB bytes %X, want %X", msbs, want)
	}
	back, err := Unpack7(packed)
	if err != nil || !bytes.Equal(back, s) {
		t.Fatalf("Unpack7: %X, %v", back, err)
	}
}

func TestVectorB(t *testing.T) {
	s := hexBytes(t, vecBStruct)
	p := hexBytes(t, vecBPacked)
	if len(s) != StructLen || len(p) != PackedLen {
		t.Fatalf("lengths: %d %d", len(s), len(p))
	}
	if got := Pack7(s); !bytes.Equal(got, p) {
		t.Fatalf("Pack7:\n got %X\nwant %X", got, p)
	}
	got, err := Unpack7(p)
	if err != nil || !bytes.Equal(got, s) {
		t.Fatalf("Unpack7: %X, %v", got, err)
	}
	// Delay 1200 ms = B0 04 in the struct, 30 04 packed with bit 5 in the set-5 MSB byte.
	if p[40] != 0x20 || p[46] != 0x30 || p[47] != 0x04 {
		t.Fatalf("delay packing: %02X %02X %02X", p[40], p[46], p[47])
	}
}

func TestUnpack7Rejects(t *testing.T) {
	good := Pack7(hexBytes(t, vecAStruct))
	cases := map[string]func(p []byte){
		"last set bit 6":  func(p []byte) { p[64] |= 0x40 },
		"msb with b7":     func(p []byte) { p[8] = 0x80 },
		"8-bit data byte": func(p []byte) { p[3] = 0x90 },
		"8-bit last byte": func(p []byte) { p[70] = 0xFF },
	}
	for name, mutate := range cases {
		p := bytes.Clone(good)
		mutate(p)
		if _, err := Unpack7(p); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestEnc14(t *testing.T) {
	cases := []struct {
		v      int
		lo, hi byte
	}{{120, 0x78, 0}, {128, 0, 1}, {129, 1, 1}, {1650, 0x72, 0x0C}, {5000, 0x08, 0x27}, {10000, 0x10, 0x4E}, {1200, 0x30, 0x09}}
	for _, c := range cases {
		lo, hi := Enc14(c.v)
		if lo != c.lo || hi != c.hi {
			t.Errorf("Enc14(%d) = %02X %02X", c.v, lo, hi)
		}
		if Dec14(lo, hi) != c.v {
			t.Errorf("Dec14(%02X %02X) != %d", lo, hi, c.v)
		}
	}
}
