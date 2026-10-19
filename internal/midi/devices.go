package midi

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// ErrBusy is returned by OpenPort when another program holds the rawmidi device.
var ErrBusy = errors.New("device busy")

// Port is an open rawmidi device.
type Port interface{ io.ReadWriteCloser }

// Device is an ALSA card with at least one rawmidi node.
type Device struct {
	Card     string // card index, e.g. "1"
	ID       string // card id, e.g. "VirMIDI"
	Name     string // short name, e.g. "Valvetronix X"
	LongName string
	UsbID    string // "VVVV:PPPP", empty for non-USB cards
	Node     string // first rawmidi node, e.g. "/dev/snd/midiC1D0"
}

// FindDevices lists cards whose id, name or long name contains match (case-insensitive)
// and that have a rawmidi node.
func FindDevices(match string) ([]Device, error) {
	return findDevices("/proc/asound", "/dev/snd", match)
}

var (
	cardLine = regexp.MustCompile(`^\s*(\d+) \[(.*?)\s*\]: (.*?) - (.*)$`)
	midiNode = regexp.MustCompile(`^midiC(\d+)D(\d+)$`)
)

func findDevices(procRoot, devRoot, match string) ([]Device, error) {
	data, err := os.ReadFile(filepath.Join(procRoot, "cards"))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil // no sound support at all
		}
		return nil, err
	}
	nodes, err := midiNodes(devRoot)
	if err != nil {
		return nil, err
	}

	match = strings.ToLower(match)
	var devs []Device
	var cur *Device
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := sc.Text()
		if m := cardLine.FindStringSubmatch(line); m != nil {
			devs = append(devs, Device{Card: m[1], ID: m[2], Name: m[4]})
			cur = &devs[len(devs)-1]
			continue
		}
		if cur != nil && cur.LongName == "" {
			cur.LongName = strings.TrimSpace(line)
		}
	}

	var out []Device
	for _, d := range devs {
		node, ok := nodes[d.Card]
		if !ok {
			continue
		}
		if !strings.Contains(strings.ToLower(d.ID), match) &&
			!strings.Contains(strings.ToLower(d.Name), match) &&
			!strings.Contains(strings.ToLower(d.LongName), match) {
			continue
		}
		d.Node = node
		if b, err := os.ReadFile(filepath.Join(procRoot, "card"+d.Card, "usbid")); err == nil {
			d.UsbID = strings.TrimSpace(string(b))
		}
		out = append(out, d)
	}
	return out, nil
}

// midiNodes maps card index to its lowest-numbered rawmidi node.
func midiNodes(devRoot string) (map[string]string, error) {
	entries, err := os.ReadDir(devRoot)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	nodes := map[string]string{}
	lowest := map[string]int{}
	for _, e := range entries {
		m := midiNode.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		card, _ := strconv.Atoi(m[1])
		dev, _ := strconv.Atoi(m[2])
		key := strconv.Itoa(card)
		if cur, ok := lowest[key]; !ok || dev < cur {
			lowest[key] = dev
			nodes[key] = filepath.Join(devRoot, e.Name())
		}
	}
	return nodes, nil
}
