package netmgr

import (
	"context"
	"errors"
	"net"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestSplitTerse(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"a:b:c", []string{"a", "b", "c"}},
		{`My\:Net:72`, []string{"My:Net", "72"}},
		{`back\\slash:x`, []string{`back\slash`, "x"}},
		{`ends\\:y`, []string{`ends\`, "y"}},
		{`a\\\:b`, []string{`a\:b`}},
		{"a:b:", []string{"a", "b", ""}},
		{":", []string{"", ""}},
		{"", []string{""}},
		{`dangling\`, []string{`dangling\`}},
		{"*:HomeNet:72:WPA1 WPA2", []string{"*", "HomeNet", "72", "WPA1 WPA2"}},
	}
	for _, c := range cases {
		if got := splitTerse(c.in); !slices.Equal(got, c.want) {
			t.Errorf("splitTerse(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	// Round trip through nmcli's escaping.
	for _, s := range []string{`a:b\c`, `::`, `\\`, "plain"} {
		if got := splitTerse(esc(s) + ":" + esc(s)); !slices.Equal(got, []string{s, s}) {
			t.Errorf("round trip %q: %q", s, got)
		}
	}
}

func TestKeyValues(t *testing.T) {
	kv := keyValues([]byte("802-11-wireless.ssid:Cafe: free\\x\n802-11-wireless.mode:ap\nIP4.ADDRESS[1]:10.42.0.1/24\n\n"))
	if kv["802-11-wireless.ssid"] != `Cafe: free\x` || kv["802-11-wireless.mode"] != "ap" || kv["IP4.ADDRESS[1]"] != "10.42.0.1/24" {
		t.Fatalf("keyValues: %q", kv)
	}
}

func TestParseAddedUUID(t *testing.T) {
	out := []byte("Connection 'home-My (flat)' (6f0c1d2e-1111-2222-3333-444455556666) successfully added.\n")
	if got := parseAddedUUID(out); got != "6f0c1d2e-1111-2222-3333-444455556666" {
		t.Fatalf("got %q", got)
	}
	if got := parseAddedUUID([]byte("something else")); got != "" {
		t.Fatalf("got %q", got)
	}
}

func TestReadIPv4(t *testing.T) {
	for out, want := range map[string]string{
		"192.168.1.86/24\n":              "192.168.1.86",
		"192.168.1.86/24 | 10.0.0.5/8\n": "192.168.1.86",
		"\n":                             "<nil>",
		"":                               "<nil>",
	} {
		m, _, _ := newTestManager(t, newFake(t))
		m.run = func(context.Context, []string) ([]byte, int, error) { return []byte(out), 0, nil }
		if got := m.readIPv4(context.Background()).String(); got != want {
			t.Errorf("%q: got %s, want %s", out, got, want)
		}
	}
}

func TestRedact(t *testing.T) {
	safe, secrets := redact([]string{"nmcli", "connection", "modify", "uuid", "u", "wifi-sec.psk", "hunter22", "x"})
	if strings.Join(safe, " ") != "nmcli connection modify uuid u wifi-sec.psk *** x" || !slices.Equal(secrets, []string{"hunter22"}) {
		t.Fatalf("redact: %q %q", safe, secrets)
	}
	safe, _ = redact([]string{"802-11-wireless-security.psk", "s3cret!!"})
	if safe[1] != "***" {
		t.Fatalf("long form not redacted: %q", safe)
	}
}

func TestNetworks(t *testing.T) {
	f := newFake(t)
	f.active = "bbbbbbbb-0000-0000-0000-000000000002"
	f.visible = []fnet{
		{ssid: "HomeNet", sec: "WPA2", signal: 60},
		{ssid: "Neighbour: 5G", sec: "WPA2 WPA3", signal: 80},
		{ssid: "HomeNet", sec: "WPA2", signal: 75}, // second BSSID
		{ssid: "", sec: "WPA2", signal: 90},     // hidden
		{ssid: `back\slash`, sec: "", signal: 20},
	}
	m, _, _ := newTestManager(t, f)
	nets, saved, err := m.Networks(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	want := []Network{
		{SSID: "Neighbour: 5G", Signal: 80, Security: "WPA2 WPA3"},
		{SSID: "HomeNet", Signal: 75, Security: "WPA2", InUse: true, Saved: true},
		{SSID: `back\slash`, Signal: 20},
	}
	if !slices.Equal(nets, want) {
		t.Fatalf("networks:\n got %+v\nwant %+v", nets, want)
	}
	wantSaved := []Saved{{UUID: "bbbbbbbb-0000-0000-0000-000000000002", Name: "netplan-wlan0-HomeNet", SSID: "HomeNet", Active: true}}
	if !slices.Equal(saved, wantSaved) {
		t.Fatalf("saved: %+v", saved)
	}
	if !slices.Contains(f.actions(), "-f IN-USE,SSID,SIGNAL,SECURITY device wifi list --rescan yes ifname wlan0") {
		t.Fatalf("no rescan: %q", f.actions())
	}
}

func TestConnectNewProfile(t *testing.T) {
	f := newFake(t)
	f.visible = []fnet{{ssid: "Cafe:Bar", sec: "WPA2", signal: 50}}
	m, _, logs := newTestManager(t, f)
	m.start(context.Background())
	if err := m.Connect(context.Background(), "Cafe:Bar", "correct horse"); err != nil {
		t.Fatal(err)
	}
	acts := f.actions()
	wantAdd := "connection add type wifi ifname wlan0 con-name home-Cafe:Bar ssid Cafe:Bar wifi-sec.key-mgmt wpa-psk " +
		"wifi-sec.psk correct horse connection.autoconnect yes connection.autoconnect-priority 10 " +
		"connection.autoconnect-retries 0 ipv4.method auto ipv6.method auto"
	if len(acts) != 2 || acts[0] != wantAdd || acts[1] != "--wait 45 connection up uuid dddddddd-0000-0000-0000-000000000001 ifname wlan0" {
		t.Fatalf("actions:\n%s", strings.Join(acts, "\n"))
	}
	m.step(context.Background())
	st := m.Status()
	if st.Mode != "sta" || *st.SSID != "Cafe:Bar" || *st.IP != "192.168.1.86" || st.LastError != nil || *st.Signal != 50 {
		t.Fatalf("status %+v", st)
	}
	if strings.Contains(logs.String(), "correct horse") {
		t.Fatal("PSK in log")
	}
	if !strings.Contains(logs.String(), "wifi-sec.psk ***") {
		t.Fatalf("exec log not redacted/written:\n%s", logs)
	}
	if !m.IPv4().Equal(net.ParseIP("192.168.1.86")) {
		t.Fatalf("IPv4 %v", m.IPv4())
	}
}

func TestConnectExistingProfileModifiesPSK(t *testing.T) {
	f := newFake(t)
	f.visible = []fnet{{ssid: "HomeNet", sec: "WPA2", signal: 70}}
	m, _, _ := newTestManager(t, f)
	if err := m.Connect(context.Background(), "HomeNet", "newpassword"); err != nil {
		t.Fatal(err)
	}
	acts := f.actions()
	want := []string{
		"connection modify uuid bbbbbbbb-0000-0000-0000-000000000002 wifi-sec.psk newpassword",
		"--wait 45 connection up uuid bbbbbbbb-0000-0000-0000-000000000002 ifname wlan0",
	}
	if !slices.Equal(acts, want) {
		t.Fatalf("actions %q", acts)
	}
	if f.prof("bbbbbbbb-0000-0000-0000-000000000002").psk != "newpassword" || len(f.profiles) != 2 {
		t.Fatal("profile not updated in place")
	}
}

func TestConnectInvalid(t *testing.T) {
	m, _, _ := newTestManager(t, newFake(t))
	for _, c := range [][2]string{{"", "password1"}, {strings.Repeat("x", 33), "password1"}, {"HomeNet", "short"},
		{"HomeNet", strings.Repeat("p", 64)}, {"HomeNet", "pass\nword"}} {
		if err := m.Connect(context.Background(), c[0], c[1]); !errors.Is(err, ErrInvalid) {
			t.Errorf("Connect(%q, %q) = %v", c[0], c[1], err)
		}
	}
	if err := m.Connect(context.Background(), "HomeNet", strings.Repeat("aB3", 21)+"f"); err != nil {
		t.Errorf("64 hex PSK rejected: %v", err)
	}
}

func TestConnectAddFailureDoesNotLeakPSK(t *testing.T) {
	f := newFake(t)
	m, _, logs := newTestManager(t, f)
	m.run = func(ctx context.Context, argv []string) ([]byte, int, error) {
		if slices.Contains(argv, "add") {
			return nil, 2, errors.New("Error: '" + argv[slices.Index(argv, "wifi-sec.psk")+1] + "' is not valid")
		}
		return f.run(ctx, argv)
	}
	err := m.Connect(context.Background(), "Other", "topsecret9")
	if err == nil || strings.Contains(err.Error(), "topsecret9") {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(logs.String(), "topsecret9") {
		t.Fatalf("PSK in log:\n%s", logs)
	}
}

func TestConnectFailureFromAPReturnsToAP(t *testing.T) {
	f := newFake(t)
	f.active = f.profiles[0].uuid // hotspot up
	f.visible = []fnet{{ssid: "HomeNet", sec: "WPA2", signal: 70}}
	f.failUp["bbbbbbbb-0000-0000-0000-000000000002"] = true
	m, _, logs := newTestManager(t, f)
	m.start(context.Background())
	m.step(context.Background())
	if m.Status().Mode != "ap" {
		t.Fatalf("mode %q", m.Status().Mode)
	}
	if err := m.Connect(context.Background(), "HomeNet", "wrongpass"); err != nil {
		t.Fatal(err)
	}
	m.step(context.Background())
	st := m.Status()
	if st.Mode != "ap" || st.LastError == nil || *st.LastError != "connect_failed: HomeNet" || st.SSID != nil {
		t.Fatalf("status %+v", st)
	}
	if st.APSSID != "VOX-Velvetronix" || st.APIP != "10.42.0.1" || *st.IP != "10.42.0.1" {
		t.Fatalf("ap fields %+v", st)
	}
	acts := f.actions()
	if acts[len(acts)-1] != "--wait 30 connection up id vox-ap" || slices.ContainsFunc(acts, deleteCall) {
		t.Fatalf("actions %q", acts)
	}
	if f.prof("bbbbbbbb-0000-0000-0000-000000000002") == nil || strings.Contains(logs.String(), "wrongpass") {
		t.Fatal("profile removed or PSK logged")
	}
}

func deleteCall(a string) bool { return strings.Contains(a, "delete") }

func TestForget(t *testing.T) {
	f := newFake(t)
	f.profiles = append(f.profiles, &fprof{name: "home-Old", uuid: "eeeeeeee-0000-0000-0000-000000000005", ssid: "Old", mode: "infrastructure"})
	f.active = "bbbbbbbb-0000-0000-0000-000000000002"
	m, _, _ := newTestManager(t, f)
	ctx := context.Background()
	if err := m.Forget(ctx, "bbbbbbbb-0000-0000-0000-000000000002"); !errors.Is(err, ErrActiveConnection) {
		t.Fatalf("active: %v", err)
	}
	if err := m.Forget(ctx, "aaaaaaaa-0000-0000-0000-000000000001"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("hotspot: %v", err)
	}
	if err := m.Forget(ctx, "cccccccc-0000-0000-0000-000000000003"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ethernet: %v", err)
	}
	if err := m.Forget(ctx, "eeeeeeee-0000-0000-0000-000000000005"); err != nil {
		t.Fatal(err)
	}
	if acts := f.actions(); !slices.Equal(acts, []string{"connection delete uuid eeeeeeee-0000-0000-0000-000000000005"}) {
		t.Fatalf("actions %q", acts)
	}
}

func TestHotspotValidation(t *testing.T) {
	m, _, _ := newTestManager(t, newFake(t))
	for _, n := range []int{0, 31, -1} {
		if err := m.Hotspot(n); !errors.Is(err, ErrInvalid) {
			t.Errorf("Hotspot(%d) = %v", n, err)
		}
	}
}

func TestStatusModes(t *testing.T) {
	f := newFake(t)
	f.down = true
	m, _, _ := newTestManager(t, f)
	m.start(context.Background())
	m.step(context.Background())
	if st := m.Status(); st.Mode != "unknown" {
		t.Fatalf("NM down: %+v", st)
	}
	f.down = false
	m.step(context.Background())
	if st := m.Status(); st.Mode != "off" || st.IP != nil || st.SSID != nil || st.Signal != nil {
		t.Fatalf("off: %+v", st)
	}
	m.busy = true
	if st := m.Status(); st.Mode != "connecting" {
		t.Fatalf("busy: %+v", st)
	}
}

func TestRunStopsOnCancel(t *testing.T) {
	f := newFake(t)
	m, _, _ := newTestManager(t, f)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { m.Run(ctx); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return")
	}
}
