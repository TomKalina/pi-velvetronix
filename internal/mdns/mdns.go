// Package mdns publishes an extra mDNS host name (e.g. vox.local) for the wlan0
// address through Avahi, like "avahi-publish -a -R <alias> <ip>".
package mdns

import (
	"context"
	"log/slog"
	"net"
	"time"
)

// Avahi entry group states (avahi-common/defs.h).
const (
	groupRegistering = 1
	groupEstablished = 2
	groupCollision   = 3
	groupFailure     = 4

	serverRunning = 2 // AVAHI_SERVER_RUNNING
)

type eventKind int

const (
	evGroupState   eventKind = iota // our entry group changed state
	evServerState                   // the Avahi server changed state
	evOwnerChanged                  // avahi-daemon (re)started or stopped
)

type event struct {
	kind  eventKind
	state int32
}

// session is one connection to Avahi.
type session interface {
	ServerState() (int32, error)
	// Publish (re)places the alias record: creates the entry group on first use,
	// otherwise resets it, then AddAddress + Commit.
	Publish(name string, ip net.IP) error
	// Withdraw resets the entry group, if any.
	Withdraw() error
	// Events is closed when the connection is lost.
	Events() <-chan event
	Close()
}

type publisher struct {
	alias string
	ip    func() net.IP
	state func(string)
	log   *slog.Logger
	dial  func(ctx context.Context) (session, error)
	poll  time.Duration // IP check interval
	retry time.Duration // reconnect delay
	last  string
}

// PublishAlias keeps alias pointing at ip() until ctx is done, reporting
// "registering", "established", "collision", "failed" or "off" (no IP) to state.
func PublishAlias(ctx context.Context, alias string, ip func() net.IP, state func(string), log *slog.Logger) {
	p := &publisher{alias: alias, ip: ip, state: state, log: log, dial: dialAvahi, poll: 5 * time.Second, retry: 5 * time.Second}
	p.run(ctx)
}

func (p *publisher) set(s string) {
	if s == p.last {
		return
	}
	p.last = s
	p.log.Info("mdns alias", "alias", p.alias, "state", s)
	p.state(s)
}

func (p *publisher) run(ctx context.Context) {
	for ctx.Err() == nil {
		s, err := p.dial(ctx)
		if err != nil {
			if p.last != "failed" {
				p.log.Warn("mdns: cannot reach avahi", "err", err)
			}
			p.set("failed")
		} else {
			p.serve(ctx, s)
			s.Close()
		}
		select {
		case <-ctx.Done():
		case <-time.After(p.retry):
		}
	}
}

// serve publishes on one session until it breaks or ctx is done.
func (p *publisher) serve(ctx context.Context, s session) {
	st, err := s.ServerState()
	if err != nil {
		p.log.Warn("mdns: avahi GetState", "err", err)
		p.set("failed")
		return
	}
	running := st == serverRunning
	published := "" // address currently in the entry group

	sync := func() bool {
		cur := p.ip()
		switch {
		case cur == nil:
			if published != "" {
				_ = s.Withdraw()
				published = ""
			}
			p.set("off")
		case !running:
			p.set("registering")
		case cur.String() != published:
			if err := s.Publish(p.alias, cur); err != nil {
				p.log.Warn("mdns: publish", "alias", p.alias, "ip", cur.String(), "err", err)
				p.set("failed")
				return false
			}
			published = cur.String()
			p.set("registering")
		}
		return true
	}

	if !sync() {
		return
	}
	t := time.NewTicker(p.poll)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			_ = s.Withdraw()
			return
		case <-t.C:
			if !sync() {
				return
			}
		case ev, ok := <-s.Events():
			if !ok {
				p.set("failed")
				return
			}
			switch ev.kind {
			case evOwnerChanged:
				p.log.Info("mdns: avahi restarted")
				p.set("failed")
				return
			case evServerState:
				running = ev.state == serverRunning
				if !running && published != "" {
					// Like avahi-publish: drop the record while the server re-registers.
					_ = s.Withdraw()
					published = ""
				}
				if !sync() {
					return
				}
			case evGroupState:
				if published == "" {
					continue
				}
				switch ev.state {
				case groupRegistering:
					p.set("registering")
				case groupEstablished:
					p.set("established")
				case groupCollision:
					p.set("collision")
				case groupFailure:
					p.set("failed")
				}
			}
		}
	}
}
