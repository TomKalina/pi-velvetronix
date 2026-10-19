// Command pi-velvetronix is the VOX Valvetronix web bridge daemon (design §3).
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/TomKalina/pi-velvetronix/internal/auth"
	"github.com/TomKalina/pi-velvetronix/internal/bridge"
	"github.com/TomKalina/pi-velvetronix/internal/mdns"
	"github.com/TomKalina/pi-velvetronix/internal/netmgr"
	"github.com/TomKalina/pi-velvetronix/internal/sdnotify"
	"github.com/TomKalina/pi-velvetronix/internal/update"
	"github.com/TomKalina/pi-velvetronix/internal/web"
)

// version is set with -ldflags "-X main.version=<ver>".
var version = "dev"

const (
	defaultState = "/var/lib/pi-velvetronix"
	mdnsAlias    = "vox.local"
)

func main() {
	args := os.Args[1:]
	cmd := "serve"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}

	var err error
	switch cmd {
	case "serve":
		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
		err = serve(ctx, args)
		stop()
	case "set-password":
		err = setPassword(args, os.Stdin)
	case "version":
		fmt.Println(version)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q (serve, set-password, version)\n", cmd)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "pi-velvetronix:", err)
		os.Exit(1)
	}
}

func setPassword(args []string, stdin io.Reader) error {
	fl := flag.NewFlagSet("set-password", flag.ExitOnError)
	state := fl.String("state", defaultState, "state directory")
	fl.Parse(args)

	line, err := bufio.NewReader(stdin).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	if err := auth.SetPassword(*state, strings.TrimRight(line, "\r\n")); err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, "password set, sessions invalidated")
	return nil
}

func serve(ctx context.Context, args []string) error {
	fl := flag.NewFlagSet("serve", flag.ExitOnError)
	listen := fl.String("listen", ":80", "HTTP listen address")
	state := fl.String("state", defaultState, "state directory (writable)")
	match := fl.String("midi-match", "Valvetronix", "case-insensitive substring of the ALSA card id, name or long name")
	device := fl.String("midi-device", "", "explicit rawmidi node, overrides --midi-match")
	apCon := fl.String("ap-con", "vox-ap", "NetworkManager connection id of the fallback hotspot")
	noNetwork := fl.Bool("no-network", false, "disable netmgr and mdns (local development)")
	updateDir := fl.String("update-dir", update.Dir, "root-owned update directory (read only)")
	fl.Parse(args)
	update.Dir = *updateDir

	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	// 1. Watchdog from process start, so a slow start cannot trip it.
	if d, ok := sdnotify.WatchdogInterval(); ok {
		go watchdog(ctx, d/2, log)
	}

	// 2. State directory.
	if err := os.MkdirAll(filepath.Join(*state, "inbox"), 0o750); err != nil {
		return err
	}

	// 3. Auth.
	store, err := auth.Load(*state)
	if err != nil {
		return fmt.Errorf("load auth: %w", err)
	}
	if !store.Configured() {
		log.Warn("no web password set, every login fails until set-password is run")
	}

	// 4. HTTP.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	amp := bridge.New(*match, *device, log)
	host, _ := os.Hostname()
	mdnsState := web.NewMDNSState(strings.TrimSuffix(host, ".local")+".local", mdnsAlias)
	var wifi web.Wifi = offWifi{}
	var nm *netmgr.Manager
	if !*noNetwork {
		nm = netmgr.New(*apCon, log)
		wifi = nm
	}
	srv := web.New(ctx, web.Config{
		Version:  version,
		StateDir: *state,
		Assets:   web.Assets(),
		Auth:     store,
		Amp:      web.FromBridge(amp),
		Wifi:     wifi,
		Updater:  web.NewUpdater(*state, version),
		MDNS:     mdnsState,
		Log:      log,
	})
	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		return err
	}
	httpSrv := &http.Server{
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}
	serveErr := make(chan error, 1)
	go func() { serveErr <- httpSrv.Serve(ln) }()
	log.Info("listening", "addr", ln.Addr().String(), "version", version)

	// 5. Self-check, then READY and confirm the release.
	if err := selfCheck(ln.Addr()); err != nil {
		return fmt.Errorf("self-check: %w", err)
	}
	if err := sdnotify.Ready(); err != nil {
		log.Warn("sd_notify READY", "error", err)
	}
	// boot-guard drops the pending entry of this version on the next start.
	if err := update.Confirm(*state, version); err != nil {
		log.Warn("write confirmed", "error", err)
	} else {
		log.Info("release confirmed", "version", version)
	}
	srv.RefreshUpdate()

	// 6. Bridge, netmgr supervisor, mdns alias.
	var wg sync.WaitGroup
	wg.Go(func() { amp.Run(ctx) })
	if nm != nil {
		wg.Go(func() { nm.Run(ctx) })
		wg.Go(func() { mdns.PublishAlias(ctx, mdnsAlias, nm.IPv4, mdnsState.Set, log) })
	}

	select {
	case <-ctx.Done():
		log.Info("shutting down")
	case err = <-serveErr:
		log.Error("http server", "error", err)
	}
	cancel() // also ends WebSocket sessions, which Shutdown does not track
	sctx, scancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer scancel()
	_ = httpSrv.Shutdown(sctx)
	wg.Wait()
	return err
}

// selfCheck asks our own /healthz over loopback (or the explicit listen IP).
func selfCheck(addr net.Addr) error {
	tcp, ok := addr.(*net.TCPAddr)
	if !ok {
		return fmt.Errorf("unexpected listener address %v", addr)
	}
	host := "127.0.0.1"
	if !tcp.IP.IsUnspecified() {
		host = tcp.IP.String()
	}
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get("http://" + net.JoinHostPort(host, strconv.Itoa(tcp.Port)) + "/healthz")
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("healthz returned %s", resp.Status)
	}
	return nil
}

func watchdog(ctx context.Context, every time.Duration, log *slog.Logger) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		if err := sdnotify.Watchdog(); err != nil {
			log.Warn("sd_notify WATCHDOG", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
