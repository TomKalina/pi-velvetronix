package voxproto

import (
	"bytes"

	"github.com/TomKalina/pi-velvetronix/internal/vox"
)

// Frame builders and parser, docs/protocol.md §1, §4, §6 (messages.ts).

const (
	funcIndex = 6 // frame index of the KORG function byte
	UserSlots = vox.UserSlots
)

var deviceInquiry = []byte{0xF0, 0x7E, 0x7F, 0x06, 0x01, 0xF7}

// DeviceInquiry returns the universal Device Inquiry frame.
func DeviceInquiry() []byte { return append([]byte(nil), deviceInquiry...) }

func ModeRequest() []byte           { return vox.Frame(vox.FnModeRequest) }
func CurrentProgramRequest() []byte { return vox.Frame(vox.FnCurrentProgramReq) }

func checkSlot(slot int) error { return checkRange("user slot", slot, 0, UserSlots-1) }

// ProgramRequest asks for user program slot (1C 00 pp).
func ProgramRequest(slot int) ([]byte, error) {
	if err := checkSlot(slot); err != nil {
		return nil, err
	}
	return vox.Frame(vox.FnProgramDumpRequest, 0x00, byte(slot)), nil
}

// SelectProgram makes user program slot active (4E 00 pp).
func SelectProgram(slot int) ([]byte, error) {
	if err := checkSlot(slot); err != nil {
		return nil, err
	}
	return vox.Frame(vox.FnModeChange, 0x00, byte(slot)), nil
}

// WriteProgram writes p into user program slot (4C 00 pp + packed struct); needs the save flag.
func WriteProgram(slot int, p Program) ([]byte, error) {
	if err := checkSlot(slot); err != nil {
		return nil, err
	}
	s, err := Encode(p)
	if err != nil {
		return nil, err
	}
	return vox.Frame(vox.FnProgramDump, append([]byte{0x00, byte(slot)}, vox.Pack7(s)...)...), nil
}

// Param builds a live parameter change 41 id sub lo hi.
func Param(id, sub byte, value int) ([]byte, error) {
	if err := checkRange("param id", int(id), 0, 0x7f); err != nil {
		return nil, err
	}
	if err := checkRange("param sub", int(sub), 0, 0x7f); err != nil {
		return nil, err
	}
	if err := checkRange("14-bit value", value, 0, 0x3fff); err != nil {
		return nil, err
	}
	lo, hi := vox.Enc14(value)
	return vox.Frame(vox.FnParamChange, id, sub, lo, hi), nil
}

func NoiseReduction(v int) ([]byte, error) {
	if err := checkRange("nr", v, 0, 100); err != nil {
		return nil, err
	}
	return Param(0x01, 0x00, v)
}

func EffectOn(slot Slot, on bool) []byte {
	v := 0
	if on {
		v = 1
	}
	f, _ := Param(0x02, SlotSub[slot], v) // always in range
	return f
}

func AmpModelFrame(m int) ([]byte, error) {
	if err := checkRange("amp model", m, 0, len(AmpModels)-1); err != nil {
		return nil, err
	}
	return Param(0x03, 0x00, m)
}

func EffectTypeFrame(slot Slot, t int) ([]byte, error) {
	if err := checkRange(slot.String()+" type", t, 0, len(SlotTypes[slot])-1); err != nil {
		return nil, err
	}
	return Param(0x03, SlotSub[slot], t)
}

func AmpParamFrame(n, v int) ([]byte, error) {
	if err := checkRange("amp param", n, 0, len(AmpParams)-1); err != nil {
		return nil, err
	}
	spec := AmpParams[n]
	if err := checkRange("amp "+spec.Name, v, spec.Min, spec.Max); err != nil {
		return nil, err
	}
	return Param(0x04, byte(n), v)
}

// Kind of a parsed frame.
type Kind string

const (
	KindAck            Kind = "ack"
	KindNak            Kind = "nak"
	KindFormatError    Kind = "format_error"
	KindWriteCompleted Kind = "write_completed"
	KindWriteError     Kind = "write_error"
	KindMode           Kind = "mode"
	KindModeChange     Kind = "mode_change"
	KindCurrentProgram Kind = "current_program"
	KindProgram        Kind = "program"
	KindParam          Kind = "param"
	KindIdentity       Kind = "identity"
	KindUnknown        Kind = "unknown"
)

// Parsed is a frame from the amp; which fields are set depends on Kind.
type Parsed struct {
	Kind Kind
	// Slot: write_completed, write_error, program.
	Slot int
	Bank int
	// Mode and ProgramNo: mode, mode_change.
	Mode      string
	ProgramNo int
	// Program: current_program, program.
	Program Program
	// ID, Sub, Value: param.
	ID, Sub byte
	Value   int
	// Major, Minor: identity.
	Major, Minor int
}

func sysexValid(f []byte) bool {
	if len(f) < 3 || f[0] != 0xF0 || f[len(f)-1] != 0xF7 {
		return false
	}
	for _, b := range f[1 : len(f)-1] {
		if b&0x80 != 0 {
			return false
		}
	}
	return true
}

func isKorg(f []byte) bool {
	return len(f) >= len(vox.Header)+2 && bytes.Equal(f[:len(vox.Header)], vox.Header)
}

// FuncOf returns the KORG function byte of a frame, or -1 for non-KORG frames.
func FuncOf(f []byte) int {
	if !isKorg(f) {
		return -1
	}
	return int(f[funcIndex])
}

func program(packed []byte) (Program, error) {
	s, err := vox.Unpack7(packed)
	if err != nil {
		return Program{}, err
	}
	return Decode(s)
}

func mode(b byte) (string, bool) {
	if int(b) >= len(ModeNames) {
		return "", false
	}
	return ModeNames[b], true
}

func parseKorg(f []byte) (Parsed, bool) {
	n := len(f)
	switch fn := f[funcIndex]; fn {
	case vox.FnACK:
		return Parsed{Kind: KindAck}, n == 8
	case vox.FnNAK:
		return Parsed{Kind: KindNak}, n == 8
	case vox.FnDataFormatError:
		return Parsed{Kind: KindFormatError}, n == 8
	case vox.FnWriteCompleted, vox.FnWriteError:
		if n != 10 || f[7] != 0x00 || f[8] >= UserSlots {
			return Parsed{}, false
		}
		k := KindWriteCompleted
		if fn == vox.FnWriteError {
			k = KindWriteError
		}
		return Parsed{Kind: k, Slot: int(f[8])}, true
	case vox.FnModeData, vox.FnModeChange:
		m, ok := mode(f[7])
		if n != 10 || !ok {
			return Parsed{}, false
		}
		k := KindMode
		if fn == vox.FnModeChange {
			k = KindModeChange
		}
		return Parsed{Kind: k, Mode: m, ProgramNo: int(f[8])}, true
	case vox.FnCurrentProgram:
		if n != 7+vox.PackedLen+1 {
			return Parsed{}, false
		}
		p, err := program(f[7 : n-1])
		return Parsed{Kind: KindCurrentProgram, Program: p}, err == nil
	case vox.FnProgramDump:
		if n != 9+vox.PackedLen+1 {
			return Parsed{}, false
		}
		p, err := program(f[9 : n-1])
		return Parsed{Kind: KindProgram, Bank: int(f[7]), Slot: int(f[8]), Program: p}, err == nil
	case vox.FnParamChange:
		if n != 12 {
			return Parsed{}, false
		}
		return Parsed{Kind: KindParam, ID: f[7], Sub: f[8], Value: vox.Dec14(f[9], f[10])}, true
	}
	return Parsed{}, false
}

// Parse parses one frame from the amp. Anything malformed or unexpected is KindUnknown.
func Parse(f []byte) Parsed {
	if !sysexValid(f) {
		return Parsed{Kind: KindUnknown}
	}
	// Identity reply: F0 7E cc 06 02 42 34 01 00 00 minor 00 major 00 F7
	if f[1] == 0x7e {
		if len(f) == 15 && f[3] == 0x06 && f[4] == 0x02 && f[5] == 0x42 && f[6] == 0x34 && f[7] == 0x01 {
			return Parsed{Kind: KindIdentity, Major: int(f[12]), Minor: int(f[10])}
		}
		return Parsed{Kind: KindUnknown}
	}
	if !isKorg(f) {
		return Parsed{Kind: KindUnknown}
	}
	if p, ok := parseKorg(f); ok {
		return p
	}
	return Parsed{Kind: KindUnknown}
}
