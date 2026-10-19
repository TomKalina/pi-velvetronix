package web

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"math"
	"net/http"
	"os"
	"strings"

	"github.com/TomKalina/pi-velvetronix/internal/auth"
	"github.com/TomKalina/pi-velvetronix/internal/netmgr"
	"github.com/TomKalina/pi-velvetronix/internal/update"
)

const (
	cookieName   = "vtx_session"
	cookieMaxAge = 30 * 24 * 3600
	indexFile    = "index.html.gz"
	maxJSONBody  = 4 << 10
)

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	gz, err := fs.ReadFile(s.Assets, indexFile)
	if err != nil {
		http.Error(w, "frontend not built", http.StatusServiceUnavailable)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-cache")
	h.Set("Vary", "Accept-Encoding")
	if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
		h.Set("Content-Encoding", "gzip")
		w.Write(gz)
		return
	}
	zr, err := gzip.NewReader(bytes.NewReader(gz))
	if err != nil {
		http.Error(w, "corrupt asset", http.StatusInternalServerError)
		return
	}
	io.Copy(w, zr)
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	if _, err := fs.Stat(s.Assets, indexFile); err != nil {
		http.Error(w, "frontend missing", http.StatusServiceUnavailable)
		return
	}
	f, err := os.CreateTemp(s.StateDir, ".healthz*")
	if err != nil {
		http.Error(w, "state dir not writable", http.StatusServiceUnavailable)
		return
	}
	f.Close()
	os.Remove(f.Name())
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	io.WriteString(w, "ok")
}

func sessionToken(r *http.Request) string {
	c, err := r.Cookie(cookieName)
	if err != nil {
		return ""
	}
	return c.Value
}

func (s *Server) authenticated(r *http.Request) bool {
	return s.Auth.Check(sessionToken(r))
}

func (s *Server) authed(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.authenticated(r) {
			writeError(w, http.StatusUnauthorized, "unauthorized", "")
			return
		}
		h(w, r)
	}
}

func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxJSONBody)).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return false
	}
	return true
}

func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{
		"authenticated": s.authenticated(r),
		"configured":    s.Auth.Configured(),
	})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	ip := remoteIP(r)
	token, err := s.Auth.Login(req.Password, ip)
	var rl auth.ErrRateLimited
	switch {
	case errors.As(err, &rl):
		writeJSON(w, http.StatusTooManyRequests, map[string]any{
			"error": "rate_limited", "retry_after": int(math.Ceil(rl.RetryAfter.Seconds())),
		})
	case errors.Is(err, auth.ErrNoPassword):
		writeError(w, http.StatusServiceUnavailable, "no_password", "")
	case errors.Is(err, auth.ErrBadPassword):
		s.Log.Warn("login failed", "ip", ip)
		writeError(w, http.StatusUnauthorized, "bad_password", "")
	case err != nil:
		s.Log.Error("login", "ip", ip, "error", err)
		writeError(w, http.StatusInternalServerError, "io_error", "")
	default:
		s.Log.Info("login", "ip", ip)
		http.SetCookie(w, &http.Cookie{Name: cookieName, Value: token, Path: "/", MaxAge: cookieMaxAge,
			HttpOnly: true, SameSite: http.SameSiteStrictMode})
		writeJSON(w, http.StatusOK, okBody)
	}
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	s.Auth.Logout(sessionToken(r))
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, SameSite: http.SameSiteStrictMode})
	writeJSON(w, http.StatusOK, okBody)
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.status())
}

// wifiError maps netmgr errors; error details are never echoed (they could carry input).
func (s *Server) wifiError(w http.ResponseWriter, op string, err error) {
	switch {
	case errors.Is(err, netmgr.ErrInvalid):
		writeError(w, http.StatusBadRequest, "bad_request", "")
	case errors.Is(err, netmgr.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "")
	case errors.Is(err, netmgr.ErrActiveConnection):
		writeError(w, http.StatusConflict, "active_connection", "")
	default:
		s.Log.Error("wifi "+op+" failed", "error", err)
		writeError(w, http.StatusInternalServerError, "wifi_failed", "")
	}
}

func (s *Server) handleNetworks(w http.ResponseWriter, r *http.Request) {
	nets, saved, err := s.Wifi.Networks(r.Context(), r.URL.Query().Get("rescan") == "1")
	if err != nil {
		s.wifiError(w, "networks", err)
		return
	}
	if nets == nil {
		nets = []netmgr.Network{}
	}
	if saved == nil {
		saved = []netmgr.Saved{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"networks": nets, "saved": saved})
}

func (s *Server) handleConnect(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SSID     string `json:"ssid"`
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := s.Wifi.Connect(r.Context(), req.SSID, req.Password); err != nil {
		s.wifiError(w, "connect", err)
		return
	}
	s.Log.Info("wifi connect requested", "ssid", req.SSID)
	writeJSON(w, http.StatusAccepted, okBody)
}

func (s *Server) handleForget(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UUID string `json:"uuid"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.UUID == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "uuid required")
		return
	}
	if err := s.Wifi.Forget(r.Context(), req.UUID); err != nil {
		s.wifiError(w, "forget", err)
		return
	}
	writeJSON(w, http.StatusOK, okBody)
}

func (s *Server) handleHotspot(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Minutes int `json:"minutes"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Minutes < 1 || req.Minutes > 30 {
		writeError(w, http.StatusBadRequest, "bad_request", "minutes must be 1..30")
		return
	}
	if err := s.Wifi.Hotspot(req.Minutes); err != nil {
		s.wifiError(w, "hotspot", err)
		return
	}
	s.Log.Info("hotspot requested", "minutes", req.Minutes)
	writeJSON(w, http.StatusAccepted, okBody)
}

func (s *Server) handleUpdateInfo(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.Updater.Info())
}

func (s *Server) handleUpdate(w http.ResponseWriter, r *http.Request) {
	if r.ContentLength > update.MaxBinary {
		writeError(w, http.StatusRequestEntityTooLarge, update.CodeTooLarge, "")
		return
	}
	if !s.uploadMu.TryLock() {
		writeError(w, http.StatusConflict, update.CodeBusy, "")
		return
	}
	defer s.uploadMu.Unlock()
	if s.Updater.Info().State != "idle" {
		writeError(w, http.StatusConflict, update.CodeBusy, "")
		return
	}

	// Validate enforces the exact limit; this only stops a runaway body.
	body := http.MaxBytesReader(w, r.Body, update.MaxBinary+1<<20)
	pkg, err := s.Updater.Validate(body)
	if err != nil {
		s.updateError(w, err)
		return
	}
	s.Amp.SetPaused(true)
	err = s.Updater.Stage(pkg)
	s.RefreshUpdate() // broadcasts "staged" (or unpauses again on failure)
	if err != nil {
		s.updateError(w, err)
		return
	}
	s.Log.Info("update staged", "version", pkg.Manifest.Version)
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true, "version": pkg.Manifest.Version})
}

func (s *Server) updateError(w http.ResponseWriter, err error) {
	var ue *update.Error
	if !errors.As(err, &ue) {
		ue = &update.Error{Code: update.CodeIOError}
	}
	status := http.StatusBadRequest
	switch ue.Code {
	case update.CodeTooLarge:
		status = http.StatusRequestEntityTooLarge
	case update.CodeBusy:
		status = http.StatusConflict
	case update.CodeIOError:
		status = http.StatusInternalServerError
	}
	s.Log.Warn("update rejected", "error", err)
	writeError(w, status, ue.Code, ue.Detail)
}
