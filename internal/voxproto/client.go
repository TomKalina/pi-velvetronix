package voxproto

import (
	"bytes"
	"errors"
	"time"

	"github.com/TomKalina/pi-velvetronix/internal/vox"
)

// Request/response engine, docs/protocol.md §4.1, §6.2, §7, §8, §9 (client.ts). Unlike the browser
// client it is synchronous: one caller at a time, and frames that arrive between calls wait in
// the connection until the next call (Sync) reads them.

// Frame is a frame from the bridge: the amp's, or (Echo) one another controller sent and the amp ACKed.
type Frame struct {
	Echo bool
	Data []byte
}

var (
	// ErrRecvTimeout is returned by Conn.Recv when no frame arrived in time.
	ErrRecvTimeout = errors.New("recv timeout")
	// ErrFramesLost is returned by Conn.Recv once after frames were dropped: the model may be stale.
	ErrFramesLost = errors.New("frames lost")
)

// Conn is a controller connection to the amp. Send errors carry the bridge error code as text
// ("amp_disconnected", "forbidden", ...). Recv returns ErrRecvTimeout, ErrFramesLost or a final
// error (the connection is gone).
type Conn interface {
	Send(save bool, frame []byte) error
	Recv(wait time.Duration) (Frame, error)
}

// Error is a failed request. Code: "timeout", "nak" (24), "format_error" (26), a bridge error code
// ("amp_disconnected", "forbidden", ...) or "closed".
type Error struct{ Code string }

func (e *Error) Error() string { return "VOX request failed: " + e.Code }

func codeOf(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return "send_failed"
}

// AmpState is the client's model of the amp.
type AmpState struct {
	Mode    string // "user", "preset", "manual"; "" before the first read
	Program *int   // active user/preset program; nil in manual mode or before the first read
	Current *Program
	Slots   [UserSlots]*Program
	Ready   bool
}

// AuditionFailure is a step the amp did not confirm.
type AuditionFailure struct {
	Step string `json:"step"`
	Code string `json:"code"`
}

// VolumeChange is the program VOLUME the amp changed itself during an audition.
type VolumeChange struct {
	From int `json:"from"`
	To   int `json:"to"`
}

// AuditionResult reports an audition.
type AuditionResult struct {
	Total   int               `json:"total"`
	Done    int               `json:"done"`
	Failed  []AuditionFailure `json:"failed"`
	Aborted *string           `json:"aborted"`
	Volume  *VolumeChange     `json:"volume"`
}

const (
	burstHold   = 200 * time.Millisecond // max wait for the default-value burst (P§6.2)
	volumeIndex = 4
)

// policy returns the P§9.2 timeout and P§9.3 retries (reads once, writes and live edits never).
func policy(frame []byte) (time.Duration, int) {
	switch FuncOf(frame) {
	case vox.FnModeRequest, vox.FnCurrentProgramReq, vox.FnProgramDumpRequest:
		return 1500 * time.Millisecond, 1
	case vox.FnProgramDump, vox.FnCurrentProgram:
		return 2000 * time.Millisecond, 0
	}
	return 1000 * time.Millisecond, 0 // device inquiry, 41, 4E
}

func failure(p Parsed) error {
	switch p.Kind {
	case KindNak:
		return &Error{Code: "nak"}
	case KindFormatError:
		return &Error{Code: "format_error"}
	}
	return nil
}

// match reports whether reply p completes (err nil) or fails the request that sent frame (P§4.1).
func match(frame []byte, p Parsed) (related bool, err error) {
	switch FuncOf(frame) {
	case -1:
		return p.Kind == KindIdentity, nil
	case vox.FnModeRequest:
		if p.Kind == KindMode {
			return true, nil
		}
		if p.Kind == KindFormatError {
			return true, &Error{Code: "format_error"}
		}
		return false, nil
	case vox.FnCurrentProgramReq:
		if p.Kind == KindCurrentProgram {
			return true, nil
		}
	case vox.FnProgramDumpRequest:
		if p.Kind == KindProgram {
			return p.Bank == int(frame[7]) && p.Slot == int(frame[8]), nil
		}
	default:
		if p.Kind == KindAck {
			return true, nil
		}
	}
	err = failure(p)
	return err != nil, err
}

// typeChangeSlot returns the effect slot of a 41 03 ss tt type-change frame.
func typeChangeSlot(frame []byte) (Slot, bool) {
	if FuncOf(frame) != vox.FnParamChange || len(frame) != 12 || frame[7] != 0x03 {
		return 0, false
	}
	return slotBySub(frame[8])
}

// Client talks to the amp over a Conn. It is not safe for concurrent use.
type Client struct {
	conn  Conn
	state AmpState
	// Deferred work found while reading frames, done by settle at the end of each call.
	stale        bool // frames were lost: read everything again
	needCurrent  bool
	clearOnFail  bool // the amp reloaded its buffer: a failed re-read clears Current
	needSlots    map[int]bool
	burst        *Hold // counting the notifications of an amp burst
	burstPending int
}

// NewClient returns a client; call ReadAll before anything else.
func NewClient(conn Conn) *Client {
	return &Client{conn: conn, needSlots: map[int]bool{}}
}

// State returns the model.
func (c *Client) State() AmpState { return c.state }

// ReadAll reads mode, Current Buffer and the 8 user programs (P§8.1).
func (c *Client) ReadAll() error {
	c.state = AmpState{}
	c.stale = false
	// The inquiry is optional; the daemon already confirms identity.
	_, _ = c.request(DeviceInquiry(), false, nil)
	m, err := c.request(ModeRequest(), false, nil)
	if err != nil {
		return err
	}
	c.setMode(m.Mode, m.ProgramNo)
	if err := c.readCurrent(); err != nil {
		return err
	}
	for slot := range UserSlots {
		f, _ := ProgramRequest(slot)
		r, err := c.request(f, false, nil)
		if err != nil {
			return err
		}
		c.state.Slots[slot] = ptr(r.Program)
	}
	c.state.Ready = true
	c.needCurrent, c.clearOnFail = false, false
	clear(c.needSlots)
	return nil
}

// Sync applies what arrived since the last call (panel changes, other controllers' edits) and
// re-reads what they made stale.
func (c *Client) Sync() error {
	for {
		f, err := c.conn.Recv(0)
		if errors.Is(err, ErrRecvTimeout) {
			break
		}
		if err := c.recvError(err); err != nil {
			return err
		}
		if err == nil {
			c.handle(f, nil)
		}
	}
	return c.settle()
}

// Select makes user program slot active and re-reads the Current Buffer (P§8.2).
func (c *Client) Select(slot int) error {
	f, err := SelectProgram(slot)
	if err != nil {
		return rangeErr(err)
	}
	if _, err := c.request(f, false, nil); err != nil {
		return err
	}
	c.setMode("user", slot)
	if err := c.readCurrent(); err != nil {
		// The amp now holds another program; the old buffer must not stay editable.
		c.state.Current = nil
		return err
	}
	return c.settle()
}

// Audition applies a whole sound to the Current Buffer only (P§8.4), one ACKed 41 at a time in the
// order of AuditionPlan. A NAK is recorded and the next step sent, except that the params of an
// effect whose type change was refused are skipped. Any other failure stops the sequence. Invalid
// settings fail with ErrRange before anything is sent.
func (c *Client) Audition(s AuditionSettings) (AuditionResult, error) {
	steps, err := AuditionPlan(s)
	if err != nil {
		return AuditionResult{}, err
	}
	volumeBefore := c.volume()
	res := AuditionResult{Total: len(steps), Failed: []AuditionFailure{}}
	refused := map[Slot]bool{}
	for _, st := range steps {
		if st.Slot != nil && !st.IsType && refused[*st.Slot] {
			res.Done++
			continue
		}
		if _, err := c.request(st.Frame, false, st.Hold); err != nil {
			code := codeOf(err)
			res.Failed = append(res.Failed, AuditionFailure{Step: st.Label, Code: code})
			if code != "nak" && code != "format_error" {
				res.Aborted = &code
				break
			}
			if st.Slot != nil && st.IsType {
				refused[*st.Slot] = true
			}
		} else {
			c.applyLocal(st.Frame)
		}
		res.Done++
	}
	// VOLUME is never sent, but the amp may change it itself on a model change (P§6.2).
	if before, after := volumeBefore, c.volume(); before != nil && after != nil && *before != *after {
		res.Volume = &VolumeChange{From: *before, To: *after}
	}
	return res, c.settle()
}

func (c *Client) volume() *int {
	if c.state.Current == nil {
		return nil
	}
	return ptr(c.state.Current.Amp[volumeIndex])
}

// Save writes p into user program slot (P§8.3): 4C (save flag) -> ACK -> 4E 00 pp -> ACK ->
// 1C 00 pp -> byte compare. Result: "verified", "mismatch" or "unverified", with "-inactive" when
// the slot could not be made the active program.
func (c *Client) Save(slot int, p Program) (string, error) {
	write, err := WriteProgram(slot, p)
	if err != nil {
		return "", rangeErr(err)
	}
	want, _ := Encode(p)
	timedOut := false
	if _, err := c.request(write, true, nil); err != nil {
		// P§8.3: on a 4C timeout skip the reselect and verify by reading the slot back.
		if codeOf(err) != "timeout" {
			return "", err
		}
		timedOut = true
	}
	selected := false
	if !timedOut {
		sel, _ := SelectProgram(slot)
		if _, err := c.request(sel, false, nil); err == nil {
			c.setMode("user", slot)
			selected = true
		}
	}
	read, _ := ProgramRequest(slot)
	check := "unverified"
	if r, err := c.request(read, false, nil); err == nil {
		c.state.Slots[slot] = ptr(r.Program)
		check = "mismatch"
		if bytes.Equal(r.Program.Raw, want) {
			check = "verified"
		}
	} else if timedOut {
		// Without a 4C ACK nothing confirms the write.
		return "", err
	}
	if selected {
		// 4E reloaded the Current Buffer from the slot.
		c.needCurrent = true
	} else {
		check += "-inactive"
	}
	return check, c.settle()
}

// settle does the deferred re-reads.
func (c *Client) settle() error {
	if c.stale {
		return c.ReadAll()
	}
	for slot := range UserSlots {
		if !c.needSlots[slot] {
			continue
		}
		delete(c.needSlots, slot)
		f, _ := ProgramRequest(slot)
		if r, err := c.request(f, false, nil); err == nil {
			c.state.Slots[slot] = ptr(r.Program)
		}
	}
	if c.needCurrent {
		if err := c.readCurrent(); err != nil && c.clearOnFail {
			c.state.Current = nil
		}
	}
	return nil
}

func (c *Client) readCurrent() error {
	r, err := c.request(CurrentProgramRequest(), false, nil)
	if err != nil {
		return err
	}
	c.state.Current = ptr(r.Program)
	c.needCurrent, c.clearOnFail = false, false
	return nil
}

// recvError handles a Recv error: nil for the ones a request survives.
func (c *Client) recvError(err error) error {
	switch {
	case err == nil, errors.Is(err, ErrRecvTimeout):
		return nil
	case errors.Is(err, ErrFramesLost):
		c.stale = true
		return nil
	}
	return &Error{Code: "closed"}
}

// request sends frame and waits for its reply, handling everything else that arrives meanwhile.
func (c *Client) request(frame []byte, save bool, hold *Hold) (Parsed, error) {
	timeout, retries := policy(frame)
	for {
		if err := c.conn.Send(save, frame); err != nil {
			return Parsed{}, &Error{Code: err.Error()}
		}
		deadline := time.Now().Add(timeout)
		for {
			f, err := c.conn.Recv(time.Until(deadline))
			if errors.Is(err, ErrRecvTimeout) {
				break
			}
			if err := c.recvError(err); err != nil {
				return Parsed{}, err
			}
			if err != nil {
				continue
			}
			if reply, done, err := c.handle(f, frame); done {
				if err != nil {
					// P§8.5: after a live-edit NAK, resync the Current Buffer.
					if FuncOf(frame) == vox.FnParamChange {
						c.needCurrent = true
					}
					return Parsed{}, err
				}
				if slot, ok := typeChangeSlot(frame); ok {
					c.waitBurst(Hold{ID: SlotParamID[slot], Count: SlotParamCount[slot]})
				} else if hold != nil {
					c.waitBurst(*hold)
				}
				return reply, nil
			}
		}
		if retries == 0 {
			return Parsed{}, &Error{Code: "timeout"}
		}
		retries--
	}
}

// waitBurst holds the next request until the amp's burst of h.Count 41 h.ID notifications has
// arrived, or burstHold passed.
func (c *Client) waitBurst(h Hold) {
	c.burst, c.burstPending = &h, h.Count
	defer func() { c.burst = nil }()
	deadline := time.Now().Add(burstHold)
	for c.burstPending > 0 {
		f, err := c.conn.Recv(time.Until(deadline))
		if errors.Is(err, ErrRecvTimeout) || c.recvError(err) != nil {
			return
		}
		if err == nil {
			c.handle(f, nil)
		}
	}
}

// handle processes one incoming frame. With a request in flight (sent != nil) it reports whether
// the frame is the reply (done) and the request's outcome.
func (c *Client) handle(f Frame, sent []byte) (reply Parsed, done bool, err error) {
	if f.Echo {
		c.peerFrame(f.Data)
		return Parsed{}, false, nil
	}
	p := Parse(f.Data)
	// P§7: these never complete a pending request, even one with the same function byte.
	switch p.Kind {
	case KindParam:
		if c.state.Current != nil {
			c.state.Current = ptr(ApplyParam(*c.state.Current, p.ID, p.Sub, p.Value))
		}
		if c.burst != nil && p.ID == c.burst.ID {
			c.burstPending--
		}
		return Parsed{}, false, nil
	case KindModeChange:
		c.onModeChange(p.Mode, p.ProgramNo)
		return Parsed{}, false, nil
	case KindWriteCompleted:
		c.needSlots[p.Slot] = true
		return Parsed{}, false, nil
	}
	if sent == nil {
		return Parsed{}, false, nil // late replies and unknown frames are dropped
	}
	related, err := match(sent, p)
	return p, related, err
}

// onModeChange handles a program change made on the amp's panel.
func (c *Client) onModeChange(mode string, program int) {
	same := c.state.Mode == mode && ((mode == "manual" && c.state.Program == nil) ||
		(c.state.Program != nil && *c.state.Program == program))
	c.setMode(mode, program)
	// An echo of our own select needs no second read.
	if !same || !c.needCurrent {
		c.needCurrent, c.clearOnFail = true, true
	}
}

// peerFrame applies a frame another controller sent (and the amp ACKed): live edits, program
// selects and saves. Its reads are not echoed.
func (c *Client) peerFrame(f []byte) {
	switch fn := FuncOf(f); {
	case fn == vox.FnParamChange && len(f) == 12:
		c.applyLocal(f)
	case fn == vox.FnModeChange && len(f) == 10 && f[8] < UserSlots:
		// 4E 00 pp loads the slot into the Current Buffer.
		c.setMode("user", int(f[8]))
		c.needCurrent, c.clearOnFail = true, true
	case fn == vox.FnProgramDump:
		if p := Parse(f); p.Kind == KindProgram && p.Slot < UserSlots {
			c.state.Slots[p.Slot] = ptr(p.Program)
		}
	}
}

// applyLocal applies an ACKed 41 frame to the model.
func (c *Client) applyLocal(f []byte) {
	if c.state.Current == nil || len(f) != 12 {
		return
	}
	c.state.Current = ptr(ApplyParam(*c.state.Current, f[funcIndex+1], f[funcIndex+2], vox.Dec14(f[funcIndex+3], f[funcIndex+4])))
}

func (c *Client) setMode(mode string, program int) {
	c.state.Mode = mode
	c.state.Program = nil
	if mode != "manual" {
		c.state.Program = ptr(program)
	}
}

func ptr[T any](v T) *T { return &v }
