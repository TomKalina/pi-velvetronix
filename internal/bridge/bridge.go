// Package bridge owns the amp's rawmidi port: discovery, identity check, a single
// controlling client and a bounded write queue.
package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/TomKalina/pi-velvetronix/internal/midi"
)

var (
	ErrAmpDisconnected = errors.New("amp_disconnected")
	ErrQueueFull       = errors.New("queue_full")
	ErrUpdating        = errors.New("updating")
)

// Amp states (design §5 AmpStatus.state).
const (
	StateDisconnected = "disconnected"
	StateDetected     = "detected"
	StateConnected    = "connected"
	StateBusy         = "busy"
	StateError        = "error"
)

const (
	queueSize       = 32
	frameBufferSize = 64
	readBufferSize  = 4096
)

var deviceInquiry = []byte{0xF0, 0x7E, 0x7F, 0x06, 0x01, 0xF7}

// AmpStatus is the amp part of the HTTP status. Empty strings encode as JSON null.
type AmpStatus struct {
	State    string
	Card     string
	Name     string
	UsbID    string
	Device   string
	Identity string
	Error    string
}

func (s AmpStatus) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		State    string  `json:"state"`
		Card     *string `json:"card"`
		Name     *string `json:"name"`
		UsbID    *string `json:"usb_id"`
		Device   *string `json:"device"`
		Identity *string `json:"identity"`
		Error    *string `json:"error"`
	}{s.State, null(s.Card), null(s.Name), null(s.UsbID), null(s.Device), null(s.Identity), null(s.Error)})
}

func null(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// Bridge connects the single controlling client to the amp.
type Bridge struct {
	log      *slog.Logger
	discover func() ([]midi.Device, error)
	open     func(node string) (midi.Port, error)

	pollInterval    time.Duration
	inquiryInterval time.Duration

	mu     sync.Mutex
	status AmpStatus
	queue  chan []byte // nil while no port is open
	ctrl   *Controller
	paused bool
	subs   map[chan AmpStatus]struct{}
}

// New creates a bridge that uses the rawmidi node device if set, else the first ALSA
// card matching match.
func New(match, device string, log *slog.Logger) *Bridge {
	discover := func() ([]midi.Device, error) { return midi.FindDevices(match) }
	if device != "" {
		discover = func() ([]midi.Device, error) { return explicitDevice(device) }
	}
	return NewWithIO(discover, midi.OpenPort, log)
}

// NewWithIO creates a bridge with custom discovery and open functions (tests, fakes).
func NewWithIO(discover func() ([]midi.Device, error), open func(node string) (midi.Port, error), log *slog.Logger) *Bridge {
	return &Bridge{
		log:             log,
		discover:        discover,
		open:            open,
		pollInterval:    time.Second,
		inquiryInterval: 30 * time.Second,
		status:          AmpStatus{State: StateDisconnected},
		subs:            map[chan AmpStatus]struct{}{},
	}
}

// explicitDevice returns the given node (with card details when ALSA knows it) if it exists.
func explicitDevice(node string) ([]midi.Device, error) {
	if _, err := os.Stat(node); err != nil {
		return nil, nil
	}
	dev := midi.Device{Node: node}
	all, _ := midi.FindDevices("")
	for _, d := range all {
		if strings.HasPrefix(filepath.Base(node), "midiC"+d.Card+"D") {
			d.Node = node
			dev = d
		}
	}
	return []midi.Device{dev}, nil
}

// Run polls for the amp and serves it until ctx is done.
func (b *Bridge) Run(ctx context.Context) {
	for {
		b.attempt(ctx)
		select {
		case <-ctx.Done():
			return
		case <-time.After(b.pollInterval):
		}
	}
}

func (b *Bridge) attempt(ctx context.Context) {
	devs, err := b.discover()
	if err != nil {
		b.setStatus(AmpStatus{State: StateError, Error: err.Error()})
		return
	}
	if len(devs) == 0 {
		b.setStatus(AmpStatus{State: StateDisconnected})
		return
	}
	dev := devs[0]
	st := AmpStatus{Card: dev.Card, Name: dev.Name, UsbID: dev.UsbID, Device: dev.Node}
	port, err := b.open(dev.Node)
	switch {
	case errors.Is(err, midi.ErrBusy):
		st.State = StateBusy
		b.setStatus(st)
		return
	case err != nil:
		st.State = StateError
		st.Error = err.Error()
		b.setStatus(st)
		return
	}
	b.log.Info("amp port opened", "device", dev.Node, "card", dev.Card, "name", dev.Name)
	st.State = StateDetected
	b.serve(ctx, port, st)
}

// serve runs I/O on an open port until it fails or ctx is done.
func (b *Bridge) serve(ctx context.Context, port midi.Port, st AmpStatus) {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)

	queue := make(chan []byte, queueSize)
	queue <- deviceInquiry
	b.mu.Lock()
	b.queue = queue
	b.mu.Unlock()
	b.setStatus(st)

	var wg sync.WaitGroup
	wg.Go(func() { b.readLoop(port, cancel) })
	wg.Go(func() {
		for {
			select {
			case <-ctx.Done():
				return
			case f := <-queue:
				if _, err := port.Write(f); err != nil {
					cancel(fmt.Errorf("write: %w", err))
					return
				}
			}
		}
	})

	tick := time.NewTicker(b.inquiryInterval)
	defer tick.Stop()
loop:
	for {
		select {
		case <-ctx.Done():
			break loop
		case <-tick.C:
			if b.Status().State == StateDetected {
				b.enqueue(deviceInquiry)
			}
		}
	}

	b.mu.Lock()
	b.queue = nil
	b.mu.Unlock()
	port.Close()
	wg.Wait()
	if err := context.Cause(ctx); err != nil && !errors.Is(err, context.Canceled) {
		b.log.Warn("amp port closed", "error", err)
	}
	b.setStatus(AmpStatus{State: StateDisconnected})
}

func (b *Bridge) readLoop(port midi.Port, cancel context.CancelCauseFunc) {
	var fr midi.Framer
	buf := make([]byte, readBufferSize)
	for {
		n, err := port.Read(buf)
		fr.Feed(buf[:n], b.fromAmp)
		if err != nil {
			cancel(fmt.Errorf("read: %w", err))
			return
		}
	}
}

func (b *Bridge) fromAmp(frame []byte) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if midi.IsIdentity(frame) && b.status.State == StateDetected {
		st := b.status
		st.State = StateConnected
		st.Identity = strings.ToUpper(fmt.Sprintf("% X", frame))
		b.setStatusLocked(st)
		b.log.Info("amp identified", "identity", st.Identity)
	}
	if b.ctrl == nil {
		return
	}
	select {
	case b.ctrl.frames <- frame:
	default:
		b.log.Warn("controller too slow, dropping amp frame", "len", len(frame))
	}
}

func (b *Bridge) enqueue(frame []byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.queue == nil {
		return ErrAmpDisconnected
	}
	select {
	case b.queue <- frame:
		return nil
	default:
		return ErrQueueFull
	}
}

// Status returns the current amp status.
func (b *Bridge) Status() AmpStatus {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.status
}

func (b *Bridge) setStatus(st AmpStatus) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.setStatusLocked(st)
}

func (b *Bridge) setStatusLocked(st AmpStatus) {
	if st == b.status {
		return
	}
	if st.State != b.status.State {
		b.log.Info("amp state", "state", st.State, "error", st.Error)
	}
	b.status = st
	for ch := range b.subs {
		select { // keep only the latest status in the 1-slot buffer
		case <-ch:
		default:
		}
		ch <- st
	}
}

// Subscribe returns a channel that receives the latest status after every change.
func (b *Bridge) Subscribe() (<-chan AmpStatus, func()) {
	ch := make(chan AmpStatus, 1)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()
	return ch, func() {
		b.mu.Lock()
		delete(b.subs, ch)
		b.mu.Unlock()
	}
}

// SetPaused blocks (true) or allows (false) client writes, e.g. during an update.
func (b *Bridge) SetPaused(p bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.paused != p {
		b.log.Info("bridge paused", "paused", p)
	}
	b.paused = p
}

// Attach makes the caller the controlling client. If a controller is already active, ok is
// false unless it has the same non-empty owner key: then the new one takes over and the old
// one's Replaced channel is closed.
func (b *Bridge) Attach(owner string) (c *Controller, ok bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.ctrl != nil {
		if owner == "" || b.ctrl.owner != owner {
			return nil, false
		}
		close(b.ctrl.replaced)
	}
	b.ctrl = &Controller{b: b, owner: owner, frames: make(chan []byte, frameBufferSize), replaced: make(chan struct{})}
	return b.ctrl, true
}

// Controller is the single client allowed to talk to the amp.
type Controller struct {
	b        *Bridge
	owner    string
	frames   chan []byte
	replaced chan struct{}
}

// Send validates a client frame and queues it for the amp.
func (c *Controller) Send(kind byte, frame []byte) error {
	fn, err := midi.CheckHostFrame(kind, frame)
	if err != nil {
		return err
	}
	b := c.b
	b.mu.Lock()
	defer b.mu.Unlock()
	switch {
	case b.paused:
		return ErrUpdating
	case b.queue == nil || (b.status.State != StateDetected && b.status.State != StateConnected):
		return ErrAmpDisconnected
	}
	select {
	case b.queue <- append([]byte(nil), frame...):
	default:
		return ErrQueueFull
	}
	if kind == 1 {
		b.log.Info(saveMessage(fn, frame))
	}
	return nil
}

func saveMessage(fn int, frame []byte) string {
	switch fn {
	case -1:
		return "save requested: func=none"
	case midi.FnProgramDump, midi.FnProgramWriteRequest:
		return fmt.Sprintf("save requested: func=%02X slot=%d", fn, frame[8])
	default:
		return fmt.Sprintf("save requested: func=%02X", fn)
	}
}

// Frames delivers complete frames received from the amp.
func (c *Controller) Frames() <-chan []byte { return c.frames }

// Replaced is closed when a new controller with the same owner key took over.
func (c *Controller) Replaced() <-chan struct{} { return c.replaced }

// Detach releases the controller slot.
func (c *Controller) Detach() {
	b := c.b
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.ctrl == c {
		b.ctrl = nil
	}
}
