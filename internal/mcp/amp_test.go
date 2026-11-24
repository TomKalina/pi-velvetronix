package mcp

import (
	"sync"
	"testing"
	"time"

	"github.com/TomKalina/pi-velvetronix/cmd/fakeamp/sim"
	"github.com/TomKalina/pi-velvetronix/internal/bridge"
	"github.com/TomKalina/pi-velvetronix/internal/voxproto"
)

// simCtrl is a controller answered by the simulator.
type simCtrl struct {
	amp      *sim.Sim
	frames   chan bridge.ObservedFrame
	replaced chan struct{}
	lost     chan struct{}
	mu       sync.Mutex
	detached bool
}

func newSimCtrl() *simCtrl {
	return &simCtrl{amp: sim.New(), frames: make(chan bridge.ObservedFrame, 64), replaced: make(chan struct{}), lost: make(chan struct{}, 1)}
}

func (c *simCtrl) Send(kind byte, f []byte) error {
	for _, r := range c.amp.Handle(f) {
		c.frames <- bridge.ObservedFrame{Data: r}
	}
	return nil
}
func (c *simCtrl) Frames() <-chan bridge.ObservedFrame { return c.frames }
func (c *simCtrl) Replaced() <-chan struct{}           { return c.replaced }
func (c *simCtrl) Lost() <-chan struct{}               { return c.lost }
func (c *simCtrl) Detach() {
	c.mu.Lock()
	c.detached = true
	c.mu.Unlock()
}
func (c *simCtrl) isDetached() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.detached
}

func status(s *session) error {
	_, err := s.use(func(c *voxproto.Client) (any, error) { return nil, nil })
	return err
}

func TestSessionIdleAndTakeover(t *testing.T) {
	old := idleTimeout
	idleTimeout = 50 * time.Millisecond
	t.Cleanup(func() { idleTimeout = old })

	var ctrls []*simCtrl
	s := newSession(func() (Controller, bool) {
		c := newSimCtrl()
		ctrls = append(ctrls, c)
		return c, true
	})
	if err := status(s); err != nil {
		t.Fatal(err)
	}
	// A second call within the idle time reuses the controller.
	if err := status(s); err != nil || len(ctrls) != 1 {
		t.Fatalf("%v, %d attaches", err, len(ctrls))
	}
	deadline := time.Now().Add(2 * time.Second)
	for !ctrls[0].isDetached() {
		if time.Now().After(deadline) {
			t.Fatal("controller not released after the idle time")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := status(s); err != nil || len(ctrls) != 2 {
		t.Fatalf("%v, %d attaches", err, len(ctrls))
	}
	// Dropped by a takeover: the next call attaches again.
	close(ctrls[1].replaced)
	if err := status(s); err != nil || len(ctrls) != 3 {
		t.Fatalf("after takeover: %v, %d attaches", err, len(ctrls))
	}
	s.close()
	if !ctrls[2].isDetached() {
		t.Fatal("close did not detach")
	}
}

func TestSessionBusy(t *testing.T) {
	s := newSession(func() (Controller, bool) { return nil, false })
	if err := status(s); err == nil || errorText(err) == "" || err.Error()[:3] != "All" {
		t.Fatalf("err %v", err)
	}
}
