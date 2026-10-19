package web

import (
	"io"
	"sync"

	"github.com/TomKalina/pi-velvetronix/internal/bridge"
	"github.com/TomKalina/pi-velvetronix/internal/update"
)

type bridgeAmp struct{ *bridge.Bridge }

// FromBridge adapts *bridge.Bridge to Amp (Attach returns the Controller interface).
func FromBridge(b *bridge.Bridge) Amp { return bridgeAmp{b} }

func (a bridgeAmp) Attach(owner string) (Controller, bool) {
	c, ok := a.Bridge.Attach(owner)
	if !ok {
		return nil, false
	}
	return c, true
}

type fileUpdater struct{ stateDir, running string }

// NewUpdater returns the Updater backed by the update package and stateDir.
func NewUpdater(stateDir, running string) Updater { return fileUpdater{stateDir, running} }

func (u fileUpdater) Info() update.UpdateInfo { return update.ReadInfo(u.stateDir, u.running) }

func (u fileUpdater) Validate(r io.Reader) (*update.Package, error) {
	return update.Validate(r, u.running)
}

func (u fileUpdater) Stage(p *update.Package) error { return p.Stage(u.stateDir) }

// MDNSState holds the alias state reported by mdns.PublishAlias.
type MDNSState struct {
	mu sync.Mutex
	st MDNSStatus
}

// NewMDNSState starts in state "off" until the publisher reports otherwise.
func NewMDNSState(hostname, alias string) *MDNSState {
	return &MDNSState{st: MDNSStatus{Hostname: hostname, Alias: alias, AliasState: "off"}}
}

// Set is the state callback for mdns.PublishAlias.
func (m *MDNSState) Set(state string) {
	m.mu.Lock()
	m.st.AliasState = state
	m.mu.Unlock()
}

func (m *MDNSState) Status() MDNSStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.st
}
