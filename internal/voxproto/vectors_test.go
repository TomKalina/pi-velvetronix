package voxproto

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

// Vectors made by web/scripts/go-data.mjs from the TypeScript protocol library.

type jsonProgram struct {
	Raw        string `json:"raw"`
	Name       string `json:"name"`
	NR         int    `json:"nr"`
	Pedal1On   bool   `json:"pedal1On"`
	Pedal2On   bool   `json:"pedal2On"`
	ReverbOn   bool   `json:"reverbOn"`
	AmpModel   int    `json:"ampModel"`
	Amp        []int  `json:"amp"`
	Pedal1Type int    `json:"pedal1Type"`
	Pedal1     []int  `json:"pedal1"`
	Pedal2Type int    `json:"pedal2Type"`
	Pedal2     []int  `json:"pedal2"`
	ReverbType int    `json:"reverbType"`
	Reverb     []int  `json:"reverb"`
}

func (j jsonProgram) program(t *testing.T) Program {
	t.Helper()
	return Program{
		Raw: unhex(t, j.Raw), Name: j.Name, NR: j.NR, AmpModel: j.AmpModel, Amp: j.Amp,
		FX: [3]Effect{{j.Pedal1On, j.Pedal1Type, j.Pedal1}, {j.Pedal2On, j.Pedal2Type, j.Pedal2}, {j.ReverbOn, j.ReverbType, j.Reverb}},
	}
}

type vectors struct {
	Audition []struct {
		ID    string `json:"id"`
		Steps []struct {
			Label  string  `json:"label"`
			Frame  string  `json:"frame"`
			Hold   *Hold   `json:"hold"`
			Slot   *string `json:"slot"`
			IsType bool    `json:"isType"`
		} `json:"steps"`
	} `json:"audition"`
	Vtxprog []struct {
		File    string `json:"file"`
		Bytes   string `json:"bytes"`
		Entries []struct {
			Index    int              `json:"index"`
			Error    string           `json:"error"`
			Program  *jsonProgram     `json:"program"`
			Settings AuditionSettings `json:"settings"`
			Packed   string           `json:"packed"`
			Write    string           `json:"write"`
			Edits    []struct {
				ID      byte        `json:"id"`
				Sub     byte        `json:"sub"`
				Value   int         `json:"value"`
				Program jsonProgram `json:"program"`
			} `json:"edits"`
		} `json:"entries"`
	} `json:"vtxprog"`
	Parse []struct {
		Frame  string `json:"frame"`
		Parsed struct {
			Kind    Kind            `json:"kind"`
			Slot    int             `json:"slot"`
			Bank    int             `json:"bank"`
			Mode    string          `json:"mode"`
			Program json.RawMessage `json:"program"`
			ID      byte            `json:"id"`
			Sub     byte            `json:"sub"`
			Value   int             `json:"value"`
			Major   int             `json:"major"`
			Minor   int             `json:"minor"`
		} `json:"parsed"`
	} `json:"parse"`
	Msgs struct {
		DeviceInquiry         string   `json:"deviceInquiry"`
		ModeRequest           string   `json:"modeRequest"`
		CurrentProgramRequest string   `json:"currentProgramRequest"`
		ProgramRequest        []string `json:"programRequest"`
		SelectProgram         []string `json:"selectProgram"`
	} `json:"msgs"`
}

func unhex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func loadVectors(t *testing.T) vectors {
	t.Helper()
	data, err := os.ReadFile("testdata/vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var v vectors
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func songByID(t *testing.T, id string) Song {
	t.Helper()
	for _, s := range Songs {
		if s.ID == id {
			return s
		}
	}
	t.Fatalf("song %s missing", id)
	return Song{}
}

func TestAuditionPlanVectors(t *testing.T) {
	v := loadVectors(t)
	if len(v.Audition) != len(Songs) || len(Songs) < 50 {
		t.Fatalf("%d vectors, %d songs", len(v.Audition), len(Songs))
	}
	for _, a := range v.Audition {
		steps, err := AuditionPlan(songByID(t, a.ID).AuditionSettings)
		if err != nil {
			t.Fatalf("%s: %v", a.ID, err)
		}
		if len(steps) != len(a.Steps) {
			t.Fatalf("%s: %d steps, want %d", a.ID, len(steps), len(a.Steps))
		}
		for i, want := range a.Steps {
			got := steps[i]
			var slot *string
			if got.Slot != nil {
				s := got.Slot.String()
				slot = &s
			}
			if got.Label != want.Label || hex.EncodeToString(got.Frame) != want.Frame || !reflect.DeepEqual(got.Hold, want.Hold) ||
				!reflect.DeepEqual(slot, want.Slot) || got.IsType != want.IsType {
				t.Fatalf("%s step %d: got %+v %x, want %+v", a.ID, i, got, got.Frame, want)
			}
		}
	}
}

func TestVtxprogVectors(t *testing.T) {
	v := loadVectors(t)
	if len(v.Vtxprog) == 0 {
		t.Fatal("no .vtxprog vectors")
	}
	for _, f := range v.Vtxprog {
		entries, err := ParseVtxprog(unhex(t, f.Bytes))
		if err != nil {
			t.Fatalf("%s: %v", f.File, err)
		}
		if len(entries) != len(f.Entries) {
			t.Fatalf("%s: %d entries, want %d", f.File, len(entries), len(f.Entries))
		}
		for i, want := range f.Entries {
			got := entries[i]
			if got.Index != want.Index || got.Error != want.Error {
				t.Fatalf("%s #%d: got index %d error %q, want %d %q", f.File, i, got.Index, got.Error, want.Index, want.Error)
			}
			if want.Program == nil {
				continue
			}
			if p := want.Program.program(t); !reflect.DeepEqual(got.Program, p) {
				t.Fatalf("%s #%d: program\n got %+v\nwant %+v", f.File, i, got.Program, p)
			}
			if s := ProgramToSettings(got.Program); !reflect.DeepEqual(s, want.Settings) {
				t.Fatalf("%s #%d: settings %+v, want %+v", f.File, i, s, want.Settings)
			}
			w, err := WriteProgram(want.Index%8, got.Program)
			if err != nil || hex.EncodeToString(w) != want.Write {
				t.Fatalf("%s #%d: write %x %v, want %s", f.File, i, w, err, want.Write)
			}
			for _, e := range want.Edits {
				if p := ApplyParam(got.Program, e.ID, e.Sub, e.Value); !reflect.DeepEqual(p, e.Program.program(t)) {
					t.Fatalf("%s #%d: edit %02x %02x %d:\n got %+v\nwant %+v", f.File, i, e.ID, e.Sub, e.Value, p, e.Program.program(t))
				}
			}
		}
	}
}

func TestParseVectors(t *testing.T) {
	v := loadVectors(t)
	for _, c := range v.Parse {
		got := Parse(unhex(t, c.Frame))
		w := c.Parsed
		want := Parsed{Kind: w.Kind}
		switch w.Kind {
		case KindWriteCompleted, KindWriteError:
			want.Slot = w.Slot
		case KindMode, KindModeChange:
			var n int
			json.Unmarshal(w.Program, &n)
			want.Mode, want.ProgramNo = w.Mode, n
		case KindCurrentProgram, KindProgram:
			var jp jsonProgram
			if err := json.Unmarshal(w.Program, &jp); err != nil {
				t.Fatal(err)
			}
			want.Bank, want.Slot, want.Program = w.Bank, w.Slot, jp.program(t)
		case KindParam:
			want.ID, want.Sub, want.Value = w.ID, w.Sub, w.Value
		case KindIdentity:
			want.Major, want.Minor = w.Major, w.Minor
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s:\n got %+v\nwant %+v", c.Frame, got, want)
		}
	}
}

func TestMessageVectors(t *testing.T) {
	m := loadVectors(t).Msgs
	eq := func(name string, got []byte, want string) {
		t.Helper()
		if hex.EncodeToString(got) != want {
			t.Errorf("%s = %x, want %s", name, got, want)
		}
	}
	eq("deviceInquiry", DeviceInquiry(), m.DeviceInquiry)
	eq("modeRequest", ModeRequest(), m.ModeRequest)
	eq("currentProgramRequest", CurrentProgramRequest(), m.CurrentProgramRequest)
	for i, slot := range []int{0, 7} {
		f, _ := ProgramRequest(slot)
		eq("programRequest", f, m.ProgramRequest[i])
		f, _ = SelectProgram(slot)
		eq("selectProgram", f, m.SelectProgram[i])
	}
	if _, err := ProgramRequest(8); err == nil {
		t.Error("ProgramRequest(8) accepted")
	}
}

func TestEncodeRejectsBadValues(t *testing.T) {
	p := ApplyParam(firstProgram(t), 0x04, 0, 101)
	if _, err := Encode(p); err == nil {
		t.Error("GAIN 101 accepted")
	}
	p = firstProgram(t)
	p.Name = "ŽLUŤOUČKÝ"
	if _, err := Encode(p); err == nil {
		t.Error("non-ASCII name accepted")
	}
	p.Name = "12345678901234567"
	if _, err := Encode(p); err == nil {
		t.Error("17-char name accepted")
	}
}

func firstProgram(t *testing.T) Program {
	t.Helper()
	for _, f := range loadVectors(t).Vtxprog {
		for _, e := range f.Entries {
			if e.Program != nil {
				return e.Program.program(t)
			}
		}
	}
	t.Fatal("no program")
	return Program{}
}

func TestParseVtxprogErrors(t *testing.T) {
	for _, c := range []struct {
		file []byte
		code string
	}{
		{[]byte("nope"), "not_vtxprog"},
		{append([]byte("VTXPROG1000 "), make([]byte, 30)...), "bad_length"},
		{append([]byte("VTXPROG1000 "), make([]byte, 20+12*62)...), "too_many"},
	} {
		if _, err := ParseVtxprog(c.file); err != VtxprogError(c.code) {
			t.Errorf("got %v, want %s", err, c.code)
		}
	}
}
