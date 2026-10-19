package sim

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"

	"github.com/TomKalina/pi-velvetronix/internal/vox"
)

func h(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(strings.Join(strings.Fields(s), ""))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func korg(fn byte, data ...byte) []byte { return vox.Frame(fn, data...) }

func one(t *testing.T, s *Sim, in []byte) []byte {
	t.Helper()
	out := s.Handle(in)
	if len(out) != 1 {
		t.Fatalf("Handle(% X): %d frames, want 1", in, len(out))
	}
	return out[0]
}

func wantFn(t *testing.T, s *Sim, in []byte, fn byte) {
	t.Helper()
	if got := one(t, s, in); got[6] != fn || len(got) != 8 {
		t.Fatalf("Handle(% X) = % X, want func %02X", in, got, fn)
	}
}

func current(t *testing.T, s *Sim) []byte {
	t.Helper()
	f := one(t, s, korg(vox.FnCurrentProgramReq))
	if len(f) != 79 || f[6] != vox.FnCurrentProgram {
		t.Fatalf("current dump: % X", f)
	}
	p, err := vox.Unpack7(f[7:78])
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func slotDump(t *testing.T, s *Sim, slot byte) []byte {
	t.Helper()
	f := one(t, s, korg(vox.FnProgramDumpRequest, 0, slot))
	if len(f) != 81 || f[6] != vox.FnProgramDump || f[7] != 0 || f[8] != slot {
		t.Fatalf("slot dump: % X", f)
	}
	p, err := vox.Unpack7(f[9:80])
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestIdentityAndMode(t *testing.T) {
	s := New()
	for _, in := range []string{"F0 7E 7F 06 01 F7", "F0 7E 00 06 01 F7"} {
		if got := one(t, s, h(t, in)); !bytes.Equal(got, h(t, "F0 7E 00 06 02 42 34 01 00 00 00 00 01 00 F7")) {
			t.Fatalf("inquiry reply % X", got)
		}
	}
	if got := one(t, s, h(t, "F0 42 30 00 01 34 12 F7")); !bytes.Equal(got, h(t, "F0 42 30 00 01 34 42 00 00 F7")) {
		t.Fatalf("mode reply % X", got)
	}
}

func TestInitialSlotsValidAndDistinct(t *testing.T) {
	s := New()
	seen := map[string]bool{}
	for i := range byte(vox.UserSlots) {
		p := slotDump(t, s, i)
		var prog [vox.StructLen]byte
		copy(prog[:], p)
		if err := validate(&prog); err != nil {
			t.Fatalf("slot %d invalid: %v", i, err)
		}
		name := fmt.Sprintf("FAKE %c%d", "AB"[i/4], i%4+1)
		if got := string(p[:16]); got != fmt.Sprintf("%-16s", name) {
			t.Fatalf("slot %d name %q", i, got)
		}
		seen[string(p)] = true
	}
	if len(seen) != vox.UserSlots {
		t.Fatalf("slots not distinct")
	}
	if !bytes.Equal(current(t, s), slotDump(t, s, 0)) {
		t.Fatal("current buffer is not a copy of slot 0")
	}
	wantFn(t, s, korg(vox.FnProgramDumpRequest, 0, 8), vox.FnNAK)
	wantFn(t, s, korg(vox.FnProgramDumpRequest, 1, 0), vox.FnNAK)
}

func TestWriteVectorAAndSelect(t *testing.T) {
	s := New()
	frame := h(t, `F0 42 30 00 01 34 4C 00 03 00 50 49 20 54 45 53 54 00 20 20 20 20 20 20 20 00 20 20 32 12 07 32 3C 00 28 46 50 1E 14 01 00 10 01 02 01 01 00 00 32 20 32 32 00 01 05 7E 00 00 32 28 32 0A 00 00 00 00 00 00 00 00 00 00 01 00 32 32 14 32 32 00 F7`)
	before := current(t, s)
	wantFn(t, s, frame, vox.FnACK)
	got := one(t, s, korg(vox.FnProgramDumpRequest, 0, 3))
	if !bytes.Equal(got, frame) {
		t.Fatalf("read back:\n% X\nwant\n% X", got, frame)
	}
	if !bytes.Equal(current(t, s), before) {
		t.Fatal("4C changed the current buffer")
	}
	wantFn(t, s, korg(vox.FnModeChange, 0, 3), vox.FnACK)
	if string(current(t, s)[:7]) != "PI TEST" {
		t.Fatal("select did not load slot 3")
	}
	if got := one(t, s, korg(vox.FnModeRequest)); got[7] != 0 || got[8] != 3 {
		t.Fatalf("mode after select % X", got)
	}
	wantFn(t, s, korg(vox.FnModeChange, 0, 8), vox.FnNAK)
	wantFn(t, s, korg(vox.FnModeChange, 2, 0), vox.FnNAK)
}

func TestWriteRejects(t *testing.T) {
	s := New()
	valid := slotDump(t, s, 1)
	write := func(p []byte) []byte { return korg(vox.FnProgramDump, append([]byte{0, 2}, vox.Pack7(p)...)...) }
	bad := map[string]func(p []byte){
		"name char":       func(p []byte) { p[3] = 0x7F },
		"amp model":       func(p []byte) { p[offAmpModel] = 0x14 },
		"gain":            func(p []byte) { p[offAmp] = 101 },
		"bias":            func(p []byte) { p[offAmp+10] = 3 },
		"reverb predelay": func(p []byte) { p[offRevType], p[0x3A] = 0, 71 },
		"chorus speed":    func(p []byte) { p[offP1Type], p[0x20], p[0x21] = 1, 0x10, 0x27+1 },
		"pedal2 type":     func(p []byte) { p[offP2Type] = 7 },
	}
	for name, mutate := range bad {
		p := bytes.Clone(valid)
		mutate(p)
		if got := one(t, s, write(p)); got[6] != vox.FnNAK {
			t.Errorf("%s: % X, want NAK", name, got)
		}
	}
	if !bytes.Equal(slotDump(t, s, 2), New().slots[2][:]) {
		t.Fatal("rejected write modified the slot")
	}
	// Unused COMP params are not range-checked and are stored as sent.
	p := bytes.Clone(valid)
	p[offP1Type], p[0x25], p[0x26] = 0, 0x7F, 0x7F
	for _, n := range []int{0, 1, 2} {
		p[pedalOff(0x20, n)] = 10
	}
	p[0x21] = 0
	p[0x24] = 2
	wantFn(t, s, write(p), vox.FnACK)
	if !bytes.Equal(slotDump(t, s, 2), p) {
		t.Fatal("stored program differs")
	}
	// Bad packing and wrong length.
	f := write(valid)
	f[9+64] = 0x40
	wantFn(t, s, f, vox.FnDataFormatError)
	wantFn(t, s, f[:len(f)-2], vox.FnDataFormatError)
	wantFn(t, s, korg(vox.FnProgramDump, append([]byte{0, 8}, vox.Pack7(valid)...)...), vox.FnNAK)
}

func TestParamChange(t *testing.T) {
	s := New() // slot 0: pedal1 COMP, pedal2 FLANGER, reverb ROOM
	ok := []struct {
		in      []byte
		off, v  int
		wide    bool
		comment string
	}{
		{korg(0x41, 0x04, 0x00, 0x32, 0x00), offAmp, 0x32, false, "gain 5.0"},
		{korg(0x41, 0x04, 0x0A, 0x02, 0x00), offAmp + 10, 2, false, "bias hot"},
		{korg(0x41, 0x01, 0x00, 0x64, 0x00), offNR, 100, false, "nr 10.0"},
		{korg(0x41, 0x03, 0x00, 0x07, 0x00), offAmpModel, 7, false, "model AC30TB"},
		{korg(0x41, 0x06, 0x00, 0x08, 0x27), 0x28, 5000, true, "flanger speed max"},
		{korg(0x41, 0x08, 0x02, 0x46, 0x00), 0x3A, 70, false, "reverb predelay max"},
		{korg(0x41, 0x00, 0x00, 0x5A, 0x00), 0, 'Z', false, "name char"},
	}
	for _, c := range ok {
		wantFn(t, s, c.in, vox.FnACK)
		p := current(t, s)
		got := int(p[c.off])
		if c.wide {
			got |= int(p[c.off+1]) << 8
		}
		if got != c.v {
			t.Errorf("%s: struct value %d, want %d", c.comment, got, c.v)
		}
	}
	wantFn(t, s, korg(0x41, 0x02, 0x04, 0x01, 0x00), vox.FnACK)
	if current(t, s)[offStatus]&0x10 == 0 {
		t.Error("reverb not switched on")
	}
	wantFn(t, s, korg(0x41, 0x02, 0x04, 0x00, 0x00), vox.FnACK)
	if current(t, s)[offStatus]&0x10 != 0 {
		t.Error("reverb not switched off")
	}

	bad := map[string][]byte{
		"gain > 100":          korg(0x41, 0x04, 0x00, 0x65, 0x00),
		"gain hi byte":        korg(0x41, 0x04, 0x00, 0x01, 0x01),
		"amp param 0C":        korg(0x41, 0x04, 0x0C, 0x00, 0x00),
		"model 14":            korg(0x41, 0x03, 0x00, 0x14, 0x00),
		"pedal1 type 0A":      korg(0x41, 0x03, 0x01, 0x0A, 0x00),
		"comp param 4 unused": korg(0x41, 0x05, 0x04, 0x00, 0x00),
		"comp voice 3":        korg(0x41, 0x05, 0x03, 0x03, 0x00),
		"flanger speed 5001":  korg(0x41, 0x06, 0x00, 0x09, 0x27),
		"reverb param 5":      korg(0x41, 0x08, 0x05, 0x00, 0x00),
		"unknown id 07":       korg(0x41, 0x07, 0x00, 0x00, 0x00),
		"effect sub 03":       korg(0x41, 0x02, 0x03, 0x01, 0x00),
		"name char 7F":        korg(0x41, 0x00, 0x00, 0x7F, 0x00),
	}
	for name, in := range bad {
		before := current(t, s)
		if got := one(t, s, in); got[6] != vox.FnNAK {
			t.Errorf("%s: % X, want NAK", name, got)
		}
		if !bytes.Equal(current(t, s), before) {
			t.Errorf("%s: changed the buffer", name)
		}
	}
}

func TestEffectTypeBurst(t *testing.T) {
	s := New()
	cases := []struct {
		in    []byte
		id    byte
		n     int
		first int
	}{
		{korg(0x41, 0x03, 0x01, 0x01, 0x00), vox.IDPedal1, 6, 1000}, // CHORUS
		{korg(0x41, 0x03, 0x02, 0x05, 0x00), vox.IDPedal2, 6, 300},  // TAPE ECHO
		{korg(0x41, 0x03, 0x04, 0x01, 0x00), vox.IDReverb, 5, 50},   // SPRING
	}
	for _, c := range cases {
		out := s.Handle(c.in)
		if len(out) != 1+c.n || out[0][6] != vox.FnACK {
			t.Fatalf("% X: %d frames, first % X", c.in, len(out), out[0])
		}
		for n, f := range out[1:] {
			if len(f) != 12 || f[6] != 0x41 || f[7] != c.id || f[8] != byte(n) {
				t.Fatalf("burst frame %d: % X", n, f)
			}
		}
		if v := vox.Dec14(out[1][9], out[1][10]); v != c.first {
			t.Errorf("% X: param 0 default %d, want %d", c.in, v, c.first)
		}
	}
	p := current(t, s)
	if p[offP1Type] != 1 || p[offP2Type] != 5 || p[offRevType] != 1 {
		t.Fatalf("types not applied: % X", p[offP1Type:offP1Type+1])
	}
	var prog [vox.StructLen]byte
	copy(prog[:], p)
	if err := validate(&prog); err != nil {
		t.Fatalf("buffer invalid after type changes: %v", err)
	}
	// Chorus speed is 14-bit live: 10000 = 10 4E.
	wantFn(t, s, korg(0x41, 0x05, 0x00, 0x10, 0x4E), vox.FnACK)
	if p := current(t, s); p[0x20] != 0x10 || p[0x21] != 0x27 {
		t.Fatalf("chorus speed bytes % X", p[0x20:0x22])
	}
}

func TestProgramWriteRequest(t *testing.T) {
	s := New()
	wantFn(t, s, korg(0x41, 0x04, 0x00, 0x00, 0x00), vox.FnACK)
	got := one(t, s, korg(vox.FnProgramWriteReq, 0, 5))
	if !bytes.Equal(got, h(t, "F0 42 30 00 01 34 21 00 05 F7")) {
		t.Fatalf("write request reply % X", got)
	}
	if !bytes.Equal(slotDump(t, s, 5), current(t, s)) {
		t.Fatal("slot 5 is not the current buffer")
	}
	if got := one(t, s, korg(vox.FnProgramWriteReq, 0, 9)); !bytes.Equal(got, h(t, "F0 42 30 00 01 34 22 00 09 F7")) {
		t.Fatalf("bad slot reply % X", got)
	}
}

func TestUnsupported(t *testing.T) {
	s := New()
	cur := korg(vox.FnCurrentProgram, vox.Pack7(s.cur[:])...)
	for name, in := range map[string][]byte{
		"40 buffer load":  cur,
		"65 custom write": korg(vox.FnCustomDump, append([]byte{0, 0}, vox.Pack7(s.cur[:])...)...),
		"31 custom read":  korg(vox.FnCustomDumpRequest, 0, 0),
		"unknown 7F":      korg(0x7F),
		"12 with data":    korg(vox.FnModeRequest, 0),
		"non-KORG":        h(t, "F0 43 10 4C 00 F7"),
		"short":           h(t, "F0 F7"),
	} {
		if got := one(t, s, in); got[6] != vox.FnDataFormatError {
			t.Errorf("%s: % X, want 26", name, got)
		}
	}
}

func TestKnobTurn(t *testing.T) {
	s := New()
	g := current(t, s)[offAmp]
	f := s.KnobTurn()
	want := (g + 7) % 101
	if !bytes.Equal(f, korg(0x41, 0x04, 0x00, want, 0x00)) {
		t.Fatalf("knob frame % X", f)
	}
	if current(t, s)[offAmp] != want {
		t.Fatal("knob did not change the buffer")
	}
	for range 200 {
		if f := s.KnobTurn(); f[9] > 100 {
			t.Fatalf("gain out of range: % X", f)
		}
	}
}
