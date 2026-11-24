package voxproto

// Tone Room program bank files (.vtxprog), vtxprog.ts: a 32-byte prelude, then 1..11 program
// structs of 62 bytes. Format notes: vox-amp-librarian doc/fileformats/vtxprog.md (MIT,
// Copyright (c) 2023 Tobias Marstaller).

const (
	vtxprogMagic   = "VTXPROG1000 "
	preludeLen     = 32
	maxVtxPrograms = 11
)

// VtxprogError is "not_vtxprog" (no prelude), "bad_length" (not prelude + n x 62 bytes) or
// "too_many" (over 11 programs).
type VtxprogError string

func (e VtxprogError) Error() string { return "invalid .vtxprog file: " + string(e) }

// VtxprogEntry is one program of a file: decoded, or why it is unusable (Error, a value out of range).
type VtxprogEntry struct {
	Index   int
	Program Program
	Error   string
}

// ParseVtxprog parses a .vtxprog file.
func ParseVtxprog(file []byte) ([]VtxprogEntry, error) {
	if len(file) < len(vtxprogMagic) || string(file[:len(vtxprogMagic)]) != vtxprogMagic {
		return nil, VtxprogError("not_vtxprog")
	}
	body := len(file) - preludeLen
	if body < StructLen || body%StructLen != 0 {
		return nil, VtxprogError("bad_length")
	}
	n := body / StructLen
	if n > maxVtxPrograms {
		return nil, VtxprogError("too_many")
	}
	out := make([]VtxprogEntry, 0, n)
	for i := range n {
		at := preludeLen + i*StructLen
		p, _ := Decode(file[at : at+StructLen]) // always 62 bytes
		if _, err := Encode(p); err != nil {
			out = append(out, VtxprogEntry{Index: i, Error: err.Error()})
			continue
		}
		out = append(out, VtxprogEntry{Index: i, Program: p})
	}
	return out, nil
}

// ProgramToSettings returns the program as a live sound, without its VOLUME.
func ProgramToSettings(p Program) AuditionSettings {
	a := p.Amp
	fx := func(slot Slot) AuditionEffect {
		e := p.FX[slot]
		return AuditionEffect{On: e.On, Type: e.Type, Params: append([]int(nil), e.Params...)}
	}
	return AuditionSettings{
		Amp: AuditionAmp{
			Model: p.AmpModel, Gain: a[0], Treble: a[1], Middle: a[2], Bass: a[3], Presence: a[5], Resonance: a[6],
			BrightCap: a[7], LowCut: a[8], MidBoost: a[9], Bias: a[10], AmpClass: a[11],
		},
		NR:     p.NR,
		Pedal1: fx(Pedal1),
		Pedal2: fx(Pedal2),
		Reverb: fx(Reverb),
	}
}
