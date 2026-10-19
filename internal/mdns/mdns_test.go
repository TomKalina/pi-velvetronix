package mdns

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"slices"
	"sync"
	"testing"
	"time"
)

type fakeSession struct {
	mu         sync.Mutex
	calls      []string
	serverSt   int32
	publishErr error
	events     chan event
	closed     bool
}

func newSession() *fakeSession {
	return &fakeSession{serverSt: serverRunning, events: make(chan event, 8)}
}

func (s *fakeSession) record(c string) {
	s.mu.Lock()
	s.calls = append(s.calls, c)
	s.mu.Unlock()
}

func (s *fakeSession) ServerState() (int32, error) { return s.serverSt, nil }

func (s *fakeSession) Publish(name string, ip net.IP) error {
	s.record("publish " + name + " " + ip.String())
	return s.publishErr
}

func (s *fakeSession) Withdraw() error { s.record("withdraw"); return nil }

func (s *fakeSession) Events() <-chan event { return s.events }

func (s *fakeSession) Close() {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
}

func (s *fakeSession) getCalls() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.calls)
}

type harness struct {
	t      *testing.T
	mu     sync.Mutex
	addr   net.IP
	states chan string
	dials  chan *fakeSession // sessions handed out to the publisher, in order
	cancel context.CancelFunc
	done   chan struct{}
}

func (h *harness) setIP(s string) {
	h.mu.Lock()
	h.addr = net.ParseIP(s)
	h.mu.Unlock()
}

func (h *harness) ip() net.IP {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.addr
}

// start runs the publisher; dial takes sessions from h.dials or fails when given nil.
func start(t *testing.T, ip string) *harness {
	h := &harness{t: t, states: make(chan string, 64), dials: make(chan *fakeSession, 8), done: make(chan struct{})}
	if ip != "" {
		h.setIP(ip)
	}
	p := &publisher{
		alias: "vox.local",
		ip:    h.ip,
		state: func(s string) { h.states <- s },
		log:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		poll:  5 * time.Millisecond,
		retry: 5 * time.Millisecond,
		dial: func(ctx context.Context) (session, error) {
			select {
			case s := <-h.dials:
				if s == nil {
					return nil, errors.New("no avahi")
				}
				return s, nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel
	go func() { p.run(ctx); close(h.done) }()
	t.Cleanup(h.stop)
	return h
}

func (h *harness) stop() {
	h.cancel()
	select {
	case <-h.done:
	case <-time.After(2 * time.Second):
		h.t.Fatal("publisher did not stop")
	}
}

// want reads the next state callbacks and checks them.
func (h *harness) want(states ...string) {
	h.t.Helper()
	for _, w := range states {
		select {
		case got := <-h.states:
			if got != w {
				h.t.Fatalf("state %q, want %q", got, w)
			}
		case <-time.After(2 * time.Second):
			h.t.Fatalf("timeout waiting for state %q", w)
		}
	}
}

func (h *harness) quiet() {
	h.t.Helper()
	select {
	case got := <-h.states:
		h.t.Fatalf("unexpected state %q", got)
	case <-time.After(30 * time.Millisecond):
	}
}

func waitCalls(t *testing.T, s *fakeSession, want ...string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if slices.Equal(s.getCalls(), want) {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("calls %q, want %q", s.getCalls(), want)
}

func TestLifecycle(t *testing.T) {
	s := newSession()
	h := start(t, "")
	h.dials <- s
	h.want("off")

	h.setIP("192.168.1.86")
	h.want("registering")
	waitCalls(t, s, "publish vox.local 192.168.1.86")
	s.events <- event{kind: evGroupState, state: groupEstablished}
	h.want("established")
	s.events <- event{kind: evGroupState, state: groupEstablished} // duplicate: no callback
	h.quiet()

	h.setIP("192.168.1.99")
	h.want("registering")
	waitCalls(t, s, "publish vox.local 192.168.1.86", "publish vox.local 192.168.1.99")
	s.events <- event{kind: evGroupState, state: groupRegistering}
	s.events <- event{kind: evGroupState, state: groupEstablished}
	h.want("established")

	h.setIP("")
	h.want("off")
	waitCalls(t, s, "publish vox.local 192.168.1.86", "publish vox.local 192.168.1.99", "withdraw")
	s.events <- event{kind: evGroupState, state: 0} // after Reset: ignored
	h.quiet()

	h.stop()
	if !s.closed {
		t.Fatal("session not closed")
	}
}

func TestGroupStateMapping(t *testing.T) {
	s := newSession()
	h := start(t, "10.42.0.1")
	h.dials <- s
	h.want("registering")
	for _, c := range []struct {
		st   int32
		want string
	}{{groupCollision, "collision"}, {groupFailure, "failed"}, {groupRegistering, "registering"}, {groupEstablished, "established"}} {
		s.events <- event{kind: evGroupState, state: c.st}
		h.want(c.want)
	}
}

func TestAvahiRestartRepublishes(t *testing.T) {
	s1, s2 := newSession(), newSession()
	h := start(t, "192.168.1.86")
	h.dials <- s1
	h.want("registering")
	s1.events <- event{kind: evGroupState, state: groupEstablished}
	h.want("established")

	s1.events <- event{kind: evOwnerChanged}
	h.want("failed")
	h.dials <- nil // avahi not back yet
	h.dials <- s2
	h.want("registering")
	waitCalls(t, s2, "publish vox.local 192.168.1.86")
	s2.events <- event{kind: evGroupState, state: groupEstablished}
	h.want("established")
	if !s1.closed {
		t.Fatal("old session not closed")
	}
}

func TestConnectionLostRedials(t *testing.T) {
	s1, s2 := newSession(), newSession()
	h := start(t, "192.168.1.86")
	h.dials <- s1
	h.want("registering")
	close(s1.events)
	h.want("failed")
	h.dials <- s2
	h.want("registering")
	waitCalls(t, s2, "publish vox.local 192.168.1.86")
}

func TestServerNotRunningWaits(t *testing.T) {
	s := newSession()
	s.serverSt = 1 // AVAHI_SERVER_REGISTERING
	h := start(t, "192.168.1.86")
	h.dials <- s
	h.want("registering")
	time.Sleep(20 * time.Millisecond)
	if c := s.getCalls(); len(c) != 0 {
		t.Fatalf("published before server running: %q", c)
	}
	s.events <- event{kind: evServerState, state: serverRunning}
	waitCalls(t, s, "publish vox.local 192.168.1.86")
	s.events <- event{kind: evGroupState, state: groupEstablished}
	h.want("established")

	// Host name collision: record withdrawn, republished once the server runs again.
	s.events <- event{kind: evServerState, state: 3}
	h.want("registering")
	s.events <- event{kind: evServerState, state: serverRunning}
	waitCalls(t, s, "publish vox.local 192.168.1.86", "withdraw", "publish vox.local 192.168.1.86")
}

func TestPublishErrorRedials(t *testing.T) {
	s1, s2 := newSession(), newSession()
	s1.publishErr = errors.New("org.freedesktop.DBus.Error.UnknownObject")
	h := start(t, "192.168.1.86")
	h.dials <- s1
	h.want("failed")
	h.dials <- s2
	h.want("registering")
	waitCalls(t, s2, "publish vox.local 192.168.1.86")
	if !s1.closed {
		t.Fatal("failed session not closed")
	}
}

func TestDialFailure(t *testing.T) {
	h := start(t, "192.168.1.86")
	h.dials <- nil
	h.want("failed")
	h.dials <- nil
	h.quiet() // still failed, no duplicate callback
	s := newSession()
	h.dials <- s
	h.want("registering")
}

func TestCancelWithdraws(t *testing.T) {
	s := newSession()
	h := start(t, "192.168.1.86")
	h.dials <- s
	h.want("registering")
	h.stop()
	if c := s.getCalls(); c[len(c)-1] != "withdraw" || !s.closed {
		t.Fatalf("calls %q closed %v", c, s.closed)
	}
}
