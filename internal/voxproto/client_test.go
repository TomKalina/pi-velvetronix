package voxproto

import (
	"errors"
	"testing"
	"time"

	"github.com/TomKalina/pi-velvetronix/cmd/fakeamp/sim"
	"github.com/TomKalina/pi-velvetronix/internal/vox"
)

// simConn answers every frame with the simulator, like the bridge does with a real amp.
type simConn struct {
	amp   *sim.Sim
	in    []Frame
	sent  [][]byte
	saves []bool
	// drop makes the amp ignore frames with this function byte (timeouts).
	drop int
	// sendErr fails every Send.
	sendErr error
}

func newSimConn() *simConn { return &simConn{amp: sim.New(), drop: -2} }

func (c *simConn) Send(save bool, f []byte) error {
	if c.sendErr != nil {
		return c.sendErr
	}
	c.sent = append(c.sent, f)
	c.saves = append(c.saves, save)
	if FuncOf(f) == c.drop {
		return nil
	}
	for _, r := range c.amp.Handle(f) {
		c.in = append(c.in, Frame{Data: r})
	}
	return nil
}

func (c *simConn) Recv(time.Duration) (Frame, error) {
	if len(c.in) == 0 {
		return Frame{}, ErrRecvTimeout
	}
	f := c.in[0]
	c.in = c.in[1:]
	return f, nil
}

func ready(t *testing.T) (*Client, *simConn) {
	t.Helper()
	conn := newSimConn()
	c := NewClient(conn)
	if err := c.ReadAll(); err != nil {
		t.Fatal(err)
	}
	return c, conn
}

func TestClientReadAll(t *testing.T) {
	c, _ := ready(t)
	s := c.State()
	if !s.Ready || s.Mode != "user" || s.Program == nil || *s.Program != 0 || s.Current == nil || s.Current.Name != "FAKE A1" {
		t.Fatalf("state %+v", s)
	}
	for i, p := range s.Slots {
		if p == nil || p.Name != "FAKE "+UserSlotNames[i] {
			t.Fatalf("slot %d: %+v", i, p)
		}
	}
}

func TestClientAuditionSong(t *testing.T) {
	c, conn := ready(t)
	song := Songs[0]
	res, err := c.Audition(song.AuditionSettings)
	if err != nil {
		t.Fatal(err)
	}
	if res.Done != res.Total || len(res.Failed) != 0 || res.Aborted != nil {
		t.Fatalf("result %+v", res)
	}
	// The model must match what the amp holds now.
	if err := c.readCurrent(); err != nil {
		t.Fatal(err)
	}
	if got := ProgramToSettings(*c.State().Current); got.Amp != song.Amp || got.NR != song.NR {
		t.Fatalf("amp %+v, want %+v", got.Amp, song.Amp)
	}
	cur := ProgramToSettings(*c.State().Current)
	for _, slot := range Slots {
		got := cur.FX(slot)
		want := song.FX(slot)
		if got.On != want.On || got.Type != want.Type {
			t.Errorf("%s: %+v, want %+v", slot, got, want)
		}
		for n, spec := range EffectParams[slot][want.Type] {
			if spec != nil && got.Params[n] != want.Params[n] {
				t.Errorf("%s %s = %d, want %d", slot, spec.Name, got.Params[n], want.Params[n])
			}
		}
	}
	for i, save := range conn.saves {
		if save {
			t.Fatalf("frame %d sent with the save flag: %x", i, conn.sent[i])
		}
	}
}

func TestClientAuditionRejectsBadValues(t *testing.T) {
	c, conn := ready(t)
	n := len(conn.sent)
	s := Songs[0].AuditionSettings
	s.Amp.Gain = 101
	if _, err := c.Audition(s); !errors.Is(err, ErrRange) {
		t.Fatalf("err %v", err)
	}
	if len(conn.sent) != n {
		t.Fatal("frames sent for invalid settings")
	}
}

func TestClientAuditionTimeoutAborts(t *testing.T) {
	c, conn := ready(t)
	conn.drop = vox.FnParamChange
	res, err := c.Audition(Songs[0].AuditionSettings)
	if err != nil {
		t.Fatal(err)
	}
	if res.Aborted == nil || *res.Aborted != "timeout" || res.Done != 0 || len(res.Failed) != 1 {
		t.Fatalf("result %+v", res)
	}
}

func TestClientSelectAndSave(t *testing.T) {
	c, conn := ready(t)
	if err := c.Select(5); err != nil {
		t.Fatal(err)
	}
	if s := c.State(); *s.Program != 5 || s.Current.Name != "FAKE B2" {
		t.Fatalf("after select: %+v", s)
	}
	p := c.State().Current.Clone()
	p.Name = "MCP TEST"
	check, err := c.Save(2, p)
	if err != nil || check != "verified" {
		t.Fatalf("save: %s %v", check, err)
	}
	if s := c.State(); s.Slots[2].Name != "MCP TEST" || *s.Program != 2 || s.Current.Name != "MCP TEST" {
		t.Fatalf("after save: slot %q program %d current %q", s.Slots[2].Name, *s.Program, s.Current.Name)
	}
	saves := 0
	for i, save := range conn.saves {
		if save {
			saves++
			if FuncOf(conn.sent[i]) != vox.FnProgramDump {
				t.Fatalf("save flag on %x", conn.sent[i])
			}
		}
	}
	if saves != 1 {
		t.Fatalf("%d frames with the save flag", saves)
	}
}

func TestClientPanelAndPeerChanges(t *testing.T) {
	c, conn := ready(t)
	// GAIN turned on the panel, and another controller selecting B4 and changing NR.
	conn.in = append(conn.in, Frame{Data: conn.amp.KnobTurn()})
	sel, _ := SelectProgram(7)
	conn.amp.Handle(sel)
	nr, _ := NoiseReduction(42)
	conn.amp.Handle(nr)
	conn.in = append(conn.in, Frame{Echo: true, Data: sel}, Frame{Echo: true, Data: nr})
	if err := c.Sync(); err != nil {
		t.Fatal(err)
	}
	s := c.State()
	if *s.Program != 7 || s.Current.Name != "FAKE B4" || s.Current.NR != 42 {
		t.Fatalf("state: program %d current %q nr %d", *s.Program, s.Current.Name, s.Current.NR)
	}
}

func TestClientSendError(t *testing.T) {
	c, conn := ready(t)
	conn.sendErr = errors.New("amp_disconnected")
	if err := c.Select(1); codeOf(err) != "amp_disconnected" {
		t.Fatalf("err %v", err)
	}
}
