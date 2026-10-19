package midi

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func feedAll(f *Framer, chunks ...[]byte) [][]byte {
	var out [][]byte
	for _, c := range chunks {
		f.Feed(c, func(fr []byte) { out = append(out, fr) })
	}
	return out
}

func TestFramer(t *testing.T) {
	long := append([]byte{0xF0}, bytes.Repeat([]byte{0x01}, maxFrame)...)
	long = append(long, 0xF7)
	exact := append([]byte{0xF0}, bytes.Repeat([]byte{0x02}, maxFrame-2)...)
	exact = append(exact, 0xF7)

	tests := []struct {
		name   string
		chunks [][]byte
		want   [][]byte
	}{
		{"simple", [][]byte{{0xF0, 0x42, 0x23, 0xF7}}, [][]byte{{0xF0, 0x42, 0x23, 0xF7}}},
		{"split across reads", [][]byte{{0xF0, 0x42}, {0x30}, {0x23, 0xF7}}, [][]byte{{0xF0, 0x42, 0x30, 0x23, 0xF7}}},
		{"realtime inside", [][]byte{{0xF0, 0xF8, 0x42, 0xFE, 0xFF, 0x23, 0xFA, 0xF7}}, [][]byte{{0xF0, 0x42, 0x23, 0xF7}}},
		{"realtime outside", [][]byte{{0xFE, 0xF8, 0xF0, 0x01, 0xF7, 0xFE}}, [][]byte{{0xF0, 0x01, 0xF7}}},
		{"F0 restarts", [][]byte{{0xF0, 0x01, 0x02, 0xF0, 0x03, 0xF7}}, [][]byte{{0xF0, 0x03, 0xF7}}},
		{"status aborts", [][]byte{{0xF0, 0x01, 0x90, 0x40, 0x7F, 0xF7}}, nil},
		{"F7 without start", [][]byte{{0x01, 0xF7, 0xF0, 0x05, 0xF7}}, [][]byte{{0xF0, 0x05, 0xF7}}},
		{"stray data ignored", [][]byte{{0x10, 0x20, 0xF0, 0x05, 0xF7, 0x30}}, [][]byte{{0xF0, 0x05, 0xF7}}},
		{"two frames", [][]byte{{0xF0, 0x01, 0xF7, 0xF0, 0x02, 0xF7}}, [][]byte{{0xF0, 0x01, 0xF7}, {0xF0, 0x02, 0xF7}}},
		{"too long discarded", [][]byte{long, {0xF0, 0x09, 0xF7}}, [][]byte{{0xF0, 0x09, 0xF7}}},
		{"max length kept", [][]byte{exact}, [][]byte{exact}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := feedAll(&Framer{}, tt.chunks...)
			if len(got) != len(tt.want) {
				t.Fatalf("got %d frames %X, want %d", len(got), got, len(tt.want))
			}
			for i := range got {
				if !bytes.Equal(got[i], tt.want[i]) {
					t.Errorf("frame %d = % X, want % X", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestFramerEmitsCopies(t *testing.T) {
	var f Framer
	got := feedAll(&f, []byte{0xF0, 0x01, 0xF7}, []byte{0xF0, 0x02, 0xF7})
	if got[0][1] != 0x01 || got[1][1] != 0x02 {
		t.Fatalf("frames share a buffer: % X", got)
	}
}

func korg(fn byte, data ...byte) []byte {
	f := []byte{0xF0, 0x42, 0x30, 0x00, 0x01, 0x34, fn}
	f = append(f, data...)
	return append(f, 0xF7)
}

func TestCheckHostFrame(t *testing.T) {
	prog71 := bytes.Repeat([]byte{0x00}, 71)
	write4C := korg(0x4C, append([]byte{0x00, 0x03}, prog71...)...)
	if len(write4C) != 81 {
		t.Fatalf("fixture length %d", len(write4C))
	}

	tests := []struct {
		name   string
		kind   byte
		frame  []byte
		wantFn int
		want   error
	}{
		{"inquiry 7F", 0, []byte{0xF0, 0x7E, 0x7F, 0x06, 0x01, 0xF7}, -1, nil},
		{"inquiry 00 kind1", 1, []byte{0xF0, 0x7E, 0x00, 0x06, 0x01, 0xF7}, -1, nil},
		{"inquiry other channel", 0, []byte{0xF0, 0x7E, 0x05, 0x06, 0x01, 0xF7}, -1, ErrForbidden},
		{"mode request", 0, korg(0x12), 0x12, nil},
		{"current program request", 0, korg(0x10), 0x10, nil},
		{"program dump request", 0, korg(0x1C, 0x00, 0x07), 0x1C, nil},
		{"custom dump request", 0, korg(0x31, 0x00, 0x02), 0x31, nil},
		{"custom dump request bad bank", 0, korg(0x31, 0x01, 0x02), 0x31, ErrForbidden},
		{"param change", 0, korg(0x41, 0x05, 0x00, 0x10, 0x00), 0x41, nil},
		{"param change kind1", 1, korg(0x41, 0x05, 0x00, 0x10, 0x00), 0x41, nil},
		{"param change short", 0, korg(0x41, 0x05, 0x00, 0x10), 0x41, ErrForbidden},
		{"mode change", 0, korg(0x4E, 0x00, 0x02), 0x4E, nil},
		{"write program kind0", 0, write4C, 0x4C, ErrSaveFlagRequired},
		{"write program kind1", 1, write4C, 0x4C, nil},
		{"write program wrong len", 1, write4C[1:], -1, ErrBadFrame},
		{"write program truncated", 1, append(write4C[:79:79], 0xF7), 0x4C, ErrForbidden},
		{"write request kind0", 0, korg(0x11, 0x00, 0x03), 0x11, ErrSaveFlagRequired},
		{"write request kind1", 1, korg(0x11, 0x00, 0x03), 0x11, nil},
		{"buffer load forbidden", 1, korg(0x40, prog71...), 0x40, ErrForbidden},
		{"custom write forbidden", 1, korg(0x65, append([]byte{0x00, 0x00}, prog71...)...), 0x65, ErrForbidden},
		{"other manufacturer", 0, []byte{0xF0, 0x41, 0x10, 0xF7}, -1, ErrForbidden},
		{"too short", 0, []byte{0xF0, 0xF7}, -1, ErrBadFrame},
		{"no F0", 0, []byte{0x00, 0x42, 0xF7}, -1, ErrBadFrame},
		{"no F7", 0, []byte{0xF0, 0x42, 0x30, 0x00, 0x01, 0x34, 0x12, 0x00}, 0x12, ErrBadFrame},
		{"inner high byte", 0, []byte{0xF0, 0x42, 0x30, 0x00, 0x01, 0x34, 0x41, 0x80, 0x00, 0x00, 0x00, 0xF7}, 0x41, ErrBadFrame},
		{"bad kind", 2, korg(0x12), 0x12, ErrBadFrame},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fn, err := CheckHostFrame(tt.kind, tt.frame)
			if !errors.Is(err, tt.want) || (tt.want == nil && err != nil) {
				t.Errorf("err = %v, want %v", err, tt.want)
			}
			if fn != tt.wantFn {
				t.Errorf("fn = %#x, want %#x", fn, tt.wantFn)
			}
		})
	}
}

func TestIsIdentity(t *testing.T) {
	reply := []byte{0xF0, 0x7E, 0x00, 0x06, 0x02, 0x42, 0x34, 0x01, 0x00, 0x00, 0x03, 0x00, 0x01, 0x00, 0xF7}
	if !IsIdentity(reply) {
		t.Error("VOX reply not recognised")
	}
	other := append([]byte(nil), reply...)
	other[6] = 0x35
	if IsIdentity(other) {
		t.Error("other family accepted")
	}
	if IsIdentity([]byte{0xF0, 0x7E, 0x7F, 0x06, 0x01, 0xF7}) {
		t.Error("inquiry request accepted as identity")
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestFindDevices(t *testing.T) {
	proc := t.TempDir()
	dev := t.TempDir()
	writeFile(t, filepath.Join(proc, "cards"), ""+
		" 0 [vc4hdmi        ]: vc4-hdmi - vc4-hdmi\n"+
		"                      vc4-hdmi\n"+
		" 1 [ValvetronixX   ]: USB-Audio - Valvetronix X\n"+
		"                      Vox Amplification Ltd. Valvetronix X at usb-3f980000.usb-1, full speed\n"+
		" 2 [VirMIDI        ]: VirMIDI - Virtual MIDI Card 1\n"+
		"                      Virtual MIDI Card 1\n"+
		"10 [VirMIDI_1      ]: VirMIDI - Virtual MIDI Card 2\n"+
		"                      Virtual MIDI Card 2\n")
	writeFile(t, filepath.Join(proc, "card1", "usbid"), "0944:0117\n")
	for _, n := range []string{"midiC1D0", "midiC2D10", "midiC2D2", "pcmC0D0p", "controlC1", "midiC10D3"} {
		writeFile(t, filepath.Join(dev, n), "")
	}

	got, err := findDevices(proc, dev, "valvetronix")
	if err != nil {
		t.Fatal(err)
	}
	want := Device{Card: "1", ID: "ValvetronixX", Name: "Valvetronix X",
		LongName: "Vox Amplification Ltd. Valvetronix X at usb-3f980000.usb-1, full speed",
		UsbID:    "0944:0117", Node: filepath.Join(dev, "midiC1D0")}
	if len(got) != 1 || got[0] != want {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}

	got, err = findDevices(proc, dev, "VirMIDI")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("VirMIDI: got %+v", got)
	}
	if got[0].Card != "2" || got[0].Node != filepath.Join(dev, "midiC2D2") || got[0].UsbID != "" {
		t.Errorf("card 2 = %+v (want lowest device D2)", got[0])
	}
	if got[1].Card != "10" || got[1].Node != filepath.Join(dev, "midiC10D3") {
		t.Errorf("card 10 = %+v", got[1])
	}

	// Card matches by long name but has no rawmidi node.
	got, _ = findDevices(proc, dev, "vc4")
	if len(got) != 0 {
		t.Errorf("card without midi node returned: %+v", got)
	}

	got, err = findDevices(filepath.Join(proc, "missing"), dev, "x")
	if err != nil || len(got) != 0 {
		t.Errorf("missing proc: %v %v", got, err)
	}
}
