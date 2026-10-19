package sdnotify

import (
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"
)

func listen(t *testing.T, name string) *net.UnixConn {
	t.Helper()
	c, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: name, Net: "unixgram"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func expect(t *testing.T, c *net.UnixConn, want string) {
	t.Helper()
	c.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 64)
	n, err := c.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(buf[:n]); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestNotifyPath(t *testing.T) {
	dir, err := os.MkdirTemp("", "sd") // short path: sun_path is limited to ~104 bytes
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "n.sock")
	c := listen(t, sock)
	t.Setenv("NOTIFY_SOCKET", sock)

	if err := Ready(); err != nil {
		t.Fatal(err)
	}
	expect(t, c, "READY=1")
	if err := Watchdog(); err != nil {
		t.Fatal(err)
	}
	expect(t, c, "WATCHDOG=1")
}

func TestNotifyAbstract(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("abstract sockets are linux-only")
	}
	name := "@pi-velvetronix-test-" + strconv.Itoa(os.Getpid())
	c := listen(t, name)
	t.Setenv("NOTIFY_SOCKET", name)
	if err := Ready(); err != nil {
		t.Fatal(err)
	}
	expect(t, c, "READY=1")
}

func TestNotifyUnset(t *testing.T) {
	t.Setenv("NOTIFY_SOCKET", "")
	if err := Ready(); err != nil {
		t.Errorf("unset socket: %v", err)
	}
}

func TestWatchdogInterval(t *testing.T) {
	t.Setenv("WATCHDOG_PID", "")
	t.Setenv("WATCHDOG_USEC", "")
	if _, ok := WatchdogInterval(); ok {
		t.Error("enabled without WATCHDOG_USEC")
	}
	t.Setenv("WATCHDOG_USEC", "30000000")
	if d, ok := WatchdogInterval(); !ok || d != 30*time.Second {
		t.Errorf("got %v %v", d, ok)
	}
	t.Setenv("WATCHDOG_PID", strconv.Itoa(os.Getpid()))
	if _, ok := WatchdogInterval(); !ok {
		t.Error("own pid rejected")
	}
	t.Setenv("WATCHDOG_PID", strconv.Itoa(os.Getpid()+1))
	if _, ok := WatchdogInterval(); ok {
		t.Error("other pid accepted")
	}
	t.Setenv("WATCHDOG_PID", "")
	t.Setenv("WATCHDOG_USEC", "abc")
	if _, ok := WatchdogInterval(); ok {
		t.Error("garbage accepted")
	}
}
