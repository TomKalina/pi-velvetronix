// Package web serves the UI, the JSON API (design §5) and the WebSocket bridge (§7).
package web

import (
	"context"
	"embed"
	"encoding/json"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/TomKalina/pi-velvetronix/internal/bridge"
	"github.com/TomKalina/pi-velvetronix/internal/netmgr"
	"github.com/TomKalina/pi-velvetronix/internal/update"
)

//go:embed all:dist
var dist embed.FS

// Assets returns the embedded frontend build (index.html.gz at the root).
func Assets() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err) // "dist" is a valid literal path
	}
	return sub
}

// Auth is implemented by *auth.Store.
type Auth interface {
	Configured() bool
	Login(password, remoteIP string) (token string, err error)
	Check(token string) bool
	Logout(token string)
}

// Controller is the controlling client's handle on the amp (*bridge.Controller).
type Controller interface {
	Send(kind byte, frame []byte) error
	Frames() <-chan []byte
	Replaced() <-chan struct{}
	Detach()
}

// Amp is the bridge; use FromBridge to adapt *bridge.Bridge.
type Amp interface {
	Attach(owner string) (Controller, bool)
	Status() bridge.AmpStatus
	Subscribe() (<-chan bridge.AmpStatus, func())
	SetPaused(bool)
}

// Wifi is implemented by *netmgr.Manager.
type Wifi interface {
	Status() netmgr.WifiStatus
	Networks(ctx context.Context, rescan bool) ([]netmgr.Network, []netmgr.Saved, error)
	Connect(ctx context.Context, ssid, psk string) error
	Forget(ctx context.Context, uuid string) error
	Hotspot(minutes int) error
}

// activityNoter is optionally implemented by Wifi: hotspot clients keep the hotspot up.
type activityNoter interface{ NoteClientActivity(ip net.IP) }

// Updater validates and stages update packages; see NewUpdater.
type Updater interface {
	Info() update.UpdateInfo
	Validate(r io.Reader) (*update.Package, error)
	Stage(p *update.Package) error
}

// MDNS reports the mDNS alias state; see MDNSState.
type MDNS interface{ Status() MDNSStatus }

// MDNSStatus is the "mdns" object of the HTTP status.
type MDNSStatus struct {
	Hostname   string `json:"hostname"`
	Alias      string `json:"alias"`
	AliasState string `json:"alias_state"`
}

// Config holds the server's dependencies.
type Config struct {
	Version  string
	StateDir string
	Assets   fs.FS
	Auth     Auth
	Amp      Amp
	Wifi     Wifi
	Updater  Updater
	MDNS     MDNS
	Log      *slog.Logger
}

const updatePoll = 2 * time.Second

// Server is the HTTP + WebSocket front end.
type Server struct {
	Config
	ctx     context.Context
	started time.Time
	handler http.Handler

	ctrlMu     sync.Mutex // orders Attach/Detach with ctrlActive updates
	ctrlActive atomic.Bool
	uploadMu   sync.Mutex // one update upload at a time

	mu         sync.Mutex
	lastUpdate update.UpdateInfo
	updateSubs map[chan update.UpdateInfo]struct{}
}

// New creates the server. WebSocket sessions and the update watcher stop when ctx is done.
func New(ctx context.Context, cfg Config) *Server {
	s := &Server{Config: cfg, ctx: ctx, started: time.Now(), updateSubs: map[chan update.UpdateInfo]struct{}{}}
	s.handler = s.routes()
	s.RefreshUpdate()
	go s.watchUpdates()
	return s
}

// Handler returns the root handler.
func (s *Server) Handler() http.Handler { return s.handler }

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.handleIndex)
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("GET /api/session", s.handleSession)
	mux.HandleFunc("POST /api/login", s.handleLogin)
	mux.HandleFunc("POST /api/logout", s.authed(s.handleLogout))
	mux.HandleFunc("GET /api/status", s.authed(s.handleStatus))
	mux.HandleFunc("GET /api/wifi/networks", s.authed(s.handleNetworks))
	mux.HandleFunc("POST /api/wifi/connect", s.authed(s.handleConnect))
	mux.HandleFunc("POST /api/wifi/forget", s.authed(s.handleForget))
	mux.HandleFunc("POST /api/wifi/hotspot", s.authed(s.handleHotspot))
	mux.HandleFunc("GET /api/update", s.authed(s.handleUpdateInfo))
	mux.HandleFunc("POST /api/update", s.authed(s.handleUpdate))
	mux.HandleFunc("GET /ws", s.handleWS)

	var h http.Handler = http.NewCrossOriginProtection().Handler(mux)
	if n, ok := s.Wifi.(activityNoter); ok {
		next := h
		h = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			n.NoteClientActivity(net.ParseIP(remoteIP(r)))
			next.ServeHTTP(w, r)
		})
	}
	return h
}

// watchUpdates polls the update state so changes made by the root updater (applying,
// failed and back to idle) reach clients and the bridge pause follows them.
func (s *Server) watchUpdates() {
	t := time.NewTicker(updatePoll)
	defer t.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-t.C:
			// Skip while an upload is staging: it pauses the bridge itself.
			if s.uploadMu.TryLock() {
				s.RefreshUpdate()
				s.uploadMu.Unlock()
			}
		}
	}
}

// RefreshUpdate re-reads the update state now (e.g. after the release was confirmed),
// pauses the bridge unless idle and notifies WebSocket clients on change.
func (s *Server) RefreshUpdate() update.UpdateInfo {
	info := s.Updater.Info()
	s.Amp.SetPaused(info.State != "idle")
	s.mu.Lock()
	defer s.mu.Unlock()
	if info == s.lastUpdate {
		return info
	}
	s.lastUpdate = info
	for ch := range s.updateSubs {
		select { // keep only the latest value in the 1-slot buffer
		case <-ch:
		default:
		}
		ch <- info
	}
	return info
}

func (s *Server) subscribeUpdates() (<-chan update.UpdateInfo, func()) {
	ch := make(chan update.UpdateInfo, 1)
	s.mu.Lock()
	s.updateSubs[ch] = struct{}{}
	s.mu.Unlock()
	return ch, func() {
		s.mu.Lock()
		delete(s.updateSubs, ch)
		s.mu.Unlock()
	}
}

// Status is the response of GET /api/status.
type Status struct {
	Version    string            `json:"version"`
	Hostname   string            `json:"hostname"`
	UptimeS    int64             `json:"uptime_s"`
	Addresses  []string          `json:"addresses"`
	Wifi       netmgr.WifiStatus `json:"wifi"`
	MDNS       MDNSStatus        `json:"mdns"`
	Amp        bridge.AmpStatus  `json:"amp"`
	Controller struct {
		Active bool `json:"active"`
	} `json:"controller"`
	Update update.UpdateInfo `json:"update"`
	Auth   struct {
		Configured bool `json:"configured"`
	} `json:"auth"`
}

func (s *Server) status() Status {
	host, _ := os.Hostname()
	st := Status{
		Version:   s.Version,
		Hostname:  host,
		UptimeS:   int64(time.Since(s.started).Seconds()),
		Addresses: ipv4Addresses(),
		Wifi:      s.Wifi.Status(),
		MDNS:      s.MDNS.Status(),
		Amp:       s.Amp.Status(),
		Update:    s.Updater.Info(),
	}
	st.Controller.Active = s.ctrlActive.Load()
	st.Auth.Configured = s.Auth.Configured()
	return st
}

func ipv4Addresses() []string {
	out := []string{}
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return out
	}
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && !n.IP.IsLoopback() && n.IP.To4() != nil {
			out = append(out, n.IP.String())
		}
	}
	return out
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

type apiError struct {
	Error  string `json:"error"`
	Detail string `json:"detail,omitempty"`
}

func writeError(w http.ResponseWriter, code int, errCode, detail string) {
	writeJSON(w, code, apiError{Error: errCode, Detail: detail})
}

var okBody = map[string]bool{"ok": true}

func remoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
