package mcp

import (
	"errors"
	"sync"
	"time"

	"github.com/TomKalina/pi-velvetronix/internal/bridge"
	"github.com/TomKalina/pi-velvetronix/internal/midi"
	"github.com/TomKalina/pi-velvetronix/internal/voxproto"
)

// idleTimeout releases the controller slot after the last tool call, as the stdio server did.
var idleTimeout = 8 * time.Second

// session is the endpoint's controller connection, shared by all MCP clients; tool calls run one
// at a time.
type session struct {
	attach func() (Controller, bool)

	mu     sync.Mutex
	ctrl   Controller
	client *voxproto.Client
	idle   *time.Timer
	gen    int // bumped on every call so an old idle timer does nothing
}

func newSession(attach func() (Controller, bool)) *session { return &session{attach: attach} }

// use runs fn with a connected client whose model is up to date.
func (s *session) use(fn func(c *voxproto.Client) (any, error)) (any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gen++
	if s.idle != nil {
		s.idle.Stop()
	}
	defer s.armIdle()
	c, err := s.connect()
	if err != nil {
		return nil, err
	}
	v, err := fn(c)
	var ve *voxproto.Error
	if errors.As(err, &ve) && ve.Code == "closed" {
		s.closeLocked()
	}
	return v, err
}

func (s *session) armIdle() {
	gen := s.gen
	s.idle = time.AfterFunc(idleTimeout, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.gen == gen {
			s.closeLocked()
		}
	})
}

func (s *session) connect() (*voxproto.Client, error) {
	if s.client != nil {
		err := s.client.Sync()
		if err == nil {
			return s.client, nil
		}
		s.closeLocked()
		var ve *voxproto.Error
		if !errors.As(err, &ve) || ve.Code != "closed" {
			return nil, err
		}
		// Dropped by a takeover: attach again.
	}
	ctrl, ok := s.attach()
	if !ok {
		return nil, userError("All 8 controller slots of the amp are taken (open pages). Close one and try again.")
	}
	s.ctrl = ctrl
	s.client = voxproto.NewClient(&ctrlConn{c: ctrl})
	if err := s.client.ReadAll(); err != nil {
		s.closeLocked()
		return nil, err
	}
	return s.client, nil
}

// close releases the controller slot (vox_disconnect, idle, shutdown).
func (s *session) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closeLocked()
}

func (s *session) closeLocked() {
	if s.ctrl != nil {
		s.ctrl.Detach()
	}
	s.ctrl, s.client = nil, nil
}

// ctrlConn adapts a bridge controller to voxproto.Conn.
type ctrlConn struct{ c Controller }

// errCode is the bridge error code of a Send error (the /ws error codes, design §7).
func errCode(err error) string {
	for _, e := range []error{midi.ErrBadFrame, midi.ErrForbidden, midi.ErrSaveFlagRequired,
		bridge.ErrAmpDisconnected, bridge.ErrQueueFull, bridge.ErrUpdating} {
		if errors.Is(err, e) {
			return e.Error()
		}
	}
	return "write_failed"
}

func (k *ctrlConn) Send(save bool, frame []byte) error {
	kind := byte(0x00)
	if save {
		kind = 0x01
	}
	if err := k.c.Send(kind, frame); err != nil {
		return errors.New(errCode(err))
	}
	return nil
}

var errReplaced = errors.New("controller replaced")

func (k *ctrlConn) Recv(wait time.Duration) (voxproto.Frame, error) {
	// Frames already queued come first, even after a takeover.
	select {
	case f := <-k.c.Frames():
		return voxproto.Frame{Echo: f.Echo, Data: f.Data}, nil
	default:
	}
	select {
	case <-k.c.Lost():
		return voxproto.Frame{}, voxproto.ErrFramesLost
	case <-k.c.Replaced():
		return voxproto.Frame{}, errReplaced
	default:
	}
	if wait <= 0 {
		return voxproto.Frame{}, voxproto.ErrRecvTimeout
	}
	t := time.NewTimer(wait)
	defer t.Stop()
	select {
	case f := <-k.c.Frames():
		return voxproto.Frame{Echo: f.Echo, Data: f.Data}, nil
	case <-k.c.Lost():
		return voxproto.Frame{}, voxproto.ErrFramesLost
	case <-k.c.Replaced():
		return voxproto.Frame{}, errReplaced
	case <-t.C:
		return voxproto.Frame{}, voxproto.ErrRecvTimeout
	}
}
