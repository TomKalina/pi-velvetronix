package voxproto

import (
	"errors"
	"fmt"
)

// Message plan for trying a whole sound live (docs/protocol.md §8.4, audition.ts). Only the Current
// Buffer changes; nothing here can write amp memory.

// AuditionEffect is one effect of a sound: all 6 (pedals) or 5 (reverb) values; indices the type
// does not use are not sent.
type AuditionEffect struct {
	On     bool  `json:"on"`
	Type   int   `json:"type"`
	Params []int `json:"params"`
}

// AuditionAmp is the amp part of a sound. There is deliberately no VOLUME.
type AuditionAmp struct {
	Model     int `json:"model"`
	Gain      int `json:"gain"`
	Treble    int `json:"treble"`
	Middle    int `json:"middle"`
	Bass      int `json:"bass"`
	Presence  int `json:"presence"`
	Resonance int `json:"resonance"`
	BrightCap int `json:"brightCap"`
	LowCut    int `json:"lowCut"`
	MidBoost  int `json:"midBoost"`
	Bias      int `json:"bias"`
	AmpClass  int `json:"ampClass"`
}

// AuditionSettings is a full sound, the same shape as a song preset.
type AuditionSettings struct {
	Amp    AuditionAmp    `json:"amp"`
	NR     int            `json:"nr"`
	Pedal1 AuditionEffect `json:"pedal1"`
	Pedal2 AuditionEffect `json:"pedal2"`
	Reverb AuditionEffect `json:"reverb"`
}

// FX returns the effect of slot.
func (s *AuditionSettings) FX(slot Slot) *AuditionEffect {
	return [3]*AuditionEffect{&s.Pedal1, &s.Pedal2, &s.Reverb}[slot]
}

// AmpFields lists the audition amp settings with their Program.Amp index, in plan order.
// Index 4 (VOLUME) is absent on purpose.
var AmpFields = []struct {
	Key   string
	Index int
	Get   func(*AuditionAmp) *int
}{
	{"gain", 0, func(a *AuditionAmp) *int { return &a.Gain }},
	{"treble", 1, func(a *AuditionAmp) *int { return &a.Treble }},
	{"middle", 2, func(a *AuditionAmp) *int { return &a.Middle }},
	{"bass", 3, func(a *AuditionAmp) *int { return &a.Bass }},
	{"presence", 5, func(a *AuditionAmp) *int { return &a.Presence }},
	{"resonance", 6, func(a *AuditionAmp) *int { return &a.Resonance }},
	{"brightCap", 7, func(a *AuditionAmp) *int { return &a.BrightCap }},
	{"lowCut", 8, func(a *AuditionAmp) *int { return &a.LowCut }},
	{"midBoost", 9, func(a *AuditionAmp) *int { return &a.MidBoost }},
	{"bias", 10, func(a *AuditionAmp) *int { return &a.Bias }},
	{"ampClass", 11, func(a *AuditionAmp) *int { return &a.AmpClass }},
}

const brightCapIndex = 7

// Hold asks the client to wait after the ACK for a burst of Count 41 ID notifications.
type Hold struct {
	ID    byte `json:"id"`
	Count int  `json:"count"`
}

// AuditionStep is one message of the plan.
type AuditionStep struct {
	Label  string // e.g. "amp GAIN", "pedal2 type"
	Frame  []byte
	Hold   *Hold
	Slot   *Slot // effect slot of a type or param step
	IsType bool
}

// ErrRange marks invalid values, rejected before anything is sent.
var ErrRange = errors.New("value out of range")

func rangeErr(err error) error { return fmt.Errorf("%w: %v", ErrRange, err) }

// AuditionPlan returns the messages for s: amp model, amp params (no VOLUME), NR, per effect its
// type then its params, then the three on/off switches. See auditionPlan in audition.ts.
func AuditionPlan(s AuditionSettings) ([]AuditionStep, error) {
	if s.Amp.Model < 0 || s.Amp.Model >= len(AmpModels) {
		return nil, rangeErr(fmt.Errorf("amp model out of range: %d", s.Amp.Model))
	}
	model := AmpModels[s.Amp.Model]
	f, _ := AmpModelFrame(s.Amp.Model)
	steps := []AuditionStep{{Label: "amp model", Frame: f, Hold: &Hold{ID: 0x04, Count: len(AmpParams)}}}

	for _, fld := range AmpFields {
		v := *fld.Get(&s.Amp)
		spec := AmpParams[fld.Index]
		if err := checkRange("amp "+spec.Name, v, spec.Min, spec.Max); err != nil {
			return nil, rangeErr(err)
		}
		if fld.Index == brightCapIndex && !model.BrightCap {
			continue
		}
		f, _ := AmpParamFrame(fld.Index, v)
		steps = append(steps, AuditionStep{Label: "amp " + spec.Name, Frame: f})
	}

	f, err := NoiseReduction(s.NR)
	if err != nil {
		return nil, rangeErr(err)
	}
	steps = append(steps, AuditionStep{Label: "nr", Frame: f})

	for _, slot := range Slots {
		fx := s.FX(slot)
		if fx.Type < 0 || fx.Type >= len(EffectParams[slot]) {
			return nil, rangeErr(fmt.Errorf("%s type out of range: %d", slot, fx.Type))
		}
		row := EffectParams[slot][fx.Type]
		if len(fx.Params) != len(row) {
			return nil, rangeErr(fmt.Errorf("%s needs %d params, got %d", slot, len(row), len(fx.Params)))
		}
		f, _ := EffectTypeFrame(slot, fx.Type)
		steps = append(steps, AuditionStep{Label: slot.String() + " type", Frame: f, Slot: &slot, IsType: true})
		for n, spec := range row {
			if spec == nil {
				continue
			}
			if err := checkRange(slot.String()+" "+spec.Name, fx.Params[n], spec.Min, spec.Max); err != nil {
				return nil, rangeErr(err)
			}
			f, _ := Param(SlotParamID[slot], byte(n), fx.Params[n])
			steps = append(steps, AuditionStep{Label: slot.String() + " " + spec.Name, Frame: f, Slot: &slot})
		}
	}

	for _, slot := range Slots {
		on := s.FX(slot).On
		state := "off"
		if on {
			state = "on"
		}
		steps = append(steps, AuditionStep{Label: slot.String() + " " + state, Frame: EffectOn(slot, on)})
	}
	return steps, nil
}
