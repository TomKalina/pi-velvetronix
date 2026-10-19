package mdns

import (
	"context"
	"net"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
)

const (
	avahiName   = "org.freedesktop.Avahi"
	ifaceServer = "org.freedesktop.Avahi.Server"
	ifaceGroup  = "org.freedesktop.Avahi.EntryGroup"

	ifUnspec         = int32(-1)  // AVAHI_IF_UNSPEC
	protoUnspec      = int32(-1)  // AVAHI_PROTO_UNSPEC
	publishNoReverse = uint32(16) // AVAHI_PUBLISH_NO_REVERSE: the host name owns the PTR

	callTimeout = 5 * time.Second
)

type dbusSession struct {
	conn   *dbus.Conn
	server dbus.BusObject
	events chan event

	mu    sync.Mutex
	group dbus.BusObject // nil until the first Publish
	path  dbus.ObjectPath
}

// dialAvahi opens a private system bus connection and subscribes to the signals
// we need. Avahi frees our entry group when this connection goes away.
func dialAvahi(ctx context.Context) (session, error) {
	conn, err := dbus.ConnectSystemBus(dbus.WithContext(ctx))
	if err != nil {
		return nil, err
	}
	matches := [][]dbus.MatchOption{
		{dbus.WithMatchSender("org.freedesktop.DBus"), dbus.WithMatchInterface("org.freedesktop.DBus"),
			dbus.WithMatchMember("NameOwnerChanged"), dbus.WithMatchArg(0, avahiName)},
		{dbus.WithMatchInterface(ifaceServer), dbus.WithMatchMember("StateChanged")},
		{dbus.WithMatchInterface(ifaceGroup), dbus.WithMatchMember("StateChanged")},
	}
	for _, m := range matches {
		if err := conn.AddMatchSignalContext(ctx, m...); err != nil {
			conn.Close()
			return nil, err
		}
	}
	s := &dbusSession{conn: conn, server: conn.Object(avahiName, "/"), events: make(chan event, 16)}
	sigs := make(chan *dbus.Signal, 16)
	conn.Signal(sigs)
	go s.translate(sigs)
	return s, nil
}

// translate turns D-Bus signals into events; sigs is closed by conn.Close.
func (s *dbusSession) translate(sigs <-chan *dbus.Signal) {
	defer close(s.events)
	for sig := range sigs {
		var ev event
		switch sig.Name {
		case "org.freedesktop.DBus.NameOwnerChanged":
			ev.kind = evOwnerChanged
		case ifaceServer + ".StateChanged", ifaceGroup + ".StateChanged":
			st, ok := firstInt32(sig.Body)
			if !ok {
				continue
			}
			ev.kind, ev.state = evServerState, st
			if sig.Name == ifaceGroup+".StateChanged" {
				s.mu.Lock()
				ours := s.group != nil && sig.Path == s.path
				s.mu.Unlock()
				if !ours {
					continue
				}
				ev.kind = evGroupState
			}
		default:
			continue
		}
		select {
		case s.events <- ev:
		case <-s.conn.Context().Done():
			return
		}
	}
}

func firstInt32(body []any) (int32, bool) {
	if len(body) == 0 {
		return 0, false
	}
	v, ok := body[0].(int32)
	return v, ok
}

func call(obj dbus.BusObject, method string, args ...any) *dbus.Call {
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()
	return obj.CallWithContext(ctx, method, 0, args...)
}

func (s *dbusSession) ServerState() (int32, error) {
	var st int32
	err := call(s.server, ifaceServer+".GetState").Store(&st)
	return st, err
}

func (s *dbusSession) Publish(name string, ip net.IP) error {
	s.mu.Lock()
	g := s.group
	s.mu.Unlock()
	if g == nil {
		var path dbus.ObjectPath
		if err := call(s.server, ifaceServer+".EntryGroupNew").Store(&path); err != nil {
			return err
		}
		g = s.conn.Object(avahiName, path)
		s.mu.Lock()
		s.group, s.path = g, path
		s.mu.Unlock()
	} else if err := call(g, ifaceGroup+".Reset").Err; err != nil {
		return err
	}
	if err := call(g, ifaceGroup+".AddAddress", ifUnspec, protoUnspec, publishNoReverse, name, ip.String()).Err; err != nil {
		return err
	}
	return call(g, ifaceGroup+".Commit").Err
}

func (s *dbusSession) Withdraw() error {
	s.mu.Lock()
	g := s.group
	s.mu.Unlock()
	if g == nil {
		return nil
	}
	return call(g, ifaceGroup+".Reset").Err
}

func (s *dbusSession) Events() <-chan event { return s.events }

func (s *dbusSession) Close() { s.conn.Close() }
