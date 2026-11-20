package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/TomKalina/pi-velvetronix/internal/bridge"
	"github.com/TomKalina/pi-velvetronix/internal/midi"
	"github.com/TomKalina/pi-velvetronix/internal/update"
)

const (
	wsReadLimit    = 4096
	wsMaxMessage   = 512
	wsPingInterval = 10 * time.Second
	wsPingTimeout  = 5 * time.Second
	wsWriteTimeout = 5 * time.Second
	closeBusy      = websocket.StatusCode(4001)
	closeTaken     = websocket.StatusCode(4003)
	closeFree      = websocket.StatusCode(4004)
	kindEcho       = 0x02
)

type helloMsg struct {
	T       string `json:"t"`
	Version string `json:"version"`
}

type ampMsg struct {
	T   string           `json:"t"`
	Amp bridge.AmpStatus `json:"amp"`
}

type updateMsg struct {
	T      string            `json:"t"`
	Update update.UpdateInfo `json:"update"`
}

type errorMsg struct {
	T    string `json:"t"`
	Code string `json:"code"`
	Func *int   `json:"func"`
}

type typeMsg struct {
	T string `json:"t"`
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	if !s.authenticated(r) {
		writeError(w, http.StatusUnauthorized, "unauthorized", "")
		return
	}
	// Default options: Origin must match Host.
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		return // Accept already wrote the HTTP error
	}
	defer c.CloseNow()
	c.SetReadLimit(wsReadLimit)
	if r.URL.Query().Get("watch") == "1" {
		s.watchWS(c, r)
		return
	}

	// Several clients control the amp at once and see each other's changes (echoes). Only when
	// all slots are taken: busy, or with ?takeover=1 the oldest controller is dropped.
	takeover := r.URL.Query().Get("takeover") == "1"
	ctrl, ok := s.Amp.Attach(takeover)
	if !ok {
		ctx, cancel := context.WithTimeout(s.ctx, wsWriteTimeout)
		defer cancel()
		_ = wsjson.Write(ctx, c, typeMsg{T: "busy"})
		_ = c.Close(closeBusy, "busy")
		return
	}
	defer ctrl.Detach()
	s.Log.Info("controller connected", "ip", remoteIP(r), "takeover", takeover)
	defer s.Log.Info("controller disconnected", "ip", remoteIP(r))

	ctx, cancel := context.WithCancel(s.ctx)
	defer cancel()
	send := func(v any) error {
		wctx, wcancel := context.WithTimeout(ctx, wsWriteTimeout)
		defer wcancel()
		return wsjson.Write(wctx, c, v)
	}

	ampCh, unsubAmp := s.Amp.Subscribe()
	defer unsubAmp()
	updCh, unsubUpd := s.subscribeUpdates()
	defer unsubUpd()

	if send(helloMsg{T: "hello", Version: s.Version}) != nil ||
		send(ampMsg{T: "amp", Amp: s.Amp.Status()}) != nil {
		return
	}

	go func() {
		defer cancel()
		for {
			typ, data, err := c.Read(ctx)
			if err != nil {
				return
			}
			if s.handleClientMessage(ctrl, typ, data, send) != nil {
				return
			}
		}
	}()

	ping := time.NewTicker(wsPingInterval)
	defer ping.Stop()
	for {
		var err error
		select {
		case <-ctx.Done():
			return
		case <-ctrl.Replaced():
			s.Log.Info("controller dropped by a takeover", "ip", remoteIP(r))
			_ = send(typeMsg{T: "taken"})
			_ = c.Close(closeTaken, "taken")
			return
		case f := <-ctrl.Frames():
			err = writeFrame(ctx, c, f)
		case <-ctrl.Lost():
			// Frames were dropped (client too slow): the page re-reads the amp.
			err = send(typeMsg{T: "resync"})
		case st := <-ampCh:
			err = send(ampMsg{T: "amp", Amp: st})
		case u := <-updCh:
			err = send(updateMsg{T: "update", Update: u})
		case <-ping.C:
			pctx, pcancel := context.WithTimeout(ctx, wsPingTimeout)
			err = c.Ping(pctx)
			pcancel()
		}
		if err != nil {
			return
		}
	}
}

// watchWS serves a read-only watcher (all controller slots were taken): amp frames (kind 0x00)
// and the controllers' frames (kind 0x02) until a slot is free; then {"t":"free"} and close 4004.
func (s *Server) watchWS(c *websocket.Conn, r *http.Request) {
	obs := s.Amp.Observe()
	defer obs.Close()
	s.Log.Info("watcher connected", "ip", remoteIP(r))
	defer s.Log.Info("watcher disconnected", "ip", remoteIP(r))

	ctx, cancel := context.WithCancel(s.ctx)
	defer cancel()
	send := func(v any) error {
		wctx, wcancel := context.WithTimeout(ctx, wsWriteTimeout)
		defer wcancel()
		return wsjson.Write(wctx, c, v)
	}
	ampCh, unsubAmp := s.Amp.Subscribe()
	defer unsubAmp()
	updCh, unsubUpd := s.subscribeUpdates()
	defer unsubUpd()

	if send(helloMsg{T: "hello", Version: s.Version}) != nil ||
		send(typeMsg{T: "watching"}) != nil ||
		send(ampMsg{T: "amp", Amp: s.Amp.Status()}) != nil {
		return
	}
	go func() {
		defer cancel()
		for {
			typ, data, err := c.Read(ctx)
			if err != nil {
				return
			}
			// A watcher cannot send to the amp; only the application ping is answered.
			var m typeMsg
			if typ == websocket.MessageText && json.Unmarshal(data, &m) == nil && m.T == "ping" && send(typeMsg{T: "pong"}) != nil {
				return
			}
		}
	}()

	ping := time.NewTicker(wsPingInterval)
	defer ping.Stop()
	for {
		var err error
		select {
		case <-ctx.Done():
			return
		case <-obs.Freed():
			_ = send(typeMsg{T: "free"})
			_ = c.Close(closeFree, "free")
			return
		case f := <-obs.Frames():
			err = writeFrame(ctx, c, f)
		case <-obs.Lost():
			err = send(typeMsg{T: "resync"})
		case st := <-ampCh:
			err = send(ampMsg{T: "amp", Amp: st})
		case u := <-updCh:
			err = send(updateMsg{T: "update", Update: u})
		case <-ping.C:
			pctx, pcancel := context.WithTimeout(ctx, wsPingTimeout)
			err = c.Ping(pctx)
			pcancel()
		}
		if err != nil {
			return
		}
	}
}

// writeFrame sends an amp frame (kind 0x00) or another controller's frame (kind 0x02).
func writeFrame(ctx context.Context, c *websocket.Conn, f bridge.ObservedFrame) error {
	kind := byte(0x00)
	if f.Echo {
		kind = kindEcho
	}
	wctx, wcancel := context.WithTimeout(ctx, wsWriteTimeout)
	defer wcancel()
	return c.Write(wctx, websocket.MessageBinary, append([]byte{kind}, f.Data...))
}

func (s *Server) handleClientMessage(ctrl Controller, typ websocket.MessageType, data []byte, send func(any) error) error {
	if typ == websocket.MessageText {
		var m typeMsg
		if json.Unmarshal(data, &m) == nil && m.T == "ping" {
			return send(typeMsg{T: "pong"})
		}
		return nil
	}

	var kind byte
	var frame []byte
	if len(data) > 0 {
		kind, frame = data[0], data[1:]
	}
	var err error
	if len(data) < 1 || len(data) > wsMaxMessage {
		err = midi.ErrBadFrame
	} else {
		err = ctrl.Send(kind, frame)
	}
	if err == nil {
		return nil
	}
	msg := errorMsg{T: "error", Code: bridgeErrorCode(err)}
	if fn, _ := midi.CheckHostFrame(kind, frame); fn >= 0 {
		msg.Func = &fn
	}
	s.Log.Info("frame rejected", "code", msg.Code, "func", fnAttr(msg.Func), "len", len(frame))
	return send(msg)
}

func fnAttr(fn *int) any {
	if fn == nil {
		return nil
	}
	return *fn
}

func bridgeErrorCode(err error) string {
	for _, e := range []error{midi.ErrBadFrame, midi.ErrForbidden, midi.ErrSaveFlagRequired,
		bridge.ErrAmpDisconnected, bridge.ErrQueueFull, bridge.ErrUpdating} {
		if errors.Is(err, e) {
			return e.Error()
		}
	}
	return "write_failed"
}
