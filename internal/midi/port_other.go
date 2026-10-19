//go:build !linux

package midi

import "errors"

// OpenPort is only implemented on linux (ALSA rawmidi).
func OpenPort(node string) (Port, error) {
	return nil, errors.New("rawmidi is only supported on linux")
}
