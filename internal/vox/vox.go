// Package vox holds the few VOX Valvetronix X protocol helpers the Go side needs
// (the browser implements the protocol itself). See docs/protocol.md.
package vox

import "fmt"

// Header is the KORG SysEx header of every VOX Valvetronix X message (P§1).
var Header = []byte{0xF0, 0x42, 0x30, 0x00, 0x01, 0x34}

// Function bytes (frame index 6), P§1 / P§4.
const (
	FnModeRequest        = 0x12
	FnCurrentProgramReq  = 0x10
	FnProgramDumpRequest = 0x1C
	FnCustomDumpRequest  = 0x31
	FnProgramWriteReq    = 0x11

	FnModeData       = 0x42
	FnCurrentProgram = 0x40
	FnProgramDump    = 0x4C
	FnCustomDump     = 0x65
	FnModeChange     = 0x4E
	FnParamChange    = 0x41

	FnDataFormatError = 0x26
	FnACK             = 0x23
	FnNAK             = 0x24
	FnWriteCompleted  = 0x21
	FnWriteError      = 0x22
)

const (
	StructLen = 62 // program struct (P§5)
	PackedLen = 71 // 7-to-8 packed program (P§2.2)
	UserSlots = 8
)

// Modes in 42 / 4E messages (P§3).
const (
	ModeUser   = 0x00
	ModePreset = 0x01
	ModeManual = 0x02
)

// Effect slot SUB values (41 02 / 41 03) and their parameter IDs (P§3).
const (
	SlotPedal1 = 0x01
	SlotPedal2 = 0x02
	SlotReverb = 0x04

	IDPedal1 = 0x05
	IDPedal2 = 0x06
	IDReverb = 0x08
)

// IdentityReply is the Device Inquiry reply of a VT-X with firmware 1.00 (P§1).
var IdentityReply = []byte{0xF0, 0x7E, 0x00, 0x06, 0x02, 0x42, 0x34, 0x01, 0x00, 0x00, 0x00, 0x00, 0x01, 0x00, 0xF7}

// Frame builds a full KORG frame: Header, fn, data..., F7.
func Frame(fn byte, data ...byte) []byte {
	f := make([]byte, 0, len(Header)+2+len(data))
	f = append(f, Header...)
	f = append(f, fn)
	f = append(f, data...)
	return append(f, 0xF7)
}

// Enc14 splits a live parameter value into LSB-first 7-bit bytes (P§2.1).
func Enc14(v int) (lo, hi byte) { return byte(v & 0x7F), byte((v >> 7) & 0x7F) }

// Dec14 joins LSB-first 7-bit bytes (P§2.1).
func Dec14(lo, hi byte) int { return int(lo&0x7F) | int(hi&0x7F)<<7 }

// Pack7 applies KORG 7-to-8 packing: each set of up to 7 bytes becomes one MSB byte
// (bit j = b7 of byte j) followed by the bytes with b7 cleared. 62 bytes -> 71.
func Pack7(data []byte) []byte {
	out := make([]byte, 0, len(data)+(len(data)+6)/7)
	for i := 0; i < len(data); i += 7 {
		g := data[i:min(i+7, len(data))]
		var msb byte
		for j, b := range g {
			if b&0x80 != 0 {
				msb |= 1 << j
			}
		}
		out = append(out, msb)
		for _, b := range g {
			out = append(out, b&0x7F)
		}
	}
	return out
}

// Unpack7 reverses Pack7. It rejects MSB bytes with b7 set or bits for absent bytes,
// and data bytes with b7 set. 71 bytes -> 62.
func Unpack7(p []byte) ([]byte, error) {
	out := make([]byte, 0, len(p))
	for i := 0; i < len(p); i += 8 {
		msb := p[i]
		n := min(7, len(p)-i-1)
		if msb&0x80 != 0 || msb>>n != 0 {
			return nil, fmt.Errorf("vox: bad MSB byte %02X at %d", msb, i)
		}
		for j := range n {
			b := p[i+1+j]
			if b&0x80 != 0 {
				return nil, fmt.Errorf("vox: 8-bit byte %02X at %d", b, i+1+j)
			}
			out = append(out, b|((msb>>j)&1)<<7)
		}
	}
	return out, nil
}
