package mcp

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/TomKalina/pi-velvetronix/internal/voxproto"
)

// The vox_* tools, the same as the former stdio server (mcp/src/server.ts) except that the
// .vtxprog tools take the file's content instead of a path.

type tool struct {
	Name        string          `json:"name"`
	Title       string          `json:"title"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
	run         func(s *Server, args json.RawMessage) (any, error)
}

func toolList() []tool { return tools }

func toolByName(name string) (tool, bool) {
	for _, t := range tools {
		if t.Name == name {
			return t, true
		}
	}
	return tool{}, false
}

const noArgs = `{"type":"object","properties":{},"additionalProperties":false}`

var tools []tool

func init() {
	tools = []tool{
		{Name: "vox_status", Title: "Amp status", InputSchema: json.RawMessage(noArgs),
			Description: "Reads the VOX Valvetronix X amp: mode, active program, the full current sound (Current Buffer) and the names of the 8 user programs A1..B4.",
			run: func(s *Server, args json.RawMessage) (any, error) {
				if err := decode(args, &struct{}{}); err != nil {
					return nil, err
				}
				return s.amp.use(func(c *voxproto.Client) (any, error) { return describeState(c.State()), nil })
			}},
		{Name: "vox_catalog", Title: "Amp models and effects", InputSchema: json.RawMessage(noArgs),
			Description: "Lists amp models, effect types per slot and their params with ranges. Knobs are 0..100 (shown on the amp as 0.0..10.0), SPEED is in mHz, TIME/PRE DELAY in ms. Needs no amp connection.",
			run:         func(*Server, json.RawMessage) (any, error) { return catalog(), nil }},
		{Name: "vox_set_sound", Title: "Set sound live", InputSchema: json.RawMessage(soundSchema),
			Description: "Sets a sound in the amp live (Current Buffer only, nothing is saved, program VOLUME is never touched). Every field is optional and merges into the current sound. When an effect type changes, params not given get neutral defaults (knobs 50).",
			run:         runSetSound},
		{Name: "vox_list_songs", Title: "List song presets",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"}},"additionalProperties":false}`),
			Description: "Lists the song presets built into pi-velvetronix (the Písničky tab), including the Tone Room presets from .vtxprog files (genre \"tone-room\"). Optional case-insensitive search in title, artist and genre.",
			run:         runListSongs},
		{Name: "vox_play_song", Title: "Play a song preset",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"id":{"type":"string","description":"Song id from vox_list_songs."}},"required":["id"],"additionalProperties":false}`),
			Description: "Sets one of the built-in song presets live (same as Zahrát in the UI; nothing is saved).",
			run:         runPlaySong},
		{Name: "vox_read_vtxprog", Title: "Read a .vtxprog file",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"data":{"type":"string","description":"The whole .vtxprog file, base64."}},"required":["data"],"additionalProperties":false}`),
			Description: "Lists the programs in a Tone Room bank file (.vtxprog), given as base64, with their full settings. Needs no amp connection.",
			run:         runReadVtxprog},
		{Name: "vox_play_vtxprog", Title: "Play a program from a .vtxprog file",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"data":{"type":"string","description":"The whole .vtxprog file, base64."},"index":{"type":"integer","minimum":0,"description":"Program index from vox_read_vtxprog (0-based)."}},"required":["data","index"],"additionalProperties":false}`),
			Description: "Sets one program of a Tone Room bank file (.vtxprog), given as base64, live (nothing is saved, program VOLUME is not sent).",
			run:         runPlayVtxprog},
		{Name: "vox_select_program", Title: "Select program",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"slot":{"type":"string","description":"A1..A4 or B1..B4."}},"required":["slot"],"additionalProperties":false}`),
			Description: "Switches the amp to user program A1..B4. Live edits that were not saved are lost.",
			run:         runSelect},
		{Name: "vox_save_program", Title: "Save program",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"slot":{"type":"string","description":"A1..A4 or B1..B4."},"name":{"type":"string","maxLength":16,"pattern":"^[\\x20-\\x7e]*$","description":"Up to 16 ASCII characters."}},"required":["slot","name"],"additionalProperties":false}`),
			Description: "Writes the current sound into user program A1..B4 under a new name, OVERWRITING what is stored there. Use only when the user explicitly asked to save into that slot.",
			run:         runSave},
		{Name: "vox_disconnect", Title: "Release control", InputSchema: json.RawMessage(noArgs),
			Description: "Closes the controller connection at once (it also closes by itself after 8 s idle).",
			run: func(s *Server, args json.RawMessage) (any, error) {
				if err := decode(args, &struct{}{}); err != nil {
					return nil, err
				}
				s.amp.close()
				return obj{{"disconnected", true}}, nil
			}},
	}
}

// decode parses tool arguments strictly: unknown fields are an error.
func decode(args json.RawMessage, v any) error {
	d := json.NewDecoder(bytes.NewReader(args))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return userError("Invalid arguments: " + err.Error())
	}
	return nil
}

// --- ordered JSON objects -----------------------------------------------------------------------

type field struct {
	K string
	V any
}

// obj is a JSON object that keeps its key order.
type obj []field

func (o obj) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, f := range o {
		if i > 0 {
			b.WriteByte(',')
		}
		k, _ := json.Marshal(f.K)
		v, err := json.Marshal(f.V)
		if err != nil {
			return nil, err
		}
		b.Write(k)
		b.WriteByte(':')
		b.Write(v)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// --- naming -------------------------------------------------------------------------------------

func norm(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// idOrName is a JSON number (id) or string (name).
type idOrName struct {
	id   *int
	name string
}

func (v *idOrName) UnmarshalJSON(b []byte) error {
	if len(b) > 0 && b[0] == '"' {
		return json.Unmarshal(b, &v.name)
	}
	var n int
	if err := json.Unmarshal(b, &n); err != nil {
		return fmt.Errorf("expected an integer id or a name")
	}
	v.id = &n
	return nil
}

func byName[T any](what string, list []T, v idOrName, name func(T) string) (int, error) {
	if v.id != nil {
		if *v.id < 0 || *v.id >= len(list) {
			return 0, userError(fmt.Sprintf("%s %d does not exist (0..%d).", what, *v.id, len(list)-1))
		}
		return *v.id, nil
	}
	for i, x := range list {
		if norm(name(x)) == norm(v.name) {
			return i, nil
		}
	}
	names := make([]string, len(list))
	for i, x := range list {
		names[i] = name(x)
	}
	return 0, userError(fmt.Sprintf("Unknown %s %q. Options: %s.", what, v.name, strings.Join(names, ", ")))
}

func modelName(m voxproto.AmpModel) string  { return m.Name }
func typeName(t voxproto.EffectType) string { return t.Name }
func effectNames(row []*voxproto.EffectParam) []string {
	var out []string
	for _, s := range row {
		if s != nil {
			out = append(out, s.Name)
		}
	}
	return out
}

func slotIndex(name string) (int, error) {
	i := slices.Index(voxproto.UserSlotNames, strings.ToUpper(name))
	if i < 0 {
		return 0, userError(fmt.Sprintf("Unknown program slot %q. Options: %s.", name, strings.Join(voxproto.UserSlotNames, ", ")))
	}
	return i, nil
}

// defaultParam is the value of an effect param the caller did not give after a type change.
func defaultParam(spec *voxproto.EffectParam) int {
	switch spec.Unit {
	case "0-100":
		return 50
	case "ms":
		if spec.Name == "TIME" {
			return 350
		}
		return 20
	case "mHz":
		return min(spec.Max, max(spec.Min, 1000))
	}
	return 0 // bool, voice
}

// --- describing the amp -------------------------------------------------------------------------

func describeEffect(slot voxproto.Slot, e voxproto.Effect) obj {
	params := obj{}
	if e.Type >= 0 && e.Type < len(voxproto.EffectParams[slot]) {
		for n, spec := range voxproto.EffectParams[slot][e.Type] {
			if spec != nil {
				params = append(params, field{spec.Name, e.Params[n]})
			}
		}
	}
	var typ any = e.Type
	if e.Type >= 0 && e.Type < len(voxproto.SlotTypes[slot]) {
		typ = voxproto.SlotTypes[slot][e.Type].Name
	}
	return obj{{"on", e.On}, {"type", typ}, {"params", params}}
}

func nameOr(names []string, v int) any {
	if v >= 0 && v < len(names) {
		return names[v]
	}
	return v
}

func describeProgram(p voxproto.Program) obj {
	var model any = p.AmpModel
	var m *voxproto.AmpModel
	if p.AmpModel >= 0 && p.AmpModel < len(voxproto.AmpModels) {
		m = &voxproto.AmpModels[p.AmpModel]
		model = m.Name
	}
	amp := obj{{"model", model}}
	for _, spec := range voxproto.AmpParams {
		v := p.Amp[spec.Index]
		name := spec.Name
		if spec.Index == 5 && m != nil {
			name = m.ToneLabel
		}
		switch spec.Kind {
		case "bias":
			amp = append(amp, field{name, nameOr(voxproto.BiasNames, v)})
		case "class":
			amp = append(amp, field{name, nameOr(voxproto.ClassNames, v)})
		case "switch":
			amp = append(amp, field{name, map[bool]string{true: "on", false: "off"}[v != 0]})
		default:
			amp = append(amp, field{name, v})
		}
	}
	return obj{
		{"name", p.Name}, {"amp", amp}, {"nr", p.NR},
		{"pedal1", describeEffect(voxproto.Pedal1, p.FX[voxproto.Pedal1])},
		{"pedal2", describeEffect(voxproto.Pedal2, p.FX[voxproto.Pedal2])},
		{"reverb", describeEffect(voxproto.Reverb, p.FX[voxproto.Reverb])},
	}
}

func describeState(s voxproto.AmpState) obj {
	var mode, program, current any
	if s.Mode != "" {
		mode = s.Mode
	}
	if s.Program != nil {
		program = *s.Program
		if s.Mode == "user" {
			program = voxproto.UserSlotNames[*s.Program]
		}
	}
	if s.Current != nil {
		current = describeProgram(*s.Current)
	}
	slots := obj{}
	for i, n := range voxproto.UserSlotNames {
		var name any
		if s.Slots[i] != nil {
			name = s.Slots[i].Name
		}
		slots = append(slots, field{n, name})
	}
	return obj{{"mode", mode}, {"program", program}, {"current", current}, {"slots", slots}}
}

func catalog() obj {
	models := make([]obj, len(voxproto.AmpModels))
	for i, m := range voxproto.AmpModels {
		models[i] = obj{{"id", m.ID}, {"name", m.Name}, {"brightCap", m.BrightCap}, {"toneLabel", m.ToneLabel}}
	}
	params := []obj{}
	for _, p := range voxproto.AmpParams {
		if p.Index != 4 { // VOLUME
			params = append(params, obj{{"name", p.Name}, {"kind", p.Kind}, {"min", p.Min}, {"max", p.Max}})
		}
	}
	effects := obj{}
	for _, slot := range voxproto.Slots {
		types := make([]obj, len(voxproto.SlotTypes[slot]))
		for i, t := range voxproto.SlotTypes[slot] {
			ps := []obj{}
			for _, spec := range voxproto.EffectParams[slot][t.ID] {
				if spec != nil {
					ps = append(ps, obj{{"name", spec.Name}, {"unit", spec.Unit}, {"min", spec.Min}, {"max", spec.Max}})
				}
			}
			types[i] = obj{{"id", t.ID}, {"name", t.Name}, {"params", ps}}
		}
		effects = append(effects, field{slot.String(), types})
	}
	return obj{
		{"ampModels", models}, {"ampParams", params}, {"bias", voxproto.BiasNames}, {"ampClass", voxproto.ClassNames},
		{"effects", effects}, {"notes", "Program VOLUME and the Power Level knob are not controllable here."},
	}
}

// --- set_sound ----------------------------------------------------------------------------------

// onOff is a JSON boolean or 0/1.
type onOff int

func (v *onOff) UnmarshalJSON(b []byte) error {
	switch string(b) {
	case "true", "1":
		*v = 1
	case "false", "0":
		*v = 0
	default:
		return fmt.Errorf("expected true/false or 0/1")
	}
	return nil
}

type ampInput struct {
	Model     *idOrName `json:"model"`
	Gain      *int      `json:"gain"`
	Treble    *int      `json:"treble"`
	Middle    *int      `json:"middle"`
	Bass      *int      `json:"bass"`
	Presence  *int      `json:"presence"`
	Resonance *int      `json:"resonance"`
	BrightCap *onOff    `json:"brightCap"`
	LowCut    *onOff    `json:"lowCut"`
	MidBoost  *onOff    `json:"midBoost"`
	Bias      *string   `json:"bias"`
	AmpClass  *string   `json:"ampClass"`
}

type effectInput struct {
	On     *bool          `json:"on"`
	Type   *idOrName      `json:"type"`
	Params map[string]int `json:"params"`
}

type soundInput struct {
	Amp    *ampInput    `json:"amp"`
	NR     *int         `json:"nr"`
	Pedal1 *effectInput `json:"pedal1"`
	Pedal2 *effectInput `json:"pedal2"`
	Reverb *effectInput `json:"reverb"`
}

const knobSchema = `{"type":"integer","minimum":0,"maximum":100}`
const onOffSchema = `{"anyOf":[{"type":"boolean"},{"type":"integer","enum":[0,1]}]}`
const effectSchema = `{"type":"object","properties":{` +
	`"on":{"type":"boolean"},` +
	`"type":{"anyOf":[{"type":"integer"},{"type":"string"}],"description":"Effect type id or name, e.g. \"FUZZ\"."},` +
	`"params":{"type":"object","additionalProperties":{"type":"integer"},"description":"Params by name as listed by vox_catalog, e.g. {\"DRIVE\": 80, \"TONE\": 40}."}` +
	`},"additionalProperties":false}`

var soundSchema = `{"type":"object","properties":{` +
	`"amp":{"type":"object","properties":{` +
	`"model":{"anyOf":[{"type":"integer"},{"type":"string"}],"description":"Amp model id or amp's own name, e.g. \"BRIT 800\"."},` +
	`"gain":` + knobSchema + `,"treble":` + knobSchema + `,"middle":` + knobSchema + `,"bass":` + knobSchema + `,` +
	`"presence":{"type":"integer","minimum":0,"maximum":100,"description":"PRESENCE, or TONE on VOX AC30 / AC30TB."},` +
	`"resonance":` + knobSchema + `,"brightCap":` + onOffSchema + `,"lowCut":` + onOffSchema + `,"midBoost":` + onOffSchema + `,` +
	`"bias":{"type":"string","enum":["OFF","COLD","HOT"]},"ampClass":{"type":"string","enum":["A","A/B"]}` +
	`},"additionalProperties":false},` +
	`"nr":{"type":"integer","minimum":0,"maximum":100,"description":"Noise reduction 0..100."},` +
	`"pedal1":` + effectSchema + `,"pedal2":` + effectSchema + `,"reverb":` + effectSchema +
	`},"additionalProperties":false}`

func (a *ampInput) value(key string) (*int, error) {
	if a == nil {
		return nil, nil
	}
	pick := func(v *onOff) *int {
		if v == nil {
			return nil
		}
		n := int(*v)
		return &n
	}
	named := func(v *string, names []string, what string) (*int, error) {
		if v == nil {
			return nil, nil
		}
		i := slices.Index(names, *v)
		if i < 0 {
			return nil, userError(fmt.Sprintf("Unknown %s %q. Options: %s.", what, *v, strings.Join(names, ", ")))
		}
		return &i, nil
	}
	switch key {
	case "gain":
		return a.Gain, nil
	case "treble":
		return a.Treble, nil
	case "middle":
		return a.Middle, nil
	case "bass":
		return a.Bass, nil
	case "presence":
		return a.Presence, nil
	case "resonance":
		return a.Resonance, nil
	case "brightCap":
		return pick(a.BrightCap), nil
	case "lowCut":
		return pick(a.LowCut), nil
	case "midBoost":
		return pick(a.MidBoost), nil
	case "bias":
		return named(a.Bias, voxproto.BiasNames, "bias")
	case "ampClass":
		return named(a.AmpClass, voxproto.ClassNames, "amp class")
	}
	return nil, nil
}

// mergeSound merges a partial sound into the current program. Program VOLUME is never part of it.
func mergeSound(cur voxproto.Program, in soundInput) (voxproto.AuditionSettings, error) {
	var out voxproto.AuditionSettings
	out.Amp.Model = cur.AmpModel
	if in.Amp != nil && in.Amp.Model != nil {
		m, err := byName("amp model", voxproto.AmpModels, *in.Amp.Model, modelName)
		if err != nil {
			return out, err
		}
		out.Amp.Model = m
	}
	for _, f := range voxproto.AmpFields {
		v, err := in.Amp.value(f.Key)
		if err != nil {
			return out, err
		}
		if v == nil {
			v = &cur.Amp[f.Index]
		}
		*f.Get(&out.Amp) = *v
	}
	out.NR = cur.NR
	if in.NR != nil {
		out.NR = *in.NR
	}
	for i, e := range []*effectInput{in.Pedal1, in.Pedal2, in.Reverb} {
		slot := voxproto.Slots[i]
		fx, err := mergeEffect(slot, cur.FX[slot], e)
		if err != nil {
			return out, err
		}
		*out.FX(slot) = fx
	}
	return out, nil
}

func mergeEffect(slot voxproto.Slot, cur voxproto.Effect, in *effectInput) (voxproto.AuditionEffect, error) {
	if in == nil {
		in = &effectInput{}
	}
	typ := cur.Type
	if in.Type != nil {
		t, err := byName(slot.String()+" type", voxproto.SlotTypes[slot], *in.Type, typeName)
		if err != nil {
			return voxproto.AuditionEffect{}, err
		}
		typ = t
	}
	if typ < 0 || typ >= len(voxproto.EffectParams[slot]) {
		return voxproto.AuditionEffect{}, userError(fmt.Sprintf("%s type %d is unknown; set a type.", slot, typ))
	}
	row := voxproto.EffectParams[slot][typ]
	given := map[string]int{}
	for k, v := range in.Params {
		given[norm(k)] = v
	}
	for k := range given {
		if !slices.ContainsFunc(row, func(s *voxproto.EffectParam) bool { return s != nil && norm(s.Name) == k }) {
			return voxproto.AuditionEffect{}, userError(fmt.Sprintf("%s %s has no param %q. Params: %s.",
				slot, voxproto.SlotTypes[slot][typ].Name, k, strings.Join(effectNames(row), ", ")))
		}
	}
	params := make([]int, len(row))
	for n, spec := range row {
		switch v, ok := given[norm(nameOf(spec))]; {
		case spec == nil:
		case ok:
			params[n] = v
		case typ == cur.Type:
			params[n] = cur.Params[n]
		default:
			params[n] = defaultParam(spec)
		}
	}
	on := cur.On
	if in.On != nil {
		on = *in.On
	}
	return voxproto.AuditionEffect{On: on, Type: typ, Params: params}, nil
}

func nameOf(s *voxproto.EffectParam) string {
	if s == nil {
		return ""
	}
	return s.Name
}

func runSetSound(s *Server, args json.RawMessage) (any, error) {
	var in soundInput
	if err := decode(args, &in); err != nil {
		return nil, err
	}
	return s.amp.use(func(c *voxproto.Client) (any, error) {
		cur := c.State().Current
		if cur == nil {
			return nil, userError("The current program is not loaded; try again.")
		}
		settings, err := mergeSound(*cur, in)
		if err != nil {
			return nil, err
		}
		res, err := c.Audition(settings)
		if err != nil {
			return nil, err
		}
		var now any
		if p := c.State().Current; p != nil {
			now = describeProgram(*p)
		}
		return obj{{"result", res}, {"current", now}}, nil
	})
}

// --- songs --------------------------------------------------------------------------------------

func songSummary(s voxproto.Song) obj {
	return obj{{"id", s.ID}, {"title", s.Title}, {"artist", s.Artist}, {"year", s.Year}, {"genre", s.Genre}}
}

func runListSongs(_ *Server, args json.RawMessage) (any, error) {
	var in struct {
		Query string `json:"query"`
	}
	if err := decode(args, &in); err != nil {
		return nil, err
	}
	q := norm(in.Query)
	out := []obj{}
	for _, song := range voxproto.Songs {
		if q == "" || strings.Contains(norm(song.Title+" "+song.Artist+" "+song.Genre), q) {
			out = append(out, songSummary(song))
		}
	}
	return out, nil
}

func runPlaySong(s *Server, args json.RawMessage) (any, error) {
	var in struct {
		ID string `json:"id"`
	}
	if err := decode(args, &in); err != nil {
		return nil, err
	}
	i := slices.IndexFunc(voxproto.Songs, func(x voxproto.Song) bool { return x.ID == in.ID })
	if i < 0 {
		return nil, userError(fmt.Sprintf("Unknown song id %q.", in.ID))
	}
	song := voxproto.Songs[i]
	return s.amp.use(func(c *voxproto.Client) (any, error) {
		res, err := c.Audition(song.AuditionSettings)
		if err != nil {
			return nil, err
		}
		return obj{{"song", append(songSummary(song), field{"description", song.Description})}, {"result", res}}, nil
	})
}

// --- .vtxprog -----------------------------------------------------------------------------------

func readVtxprog(data string) ([]voxproto.VtxprogEntry, error) {
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(data))
	if err != nil {
		return nil, userError("data is not valid base64.")
	}
	entries, err := voxproto.ParseVtxprog(b)
	if err != nil {
		var ve voxproto.VtxprogError
		if errors.As(err, &ve) {
			return nil, userError(fmt.Sprintf("Not a usable .vtxprog file: %s.", string(ve)))
		}
		return nil, err
	}
	return entries, nil
}

func runReadVtxprog(_ *Server, args json.RawMessage) (any, error) {
	var in struct {
		Data string `json:"data"`
	}
	if err := decode(args, &in); err != nil {
		return nil, err
	}
	entries, err := readVtxprog(in.Data)
	if err != nil {
		return nil, err
	}
	out := make([]obj, len(entries))
	for i, e := range entries {
		if e.Error != "" {
			out[i] = obj{{"index", e.Index}, {"error", e.Error}}
			continue
		}
		out[i] = append(obj{{"index", e.Index}}, describeProgram(e.Program)...)
	}
	return out, nil
}

func runPlayVtxprog(s *Server, args json.RawMessage) (any, error) {
	var in struct {
		Data  string `json:"data"`
		Index *int   `json:"index"`
	}
	if err := decode(args, &in); err != nil {
		return nil, err
	}
	if in.Index == nil {
		return nil, userError("index is required.")
	}
	entries, err := readVtxprog(in.Data)
	if err != nil {
		return nil, err
	}
	i := slices.IndexFunc(entries, func(e voxproto.VtxprogEntry) bool { return e.Index == *in.Index })
	if i < 0 {
		return nil, userError(fmt.Sprintf("The file has no program %d.", *in.Index))
	}
	e := entries[i]
	if e.Error != "" {
		return nil, userError(fmt.Sprintf("Program %d has a value the amp does not accept: %s", e.Index, e.Error))
	}
	return s.amp.use(func(c *voxproto.Client) (any, error) {
		res, err := c.Audition(voxproto.ProgramToSettings(e.Program))
		if err != nil {
			return nil, err
		}
		return obj{{"program", e.Program.Name}, {"result", res}}, nil
	})
}

// --- programs -----------------------------------------------------------------------------------

func runSelect(s *Server, args json.RawMessage) (any, error) {
	var in struct {
		Slot string `json:"slot"`
	}
	if err := decode(args, &in); err != nil {
		return nil, err
	}
	slot, err := slotIndex(in.Slot)
	if err != nil {
		return nil, err
	}
	return s.amp.use(func(c *voxproto.Client) (any, error) {
		if err := c.Select(slot); err != nil {
			return nil, err
		}
		return describeState(c.State()), nil
	})
}

func runSave(s *Server, args json.RawMessage) (any, error) {
	var in struct {
		Slot string  `json:"slot"`
		Name *string `json:"name"`
	}
	if err := decode(args, &in); err != nil {
		return nil, err
	}
	slot, err := slotIndex(in.Slot)
	if err != nil {
		return nil, err
	}
	if in.Name == nil {
		return nil, userError("name is required.")
	}
	name := *in.Name
	if len([]rune(name)) > voxproto.NameLen || strings.ContainsFunc(name, func(r rune) bool { return r < 0x20 || r > 0x7e }) {
		return nil, userError("name must be up to 16 ASCII characters.")
	}
	return s.amp.use(func(c *voxproto.Client) (any, error) {
		st := c.State()
		if st.Current == nil {
			return nil, userError("The current program is not loaded; try again.")
		}
		var overwritten any
		if st.Slots[slot] != nil {
			overwritten = st.Slots[slot].Name
		}
		p := st.Current.Clone()
		p.Name = name
		check, err := c.Save(slot, p)
		if err != nil {
			return nil, err
		}
		return obj{{"slot", voxproto.UserSlotNames[slot]}, {"name", name}, {"overwritten", overwritten}, {"check", check}}, nil
	})
}
