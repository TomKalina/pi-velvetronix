package netmgr

import (
	"context"
	"fmt"
	"net"
	"slices"
	"strings"
	"testing"
	"time"
)

const (
	fifiUUID = "bbbbbbbb-0000-0000-0000-000000000002"
	upAP     = "--wait 30 connection up id vox-ap"
	upHomeNet   = "--wait 45 connection up uuid " + fifiUUID + " ifname wlan0"
	rescan   = "-f IN-USE,SSID,SIGNAL,SECURITY device wifi list --rescan yes ifname wlan0"
)

// tick advances the clock by d and runs one supervisor step, returning the
// state-changing nmcli calls it made.
func tick(m *Manager, f *fakeNM, clk *clock, d time.Duration) []string {
	clk.advance(d)
	f.resetCalls()
	m.step(context.Background())
	return f.actions()
}

func expect(t *testing.T, what string, got []string, want ...string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Fatalf("%s: actions\n got %q\nwant %q", what, got, want)
	}
}

func TestBootWaitsThenRaisesAP(t *testing.T) {
	f := newFake(t)
	m, clk, _ := newTestManager(t, f)
	m.start(context.Background())
	expect(t, "t=0", tick(m, f, clk, 0))
	expect(t, "t=59s", tick(m, f, clk, 59*time.Second))
	if m.Status().Mode != "off" {
		t.Fatalf("mode %q", m.Status().Mode)
	}
	expect(t, "t=60s", tick(m, f, clk, time.Second), upAP)
	if st := m.Status(); st.Mode != "ap" || *st.IP != "10.42.0.1" {
		t.Fatalf("status %+v", st)
	}
}

func TestBootWithSTANeverRaisesAP(t *testing.T) {
	f := newFake(t)
	f.active = fifiUUID
	f.visible = []fnet{{ssid: "HomeNet", signal: 64}}
	m, clk, _ := newTestManager(t, f)
	m.start(context.Background())
	for range 30 {
		expect(t, "sta", tick(m, f, clk, 5*time.Second))
	}
	st := m.Status()
	if st.Mode != "sta" || *st.SSID != "HomeNet" || *st.Signal != 64 {
		t.Fatalf("status %+v", st)
	}
}

func TestSTALostRaisesAPAfter90s(t *testing.T) {
	f := newFake(t)
	f.active = fifiUUID
	m, clk, _ := newTestManager(t, f)
	m.start(context.Background())
	expect(t, "sta", tick(m, f, clk, 0))

	// A short drop that NetworkManager recovers from does not trigger the hotspot.
	f.active = ""
	expect(t, "lost", tick(m, f, clk, 5*time.Second))
	expect(t, "lost+60s", tick(m, f, clk, 60*time.Second))
	f.active = fifiUUID
	expect(t, "back", tick(m, f, clk, 5*time.Second))
	f.active = ""
	expect(t, "lost again", tick(m, f, clk, 5*time.Second))
	expect(t, "lost+89s", tick(m, f, clk, 89*time.Second))
	expect(t, "lost+90s", tick(m, f, clk, time.Second), upAP)
	if m.Status().Mode != "ap" {
		t.Fatalf("mode %q", m.Status().Mode)
	}
}

func TestAPFailedStartRetries(t *testing.T) {
	f := newFake(t)
	f.failUp[f.profiles[0].uuid] = true
	m, clk, _ := newTestManager(t, f)
	m.start(context.Background())
	expect(t, "boot", tick(m, f, clk, 60*time.Second), upAP)
	if st := m.Status(); st.LastError == nil || *st.LastError != "hotspot_failed" {
		t.Fatalf("status %+v", st)
	}
	expect(t, "+25s", tick(m, f, clk, 25*time.Second))
	delete(f.failUp, f.profiles[0].uuid)
	expect(t, "+30s", tick(m, f, clk, 5*time.Second), upAP)
	if m.Status().Mode != "ap" {
		t.Fatal("hotspot not up")
	}
}

func TestAPIdleReturnWithBackoff(t *testing.T) {
	f := newFake(t)
	f.active = f.profiles[0].uuid // already in AP mode (e.g. daemon restart)
	f.visible = []fnet{{ssid: "HomeNet", signal: 40}}
	f.failUp[fifiUUID] = true
	m, clk, logs := newTestManager(t, f)
	m.start(context.Background())
	expect(t, "t=0", tick(m, f, clk, 0))

	// Client activity from the hotspot subnet postpones the attempt; other addresses do not.
	clk.advance(4*time.Minute + 59*time.Second)
	m.NoteClientActivity(net.ParseIP("10.42.0.23"))
	m.NoteClientActivity(net.ParseIP("192.168.1.5"))
	expect(t, "active", tick(m, f, clk, 0))
	expect(t, "idle 4m59s", tick(m, f, clk, 4*time.Minute+59*time.Second))
	expect(t, "idle 5m", tick(m, f, clk, time.Second), rescan, upHomeNet, upAP)

	// Back on the hotspot; next attempts after 2, 5, 10, 10 minutes.
	for i, gap := range []time.Duration{2 * time.Minute, 5 * time.Minute, 10 * time.Minute, 10 * time.Minute} {
		expect(t, "before backoff", tick(m, f, clk, gap-5*time.Second))
		expect(t, "backoff", tick(m, f, clk, 5*time.Second), rescan, upHomeNet, upAP)
		if m.failures != i+2 {
			t.Fatalf("failures %d", m.failures)
		}
	}
	st := m.Status()
	if st.Mode != "ap" || *st.LastError != "connect_failed: HomeNet" {
		t.Fatalf("status %+v", st)
	}

	// Home network fixed: next attempt succeeds and resets the backoff.
	delete(f.failUp, fifiUUID)
	expect(t, "success", tick(m, f, clk, 10*time.Minute), rescan, upHomeNet)
	if st := m.Status(); st.Mode != "sta" || st.LastError != nil || m.failures != 0 {
		t.Fatalf("status %+v failures %d", st, m.failures)
	}
	for _, c := range f.calls {
		if slices.Contains(c, "delete") {
			t.Fatalf("profile deleted: %q", c)
		}
	}
	if strings.Contains(logs.String(), "level=ERROR") {
		t.Fatalf("errors logged:\n%s", logs)
	}
}

// addSaved adds a saved client profile to the fake and returns its "connection up" action.
func addSaved(f *fakeNM, n int, ssid string, prio int) string {
	uuid := fmt.Sprintf("eeeeeeee-0000-0000-0000-%012d", n)
	f.profiles = append(f.profiles, &fprof{name: ssid, uuid: uuid, ssid: ssid, mode: "infrastructure", prio: prio})
	return "--wait 45 connection up uuid " + uuid + " ifname wlan0"
}

func TestAPLeaveTriesNextSavedNetwork(t *testing.T) {
	f := newFake(t)
	f.active = f.profiles[0].uuid
	upBad := addSaved(f, 1, "HomeNet-5G", 10) // wrong password, outranks HomeNet (priority 0)
	f.failUp[f.profiles[2].uuid] = true
	f.visible = []fnet{{ssid: "HomeNet", signal: 40}, {ssid: "HomeNet-5G", signal: 80}}
	m, clk, _ := newTestManager(t, f)
	m.start(context.Background())
	expect(t, "t=0", tick(m, f, clk, 0))
	expect(t, "idle 5m", tick(m, f, clk, 5*time.Minute), rescan, upBad, upHomeNet)
	if st := m.Status(); st.Mode != "sta" || *st.SSID != "HomeNet" || st.LastError != nil || m.failures != 0 {
		t.Fatalf("status %+v failures %d", st, m.failures)
	}
}

func TestAPLeaveOrderAndCandidateLimit(t *testing.T) {
	f := newFake(t)
	f.active = f.profiles[0].uuid
	upA := addSaved(f, 1, "A", 5)
	upB := addSaved(f, 2, "B", 5)
	upC := addSaved(f, 3, "C", 1)
	for _, p := range f.profiles[1:] {
		f.failUp[p.uuid] = true
	}
	// Same priority: stronger signal first. HomeNet (priority 0) is the 4th and not tried.
	f.visible = []fnet{{ssid: "HomeNet", signal: 90}, {ssid: "C", signal: 70}, {ssid: "A", signal: 30}, {ssid: "B", signal: 60}}
	m, clk, _ := newTestManager(t, f)
	m.start(context.Background())
	expect(t, "t=0", tick(m, f, clk, 0))
	expect(t, "idle 5m", tick(m, f, clk, 5*time.Minute), rescan, upB, upA, upC, upAP)
	if st := m.Status(); st.Mode != "ap" || m.failures != 1 {
		t.Fatalf("status %+v failures %d", st, m.failures)
	}
}

func TestAPNoSavedNetworkVisible(t *testing.T) {
	f := newFake(t)
	f.active = f.profiles[0].uuid
	f.visible = []fnet{{ssid: "Stranger", signal: 90}}
	m, clk, _ := newTestManager(t, f)
	m.start(context.Background())
	expect(t, "t=0", tick(m, f, clk, 0))
	expect(t, "idle", tick(m, f, clk, 5*time.Minute), rescan)
	expect(t, "+1m", tick(m, f, clk, time.Minute))
	expect(t, "+2m", tick(m, f, clk, time.Minute), rescan)
	if m.Status().Mode != "ap" {
		t.Fatal("left AP")
	}
}

func TestHotspotRequestFromSTA(t *testing.T) {
	f := newFake(t)
	f.active = fifiUUID
	f.visible = []fnet{{ssid: "HomeNet", signal: 70}}
	m, clk, _ := newTestManager(t, f)
	m.start(context.Background())
	expect(t, "sta", tick(m, f, clk, 0))
	if err := m.Hotspot(10); err != nil {
		t.Fatal(err)
	}
	expect(t, "hotspot", tick(m, f, clk, time.Second), upAP)
	if m.Status().Mode != "ap" {
		t.Fatal("not in AP mode")
	}
	// Idle for more than 5 min does not end a requested hotspot early.
	expect(t, "+6m idle", tick(m, f, clk, 6*time.Minute))
	// Active use does not extend it past the requested time.
	m.NoteClientActivity(net.ParseIP("10.42.0.10"))
	expect(t, "+9m59s", tick(m, f, clk, 3*time.Minute+59*time.Second))
	m.NoteClientActivity(net.ParseIP("10.42.0.10"))
	expect(t, "+10m", tick(m, f, clk, time.Second), rescan, upHomeNet)
	if st := m.Status(); st.Mode != "sta" || *st.SSID != "HomeNet" {
		t.Fatalf("status %+v", st)
	}
}

func TestHotspotRequestWhileInAPOnlySetsTimer(t *testing.T) {
	f := newFake(t)
	f.active = f.profiles[0].uuid
	f.visible = []fnet{{ssid: "HomeNet", signal: 70}}
	m, clk, _ := newTestManager(t, f)
	m.start(context.Background())
	expect(t, "ap", tick(m, f, clk, 0))
	if err := m.Hotspot(1); err != nil {
		t.Fatal(err)
	}
	expect(t, "request", tick(m, f, clk, 0))
	expect(t, "+59s", tick(m, f, clk, 59*time.Second))
	expect(t, "+60s", tick(m, f, clk, time.Second), rescan, upHomeNet)
}

func TestHotspotExpiryWithoutHomeFallsBackToIdleRule(t *testing.T) {
	f := newFake(t)
	f.active = fifiUUID
	m, clk, _ := newTestManager(t, f)
	m.start(context.Background())
	expect(t, "sta", tick(m, f, clk, 0))
	_ = m.Hotspot(2)
	expect(t, "hotspot", tick(m, f, clk, 0), upAP)
	f.visible = nil // home network gone
	expect(t, "expiry", tick(m, f, clk, 2*time.Minute), rescan)
	expect(t, "+2m (idle 4m)", tick(m, f, clk, 2*time.Minute))
	expect(t, "+3m (idle 5m)", tick(m, f, clk, time.Minute), rescan)
}

func TestConnectDuringSupervisorIsSerialized(t *testing.T) {
	f := newFake(t)
	f.visible = []fnet{{ssid: "HomeNet", signal: 70}}
	m, _, _ := newTestManager(t, f)
	m.start(context.Background())
	started := make(chan struct{})
	release := make(chan struct{})
	m.spawn = func(fn func()) { go fn() }
	inner := m.run
	m.run = func(ctx context.Context, argv []string) ([]byte, int, error) {
		if slices.Contains(argv, "up") {
			close(started)
			<-release
		}
		return inner(ctx, argv)
	}
	if err := m.Connect(context.Background(), "HomeNet", "password1"); err != nil {
		t.Fatal(err)
	}
	<-started
	if m.Status().Mode != "connecting" {
		t.Fatalf("mode %q during activation", m.Status().Mode)
	}
	stepped := make(chan struct{})
	go func() { m.step(context.Background()); close(stepped) }()
	select {
	case <-stepped:
		t.Fatal("supervisor step ran during activation")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	<-stepped
	if m.Status().Mode != "sta" {
		t.Fatalf("mode %q after activation", m.Status().Mode)
	}
}
