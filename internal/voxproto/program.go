package voxproto

import (
	"fmt"
	"strings"

	"github.com/TomKalina/pi-velvetronix/internal/vox"
)

// 62-byte program struct, docs/protocol.md §5 (program.ts).

const (
	StructLen = vox.StructLen
	NameLen   = 16
)

// Struct offsets (P§5.1).
const (
	offNR       = 0x10
	offStatus   = 0x11
	offAmpModel = 0x12
	offAmp      = 0x13
)

var (
	offType  = [3]int{0x1f, 0x27, 0x37}
	offParam = [3]int{0x20, 0x28, 0x38}
	// SlotBit is the effect-status bit of each slot, SlotSub its SUB in 41 02 ss / 41 03 ss.
	SlotBit     = [3]byte{0x02, 0x04, 0x10}
	SlotSub     = [3]byte{vox.SlotPedal1, vox.SlotPedal2, vox.SlotReverb}
	SlotParamID = [3]byte{vox.IDPedal1, vox.IDPedal2, vox.IDReverb}
	// SlotParamCount is the number of params of each slot.
	SlotParamCount = [3]int{6, 6, 5}
)

// Effect is one effect slot of a program.
type Effect struct {
	On     bool
	Type   int
	Params []int
}

// Program is a decoded program struct. Raw keeps the 62 bytes as read so Encode preserves
// reserved bits.
type Program struct {
	Raw      []byte
	Name     string // up to 16 ASCII chars 0x20..0x7E, trailing spaces trimmed
	NR       int
	AmpModel int
	// Amp holds 12 values in P§5.1 order: gain, treble, middle, bass, volume, presence/tone,
	// resonance, bright cap, low cut, mid boost, bias shift, class.
	Amp []int
	FX  [3]Effect
}

// Clone returns a deep copy.
func (p Program) Clone() Program {
	q := p
	q.Raw = append([]byte(nil), p.Raw...)
	q.Amp = append([]int(nil), p.Amp...)
	for i, e := range p.FX {
		q.FX[i].Params = append([]int(nil), e.Params...)
	}
	return q
}

// Decode decodes a 62-byte program struct.
func Decode(s []byte) (Program, error) {
	if len(s) != StructLen {
		return Program{}, fmt.Errorf("program struct must be %d bytes, got %d", StructLen, len(s))
	}
	raw := append([]byte(nil), s...)
	p := Program{
		Raw:      raw,
		Name:     strings.TrimRight(latin1(raw[:NameLen]), " "),
		NR:       int(raw[offNR]),
		AmpModel: int(raw[offAmpModel]),
		Amp:      ints(raw[offAmp : offAmp+12]),
	}
	status := raw[offStatus]
	for _, slot := range Slots {
		base := offParam[slot]
		var params []int
		if slot == Reverb {
			params = ints(raw[base : base+5])
		} else {
			// Pedal param 0 is LE16 at base; params 1..5 are single bytes at base+1+n.
			params = []int{int(raw[base]) | int(raw[base+1])<<8}
			params = append(params, ints(raw[base+2:base+7])...)
		}
		p.FX[slot] = Effect{On: status&SlotBit[slot] != 0, Type: int(raw[offType[slot]]), Params: params}
	}
	return p, nil
}

// latin1 maps each byte to the rune of the same value, like String.fromCharCode in program.ts.
func latin1(b []byte) string {
	r := make([]rune, len(b))
	for i, c := range b {
		r[i] = rune(c)
	}
	return string(r)
}

// padName pads name with spaces to NameLen runes.
func padName(name string) []rune {
	r := []rune(name)
	for len(r) < NameLen {
		r = append(r, ' ')
	}
	return r
}

func ints(b []byte) []int {
	out := make([]int, len(b))
	for i, v := range b {
		out[i] = int(v)
	}
	return out
}

func checkRange(what string, v, lo, hi int) error {
	if v < lo || v > hi {
		return fmt.Errorf("%s out of range %d..%d: %d", what, lo, hi, v)
	}
	return nil
}

func checkEffect(slot Slot, e Effect) error {
	if err := checkRange(slot.String()+" type", e.Type, 0, len(SlotTypes[slot])-1); err != nil {
		return err
	}
	if len(e.Params) != SlotParamCount[slot] {
		return fmt.Errorf("%s needs %d params", slot, SlotParamCount[slot])
	}
	row := EffectParams[slot][e.Type]
	for n, v := range e.Params {
		var err error
		if spec := row[n]; spec != nil {
			err = checkRange(slot.String()+" "+spec.Name, v, spec.Min, spec.Max)
		} else {
			// Unused indices keep whatever byte the amp returned.
			err = checkRange(fmt.Sprintf("%s param %d (unused)", slot, n), v, 0, 0xff)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// Encode validates every field (P§11.7) and returns the 62-byte struct, starting from p.Raw.
func Encode(p Program) ([]byte, error) {
	if len(p.Raw) != StructLen {
		return nil, fmt.Errorf("raw must be %d bytes", StructLen)
	}
	name := []rune(p.Name)
	if len(name) > NameLen {
		return nil, fmt.Errorf("name longer than %d chars", NameLen)
	}
	for _, c := range name {
		if c < 0x20 || c > 0x7e {
			return nil, fmt.Errorf("name contains invalid char %q", c)
		}
	}
	if err := checkRange("nr", p.NR, 0, 100); err != nil {
		return nil, err
	}
	if err := checkRange("amp model", p.AmpModel, 0, len(AmpModels)-1); err != nil {
		return nil, err
	}
	if len(p.Amp) != len(AmpParams) {
		return nil, fmt.Errorf("amp needs %d params", len(AmpParams))
	}
	for _, spec := range AmpParams {
		if err := checkRange("amp "+spec.Name, p.Amp[spec.Index], spec.Min, spec.Max); err != nil {
			return nil, err
		}
	}
	for _, slot := range Slots {
		if err := checkEffect(slot, p.FX[slot]); err != nil {
			return nil, err
		}
	}

	s := append([]byte(nil), p.Raw...)
	for i, c := range padName(p.Name) {
		s[i] = byte(c) // ASCII, checked above
	}
	s[offNR] = byte(p.NR)
	status := s[offStatus] &^ (SlotBit[Pedal1] | SlotBit[Pedal2] | SlotBit[Reverb])
	for _, slot := range Slots {
		if p.FX[slot].On {
			status |= SlotBit[slot]
		}
	}
	s[offStatus] = status
	s[offAmpModel] = byte(p.AmpModel)
	for i, v := range p.Amp {
		s[offAmp+i] = byte(v)
	}
	for _, slot := range Slots {
		e := p.FX[slot]
		s[offType[slot]] = byte(e.Type)
		base := offParam[slot]
		if slot == Reverb {
			for n, v := range e.Params {
				s[base+n] = byte(v)
			}
			continue
		}
		s[base] = byte(e.Params[0] & 0xff)
		s[base+1] = byte(e.Params[0] >> 8)
		for n := 1; n < 6; n++ {
			s[base+1+n] = byte(e.Params[n])
		}
	}
	return s, nil
}

// slotBySub returns the slot whose SUB in 41 02 / 41 03 is sub.
func slotBySub(sub byte) (Slot, bool) {
	for _, slot := range Slots {
		if SlotSub[slot] == sub {
			return slot, true
		}
	}
	return 0, false
}

// ApplyParam applies a 41 ID SUB value change to a copy of p. Unknown targets return p unchanged.
func ApplyParam(p Program, id, sub byte, value int) Program {
	q := p.Clone()
	switch id {
	case 0x00:
		if sub >= NameLen {
			return p
		}
		name := padName(p.Name)
		name[sub] = rune(value & 0xff)
		q.Name = strings.TrimRight(string(name), " ")
	case 0x01:
		if sub != 0 {
			return p
		}
		q.NR = value
	case 0x02:
		slot, ok := slotBySub(sub)
		if !ok {
			return p
		}
		q.FX[slot].On = value != 0
	case 0x03:
		if sub == 0 {
			q.AmpModel = value
			break
		}
		slot, ok := slotBySub(sub)
		if !ok {
			return p
		}
		q.FX[slot].Type = value
	case 0x04:
		if int(sub) >= len(AmpParams) {
			return p
		}
		q.Amp[sub] = value
	default:
		for _, slot := range Slots {
			if id == SlotParamID[slot] {
				if int(sub) >= SlotParamCount[slot] {
					return p
				}
				q.FX[slot].Params[sub] = value
				return q
			}
		}
		return p
	}
	return q
}
