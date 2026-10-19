package netmgr

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeNM emulates the subset of nmcli the Manager uses, with terse escaped output.
type fakeNM struct {
	t        *testing.T
	mu       sync.Mutex
	calls    [][]string
	profiles []*fprof
	active   string // uuid on wlan0
	staIP    string
	visible  []fnet
	failUp   map[string]bool // uuid -> activation fails
	down     bool            // NetworkManager not running
	nextUUID int
}

type fprof struct {
	name, uuid, ssid, mode, psk string
	prio                        int
}

type fnet struct {
	ssid, sec string
	signal    int
}

func esc(s string) string { return strings.NewReplacer(`\`, `\\`, `:`, `\:`).Replace(s) }

func newFake(t *testing.T) *fakeNM {
	f := &fakeNM{t: t, failUp: map[string]bool{}, staIP: "192.168.1.86"}
	f.profiles = []*fprof{
		{name: "vox-ap", uuid: "aaaaaaaa-0000-0000-0000-000000000001", ssid: "VOX-Velvetronix", mode: "ap", prio: -100},
		{name: "netplan-wlan0-HomeNet", uuid: "bbbbbbbb-0000-0000-0000-000000000002", ssid: "HomeNet", mode: "infrastructure"},
	}
	return f
}

func (f *fakeNM) prof(uuid string) *fprof {
	for _, p := range f.profiles {
		if p.uuid == uuid {
			return p
		}
	}
	return nil
}

func (f *fakeNM) run(_ context.Context, argv []string) ([]byte, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, slices.Clone(argv))
	if f.down {
		return nil, 8, errors.New("Error: NetworkManager is not running.")
	}
	if !slices.Equal(argv[:4], []string{"nmcli", "-t", "-e", "yes"}) {
		f.t.Fatalf("bad prefix: %q", argv)
	}
	a := strings.Join(argv[4:], " ")
	var out strings.Builder
	switch {
	case a == "-f UUID,TYPE,DEVICE,STATE connection show --active":
		if p := f.prof(f.active); p != nil {
			fmt.Fprintf(&out, "%s:802-11-wireless:wlan0:activated\n", p.uuid)
		}
		out.WriteString("11111111-2222-3333-4444-555555555555:loopback:lo:activated\n")
	case a == "-g IP4.ADDRESS device show wlan0":
		if p := f.prof(f.active); p != nil && p.mode == "ap" {
			out.WriteString("10.42.0.1/24\n")
		} else if p != nil {
			fmt.Fprintf(&out, "%s/24\n", f.staIP)
		} else {
			out.WriteString("\n")
		}
	case strings.HasPrefix(a, "-f 802-11-wireless.ssid,802-11-wireless.mode connection show uuid "):
		p := f.prof(argv[len(argv)-1])
		if p == nil {
			return nil, 10, errors.New("Error: no such connection profile.")
		}
		fmt.Fprintf(&out, "802-11-wireless.ssid:%s\n802-11-wireless.mode:%s\n", p.ssid, p.mode)
	case a == "-f NAME,UUID,TYPE,AUTOCONNECT-PRIORITY,ACTIVE connection show":
		for _, p := range f.profiles {
			act := "no"
			if p.uuid == f.active {
				act = "yes"
			}
			fmt.Fprintf(&out, "%s:%s:802-11-wireless:%d:%s\n", esc(p.name), p.uuid, p.prio, act)
		}
		out.WriteString("Wired connection 1:cccccccc-0000-0000-0000-000000000003:802-3-ethernet:-999:no\n")
	case strings.HasPrefix(a, "-f IN-USE,SSID,SIGNAL,SECURITY device wifi list --rescan "):
		cur := f.prof(f.active)
		for _, n := range f.visible {
			use := " "
			if cur != nil && cur.mode != "ap" && cur.ssid == n.ssid {
				use = "*"
			}
			fmt.Fprintf(&out, "%s:%s:%d:%s\n", use, esc(n.ssid), n.signal, n.sec)
		}
	case strings.HasPrefix(a, "connection modify uuid "):
		p := f.prof(argv[7])
		if p == nil || argv[8] != "wifi-sec.psk" {
			return nil, 10, errors.New("Error: unknown connection")
		}
		p.psk = argv[9]
	case strings.HasPrefix(a, "connection add "):
		kv := map[string]string{}
		for i := 6; i+1 < len(argv); i += 2 {
			kv[argv[i]] = argv[i+1]
		}
		if len(kv["wifi-sec.psk"]) < 8 {
			return nil, 2, fmt.Errorf("Error: failed to modify 802-11-wireless-security.psk: '%s' is not a valid PSK.", kv["wifi-sec.psk"])
		}
		f.nextUUID++
		p := &fprof{name: kv["con-name"], uuid: fmt.Sprintf("dddddddd-0000-0000-0000-%012d", f.nextUUID), ssid: kv["ssid"], mode: "infrastructure", psk: kv["wifi-sec.psk"], prio: 10}
		f.profiles = append(f.profiles, p)
		fmt.Fprintf(&out, "Connection '%s' (%s) successfully added.\n", p.name, p.uuid)
	case strings.HasPrefix(a, "--wait 45 connection up uuid ") && strings.HasSuffix(a, " ifname wlan0"):
		uuid := argv[9]
		p := f.prof(uuid)
		visible := p != nil && slices.ContainsFunc(f.visible, func(n fnet) bool { return n.ssid == p.ssid })
		if p == nil || f.failUp[uuid] || !visible {
			f.active = ""
			return nil, 4, errors.New("Error: Connection activation failed: Secrets were required, but not provided.")
		}
		f.active = uuid
		fmt.Fprintf(&out, "Connection successfully activated (D-Bus active path: /org/freedesktop/NetworkManager/ActiveConnection/7)\n")
	case a == "--wait 30 connection up id vox-ap":
		if f.failUp[f.profiles[0].uuid] {
			return nil, 4, errors.New("Error: Connection activation failed.")
		}
		f.active = f.profiles[0].uuid
	case strings.HasPrefix(a, "connection delete uuid "):
		uuid := argv[7]
		i := slices.IndexFunc(f.profiles, func(p *fprof) bool { return p.uuid == uuid })
		if i < 0 {
			return nil, 10, errors.New("Error: unknown connection")
		}
		f.profiles = slices.Delete(f.profiles, i, i+1)
	default:
		f.t.Fatalf("unexpected nmcli call: %q", argv)
	}
	return []byte(out.String()), 0, nil
}

// actions returns the state-changing calls (everything except queries), joined.
func (f *fakeNM) actions() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var r []string
	for _, c := range f.calls {
		a := strings.Join(c[4:], " ")
		if strings.Contains(a, "connection up") || strings.Contains(a, "connection add") ||
			strings.Contains(a, "connection modify") || strings.Contains(a, "connection delete") ||
			strings.Contains(a, "--rescan yes") {
			r = append(r, a)
		}
	}
	return r
}

func (f *fakeNM) resetCalls() {
	f.mu.Lock()
	f.calls = nil
	f.mu.Unlock()
}

type clock struct{ t time.Time }

func (c *clock) now() time.Time          { return c.t }
func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }

// newTestManager returns a Manager on the fake with a fake clock, inline async
// activations, and its log in buf.
func newTestManager(t *testing.T, f *fakeNM) (*Manager, *clock, *bytes.Buffer) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	m := New("vox-ap", log)
	clk := &clock{t: time.Date(2026, 10, 19, 10, 0, 0, 0, time.UTC)}
	m.run = f.run
	m.now = clk.now
	m.spawn = func(fn func()) { fn() }
	return m, clk, &buf
}
