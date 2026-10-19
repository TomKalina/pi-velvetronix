package web

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	closeReplaced  = websocket.StatusCode(4002)
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

	// Same session (another tab, or a reconnect before the old socket timed out) takes over.
	owner := sha256.Sum256([]byte(sessionToken(r)))
	s.ctrlMu.Lock()
	ctrl, ok := s.Amp.Attach(hex.EncodeToString(owner[:]))
	if ok {
		s.ctrlActive.Store(true)
	}
	s.ctrlMu.Unlock()
	if !ok {
		ctx, cancel := context.WithTimeout(s.ctx, wsWriteTimeout)
		defer cancel()
		_ = wsjson.Write(ctx, c, typeMsg{T: "busy"})
		_ = c.Close(closeBusy, "busy")
		return
	}
	defer func() {
		s.ctrlMu.Lock()
		defer s.ctrlMu.Unlock()
		select {
		case <-ctrl.Replaced(): // the slot belongs to the new controller
		default:
			s.ctrlActive.Store(false)
		}
		ctrl.Detach()
	}()
	s.Log.Info("controller connected", "ip", remoteIP(r))
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
			s.Log.Info("controller replaced by the same session", "ip", remoteIP(r))
			_ = send(typeMsg{T: "replaced"})
			_ = c.Close(closeReplaced, "replaced")
			return
		case f := <-ctrl.Frames():
			wctx, wcancel := context.WithTimeout(ctx, wsWriteTimeout)
			err = c.Write(wctx, websocket.MessageBinary, append([]byte{0x00}, f...))
			wcancel()
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
