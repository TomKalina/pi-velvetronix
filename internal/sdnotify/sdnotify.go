// Package sdnotify implements the systemd sd_notify protocol (READY, WATCHDOG) without deps.
package sdnotify

import (
	"net"
	"os"
	"strconv"
	"time"
)

// Ready tells systemd that startup finished.
func Ready() error { return send("READY=1") }

// Watchdog sends a watchdog keep-alive.
func Watchdog() error { return send("WATCHDOG=1") }

// send writes state to $NOTIFY_SOCKET; it is a no-op when the variable is unset.
// A leading '@' (abstract socket) is mapped to a NUL byte by the Go runtime on Linux.
func send(state string) error {
	addr := os.Getenv("NOTIFY_SOCKET")
	if addr == "" {
		return nil
	}
	conn, err := net.DialUnix("unixgram", nil, &net.UnixAddr{Name: addr, Net: "unixgram"})
	if err != nil {
		return err
	}
	defer conn.Close()
	_, err = conn.Write([]byte(state))
	return err
}

// WatchdogInterval returns the watchdog timeout configured by systemd (WATCHDOG_USEC),
// if it is meant for this process (WATCHDOG_PID unset or equal to our PID).
func WatchdogInterval() (time.Duration, bool) {
	usec, err := strconv.ParseUint(os.Getenv("WATCHDOG_USEC"), 10, 63)
	if err != nil || usec == 0 {
		return 0, false
	}
	if pid := os.Getenv("WATCHDOG_PID"); pid != "" && pid != strconv.Itoa(os.Getpid()) {
		return 0, false
	}
	return time.Duration(usec) * time.Microsecond, true
}
