package web

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/TomKalina/pi-velvetronix/internal/auth"
	"github.com/TomKalina/pi-velvetronix/internal/bridge"
	"github.com/TomKalina/pi-velvetronix/internal/midi"
	"github.com/TomKalina/pi-velvetronix/internal/netmgr"
	"github.com/TomKalina/pi-velvetronix/internal/update"
)

// --- fakes ---

type fakePort struct {
	in     chan []byte // amp -> host
	out    chan []byte // host -> amp
	closed chan struct{}
	once   sync.Once
}

func newFakePort() *fakePort {
	return &fakePort{in: make(chan []byte, 16), out: make(chan []byte, 64), closed: make(chan struct{})}
}

func (p *fakePort) Read(b []byte) (int, error) {
	select {
	case d := <-p.in:
		return copy(b, d), nil
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

type fakeWifi struct {
	mu      sync.Mutex
	calls   []string
	err     error
	nets    []netmgr.Network
	saved   []netmgr.Saved
	noted   int
	lastPSK string
}

func (f *fakeWifi) Status() netmgr.WifiStatus {
	ssid, ip := "HomeNet", "192.168.1.86"
	sig := 70
	return netmgr.WifiStatus{Mode: "sta", SSID: &ssid, IP: &ip, Signal: &sig, APSSID: "VOX-Velvetronix", APIP: "10.42.0.1"}
}

func (f *fakeWifi) record(c string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, c)
	return f.err
}

func (f *fakeWifi) Networks(ctx context.Context, rescan bool) ([]netmgr.Network, []netmgr.Saved, error) {
	if rescan {
		return f.nets, f.saved, f.record("networks rescan")
	}
	return f.nets, f.saved, f.record("networks")
}

func (f *fakeWifi) Connect(ctx context.Context, ssid, psk string) error {
	f.mu.Lock()
	f.lastPSK = psk
	f.mu.Unlock()
	return f.record("connect " + ssid)
}

func (f *fakeWifi) Forget(ctx context.Context, uuid string) error { return f.record("forget " + uuid) }

func (f *fakeWifi) Hotspot(minutes int) error { return f.record("hotspot") }

func (f *fakeWifi) NoteClientActivity(ip net.IP) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if ip != nil {
		f.noted++
	}
}

type fakeUpdater struct {
	mu          sync.Mutex
	info        update.UpdateInfo
	validateErr error
	stageErr    error
	body        []byte
}

func (f *fakeUpdater) Info() update.UpdateInfo {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.info
}

func (f *fakeUpdater) Validate(r io.Reader) (*update.Package, error) {
	b, err := io.ReadAll(r)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.body = b
	if err != nil {
		return nil, err
	}
	if f.validateErr != nil {
		return nil, f.validateErr
	}
	return &update.Package{Manifest: update.Manifest{Version: "0.2.0"}}, nil
}

func (f *fakeUpdater) Stage(p *update.Package) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.stageErr != nil {
		return f.stageErr
	}
	f.info.State = "staged"
	return nil
}

type fakeAuth struct {
	configured bool
	loginErr   error
	tokens     map[string]bool
}

func (f *fakeAuth) Configured() bool { return f.configured }
func (f *fakeAuth) Login(pw, ip string) (string, error) {
	if f.loginErr != nil {
		return "", f.loginErr
	}
	f.tokens["tok"] = true
	return "tok", nil
}
func (f *fakeAuth) Check(t string) bool { return f.tokens[t] }
func (f *fakeAuth) Logout(t string)     { delete(f.tokens, t) }

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// --- harness ---

var testDev = midi.Device{Card: "1", ID: "ValvetronixX", Name: "Valvetronix X", UsbID: "0944:0117", Node: "/dev/snd/midiC1D0"}

type harness struct {
	ts      *httptest.Server
	srv     *Server
	state   string
	wifi    *fakeWifi
	upd     *fakeUpdater
	ports   chan *fakePort
	bridge  *bridge.Bridge
	logs    *syncBuf
	devices func(bool)
}

func gzipBytes(s string) []byte {
	var b bytes.Buffer
	w := gzip.NewWriter(&b)
	io.WriteString(w, s)
	w.Close()
	return b.Bytes()
}

func newHarness(t *testing.T, a Auth, assets fstest.MapFS) *harness {
	t.Helper()
	h := &harness{state: t.TempDir(), wifi: &fakeWifi{}, upd: &fakeUpdater{info: update.UpdateInfo{State: "idle", CurrentVersion: "0.1.0"}},
		ports: make(chan *fakePort, 4), logs: &syncBuf{}}
	if assets == nil {
		assets = fstest.MapFS{indexFile: {Data: gzipBytes("<!doctype html><title>VOX</title>")}}
	}
	log := slog.New(slog.NewTextHandler(h.logs, &slog.HandlerOptions{Level: slog.LevelDebug}))

	var mu sync.Mutex
	present := false
	h.devices = func(p bool) { mu.Lock(); present = p; mu.Unlock() }
	discover := func() ([]midi.Device, error) {
		mu.Lock()
		defer mu.Unlock()
		if !present {
			return nil, nil
		}
		return []midi.Device{testDev}, nil
	}
	open := func(string) (midi.Port, error) {
		p := newFakePort()
		h.ports <- p
		return p, nil
	}
	h.bridge = bridge.NewWithIO(discover, open, log)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { h.bridge.Run(ctx); close(done) }()

	h.srv = New(ctx, Config{Version: "0.1.0", StateDir: h.state, Assets: assets, Auth: a, Amp: FromBridge(h.bridge),
		Wifi: h.wifi, Updater: h.upd, MDNS: NewMDNSState("velvetronix.local", "vox.local"), Log: log})
	h.ts = httptest.NewServer(h.srv.Handler())
	t.Cleanup(func() {
		cancel()
		h.ts.Close()
		<-done
	})
	return h
}

func (h *harness) do(t *testing.T, c *http.Client, method, path, body string) (*http.Response, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(method, h.ts.URL+path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	var m map[string]any
	json.Unmarshal(data, &m)
	return resp, m
}

func jarClient() *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{Jar: jar}
}

func fakeAuthed() *fakeAuth {
	return &fakeAuth{configured: true, tokens: map[string]bool{}}
}

func loggedIn(t *testing.T, h *harness) *http.Client {
	t.Helper()
	c := jarClient()
	if resp, _ := h.do(t, c, "POST", "/api/login", `{"password":"x"}`); resp.StatusCode != 200 {
		t.Fatalf("login: %d", resp.StatusCode)
	}
	return c
}

// --- tests ---

func TestIndex(t *testing.T) {
	h := newHarness(t, fakeAuthed(), nil)
	req, _ := http.NewRequest("GET", h.ts.URL+"/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	resp, err := http.DefaultTransport.RoundTrip(req) // no transparent decompression
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.Header.Get("Content-Encoding") != "gzip" || resp.Header.Get("Cache-Control") != "no-cache" ||
		!bytes.HasPrefix(body, []byte{0x1f, 0x8b}) || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") {
		t.Errorf("gzip index: %v %q", resp.Header, body[:2])
	}

	req.Header.Del("Accept-Encoding")
	resp, err = http.DefaultTransport.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.Header.Get("Content-Encoding") != "" || !strings.Contains(string(body), "<title>VOX</title>") {
		t.Errorf("plain index: %v %q", resp.Header, body)
	}

	if resp, _ := h.do(t, http.DefaultClient, "GET", "/nope", ""); resp.StatusCode != 404 {
		t.Errorf("unknown path: %d", resp.StatusCode)
	}
}

func TestHealthz(t *testing.T) {
	h := newHarness(t, fakeAuthed(), nil)
	resp, _ := h.do(t, http.DefaultClient, "GET", "/healthz", "")
	if resp.StatusCode != 200 {
		t.Errorf("healthz = %d", resp.StatusCode)
	}

	missing := newHarness(t, fakeAuthed(), fstest.MapFS{".gitkeep": {}})
	if resp, _ := missing.do(t, http.DefaultClient, "GET", "/healthz", ""); resp.StatusCode != 503 {
		t.Errorf("healthz without asset = %d", resp.StatusCode)
	}
	if resp, _ := missing.do(t, http.DefaultClient, "GET", "/", ""); resp.StatusCode != 503 {
		t.Errorf("index without asset = %d", resp.StatusCode)
	}

	os.RemoveAll(h.state)
	if resp, _ := h.do(t, http.DefaultClient, "GET", "/healthz", ""); resp.StatusCode != 503 {
		t.Errorf("healthz without state dir = %d", resp.StatusCode)
	}
}

func TestEmbeddedAssets(t *testing.T) {
	// The embedded FS must at least contain the committed .gitkeep.
	if _, err := Assets().Open(".gitkeep"); err != nil {
		t.Errorf("embedded dist: %v", err)
	}
}

func TestLoginErrors(t *testing.T) {
	a := fakeAuthed()
	h := newHarness(t, a, nil)
	c := jarClient()

	cases := []struct {
		err    error
		status int
		code   string
	}{
		{auth.ErrBadPassword, 401, "bad_password"},
		{auth.ErrNoPassword, 503, "no_password"},
		{auth.ErrRateLimited{RetryAfter: 1500 * time.Millisecond}, 429, "rate_limited"},
		{errors.New("disk full"), 500, "io_error"},
	}
	for _, tc := range cases {
		a.loginErr = tc.err
		resp, m := h.do(t, c, "POST", "/api/login", `{"password":"secret-pw"}`)
		if resp.StatusCode != tc.status || m["error"] != tc.code {
			t.Errorf("%v: %d %v", tc.err, resp.StatusCode, m)
		}
		if tc.code == "rate_limited" && m["retry_after"] != 2.0 {
			t.Errorf("retry_after = %v", m["retry_after"])
		}
	}
	if resp, m := h.do(t, c, "POST", "/api/login", `not json`); resp.StatusCode != 400 || m["error"] != "bad_request" {
		t.Errorf("bad json: %d %v", resp.StatusCode, m)
	}
	if strings.Contains(h.logs.String(), "secret-pw") {
		t.Error("password logged")
	}
}

func TestSessionLoginLogout(t *testing.T) {
	h := newHarness(t, fakeAuthed(), nil)
	c := jarClient()
	_, m := h.do(t, c, "GET", "/api/session", "")
	if m["authenticated"] != false || m["configured"] != true {
		t.Errorf("session before login: %v", m)
	}
	if resp, m := h.do(t, c, "GET", "/api/status", ""); resp.StatusCode != 401 || m["error"] != "unauthorized" {
		t.Errorf("status unauthenticated: %d %v", resp.StatusCode, m)
	}

	resp, m := h.do(t, c, "POST", "/api/login", `{"password":"x"}`)
	if resp.StatusCode != 200 || m["ok"] != true {
		t.Fatalf("login: %d %v", resp.StatusCode, m)
	}
	var ck *http.Cookie
	for _, k := range resp.Cookies() {
		if k.Name == cookieName {
			ck = k
		}
	}
	if ck == nil || ck.Value != "tok" || !ck.HttpOnly || ck.SameSite != http.SameSiteStrictMode || ck.Path != "/" || ck.MaxAge != 2592000 || ck.Secure {
		t.Errorf("cookie = %+v", ck)
	}
	if !strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json; charset=utf-8") {
		t.Errorf("content type %q", resp.Header.Get("Content-Type"))
	}
	if _, m := h.do(t, c, "GET", "/api/session", ""); m["authenticated"] != true {
		t.Errorf("session after login: %v", m)
	}

	resp, m = h.do(t, c, "POST", "/api/logout", "")
	if resp.StatusCode != 200 || m["ok"] != true {
		t.Errorf("logout: %d %v", resp.StatusCode, m)
	}
	if resp, _ := h.do(t, c, "GET", "/api/status", ""); resp.StatusCode != 401 {
		t.Errorf("status after logout: %d", resp.StatusCode)
	}
}

func TestCrossOrigin(t *testing.T) {
	h := newHarness(t, fakeAuthed(), nil)
	req, _ := http.NewRequest("POST", h.ts.URL+"/api/login", strings.NewReader(`{"password":"x"}`))
	req.Header.Set("Origin", "http://evil.example")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("cross-origin POST = %d", resp.StatusCode)
	}
	req, _ = http.NewRequest("POST", h.ts.URL+"/api/login", strings.NewReader(`{"password":"x"}`))
	req.Header.Set("Origin", h.ts.URL)
	resp, _ = http.DefaultClient.Do(req)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("same-origin POST = %d", resp.StatusCode)
	}
}

func TestStatus(t *testing.T) {
	h := newHarness(t, fakeAuthed(), nil)
	c := loggedIn(t, h)
	resp, m := h.do(t, c, "GET", "/api/status", "")
	if resp.StatusCode != 200 {
		t.Fatal(resp.StatusCode)
	}
	for _, k := range []string{"version", "hostname", "uptime_s", "addresses", "wifi", "mdns", "amp", "controller", "update", "auth"} {
		if _, ok := m[k]; !ok {
			t.Errorf("status lacks %q", k)
		}
	}
	if m["version"] != "0.1.0" {
		t.Errorf("version %v", m["version"])
	}
	if _, ok := m["addresses"].([]any); !ok {
		t.Errorf("addresses = %v", m["addresses"])
	}
	if w := m["wifi"].(map[string]any); w["mode"] != "sta" || w["ssid"] != "HomeNet" || w["last_error"] != nil {
		t.Errorf("wifi %v", w)
	}
	if md := m["mdns"].(map[string]any); md["alias"] != "vox.local" || md["alias_state"] != "off" {
		t.Errorf("mdns %v", md)
	}
	if a := m["amp"].(map[string]any); a["state"] != "disconnected" || a["card"] != nil {
		t.Errorf("amp %v", a)
	}
	if m["controller"].(map[string]any)["active"] != false || m["auth"].(map[string]any)["configured"] != true {
		t.Errorf("controller/auth %v %v", m["controller"], m["auth"])
	}
	if u := m["update"].(map[string]any); u["state"] != "idle" || u["previous_version"] != nil {
		t.Errorf("update %v", u)
	}
}

func TestWifiHandlers(t *testing.T) {
	h := newHarness(t, fakeAuthed(), nil)
	c := loggedIn(t, h)

	resp, m := h.do(t, c, "GET", "/api/wifi/networks", "")
	if resp.StatusCode != 200 || m["networks"] == nil || m["saved"] == nil {
		t.Errorf("networks: %d %v", resp.StatusCode, m)
	}
	h.wifi.nets = []netmgr.Network{{SSID: "HomeNet", Signal: 70, Security: "WPA2", InUse: true, Saved: true}}
	_, m = h.do(t, c, "GET", "/api/wifi/networks?rescan=1", "")
	if n := m["networks"].([]any); len(n) != 1 || n[0].(map[string]any)["in_use"] != true {
		t.Errorf("networks rescan: %v", m)
	}

	if resp, m := h.do(t, c, "POST", "/api/wifi/connect", `{"ssid":"HomeNet","password":"tajneheslo"}`); resp.StatusCode != 202 || m["ok"] != true {
		t.Errorf("connect: %d %v", resp.StatusCode, m)
	}
	if h.wifi.lastPSK != "tajneheslo" || strings.Contains(h.logs.String(), "tajneheslo") {
		t.Error("psk not passed or logged")
	}
	if resp, _ := h.do(t, c, "POST", "/api/wifi/forget", `{"uuid":"u-1"}`); resp.StatusCode != 200 {
		t.Errorf("forget: %d", resp.StatusCode)
	}
	if resp, _ := h.do(t, c, "POST", "/api/wifi/hotspot", `{"minutes":10}`); resp.StatusCode != 202 {
		t.Errorf("hotspot: %d", resp.StatusCode)
	}
	if resp, _ := h.do(t, c, "POST", "/api/wifi/hotspot", `{"minutes":31}`); resp.StatusCode != 400 {
		t.Errorf("hotspot 31: %d", resp.StatusCode)
	}
	if h.wifi.noted == 0 {
		t.Error("client activity not reported to netmgr")
	}
	want := []string{"networks", "networks rescan", "connect HomeNet", "forget u-1", "hotspot"}
	if strings.Join(h.wifi.calls, ",") != strings.Join(want, ",") {
		t.Errorf("calls = %v", h.wifi.calls)
	}

	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{netmgr.ErrActiveConnection, 409, "active_connection"},
		{netmgr.ErrNotFound, 404, "not_found"},
		{netmgr.ErrInvalid, 400, "bad_request"},
		{errors.New("nmcli exploded"), 500, "wifi_failed"},
	} {
		h.wifi.err = tc.err
		if resp, m := h.do(t, c, "POST", "/api/wifi/forget", `{"uuid":"u-1"}`); resp.StatusCode != tc.status || m["error"] != tc.code {
			t.Errorf("forget %v: %d %v", tc.err, resp.StatusCode, m)
		}
	}
}

func TestUpdateHandler(t *testing.T) {
	h := newHarness(t, fakeAuthed(), nil)
	c := loggedIn(t, h)

	if _, m := h.do(t, c, "GET", "/api/update", ""); m["state"] != "idle" || m["current_version"] != "0.1.0" {
		t.Errorf("info %v", m)
	}

	for _, tc := range []struct {
		code   string
		status int
	}{
		{update.CodeNotGzip, 400}, {update.CodeWrongArch, 400}, {update.CodeTooLarge, 413}, {update.CodeIOError, 500},
	} {
		h.upd.validateErr = &update.Error{Code: tc.code}
		if resp, m := h.do(t, c, "POST", "/api/update", "x"); resp.StatusCode != tc.status || m["error"] != tc.code {
			t.Errorf("%s: %d %v", tc.code, resp.StatusCode, m)
		}
	}
	h.upd.validateErr = nil

	// Declared body over the limit is refused before reading.
	req, _ := http.NewRequest("POST", h.ts.URL+"/api/update", strings.NewReader("x"))
	req.ContentLength = update.MaxBinary + 1
	req.Body = io.NopCloser(io.LimitReader(zeroReader{}, update.MaxBinary+1))
	resp, err := c.Do(req)
	if err == nil {
		resp.Body.Close()
		if resp.StatusCode != 413 {
			t.Errorf("too large: %d", resp.StatusCode)
		}
	}

	h.upd.stageErr = &update.Error{Code: update.CodeIOError, Detail: "disk full"}
	if resp, _ := h.do(t, c, "POST", "/api/update", "pkg"); resp.StatusCode != 500 {
		t.Errorf("stage failure: %d", resp.StatusCode)
	}
	h.upd.stageErr = nil

	resp, m := h.do(t, c, "POST", "/api/update", "pkg-bytes")
	if resp.StatusCode != 202 || m["ok"] != true || m["version"] != "0.2.0" || string(h.upd.body) != "pkg-bytes" {
		t.Errorf("update: %d %v", resp.StatusCode, m)
	}
	if resp, m := h.do(t, c, "POST", "/api/update", "again"); resp.StatusCode != 409 || m["error"] != "busy" {
		t.Errorf("second update: %d %v", resp.StatusCode, m)
	}

	if resp, _ := h.do(t, jarClient(), "POST", "/api/update", "x"); resp.StatusCode != 401 {
		t.Errorf("unauthenticated update: %d", resp.StatusCode)
	}
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

func TestUpdateWatcherUnpauses(t *testing.T) {
	h := newHarness(t, fakeAuthed(), nil)
	h.devices(true)
	p := <-h.ports
	<-p.out // inquiry
	ctrl, _ := h.srv.Amp.Attach("test")
	defer ctrl.Detach()
	modeReq := []byte{0xF0, 0x42, 0x30, 0x00, 0x01, 0x34, 0x12, 0xF7}
	waitUntil(t, func() bool { return ctrl.Send(0, modeReq) == nil })

	h.upd.mu.Lock()
	h.upd.info.State = "applying"
	h.upd.mu.Unlock()
	h.srv.RefreshUpdate()
	if err := ctrl.Send(0, modeReq); !errors.Is(err, bridge.ErrUpdating) {
		t.Errorf("applying: %v", err)
	}
	// The updater failed and removed its files: back to idle.
	h.upd.mu.Lock()
	h.upd.info.State = "idle"
	h.upd.info.LastResult = "failed sha256"
	h.upd.mu.Unlock()
	waitUntil(t, func() bool { return ctrl.Send(0, modeReq) == nil }) // via the 2 s poller
}

func waitUntil(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
