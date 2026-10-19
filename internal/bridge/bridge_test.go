package bridge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/TomKalina/pi-velvetronix/internal/midi"
)

type fakePort struct {
	in      chan []byte // amp -> host
	out     chan []byte // host -> amp
	readErr chan error
	closed  chan struct{}
	once    sync.Once
}

func newFakePort(outBuf int) *fakePort {
	return &fakePort{in: make(chan []byte, 16), out: make(chan []byte, outBuf),
		readErr: make(chan error, 1), closed: make(chan struct{})}
}

func (p *fakePort) Read(b []byte) (int, error) {
	select {
	case d := <-p.in:
		return copy(b, d), nil
	case err := <-p.readErr:
		return 0, err
	case <-p.closed:
		return 0, os.ErrClosed
	}
}

func (p *fakePort) Write(b []byte) (int, error) {
	select {
	case p.out <- append([]byte(nil), b...):
		return len(b), nil
	case <-p.closed:
		return 0, os.ErrClosed
	}
}

func (p *fakePort) Close() error {
	p.once.Do(func() { close(p.closed) })
	return nil
}

type fakeEnv struct {
	mu      sync.Mutex
	devs    []midi.Device
	openErr error
	ports   chan *fakePort
	outBuf  int
}

func (e *fakeEnv) discover() ([]midi.Device, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.devs, nil
}

func (e *fakeEnv) open(node string) (midi.Port, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.openErr != nil {
		return nil, e.openErr
	}
	p := newFakePort(e.outBuf)
	e.ports <- p
	return p, nil
}

var (
	testDev  = midi.Device{Card: "1", ID: "ValvetronixX", Name: "Valvetronix X", UsbID: "0944:0117", Node: "/dev/snd/midiC1D0"}
	identity = []byte{0xF0, 0x7E, 0x00, 0x06, 0x02, 0x42, 0x34, 0x01, 0x00, 0x00, 0x03, 0x00, 0x01, 0x00, 0xF7}
	modeReq  = []byte{0xF0, 0x42, 0x30, 0x00, 0x01, 0x34, 0x12, 0xF7}
)

type logBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *logBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *logBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func start(t *testing.T, env *fakeEnv) (*Bridge, *logBuf) {
	t.Helper()
	if env.ports == nil {
		env.ports = make(chan *fakePort, 4)
	}
	if env.outBuf == 0 {
		env.outBuf = 64
	}
	logs := &logBuf{}
	b := NewWithIO(env.discover, env.open, slog.New(slog.NewTextHandler(logs, nil)))
	b.pollInterval = 10 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { b.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return b, logs
}

func waitState(t *testing.T, b *Bridge, state string) AmpStatus {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if st := b.Status(); st.State == state {
			return st
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("state = %q, want %q", b.Status().State, state)
	return AmpStatus{}
}

func recv[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for channel")
	}
	var zero T
	return zero
}

func connect(t *testing.T, env *fakeEnv, b *Bridge) *fakePort {
	t.Helper()
	p := recv(t, env.ports)
	if got := recv(t, p.out); !bytes.Equal(got, deviceInquiry) {
		t.Fatalf("first write = % X, want device inquiry", got)
	}
	waitState(t, b, StateDetected)
	p.in <- identity
	waitState(t, b, StateConnected)
	return p
}

func TestDiscoveryAndIdentity(t *testing.T) {
	env := &fakeEnv{}
	b, _ := start(t, env)
	waitState(t, b, StateDisconnected)

	env.mu.Lock()
	env.devs = []midi.Device{testDev}
	env.mu.Unlock()
	p := recv(t, env.ports)
	if got := recv(t, p.out); !bytes.Equal(got, deviceInquiry) {
		t.Fatalf("first write = % X", got)
	}
	st := waitState(t, b, StateDetected)
	if st.Card != "1" || st.Name != "Valvetronix X" || st.UsbID != "0944:0117" || st.Device != testDev.Node || st.Identity != "" {
		t.Fatalf("detected status = %+v", st)
	}

	// A non-VOX inquiry reply does not confirm identity.
	p.in <- []byte{0xF0, 0x7E, 0x00, 0x06, 0x02, 0x41, 0x10, 0x01, 0xF7}
	// Realtime bytes interleaved inside the reply must not break framing.
	p.in <- append([]byte{0xFE}, identity[:5]...)
	p.in <- append([]byte{0xF8}, identity[5:]...)
	st = waitState(t, b, StateConnected)
	if st.Identity != "F0 7E 00 06 02 42 34 01 00 00 03 00 01 00 F7" {
		t.Errorf("identity = %q", st.Identity)
	}

	// Device unplugged: read fails with ENODEV-like error.
	env.mu.Lock()
	env.devs = nil
	env.mu.Unlock()
	p.readErr <- errors.New("no such device")
	st = waitState(t, b, StateDisconnected)
	if st != (AmpStatus{State: StateDisconnected}) {
		t.Errorf("disconnected status keeps details: %+v", st)
	}
}

func TestInquiryRepeatsWhileDetected(t *testing.T) {
	env := &fakeEnv{devs: []midi.Device{testDev}, ports: make(chan *fakePort, 4), outBuf: 64}
	logs := &logBuf{}
	b := NewWithIO(env.discover, env.open, slog.New(slog.NewTextHandler(logs, nil)))
	b.inquiryInterval = 20 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.Run(ctx)

	p := recv(t, env.ports)
	for i := 0; i < 3; i++ {
		if got := recv(t, p.out); !bytes.Equal(got, deviceInquiry) {
			t.Fatalf("write %d = % X", i, got)
		}
	}
	p.in <- identity
	waitState(t, b, StateConnected)
	// Drain anything sent before the state flipped, then expect silence.
	time.Sleep(30 * time.Millisecond)
	for len(p.out) > 0 {
		<-p.out
	}
	select {
	case f := <-p.out:
		t.Fatalf("inquiry sent while connected: % X", f)
	case <-time.After(80 * time.Millisecond):
	}
}

func TestBusyAndOpenError(t *testing.T) {
	env := &fakeEnv{devs: []midi.Device{testDev}, openErr: midi.ErrBusy}
	b, _ := start(t, env)
	st := waitState(t, b, StateBusy)
	if st.Device != testDev.Node {
		t.Errorf("busy status = %+v", st)
	}

	env.mu.Lock()
	env.openErr = errors.New("permission denied")
	env.mu.Unlock()
	st = waitState(t, b, StateError)
	if st.Error != "permission denied" {
		t.Errorf("error status = %+v", st)
	}

	env.mu.Lock()
	env.openErr = nil
	env.mu.Unlock()
	connect(t, env, b)
}

func TestSingleController(t *testing.T) {
	b, _ := start(t, &fakeEnv{})
	c, ok := b.Attach("a")
	if !ok {
		t.Fatal("first attach failed")
	}
	if _, ok := b.Attach("b"); ok {
		t.Fatal("second attach succeeded")
	}
	c.Detach()
	c.Detach() // idempotent
	c2, ok := b.Attach("b")
	if !ok {
		t.Fatal("attach after detach failed")
	}
	c.Detach() // stale controller must not detach the new one
	if _, ok := b.Attach("a"); ok {
		t.Fatal("stale detach released the new controller")
	}
	c2.Detach()
}

func TestSameOwnerTakesOver(t *testing.T) {
	b, _ := start(t, &fakeEnv{})
	c, _ := b.Attach("a")
	c2, ok := b.Attach("a")
	if !ok {
		t.Fatal("same owner could not take over")
	}
	select {
	case <-c.Replaced():
	default:
		t.Fatal("old controller not told it was replaced")
	}
	select {
	case <-c2.Replaced():
		t.Fatal("new controller marked replaced")
	default:
	}
	c.Detach() // the replaced controller must not release the slot
	if _, ok := b.Attach("b"); ok {
		t.Fatal("other owner attached after takeover")
	}
	c2.Detach()

	e, _ := b.Attach("")
	if _, ok := b.Attach(""); ok {
		t.Fatal("empty owner key took over")
	}
	e.Detach()
}

func TestSendChecks(t *testing.T) {
	env := &fakeEnv{}
	b, logs := start(t, env)
	c, _ := b.Attach("a")
	defer c.Detach()

	if err := c.Send(0, []byte{0xF0, 0xF7}); !errors.Is(err, midi.ErrBadFrame) {
		t.Errorf("bad frame: %v", err)
	}
	if err := c.Send(0, modeReq); !errors.Is(err, ErrAmpDisconnected) {
		t.Errorf("no amp: %v", err)
	}

	env.mu.Lock()
	env.devs = []midi.Device{testDev}
	env.mu.Unlock()
	p := connect(t, env, b)

	frame := append([]byte(nil), modeReq...)
	if err := c.Send(0, frame); err != nil {
		t.Fatal(err)
	}
	frame[6] = 0x00 // Send must have copied the frame
	if got := recv(t, p.out); !bytes.Equal(got, modeReq) {
		t.Errorf("written % X", got)
	}

	write := []byte{0xF0, 0x42, 0x30, 0x00, 0x01, 0x34, 0x11, 0x00, 0x03, 0xF7}
	if err := c.Send(0, write); !errors.Is(err, midi.ErrSaveFlagRequired) {
		t.Errorf("kind 0 write: %v", err)
	}
	if err := c.Send(1, write); err != nil {
		t.Errorf("kind 1 write: %v", err)
	}
	recv(t, p.out)
	if !bytes.Contains([]byte(logs.String()), []byte("save requested: func=11 slot=3")) {
		t.Errorf("save not logged:\n%s", logs.String())
	}

	b.SetPaused(true)
	if err := c.Send(0, modeReq); !errors.Is(err, ErrUpdating) {
		t.Errorf("paused: %v", err)
	}
	// Frame checks come before the pause check.
	if err := c.Send(0, write); !errors.Is(err, midi.ErrSaveFlagRequired) {
		t.Errorf("paused kind 0 write: %v", err)
	}
	b.SetPaused(false)
	if err := c.Send(0, modeReq); err != nil {
		t.Errorf("unpaused: %v", err)
	}
}

func TestQueueFull(t *testing.T) {
	env := &fakeEnv{devs: []midi.Device{testDev}, outBuf: 1}
	b, _ := start(t, env)
	p := recv(t, env.ports)
	waitState(t, b, StateDetected)
	c, _ := b.Attach("a")
	defer c.Detach()

	// The port accepts one frame (the inquiry), then the writer blocks on the next one.
	var err error
	for i := 0; i < queueSize+3 && err == nil; i++ {
		err = c.Send(0, modeReq)
	}
	if !errors.Is(err, ErrQueueFull) {
		t.Fatalf("err = %v, want queue_full", err)
	}
	<-p.out
	<-p.out
	waitFor(t, func() bool { return c.Send(0, modeReq) == nil })
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met")
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func TestFramesForwarding(t *testing.T) {
	env := &fakeEnv{devs: []midi.Device{testDev}}
	b, logs := start(t, env)
	p := connect(t, env, b) // no controller: identity reply dropped silently

	c, _ := b.Attach("a")
	defer c.Detach()
	ack := []byte{0xF0, 0x42, 0x30, 0x00, 0x01, 0x34, 0x23, 0xF7}
	p.in <- append(append([]byte{0xF8}, ack...), 0xFE)
	if got := recv(t, c.Frames()); !bytes.Equal(got, ack) {
		t.Errorf("forwarded % X", got)
	}

	// Controller not reading: buffer fills and further frames are dropped.
	for i := 0; i < frameBufferSize+5; i++ {
		p.in <- ack
	}
	waitFor(t, func() bool { return bytes.Contains([]byte(logs.String()), []byte("dropping amp frame")) })
	if len(c.Frames()) != frameBufferSize {
		t.Errorf("buffered %d frames", len(c.Frames()))
	}
}

func TestSubscribe(t *testing.T) {
	env := &fakeEnv{devs: []midi.Device{testDev}, openErr: midi.ErrBusy}
	b, _ := start(t, env)
	waitState(t, b, StateBusy)
	ch, cancel := b.Subscribe()
	defer cancel()

	env.mu.Lock()
	env.openErr = nil
	env.mu.Unlock()
	p := recv(t, env.ports)
	<-p.out
	waitState(t, b, StateDetected)
	p.in <- identity
	waitState(t, b, StateConnected)
	// The 1-slot channel holds only the latest status.
	if st := recv(t, ch); st.State != StateConnected {
		t.Errorf("latest = %+v", st)
	}
	cancel()
	p.readErr <- io.EOF
	waitState(t, b, StateDisconnected)
	select {
	case st := <-ch:
		t.Errorf("unsubscribed channel got %+v", st)
	default:
	}
}

func TestAmpStatusJSON(t *testing.T) {
	got, _ := json.Marshal(AmpStatus{State: StateBusy, Device: "/dev/snd/midiC1D0"})
	want := `{"state":"busy","card":null,"name":null,"usb_id":null,"device":"/dev/snd/midiC1D0","identity":null,"error":null}`
	if string(got) != want {
		t.Errorf("got %s", got)
	}
}
