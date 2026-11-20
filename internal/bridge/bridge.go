// Package bridge owns the amp's rawmidi port: discovery, identity check, up to
// MaxControllers controlling clients sharing one serialized write queue, and observers.
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
	// MaxControllers is how many clients may control the amp at once.
	MaxControllers = 8
	// replyTimeout bounds how long one client's request holds the queue (client timeouts are 1-2 s).
	replyTimeout = 2 * time.Second
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
	queue  chan outFrame // nil while no port is open
	ctrls  []*Controller // oldest first
	// pending is the controller whose request (pendingFrame) was written last and is not
	// answered yet; the amp's reply goes to it alone. replied is closed when the reply arrives.
	pending      *Controller
	pendingFrame []byte
	replied      chan struct{}
	paused bool
	subs   map[chan AmpStatus]struct{}
	obs    map[*Observer]struct{}
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
		obs:             map[*Observer]struct{}{},
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

	queue := make(chan outFrame, queueSize)
	queue <- outFrame{data: deviceInquiry}
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
				var replied chan struct{}
				if f.from != nil {
					// Requests from several controllers are answered strictly in turn (ACK/NAK
					// or data), so only one is outstanding and its reply can be routed back.
					replied = make(chan struct{})
					b.mu.Lock()
					b.pending, b.pendingFrame, b.replied = f.from, f.data, replied
					b.mu.Unlock()
				}
				if _, err := port.Write(f.data); err != nil {
					cancel(fmt.Errorf("write: %w", err))
					return
				}
				if replied == nil {
					continue
				}
				select {
				case <-replied:
				case <-time.After(replyTimeout):
				case <-ctx.Done():
				}
				b.mu.Lock()
				if b.replied == replied {
					b.pending, b.pendingFrame, b.replied = nil, nil, nil
				}
				b.mu.Unlock()
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
				b.enqueue(outFrame{data: deviceInquiry})
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
	f := ObservedFrame{Data: frame}
	b.observeLocked(f)
	if b.pending != nil && midi.IsReply(frame) {
		b.pending.deliverLocked(f)
		if midi.IsAck(frame) {
			// Applied by the amp: now the other controllers and the observers see the change.
			// A refused frame (NAK) is never shown to them.
			echo := ObservedFrame{Echo: true, Data: b.pendingFrame}
			b.observeLocked(echo)
			for _, o := range b.ctrls {
				if o != b.pending {
					o.deliverLocked(echo)
				}
			}
		}
		close(b.replied)
		b.pending, b.pendingFrame, b.replied = nil, nil, nil
		return
	}
	// Changes made on the amp's panel (and replies nobody waits for) go to every controller.
	for _, c := range b.ctrls {
		c.deliverLocked(f)
	}
}

// ObservedFrame is a frame shown to observers: from the amp, or (Echo) sent by the controller.
type ObservedFrame struct {
	Echo bool
	Data []byte
}

// Observer watches the controller's traffic without being able to send.
type Observer struct {
	b      *Bridge
	frames chan ObservedFrame
	freed  chan struct{}
	lost   chan struct{} // signalled when a frame was dropped
}

// Observe registers a read-only watcher. Its Freed channel is closed once a controller slot
// is free (at once if one is), so the watcher can try to become a controller.
func (b *Bridge) Observe() *Observer {
	b.mu.Lock()
	defer b.mu.Unlock()
	o := &Observer{b: b, frames: make(chan ObservedFrame, frameBufferSize), freed: make(chan struct{}), lost: make(chan struct{}, 1)}
	if len(b.ctrls) < MaxControllers {
		close(o.freed)
		return o
	}
	b.obs[o] = struct{}{}
	return o
}

// Frames delivers amp frames and echoes of the controller's frames.
func (o *Observer) Frames() <-chan ObservedFrame { return o.frames }

// Freed is closed when a controller slot becomes free.
func (o *Observer) Freed() <-chan struct{} { return o.freed }

// Lost is signalled after frames were dropped for this observer.
func (o *Observer) Lost() <-chan struct{} { return o.lost }

// Close unregisters the observer.
func (o *Observer) Close() {
	o.b.mu.Lock()
	defer o.b.mu.Unlock()
	delete(o.b.obs, o)
}

func (b *Bridge) observeLocked(f ObservedFrame) {
	for o := range b.obs {
		select {
		case o.frames <- f:
		default:
			b.log.Warn("observer too slow, dropping frame", "len", len(f.Data))
			signal(o.lost)
		}
	}
}

func (b *Bridge) freeObserversLocked() {
	for o := range b.obs {
		close(o.freed)
		delete(b.obs, o)
	}
}

// outFrame is a frame for the amp; from is the controller that sent it (nil: the bridge itself).
type outFrame struct {
	data []byte
	from *Controller
}

func (b *Bridge) enqueue(frame outFrame) error {
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

// Attach adds a controlling client. Up to MaxControllers may control the amp at once; when
// all slots are taken, ok is false unless takeover is set: then the oldest controller is
// dropped and its Replaced channel closed.
func (b *Bridge) Attach(takeover bool) (c *Controller, ok bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.ctrls) >= MaxControllers {
		if !takeover {
			return nil, false
		}
		old := b.ctrls[0]
		b.ctrls = b.ctrls[1:]
		close(old.replaced)
	}
	c = &Controller{b: b, frames: make(chan ObservedFrame, frameBufferSize), replaced: make(chan struct{}), lost: make(chan struct{}, 1)}
	b.ctrls = append(b.ctrls, c)
	return c, true
}

// Controllers returns how many clients control the amp.
func (b *Bridge) Controllers() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.ctrls)
}

// Controller is one client allowed to talk to the amp.
type Controller struct {
	b        *Bridge
	frames   chan ObservedFrame
	replaced chan struct{}
	lost     chan struct{} // signalled when a frame was dropped
}

func (c *Controller) deliverLocked(f ObservedFrame) {
	select {
	case c.frames <- f:
	default:
		c.b.log.Warn("controller too slow, dropping frame", "len", len(f.Data), "echo", f.Echo)
		signal(c.lost)
	}
}

// Lost is signalled after frames were dropped for this controller: its model may be stale.
func (c *Controller) Lost() <-chan struct{} { return c.lost }

func signal(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
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
	case b.queue <- outFrame{data: append([]byte(nil), frame...), from: c}:
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

// Frames delivers the amp's replies to this controller's requests, the amp's own change
// reports, and (Echo) the frames the other controllers send.
func (c *Controller) Frames() <-chan ObservedFrame { return c.frames }

// Replaced is closed when a takeover dropped this controller.
func (c *Controller) Replaced() <-chan struct{} { return c.replaced }

// Detach releases the controller slot.
func (c *Controller) Detach() {
	b := c.b
	b.mu.Lock()
	defer b.mu.Unlock()
	for i, o := range b.ctrls {
		if o == c {
			b.ctrls = append(b.ctrls[:i:i], b.ctrls[i+1:]...)
			b.freeObserversLocked()
			return
		}
	}
}
