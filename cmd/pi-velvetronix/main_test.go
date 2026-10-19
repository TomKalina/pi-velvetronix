package main

import (
	"context"
	"encoding/json"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TomKalina/pi-velvetronix/internal/auth"
	"github.com/TomKalina/pi-velvetronix/internal/update"
	"github.com/TomKalina/pi-velvetronix/internal/web"
)

func TestSetPassword(t *testing.T) {
	for _, in := range []string{"heslo1234\n", "heslo1234\r\n", "heslo1234"} {
		dir := t.TempDir()
		if err := setPassword([]string{"--state", dir}, strings.NewReader(in)); err != nil {
			t.Fatalf("%q: %v", in, err)
		}
		s, err := auth.Load(dir)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.Login("heslo1234", "127.0.0.1"); err != nil {
			t.Errorf("%q: login: %v", in, err)
		}
	}
	if err := setPassword([]string{"--state", t.TempDir()}, strings.NewReader("short\n")); err == nil {
		t.Error("short password accepted")
	}
}

func TestSelfCheck(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			w.WriteHeader(404)
		}
	}))
	defer ok.Close()
	if err := selfCheck(ok.Listener.Addr()); err != nil {
		t.Errorf("healthy: %v", err)
	}
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	defer bad.Close()
	if err := selfCheck(bad.Listener.Addr()); err == nil {
		t.Error("503 accepted")
	}
}

func freePort(t *testing.T) string {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().String()
}

func TestServe(t *testing.T) {
	if _, err := fs.Stat(web.Assets(), "index.html.gz"); err != nil {
		t.Skip("frontend not built (internal/web/dist/index.html.gz missing)")
	}
	state := t.TempDir()
	updateDir := t.TempDir()
	pending := filepath.Join(updateDir, "pending")
	os.WriteFile(pending, []byte("dev 1\n"), 0o644)
	oldDir := update.Dir
	t.Cleanup(func() { update.Dir = oldDir })

	sockDir, _ := os.MkdirTemp("", "sd")
	defer os.RemoveAll(sockDir)
	sock := filepath.Join(sockDir, "n")
	notify, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: sock, Net: "unixgram"})
	if err != nil {
		t.Fatal(err)
	}
	defer notify.Close()
	t.Setenv("NOTIFY_SOCKET", sock)
	t.Setenv("WATCHDOG_USEC", "200000")
	t.Setenv("WATCHDOG_PID", "")

	addr := freePort(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- serve(ctx, []string{"--listen", addr, "--state", state, "--update-dir", updateDir,
			"--no-network", "--midi-device", filepath.Join(state, "nope")})
	}()

	// Watchdog pings start right away; READY follows the self-check.
	var got []string
	buf := make([]byte, 64)
	notify.SetReadDeadline(time.Now().Add(5 * time.Second))
	for !contains(got, "READY=1") {
		n, err := notify.Read(buf)
		if err != nil {
			t.Fatalf("notifications %v: %v", got, err)
		}
		got = append(got, string(buf[:n]))
	}
	if got[0] != "WATCHDOG=1" {
		t.Errorf("first notification %q, want WATCHDOG=1", got[0])
	}
	// Confirmed via <state>/confirmed; pending in the root-owned dir is left to boot-guard.
	waitFor(t, func() bool {
		b, _ := os.ReadFile(filepath.Join(state, "confirmed"))
		return string(b) == "dev\n"
	})
	if _, err := os.Stat(pending); err != nil {
		t.Errorf("pending touched by the daemon: %v", err)
	}
	if update.Dir != updateDir {
		t.Errorf("update.Dir = %q, want %q", update.Dir, updateDir)
	}
	if fi, err := os.Stat(filepath.Join(state, "inbox")); err != nil || !fi.IsDir() {
		t.Error("inbox missing")
	}
	if _, err := os.Stat(filepath.Join(state, "update")); !os.IsNotExist(err) {
		t.Errorf("state/update created: %v", err)
	}

	resp, err := http.Get("http://" + addr + "/api/session")
	if err != nil {
		t.Fatal(err)
	}
	var sess map[string]bool
	json.NewDecoder(resp.Body).Decode(&sess)
	resp.Body.Close()
	if sess["configured"] {
		t.Errorf("session = %v, want unconfigured", sess)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("serve returned %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("serve did not stop")
	}
}

func TestServeSelfCheckFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	// A state directory that cannot be written makes /healthz fail.
	state := t.TempDir()
	os.MkdirAll(filepath.Join(state, "inbox"), 0o750)
	os.Chmod(state, 0o500)
	defer os.Chmod(state, 0o750)
	err := serve(context.Background(), []string{"--listen", freePort(t), "--state", state, "--no-network"})
	if err == nil || !strings.Contains(err.Error(), "self-check") {
		t.Fatalf("err = %v, want self-check failure", err)
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
