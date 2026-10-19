//go:build linux

package midi

import (
	"errors"
	"os"
	"syscall"
)

// OpenPort opens a rawmidi node. O_NONBLOCK makes a busy device fail with EBUSY instead
// of blocking open(); the Go runtime poller then gives blocking-style Read/Write.
func OpenPort(node string) (Port, error) {
	f, err := os.OpenFile(node, os.O_RDWR|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		if errors.Is(err, syscall.EBUSY) {
			return nil, ErrBusy
		}
		return nil, err
	}
	return f, nil
}
