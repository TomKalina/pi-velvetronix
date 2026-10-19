// Command fakeamp simulates a VOX Valvetronix X on a rawmidi device (e.g. one end of
// an ALSA VirMIDI loopback) so the daemon and the browser can be tested without the amp.
package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/TomKalina/pi-velvetronix/cmd/fakeamp/sim"
	"github.com/TomKalina/pi-velvetronix/internal/midi"
)

func main() {
	device := flag.String("device", "", "rawmidi node to serve, e.g. /dev/snd/midiC1D0 (required)")
	knobEvery := flag.Duration("knob-every", 0, "emit an unsolicited GAIN change this often (0 = off)")
	flag.Parse()
	if *device == "" {
		fmt.Fprintln(os.Stderr, "fakeamp: --device is required")
		flag.Usage()
		os.Exit(2)
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	port, err := midi.OpenPort(*device)
	if err != nil {
		log.Error("open", "device", *device, "err", err)
		os.Exit(1)
	}
	log.Info("fakeamp ready", "device", *device, "knob_every", *knobEvery)

	amp := sim.New()
	// Single writer so responses and knob notifications never interleave inside a frame.
	out := make(chan []byte, 64)
	go func() {
		for f := range out {
			log.Info("tx", "frame", fmt.Sprintf("% X", f))
			if _, err := port.Write(f); err != nil {
				log.Error("write", "err", err)
				os.Exit(1)
			}
		}
	}()
	if *knobEvery > 0 {
		go func() {
			for range time.Tick(*knobEvery) {
				out <- amp.KnobTurn()
			}
		}()
	}

	var framer midi.Framer
	buf := make([]byte, 4096)
	for {
		n, err := port.Read(buf)
		if err != nil {
			log.Error("read", "err", err)
			os.Exit(1)
		}
		framer.Feed(buf[:n], func(frame []byte) {
			log.Info("rx", "frame", fmt.Sprintf("% X", frame))
			for _, r := range amp.Handle(frame) {
				out <- r
			}
		})
	}
}
