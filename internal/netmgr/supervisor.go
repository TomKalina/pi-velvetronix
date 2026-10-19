package netmgr

import (
	"cmp"
	"context"
	"net"
	"slices"
	"strings"
	"time"
)

// Supervisor timing (design §8).
const (
	tickEvery   = 5 * time.Second
	bootWait    = 60 * time.Second // wait for STA after start
	staLostWait = 90 * time.Second // STA down this long -> hotspot
	apIdleWait  = 5 * time.Minute  // no hotspot client activity -> try home Wi-Fi
	apRetryWait = 30 * time.Second // after a failed hotspot start
)

// homeCandidates is how many visible saved networks one attempt to leave AP mode tries.
const homeCandidates = 3

// backoff between failed attempts to leave AP mode.
var backoff = []time.Duration{2 * time.Minute, 5 * time.Minute, 10 * time.Minute}

// Run is the supervisor loop. It returns when ctx is done.
func (m *Manager) Run(ctx context.Context) {
	m.start(ctx)
	t := time.NewTicker(tickEvery)
	defer t.Stop()
	for {
		m.step(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-m.wake:
		}
	}
}

func (m *Manager) start(ctx context.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	m.ctx = ctx
	m.staDeadline = now.Add(bootWait)
	m.lastActivity = now
}

type action int

const (
	actNone action = iota
	actRaiseAP
	actLeaveAP
)

// step observes NetworkManager once and takes at most one action.
func (m *Manager) step(ctx context.Context) {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	snap := m.poll(ctx)

	m.mu.Lock()
	now := m.now()
	m.snap = snap
	if snap.inAP() && snap.ssid != "" {
		m.apSSID = snap.ssid
	}
	act := actNone
	hotspotMinutes := 0
	switch {
	case !snap.ok:
		// NetworkManager unreachable: nothing sensible to do.
	case m.hotspotReq > 0:
		hotspotMinutes, m.hotspotReq = m.hotspotReq, 0
		if snap.inAP() {
			m.hotspotUntil = now.Add(time.Duration(hotspotMinutes) * time.Minute)
		} else {
			act = actRaiseAP
		}
	case snap.sta():
		m.staDeadline = time.Time{}
		m.failures = 0
		m.hotspotUntil = time.Time{}
	case snap.inAP():
		m.staDeadline = time.Time{}
		if !m.hotspotUntil.IsZero() {
			if !now.Before(m.hotspotUntil) {
				act = actLeaveAP
			}
		} else if now.Sub(m.lastActivity) >= apIdleWait && !now.Before(m.nextAttempt) {
			act = actLeaveAP
		}
	default: // off or NetworkManager still activating
		if m.staDeadline.IsZero() {
			m.staDeadline = now.Add(staLostWait)
			m.log.Info("wifi client connection lost")
		} else if !now.Before(m.staDeadline) {
			act = actRaiseAP
		}
	}
	if act != actNone {
		m.busy = true
	}
	m.mu.Unlock()

	switch act {
	case actRaiseAP:
		if m.raiseAP(ctx) {
			m.mu.Lock()
			now := m.now()
			m.lastActivity = now // grace period before the first attempt to leave
			if hotspotMinutes > 0 {
				m.hotspotUntil = now.Add(time.Duration(hotspotMinutes) * time.Minute)
			}
			m.mu.Unlock()
		}
	case actLeaveAP:
		m.leaveAP(ctx)
	}
	if act != actNone {
		snap := m.poll(ctx)
		m.mu.Lock()
		m.snap = snap
		m.busy = false
		m.mu.Unlock()
	}
}

// leaveAP rescans and brings up a visible saved network; on failure it
// returns to the hotspot and backs off. Caller holds opMu.
func (m *Manager) leaveAP(ctx context.Context) {
	m.mu.Lock()
	m.hotspotUntil = time.Time{}
	m.mu.Unlock()

	if !m.tryHome(ctx) {
		m.mu.Lock()
		m.failures++
		m.nextAttempt = m.now().Add(backoff[min(m.failures, len(backoff))-1])
		m.mu.Unlock()
	}
}

func (m *Manager) tryHome(ctx context.Context) bool {
	nets, err := m.scan(ctx, true)
	if err != nil {
		return false
	}
	saved, err := m.saved(ctx)
	if err != nil {
		return false
	}
	signal := map[string]int{}
	for _, n := range nets {
		signal[n.SSID] = n.Signal
	}
	var visible []Saved
	for _, s := range saved {
		if _, ok := signal[s.SSID]; ok {
			visible = append(visible, s)
		}
	}
	if len(visible) == 0 {
		m.log.Info("wifi no saved network visible, staying on hotspot")
		return false
	}
	// Best first, so one bad profile does not hide a working one.
	slices.SortFunc(visible, func(a, b Saved) int {
		return cmp.Or(cmp.Compare(b.Priority, a.Priority), cmp.Compare(signal[b.SSID], signal[a.SSID]))
	})
	for _, s := range visible[:min(len(visible), homeCandidates)] {
		if m.up(ctx, s.UUID, s.SSID) {
			return true
		}
	}
	m.raiseAP(ctx)
	return false
}

// poll reads the active wlan0 profile, its mode, the IPv4 address and the signal.
func (m *Manager) poll(ctx context.Context) snapshot {
	s := snapshot{signal: -1}
	out, err := m.nm(ctx, "-f", "UUID,TYPE,DEVICE,STATE", "connection", "show", "--active")
	if err != nil {
		return s
	}
	s.ok = true
	for _, f := range rows(out) {
		if len(f) >= 4 && f[2] == iface && isWifiType(f[1]) {
			s.uuid, s.activated = f[0], f[3] == "activated"
		}
	}
	s.ip = m.readIPv4(ctx)
	if s.uuid == "" {
		return s
	}
	p := m.profileCache.p
	if m.profileCache.uuid != s.uuid {
		if p, err = m.profile(ctx, s.uuid); err != nil {
			return s
		}
		m.profileCache.uuid, m.profileCache.p = s.uuid, p
	}
	s.ssid, s.ap = p.ssid, p.mode == "ap"
	if s.activated && !s.ap {
		if nets, err := m.scan(ctx, false); err == nil {
			for _, n := range nets {
				if n.InUse {
					s.signal = n.Signal
				}
			}
		}
	}
	return s
}

// readIPv4 parses "nmcli -t -g IP4.ADDRESS device show wlan0": "a/24 | b/24" or "".
func (m *Manager) readIPv4(ctx context.Context) net.IP {
	out, err := m.nm(ctx, "-g", "IP4.ADDRESS", "device", "show", iface)
	if err != nil {
		return nil
	}
	r := rows(out)
	if len(r) == 0 {
		return nil
	}
	first, _, _ := strings.Cut(r[0][0], " | ")
	addr, _, _ := strings.Cut(strings.TrimSpace(first), "/")
	return net.ParseIP(addr).To4()
}
