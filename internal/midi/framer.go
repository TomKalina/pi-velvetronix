// Package midi handles SysEx framing, host frame validation and ALSA rawmidi access.
package midi

const maxFrame = 4096

// Framer reassembles complete SysEx frames (F0 ... F7) from a raw MIDI byte stream.
type Framer struct {
	buf     []byte
	inSysEx bool
}

// Feed consumes p and calls emit for every complete frame. emit receives a fresh copy.
// Realtime bytes (F8-FF) are dropped anywhere, F0 restarts a frame, any other status
// byte aborts an unfinished frame, and frames longer than 4096 bytes are discarded.
func (f *Framer) Feed(p []byte, emit func(frame []byte)) {
	for _, b := range p {
		switch {
		case b >= 0xF8:
			// Realtime: does not affect SysEx state.
		case b == 0xF0:
			f.buf = append(f.buf[:0], b)
			f.inSysEx = true
		case b == 0xF7:
			if f.inSysEx {
				f.buf = append(f.buf, b)
				if len(f.buf) <= maxFrame {
					emit(append([]byte(nil), f.buf...))
				}
			}
			f.reset()
		case b >= 0x80:
			f.reset()
		default:
			if !f.inSysEx {
				continue // channel data outside SysEx: not used by the VOX protocol
			}
			if len(f.buf) >= maxFrame {
				f.reset() // too long: discard until the next F0
				continue
			}
			f.buf = append(f.buf, b)
		}
	}
}

func (f *Framer) reset() {
	f.buf = f.buf[:0]
	f.inSysEx = false
}
