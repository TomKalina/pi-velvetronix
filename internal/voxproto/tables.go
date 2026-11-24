// Package voxproto is the Go port of the browser's VOX protocol library (web/src/protocol) for the
// MCP endpoint (design §7.1): frame builders and parser, the program struct, the audition plan,
// .vtxprog files and a request/response client. The tables and song presets are exported from the
// TypeScript sources by web/scripts/go-data.mjs, and the tests replay vectors made by the same script.
package voxproto

import (
	_ "embed"
	"encoding/json"
)

//go:embed data/tables.json
var tablesJSON []byte

// AmpModel is one amp model (tables.ts AmpModel).
type AmpModel struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	BrightCap bool   `json:"brightCap"` // the BRIGHT CAP switch exists for this model
	ToneLabel string `json:"toneLabel"` // label of amp param 5: "TONE" or "PRESENCE"
}

// AmpParam is one amp parameter; Index is the SUB in 41 04 nn and the index into Program.Amp.
type AmpParam struct {
	Index int    `json:"index"`
	Name  string `json:"name"`
	Kind  string `json:"kind"` // knob, switch, bias, class
	Min   int    `json:"min"`
	Max   int    `json:"max"`
}

// EffectType is one effect type of a slot.
type EffectType struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// EffectParam is one effect parameter. Unit: "0-100", "mHz", "ms", "bool", "voice".
type EffectParam struct {
	Name string `json:"name"`
	Unit string `json:"unit"`
	Min  int    `json:"min"`
	Max  int    `json:"max"`
}

// Slot is an effect slot.
type Slot int

const (
	Pedal1 Slot = iota
	Pedal2
	Reverb
)

// Slots lists the effect slots in protocol order.
var Slots = [3]Slot{Pedal1, Pedal2, Reverb}

var slotNames = [3]string{"pedal1", "pedal2", "reverb"}

func (s Slot) String() string { return slotNames[s] }

var (
	AmpModels []AmpModel
	AmpParams []AmpParam
	// SlotTypes[slot] lists the effect types of the slot.
	SlotTypes [3][]EffectType
	// EffectParams[slot][type][n] is param n (= live SUB) of that type; nil = index unused.
	EffectParams  [3][][]*EffectParam
	BiasNames     []string
	ClassNames    []string
	ModeNames     []string
	UserSlotNames []string
)

func init() {
	var t struct {
		AmpModels    []AmpModel   `json:"ampModels"`
		AmpParams    []AmpParam   `json:"ampParams"`
		Pedal1Types  []EffectType `json:"pedal1Types"`
		Pedal2Types  []EffectType `json:"pedal2Types"`
		ReverbTypes  []EffectType `json:"reverbTypes"`
		EffectParams struct {
			Pedal1 [][]*EffectParam `json:"pedal1"`
			Pedal2 [][]*EffectParam `json:"pedal2"`
			Reverb [][]*EffectParam `json:"reverb"`
		} `json:"effectParams"`
		BiasNames     []string `json:"biasNames"`
		ClassNames    []string `json:"classNames"`
		ModeNames     []string `json:"modeNames"`
		UserSlotNames []string `json:"userSlotNames"`
	}
	if err := json.Unmarshal(tablesJSON, &t); err != nil {
		panic("voxproto: data/tables.json: " + err.Error()) // embedded, generated file
	}
	AmpModels, AmpParams = t.AmpModels, t.AmpParams
	SlotTypes = [3][]EffectType{t.Pedal1Types, t.Pedal2Types, t.ReverbTypes}
	EffectParams = [3][][]*EffectParam{t.EffectParams.Pedal1, t.EffectParams.Pedal2, t.EffectParams.Reverb}
	BiasNames, ClassNames, ModeNames, UserSlotNames = t.BiasNames, t.ClassNames, t.ModeNames, t.UserSlotNames
}
