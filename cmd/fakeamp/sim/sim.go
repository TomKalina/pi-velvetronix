// Package sim simulates the SysEx side of a VOX Valvetronix X amp (docs/protocol.md §4)
// for loopback tests. It is deliberately strict: invalid values get NAK 24 and
// malformed or unsupported messages get DATA FORMAT ERROR 26.
package sim

import (
	"bytes"
	"fmt"
	"sync"

	"github.com/TomKalina/pi-velvetronix/internal/vox"
)

// Struct offsets (P§5.1).
const (
	offName     = 0x00
	offNR       = 0x10
	offStatus   = 0x11
	offAmpModel = 0x12
	offAmp      = 0x13 // 12 amp params, 41 04 00..0B
	offP1Type   = 0x1F
	offP2Type   = 0x27
	offRevType  = 0x37
	nameLen     = 16
)

type rng struct{ min, max, def int }

var (
	unused = rng{-1, -1, 0}
	pct    = rng{0, 100, 50}
	sw     = rng{0, 1, 0}
)

// Effect parameter ranges per type, index n = live SUB (P§5.2).
func pedal1Params(t byte) []rng {
	switch {
	case t == 0: // COMP
		return []rng{pct, pct, pct, {0, 2, 0}, unused, unused}
	case t == 1: // CHORUS
		return []rng{{100, 10000, 1000}, pct, pct, pct, sw, sw}
	case t <= 9: // overdrive, distortion
		return []rng{pct, pct, pct, pct, pct, pct}
	}
	return nil
}

func pedal2Params(t byte) []rng {
	switch {
	case t == 0: // FLANGER
		return []rng{{100, 5000, 1000}, pct, pct, sw, sw, pct}
	case t <= 3: // PHASER
		return []rng{{100, 10000, 1000}, pct, pct, pct, unused, unused}
	case t == 4: // TREMOLO
		return []rng{{1650, 10000, 3000}, pct, pct, pct, pct, unused}
	case t <= 6: // DELAY
		return []rng{{30, 1200, 300}, pct, pct, pct, pct, pct}
	}
	return nil
}

func reverbParams(t byte) []rng {
	if t > 3 {
		return nil
	}
	return []rng{pct, pct, {0, 70, 20}, pct, pct}
}

// effect describes one effect slot: SUB in 41 02/41 03, param ID in 41 ID n,
// type offset and where param n lives in the struct.
type effect struct {
	sub, id byte
	typeOff int
	maxType byte
	params  func(byte) []rng
	off     func(n int) int // struct offset of param n; n=0 of pedals is LE16
	wide0   bool
}

var effects = []effect{
	{vox.SlotPedal1, vox.IDPedal1, offP1Type, 9, pedal1Params, func(n int) int { return pedalOff(0x20, n) }, true},
	{vox.SlotPedal2, vox.IDPedal2, offP2Type, 6, pedal2Params, func(n int) int { return pedalOff(0x28, n) }, true},
	{vox.SlotReverb, vox.IDReverb, offRevType, 3, reverbParams, func(n int) int { return 0x38 + n }, false},
}

func pedalOff(base, n int) int {
	if n == 0 {
		return base
	}
	return base + 1 + n
}

func effectBySub(sub byte) *effect {
	for i := range effects {
		if effects[i].sub == sub {
			return &effects[i]
		}
	}
	return nil
}

func effectByID(id byte) *effect {
	for i := range effects {
		if effects[i].id == id {
			return &effects[i]
		}
	}
	return nil
}

func (e *effect) get(p *[vox.StructLen]byte, n int) int {
	o := e.off(n)
	if n == 0 && e.wide0 {
		return int(p[o]) | int(p[o+1])<<8
	}
	return int(p[o])
}

func (e *effect) set(p *[vox.StructLen]byte, n, v int) {
	o := e.off(n)
	if n == 0 && e.wide0 {
		p[o], p[o+1] = byte(v), byte(v>>8)
		return
	}
	p[o] = byte(v)
}

// setDefaults resets all params of the effect to the defaults of its current type.
func (e *effect) setDefaults(p *[vox.StructLen]byte) {
	for n, r := range e.params(p[e.typeOff]) {
		e.set(p, n, r.def)
	}
}

// ampRange returns the range of amp param nn (41 04 nn, P§5.1).
func ampRange(nn int) (rng, bool) {
	switch {
	case nn <= 6:
		return pct, true
	case nn == 0x0A: // BIAS SHIFT
		return rng{0, 2, 0}, true
	case nn <= 0x0B: // BRIGHT CAP, LOW CUT, MID BOOST, CLASS
		return sw, true
	}
	return rng{}, false
}

func validate(p *[vox.StructLen]byte) error {
	for i := range nameLen {
		if c := p[offName+i]; c < 0x20 || c > 0x7E {
			return fmt.Errorf("name char %d = %02X", i, c)
		}
	}
	if p[offNR] > 100 {
		return fmt.Errorf("nr %d", p[offNR])
	}
	if p[offAmpModel] > 0x13 {
		return fmt.Errorf("amp model %02X", p[offAmpModel])
	}
	for nn := range 12 {
		r, _ := ampRange(nn)
		if v := int(p[offAmp+nn]); v > r.max {
			return fmt.Errorf("amp param %d = %d", nn, v)
		}
	}
	for i := range effects {
		e := &effects[i]
		t := p[e.typeOff]
		if t > e.maxType {
			return fmt.Errorf("effect %02X type %d", e.id, t)
		}
		for n, r := range e.params(t) {
			if r == unused {
				continue
			}
			if v := e.get(p, n); v < r.min || v > r.max {
				return fmt.Errorf("effect %02X param %d = %d", e.id, n, v)
			}
		}
	}
	return nil
}

// Sim is the amp state: always User mode, 8 user slots and the Current Buffer.
type Sim struct {
	mu    sync.Mutex
	slot  byte
	slots [vox.UserSlots][vox.StructLen]byte
	cur   [vox.StructLen]byte
}

// New returns an amp in User mode on slot 0 with 8 valid, distinct programs
// named "FAKE A1".."FAKE B4".
func New() *Sim {
	s := &Sim{}
	for i := range s.slots {
		p := &s.slots[i]
		name := fmt.Sprintf("FAKE %c%d", "AB"[i/4], i%4+1)
		copy(p[:nameLen], bytes.Repeat([]byte{' '}, nameLen))
		copy(p[:], name)
		p[offNR] = byte(10 + 5*i)
		// Pedal1/pedal2/reverb on-off patterns (bits 1, 2, 4).
		p[offStatus] = byte(i&1)<<vox.SlotPedal1 | byte(i>>1&1)<<vox.SlotPedal2 | byte(i>>2&1)<<vox.SlotReverb
		p[offAmpModel] = byte(2 * i)
		for nn := range 7 {
			p[offAmp+nn] = byte(10*i + 5*nn)
		}
		p[offAmp+7] = byte(i % 2)       // bright cap
		p[offAmp+8] = byte((i / 2) % 2) // low cut
		p[offAmp+9] = byte((i / 4) % 2) // mid boost
		p[offAmp+10] = byte(i % 3)      // bias
		p[offAmp+11] = byte((i + 1) % 2)
		p[offP1Type] = byte(i)
		p[offP2Type] = byte(i % 7)
		p[offRevType] = byte(i % 4)
		for j := range effects {
			effects[j].setDefaults(p)
		}
	}
	s.cur = s.slots[0]
	return s
}

func isInquiry(f []byte) bool {
	return len(f) == 6 && f[0] == 0xF0 && f[1] == 0x7E && (f[2] == 0x00 || f[2] == 0x7F) &&
		f[3] == 0x06 && f[4] == 0x01 && f[5] == 0xF7
}

func reply(fn byte, data ...byte) [][]byte { return [][]byte{vox.Frame(fn, data...)} }

var (
	ack       = func() [][]byte { return reply(vox.FnACK) }
	nak       = func() [][]byte { return reply(vox.FnNAK) }
	formatErr = func() [][]byte { return reply(vox.FnDataFormatError) }
)

// Handle processes one complete frame from the host and returns the frames the
// amp sends in response, in order.
func (s *Sim) Handle(f []byte) [][]byte {
	if isInquiry(f) {
		return [][]byte{bytes.Clone(vox.IdentityReply)}
	}
	if len(f) < 8 || !bytes.Equal(f[:6], vox.Header) || f[len(f)-1] != 0xF7 {
		return formatErr()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	data := f[7 : len(f)-1]
	switch fn := f[6]; {
	case fn == vox.FnModeRequest && len(data) == 0:
		return reply(vox.FnModeData, vox.ModeUser, s.slot)
	case fn == vox.FnCurrentProgramReq && len(data) == 0:
		return reply(vox.FnCurrentProgram, vox.Pack7(s.cur[:])...)
	case fn == vox.FnProgramDumpRequest && len(data) == 2:
		m, p := data[0], data[1]
		if m != vox.ModeUser || p >= vox.UserSlots {
			return nak()
		}
		return reply(vox.FnProgramDump, append([]byte{m, p}, vox.Pack7(s.slots[p][:])...)...)
	case fn == vox.FnParamChange && len(data) == 4:
		return s.param(data[0], data[1], vox.Dec14(data[2], data[3]), data[3])
	case fn == vox.FnModeChange && len(data) == 2:
		m, p := data[0], data[1]
		if m != vox.ModeUser || p >= vox.UserSlots {
			return nak()
		}
		s.slot, s.cur = p, s.slots[p]
		return ack()
	case fn == vox.FnProgramDump && len(data) == 2+vox.PackedLen:
		m, p := data[0], data[1]
		if m != vox.ModeUser || p >= vox.UserSlots {
			return nak()
		}
		raw, err := vox.Unpack7(data[2:])
		if err != nil {
			return formatErr()
		}
		var prog [vox.StructLen]byte
		copy(prog[:], raw)
		if validate(&prog) != nil {
			return nak()
		}
		// Internal memory only; the Current Buffer changes on the next select (P§8.3).
		s.slots[p] = prog
		return ack()
	case fn == vox.FnProgramWriteReq && len(data) == 2:
		m, p := data[0], data[1]
		if m != 0 || p >= vox.UserSlots {
			return reply(vox.FnWriteError, m, p)
		}
		s.slots[p] = s.cur
		return reply(vox.FnWriteCompleted, m, p)
	}
	// Includes 40 (buffer load) and 65 (custom write), which the simulator does not support.
	return formatErr()
}

// param applies 41 id sub lo hi to the Current Buffer.
func (s *Sim) param(id, sub byte, v int, hi byte) [][]byte {
	p := &s.cur
	narrow := hi == 0
	switch id {
	case 0x00: // name char
		if sub >= nameLen || !narrow || v < 0x20 || v > 0x7E {
			return nak()
		}
		p[offName+int(sub)] = byte(v)
	case 0x01: // NR SENS
		if sub != 0 || !narrow || v > 100 {
			return nak()
		}
		p[offNR] = byte(v)
	case 0x02: // effect on/off
		if effectBySub(sub) == nil || !narrow || v > 1 {
			return nak()
		}
		p[offStatus] = p[offStatus]&^(1<<sub) | byte(v)<<sub
	case 0x03: // amp model or effect type
		if !narrow {
			return nak()
		}
		if sub == 0 {
			if v > 0x13 {
				return nak()
			}
			p[offAmpModel] = byte(v)
			return ack()
		}
		e := effectBySub(sub)
		if e == nil || v > int(e.maxType) {
			return nak()
		}
		p[e.typeOff] = byte(v)
		e.setDefaults(p)
		// ACK, then the new type's default values (P§6.2).
		out := ack()
		for n := range e.params(byte(v)) {
			lo, hi := vox.Enc14(e.get(p, n))
			out = append(out, vox.Frame(vox.FnParamChange, e.id, byte(n), lo, hi))
		}
		return out
	case 0x04: // amp param
		r, ok := ampRange(int(sub))
		if !ok || !narrow || v > r.max {
			return nak()
		}
		p[offAmp+int(sub)] = byte(v)
	default:
		e := effectByID(id)
		if e == nil {
			return nak()
		}
		params := e.params(p[e.typeOff])
		if int(sub) >= len(params) {
			return nak()
		}
		r := params[sub]
		if r == unused || (sub != 0 || !e.wide0) && !narrow || v < r.min || v > r.max {
			return nak()
		}
		e.set(p, int(sub), v)
	}
	return ack()
}

// KnobTurn simulates the GAIN knob moving on the panel: it changes the Current
// Buffer and returns the unsolicited 41 04 00 vv 00 notification.
func (s *Sim) KnobTurn() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	v := (s.cur[offAmp] + 7) % 101
	s.cur[offAmp] = v
	return vox.Frame(vox.FnParamChange, 0x04, 0x00, v, 0x00)
}
