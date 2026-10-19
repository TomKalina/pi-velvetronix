package main

import (
	"context"
	"errors"

	"github.com/TomKalina/pi-velvetronix/internal/netmgr"
)

var errNoNetwork = errors.New("network management disabled (--no-network)")

// offWifi stands in for netmgr with --no-network: Wi-Fi mode "off", every change fails.
type offWifi struct{}

func (offWifi) Status() netmgr.WifiStatus {
	return netmgr.WifiStatus{Mode: "off", APSSID: "VOX-Velvetronix", APIP: "10.42.0.1"}
}

func (offWifi) Networks(context.Context, bool) ([]netmgr.Network, []netmgr.Saved, error) {
	return nil, nil, nil
}

func (offWifi) Connect(context.Context, string, string) error { return errNoNetwork }
func (offWifi) Forget(context.Context, string) error          { return errNoNetwork }
func (offWifi) Hotspot(int) error                             { return errNoNetwork }
