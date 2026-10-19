// Package netmgr drives NetworkManager through nmcli: Wi-Fi client profiles, the
// fallback hotspot, and a supervisor that switches between them (design §8).
package netmgr

import (
	"cmp"
	"context"
	"errors"
	"log/slog"
	"net"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	iface         = "wlan0"
	defaultAPSSID = "VOX-Velvetronix"
	defaultAPIP   = "10.42.0.1"
)

var apNet = &net.IPNet{IP: net.IPv4(10, 42, 0, 0).To4(), Mask: net.CIDRMask(24, 32)}

var (
	// ErrActiveConnection: Forget was asked to delete the active profile (HTTP 409).
	ErrActiveConnection = errors.New("active_connection")
	// ErrNotFound: no saved Wi-Fi client profile with that UUID.
	ErrNotFound = errors.New("not_found")
	// ErrInvalid: bad SSID, PSK or hotspot duration.
	ErrInvalid = errors.New("invalid")
)

// Manager wraps nmcli and runs the STA/AP supervisor.
type Manager struct {
	apCon string
	log   *slog.Logger
	run   Runner
	now   func() time.Time
	spawn func(func()) // runs async activations; tests run them inline

	opMu sync.Mutex    // serializes supervisor steps and activations
	wake chan struct{} // nudges the supervisor loop

	mu           sync.Mutex
	ctx          context.Context // Run's context, used by async activations
	snap         snapshot
	apSSID       string
	busy         bool // an activation is in progress
	lastErr      string
	staDeadline  time.Time // when to raise the AP if STA is still not up; zero while STA is up
	lastActivity time.Time // last request from a hotspot client
	hotspotUntil time.Time // end of a user-requested hotspot; zero if none
	hotspotReq   int       // minutes of a pending hotspot request
	failures     int       // failed attempts to leave AP mode, for backoff
	nextAttempt  time.Time

	profileCache struct { // guarded by opMu
		uuid string
		p    profile
	}
}

// New returns a Manager for the hotspot connection id apCon.
func New(apCon string, log *slog.Logger) *Manager {
	return &Manager{
		apCon:  apCon,
		log:    log,
		run:    execRunner,
		now:    time.Now,
		spawn:  func(f func()) { go f() },
		wake:   make(chan struct{}, 1),
		ctx:    context.Background(),
		snap:   snapshot{signal: -1},
		apSSID: defaultAPSSID,
	}
}

// snapshot is what the supervisor last observed.
type snapshot struct {
	ok        bool   // nmcli answered
	uuid      string // active profile on wlan0
	ssid      string
	ap        bool
	activated bool
	ip        net.IP
	signal    int // -1 unknown
}

func (s snapshot) sta() bool  { return s.ok && s.uuid != "" && s.activated && !s.ap }
func (s snapshot) inAP() bool { return s.ok && s.uuid != "" && s.activated && s.ap }

// Status returns the last observed Wi-Fi state; it never runs nmcli.
func (m *Manager) Status() WifiStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.snap
	st := WifiStatus{APSSID: m.apSSID, APIP: defaultAPIP}
	switch {
	case m.busy:
		st.Mode = "connecting"
	case !s.ok:
		st.Mode = "unknown"
	case s.uuid != "" && !s.activated:
		st.Mode = "connecting"
	case s.uuid == "":
		st.Mode = "off"
	case s.ap:
		st.Mode = "ap"
	default:
		st.Mode = "sta"
	}
	if st.Mode == "sta" {
		st.SSID = ptr(s.ssid)
		if s.signal >= 0 {
			st.Signal = ptr(s.signal)
		}
	}
	if s.ip != nil {
		st.IP = ptr(s.ip.String())
	}
	if m.lastErr != "" {
		st.LastError = ptr(m.lastErr)
	}
	return st
}

func ptr[T any](v T) *T { return &v }

// IPv4 returns the last observed wlan0 IPv4 address, or nil.
func (m *Manager) IPv4() net.IP {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.snap.ip
}

// NoteClientActivity records a request from ip; requests from the hotspot subnet
// keep the hotspot up.
func (m *Manager) NoteClientActivity(ip net.IP) {
	if ip == nil || !apNet.Contains(ip) {
		return
	}
	m.mu.Lock()
	m.lastActivity = m.now()
	m.mu.Unlock()
}

type profile struct{ ssid, mode string }

func (m *Manager) profile(ctx context.Context, uuid string) (profile, error) {
	out, err := m.nm(ctx, "-f", "802-11-wireless.ssid,802-11-wireless.mode", "connection", "show", "uuid", uuid)
	if err != nil {
		return profile{}, err
	}
	kv := keyValues(out)
	return profile{ssid: kv["802-11-wireless.ssid"], mode: kv["802-11-wireless.mode"]}, nil
}

func isWifiType(t string) bool { return t == "802-11-wireless" || t == "wifi" }

// saved lists Wi-Fi client profiles (the hotspot and other AP-mode profiles excluded).
func (m *Manager) saved(ctx context.Context) ([]Saved, error) {
	out, err := m.nm(ctx, "-f", "NAME,UUID,TYPE,AUTOCONNECT-PRIORITY,ACTIVE", "connection", "show")
	if err != nil {
		return nil, err
	}
	var list []Saved
	for _, f := range rows(out) {
		if len(f) < 5 || !isWifiType(f[2]) || f[0] == m.apCon {
			continue
		}
		p, err := m.profile(ctx, f[1])
		if err != nil {
			return nil, err
		}
		if p.mode == "ap" {
			continue
		}
		prio, _ := strconv.Atoi(f[3])
		list = append(list, Saved{UUID: f[1], Name: f[0], SSID: p.ssid, Priority: prio, Active: f[4] == "yes"})
	}
	return list, nil
}

// scan returns visible networks, one entry per SSID with its strongest signal.
func (m *Manager) scan(ctx context.Context, rescan bool) ([]Network, error) {
	r := "no"
	if rescan {
		r = "yes"
	}
	out, err := m.nm(ctx, "-f", "IN-USE,SSID,SIGNAL,SECURITY", "device", "wifi", "list", "--rescan", r, "ifname", iface)
	if err != nil {
		return nil, err
	}
	var nets []Network
	idx := map[string]int{}
	for _, f := range rows(out) {
		if len(f) < 4 || f[1] == "" { // hidden network
			continue
		}
		sig, _ := strconv.Atoi(f[2])
		n := Network{SSID: f[1], Signal: sig, Security: f[3], InUse: strings.TrimSpace(f[0]) == "*"}
		i, seen := idx[n.SSID]
		if !seen {
			idx[n.SSID] = len(nets)
			nets = append(nets, n)
			continue
		}
		if n.Signal > nets[i].Signal {
			n.InUse = n.InUse || nets[i].InUse
			nets[i] = n
		} else {
			nets[i].InUse = nets[i].InUse || n.InUse
		}
	}
	slices.SortStableFunc(nets, func(a, b Network) int { return cmp.Compare(b.Signal, a.Signal) })
	return nets, nil
}

// Networks returns visible networks and saved profiles.
func (m *Manager) Networks(ctx context.Context, rescan bool) ([]Network, []Saved, error) {
	nets, err := m.scan(ctx, rescan)
	if err != nil {
		return nil, nil, err
	}
	saved, err := m.saved(ctx)
	if err != nil {
		return nil, nil, err
	}
	for i := range nets {
		nets[i].Saved = slices.ContainsFunc(saved, func(s Saved) bool { return s.SSID == nets[i].SSID })
	}
	return nets, saved, nil
}

func validPSK(psk string) bool {
	if len(psk) == 64 {
		for _, c := range psk {
			if !strings.ContainsRune("0123456789abcdefABCDEF", c) {
				return false
			}
		}
		return true
	}
	if len(psk) < 8 || len(psk) > 63 {
		return false
	}
	for i := range len(psk) {
		if psk[i] < 0x20 || psk[i] > 0x7E {
			return false
		}
	}
	return true
}

// Connect stores psk in the profile for ssid (creating "home-<ssid>" if there is
// none) and activates it in the background. The result shows up in Status.
func (m *Manager) Connect(ctx context.Context, ssid, psk string) error {
	if len(ssid) == 0 || len(ssid) > 32 || strings.ContainsAny(ssid, "\x00\n") || !validPSK(psk) {
		return ErrInvalid
	}
	saved, err := m.saved(ctx)
	if err != nil {
		return err
	}
	var uuid string
	if i := slices.IndexFunc(saved, func(s Saved) bool { return s.SSID == ssid }); i >= 0 {
		uuid = saved[i].UUID
		if _, err := m.nm(ctx, "connection", "modify", "uuid", uuid, "wifi-sec.psk", psk); err != nil {
			return err
		}
	} else {
		name := "home-" + ssid
		out, err := m.nm(ctx, "connection", "add", "type", "wifi", "ifname", iface, "con-name", name, "ssid", ssid,
			"wifi-sec.key-mgmt", "wpa-psk", "wifi-sec.psk", psk,
			"connection.autoconnect", "yes", "connection.autoconnect-priority", "10",
			"connection.autoconnect-retries", "0", "ipv4.method", "auto", "ipv6.method", "auto")
		if err != nil {
			return err
		}
		if uuid = parseAddedUUID(out); uuid == "" {
			return errors.New("netmgr: cannot parse UUID of the new profile")
		}
		m.log.Info("wifi profile added", "name", name, "uuid", uuid)
	}
	m.mu.Lock()
	m.busy = true
	m.lastErr = ""
	m.mu.Unlock()
	m.spawn(func() { m.activate(uuid, ssid) })
	return nil
}

// activate brings up a client profile; on failure it records last_error and, if the
// hotspot was up before, returns to it so the user keeps access.
func (m *Manager) activate(uuid, ssid string) {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	m.mu.Lock()
	ctx, wasAP := m.ctx, m.snap.inAP()
	m.busy = true
	m.mu.Unlock()
	defer m.endBusy()

	if m.up(ctx, uuid, ssid) {
		return
	}
	if wasAP {
		m.raiseAP(ctx)
	}
}

func (m *Manager) endBusy() {
	m.mu.Lock()
	m.busy = false
	m.mu.Unlock()
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

// up activates a client profile and updates the supervisor state. Caller holds opMu.
func (m *Manager) up(ctx context.Context, uuid, ssid string) bool {
	m.log.Info("wifi connecting", "ssid", ssid, "uuid", uuid)
	_, err := m.nm(ctx, "--wait", "45", "connection", "up", "uuid", uuid, "ifname", iface)
	m.mu.Lock()
	defer m.mu.Unlock()
	if err != nil {
		m.lastErr = "connect_failed: " + ssid
		m.staDeadline = m.now().Add(staLostWait)
		return false
	}
	m.lastErr = ""
	m.failures = 0
	m.hotspotUntil = time.Time{}
	m.staDeadline = time.Time{}
	return true
}

// raiseAP brings up the hotspot. Caller holds opMu.
func (m *Manager) raiseAP(ctx context.Context) bool {
	m.log.Info("wifi starting hotspot", "con", m.apCon)
	_, err := m.nm(ctx, "--wait", "30", "connection", "up", "id", m.apCon)
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	if err != nil {
		m.lastErr = "hotspot_failed"
		m.staDeadline = now.Add(apRetryWait)
		return false
	}
	m.staDeadline = time.Time{}
	return true
}

// Forget deletes a saved Wi-Fi client profile. The active one is refused.
func (m *Manager) Forget(ctx context.Context, uuid string) error {
	saved, err := m.saved(ctx)
	if err != nil {
		return err
	}
	i := slices.IndexFunc(saved, func(s Saved) bool { return s.UUID == uuid })
	if i < 0 {
		return ErrNotFound
	}
	if saved[i].Active {
		return ErrActiveConnection
	}
	if _, err := m.nm(ctx, "connection", "delete", "uuid", uuid); err != nil {
		return err
	}
	m.log.Info("wifi profile deleted", "name", saved[i].Name, "uuid", uuid)
	return nil
}

// Hotspot switches to the hotspot now and tries home Wi-Fi again after minutes.
// The switch itself runs in the supervisor loop.
func (m *Manager) Hotspot(minutes int) error {
	if minutes < 1 || minutes > 30 {
		return ErrInvalid
	}
	m.mu.Lock()
	m.hotspotReq = minutes
	m.mu.Unlock()
	select {
	case m.wake <- struct{}{}:
	default:
	}
	return nil
}
