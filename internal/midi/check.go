package midi

import "errors"

var (
	ErrBadFrame         = errors.New("bad_frame")
	ErrForbidden        = errors.New("forbidden")
	ErrSaveFlagRequired = errors.New("save_flag_required")
)

// KORG function bytes the host may send (design §7).
const (
	FnProgramWriteRequest = 0x11
	FnProgramDump         = 0x4C
)

var korgHeader = [6]byte{0xF0, 0x42, 0x30, 0x00, 0x01, 0x34}

// korgFunc returns frame[6] for frames starting with the VOX KORG header, else -1.
func korgFunc(frame []byte) int {
	if len(frame) < 8 || [6]byte(frame[:6]) != korgHeader {
		return -1
	}
	return int(frame[6])
}

// CheckHostFrame validates a client frame against the host->amp allow-list.
// fn is the KORG function byte (frame[6]) or -1 for non-KORG frames; it is returned
// even when err != nil.
func CheckHostFrame(kind byte, frame []byte) (fn int, err error) {
	fn = korgFunc(frame)
	if kind > 1 || len(frame) < 3 || frame[0] != 0xF0 || frame[len(frame)-1] != 0xF7 {
		return fn, ErrBadFrame
	}
	for _, b := range frame[1 : len(frame)-1] {
		if b >= 0x80 {
			return fn, ErrBadFrame
		}
	}

	if len(frame) == 6 && frame[1] == 0x7E && (frame[2] == 0x00 || frame[2] == 0x7F) &&
		frame[3] == 0x06 && frame[4] == 0x01 {
		return fn, nil // device inquiry
	}
	if fn < 0 {
		return fn, ErrForbidden
	}

	n := len(frame)
	switch fn {
	case 0x12, 0x10: // mode request, current program request
		if n == 8 {
			return fn, nil
		}
	case 0x1C, 0x4E: // program dump request (0m pp), mode change (mm pp)
		if n == 10 {
			return fn, nil
		}
	case 0x31: // custom dump request (00 pp)
		if n == 10 && frame[7] == 0x00 {
			return fn, nil
		}
	case 0x41: // parameter change (ID SUB lo hi)
		if n == 12 {
			return fn, nil
		}
	case FnProgramDump: // write program to slot (00 pp <71>)
		if n == 81 && frame[7] == 0x00 {
			return fn, saveFlag(kind)
		}
	case FnProgramWriteRequest: // program write request (00 pp)
		if n == 10 && frame[7] == 0x00 {
			return fn, saveFlag(kind)
		}
	}
	return fn, ErrForbidden
}

func saveFlag(kind byte) error {
	if kind != 1 {
		return ErrSaveFlagRequired
	}
	return nil
}

// IsIdentity reports whether frame is a Device Inquiry reply from a VOX Valvetronix X
// (F0 7E xx 06 02 42 34 01 ... F7).
func IsIdentity(frame []byte) bool {
	return len(frame) >= 9 && frame[0] == 0xF0 && frame[1] == 0x7E && frame[3] == 0x06 &&
		frame[4] == 0x02 && frame[5] == 0x42 && frame[6] == 0x34 && frame[7] == 0x01 &&
		frame[len(frame)-1] == 0xF7
}
