package web

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/TomKalina/pi-velvetronix/internal/auth"
)

type wsMsg struct {
	typ  websocket.MessageType
	data []byte
	t    string // "t" of text messages
	err  error
}

type wsClient struct {
	c    *websocket.Conn
	msgs chan wsMsg
}

func dialWS(t *testing.T, h *harness, cookie string, header http.Header) (*wsClient, *http.Response, error) {
	t.Helper()
	if header == nil {
		header = http.Header{}
	}
	if cookie != "" {
		header.Set("Cookie", cookieName+"="+cookie)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, resp, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(h.ts.URL, "http")+"/ws", &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		return nil, resp, err
	}
	wc := &wsClient{c: c, msgs: make(chan wsMsg, 64)}
	go func() {
		for {
			typ, data, err := c.Read(context.Background())
			m := wsMsg{typ: typ, data: data, err: err}
			if err == nil && typ == websocket.MessageText {
				var v struct{ T string }
				json.Unmarshal(data, &v)
				m.t = v.T
			}
			wc.msgs <- m
			if err != nil {
				return
			}
		}
	}()
	t.Cleanup(func() { c.CloseNow() })
	return wc, resp, nil
}

// next returns the first message matching pred, skipping others.
func (wc *wsClient) next(t *testing.T, what string, pred func(wsMsg) bool) wsMsg {
	t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case m := <-wc.msgs:
			if m.err != nil && !pred(m) {
				t.Fatalf("waiting for %s: %v", what, m.err)
			}
			if pred(m) {
				return m
			}
		case <-timeout:
			t.Fatalf("timeout waiting for %s", what)
		}
	}
}

func (wc *wsClient) text(t *testing.T, typ string) map[string]any {
	t.Helper()
	m := wc.next(t, typ, func(m wsMsg) bool { return m.t == typ })
	var v map[string]any
	json.Unmarshal(m.data, &v)
	return v
}

func (wc *wsClient) ampState(t *testing.T, state string) map[string]any {
	t.Helper()
	m := wc.next(t, "amp "+state, func(m wsMsg) bool {
		return m.t == "amp" && bytes.Contains(m.data, []byte(`"state":"`+state+`"`))
	})
	var v map[string]any
	json.Unmarshal(m.data, &v)
	return v["amp"].(map[string]any)
}

func (wc *wsClient) binary(t *testing.T, want []byte) {
	t.Helper()
	wc.next(t, "binary frame", func(m wsMsg) bool { return m.typ == websocket.MessageBinary && bytes.Equal(m.data, want) })
}

func (wc *wsClient) send(t *testing.T, kind byte, frame []byte) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := wc.c.Write(ctx, websocket.MessageBinary, append([]byte{kind}, frame...)); err != nil {
		t.Fatal(err)
	}
}

func (wc *wsClient) expectError(t *testing.T, code string, fn any) {
	t.Helper()
	v := wc.text(t, "error")
	if v["code"] != code || v["func"] != fn {
		t.Fatalf("error = %v, want code %s func %v", v, code, fn)
	}
}

func korg(fn byte, data ...byte) []byte {
	f := append([]byte{0xF0, 0x42, 0x30, 0x00, 0x01, 0x34, fn}, data...)
	return append(f, 0xF7)
}

func TestEndToEnd(t *testing.T) {
	state := t.TempDir()
	if err := auth.SetPassword(state, "heslo-k-voxu"); err != nil {
		t.Fatal(err)
	}
	store, err := auth.Load(state)
	if err != nil {
		t.Fatal(err)
	}
	h := newHarness(t, store, nil)

	// Login with the real auth store.
	c := jarClient()
	if resp, m := h.do(t, c, "POST", "/api/login", `{"password":"spatne-heslo"}`); resp.StatusCode != 401 || m["error"] != "bad_password" {
		t.Fatalf("bad login: %d %v", resp.StatusCode, m)
	}
	resp, _ := h.do(t, c, "POST", "/api/login", `{"password":"heslo-k-voxu"}`)
	if resp.StatusCode != 200 {
		t.Fatalf("login: %d", resp.StatusCode)
	}
	var token string
	for _, k := range resp.Cookies() {
		if k.Name == cookieName {
			token = k.Value
		}
	}
	if token == "" {
		t.Fatal("no session cookie")
	}

	// WebSocket needs the cookie (checked before the upgrade) and a same-host Origin.
	if _, resp, err := dialWS(t, h, "", nil); err == nil || resp == nil || resp.StatusCode != 401 {
		t.Fatalf("ws without cookie: %v %v", err, resp)
	}
	if _, resp, err := dialWS(t, h, token, http.Header{"Origin": {"http://evil.example"}}); err == nil || resp == nil || resp.StatusCode != 403 {
		t.Fatalf("ws with foreign origin: %v %v", err, resp)
	}

	ws, _, err := dialWS(t, h, token, nil)
	if err != nil {
		t.Fatal(err)
	}
	if v := ws.text(t, "hello"); v["version"] != "0.1.0" {
		t.Errorf("hello = %v", v)
	}
	ws.ampState(t, "disconnected")
	if _, m := h.do(t, c, "GET", "/api/status", ""); m["controller"].(map[string]any)["active"] != true {
		t.Errorf("controller not active in status: %v", m["controller"])
	}

	modeReq := korg(0x12)
	ws.send(t, 0, modeReq)
	ws.expectError(t, "amp_disconnected", float64(0x12))

	// Amp appears: the daemon sends its own Device Inquiry, then waits for the reply.
	h.devices(true)
	port := <-h.ports
	if got := <-port.out; !bytes.Equal(got, []byte{0xF0, 0x7E, 0x7F, 0x06, 0x01, 0xF7}) {
		t.Fatalf("daemon wrote % X", got)
	}
	ws.ampState(t, "detected")
	identity := []byte{0xF0, 0x7E, 0x00, 0x06, 0x02, 0x42, 0x34, 0x01, 0x00, 0x00, 0x03, 0x00, 0x01, 0x00, 0xF7}
	port.in <- identity
	amp := ws.ampState(t, "connected")
	if amp["identity"] != "F0 7E 00 06 02 42 34 01 00 00 03 00 01 00 F7" || amp["usb_id"] != "0944:0117" {
		t.Errorf("amp = %v", amp)
	}
	ws.binary(t, append([]byte{0x00}, identity...)) // the reply is forwarded too

	// Allowed frame reaches the amp.
	ws.send(t, 0, modeReq)
	if got := <-port.out; !bytes.Equal(got, modeReq) {
		t.Fatalf("amp got % X", got)
	}

	// Forbidden and malformed frames.
	prog71 := bytes.Repeat([]byte{0x00}, 71)
	ws.send(t, 1, korg(0x40, prog71...)) // buffer load
	ws.expectError(t, "forbidden", float64(0x40))
	ws.send(t, 0, []byte{0xF0, 0x41, 0x10, 0x00, 0xF7}) // other manufacturer
	ws.expectError(t, "forbidden", nil)
	ws.send(t, 2, modeReq)
	ws.expectError(t, "bad_frame", float64(0x12))
	ws.send(t, 0, append(korg(0x41, 0x05, 0x00), make([]byte, 600)...))
	ws.expectError(t, "bad_frame", float64(0x41))
	if err := ws.c.Write(context.Background(), websocket.MessageBinary, nil); err != nil {
		t.Fatal(err)
	}
	ws.expectError(t, "bad_frame", nil)

	// Writes to amp memory need kind 1.
	write := korg(0x4C, append([]byte{0x00, 0x03}, prog71...)...)
	ws.send(t, 0, write)
	ws.expectError(t, "save_flag_required", float64(0x4C))
	ws.send(t, 1, write)
	if got := <-port.out; !bytes.Equal(got, write) {
		t.Fatalf("save not forwarded")
	}
	waitUntil(t, func() bool { return strings.Contains(h.logs.String(), "save requested: func=4C slot=3") })

	// Application-level ping.
	if err := ws.c.Write(context.Background(), websocket.MessageText, []byte(`{"t":"ping"}`)); err != nil {
		t.Fatal(err)
	}
	ws.text(t, "pong")

	// Amp -> client forwarding, realtime bytes stripped.
	ack := korg(0x23)
	port.in <- append(append([]byte{0xF8, 0xF0, 0x42}, ack[2:5]...), append([]byte{0xFE}, ack[5:]...)...)
	ws.binary(t, append([]byte{0x00}, ack...))

	// The same session (another tab, or a reconnect before the old socket timed out) takes over.
	ws2, _, err := dialWS(t, h, token, nil)
	if err != nil {
		t.Fatal(err)
	}
	ws2.text(t, "hello")
	ws.text(t, "replaced")
	m := ws.next(t, "close", func(m wsMsg) bool { return m.err != nil })
	if websocket.CloseStatus(m.err) != 4002 {
		t.Errorf("replaced client close = %v", m.err)
	}
	waitUntil(t, func() bool { return strings.Contains(h.logs.String(), "controller disconnected") })
	time.Sleep(50 * time.Millisecond) // let the old handler's deferred cleanup finish
	if _, m := h.do(t, c, "GET", "/api/status", ""); m["controller"].(map[string]any)["active"] != true {
		t.Errorf("controller not active after takeover: %v", m["controller"])
	}
	ws = ws2
	ws.ampState(t, "connected")
	port.in <- ack
	ws.binary(t, append([]byte{0x00}, ack...)) // amp frames now go to the new controller

	// A different session is turned away.
	c2 := jarClient()
	resp, _ = h.do(t, c2, "POST", "/api/login", `{"password":"heslo-k-voxu"}`)
	if resp.StatusCode != 200 {
		t.Fatalf("second login: %d", resp.StatusCode)
	}
	var token2 string
	for _, k := range resp.Cookies() {
		if k.Name == cookieName {
			token2 = k.Value
		}
	}
	other, _, err := dialWS(t, h, token2, nil)
	if err != nil {
		t.Fatal(err)
	}
	other.text(t, "busy")
	m = other.next(t, "close", func(m wsMsg) bool { return m.err != nil })
	if websocket.CloseStatus(m.err) != 4001 {
		t.Errorf("other session close = %v", m.err)
	}

	// Update staged: clients are told, writes are refused.
	if resp, _ := h.do(t, c, "POST", "/api/update", "package"); resp.StatusCode != 202 {
		t.Fatalf("update: %d", resp.StatusCode)
	}
	if v := ws.text(t, "update"); v["update"].(map[string]any)["state"] != "staged" {
		t.Errorf("update msg = %v", v)
	}
	ws.send(t, 0, modeReq)
	ws.expectError(t, "updating", float64(0x12))
	h.upd.mu.Lock()
	h.upd.info.State = "idle"
	h.upd.mu.Unlock()
	h.srv.RefreshUpdate()

	// Amp unplugged.
	h.devices(false)
	port.Close()
	ws.ampState(t, "disconnected")

	// Closing the controller frees the slot.
	ws.c.Close(websocket.StatusNormalClosure, "")
	waitUntil(t, func() bool {
		_, m := h.do(t, c, "GET", "/api/status", "")
		return m["controller"].(map[string]any)["active"] == false
	})
	ws3, _, err := dialWS(t, h, token, nil)
	if err != nil {
		t.Fatal(err)
	}
	ws3.text(t, "hello")

	// Logout invalidates the session for new WebSocket connections.
	h.do(t, c, "POST", "/api/logout", "")
	if _, resp, err := dialWS(t, h, token, nil); err == nil || resp.StatusCode != 401 {
		t.Errorf("ws after logout: %v", err)
	}
	if strings.Contains(h.logs.String(), token) || strings.Contains(h.logs.String(), "heslo-k-voxu") {
		t.Error("secret in logs")
	}
}
