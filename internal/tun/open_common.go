package tun

import (
	"errors"
	"net"
	"strings"
)

var (
	ErrUnsupportedPlatform = errors.New("tun: unsupported platform")
	ErrDeviceNameTooLong   = errors.New("tun: device name too long")
	ErrInvalidMTU          = errors.New("tun: invalid mtu")
	ErrInvalidAddressCIDR  = errors.New("tun: invalid address cidr")
	ErrInvalidRouteCIDR    = errors.New("tun: invalid route cidr")
	ErrInvalidConfigMode   = errors.New("tun: invalid config mode")
)

const (
	ConfigModeReplace = "replace"
	ConfigModeAdd     = "add"
)

type OpenOptions struct {
	// Name is the desired TUN device name, for example "tun0".
	// If empty, platform default naming is used.
	Name string
	// MTU is an optional interface MTU to apply on open.
	// Zero means leave MTU unchanged.
	MTU int
	// SkipUp disables automatic IFF_UP on open.
	// By default, Open brings the interface up.
	SkipUp bool
	// Addresses are optional interface addresses in CIDR form.
	Addresses []string
	// Routes are optional route destinations in CIDR form.
	// The literal value "default" is also accepted.
	Routes []string
	// ConfigMode controls how Addresses/Routes are applied: "replace" or "add".
	// Empty value defaults to "replace".
	ConfigMode string
	// CleanupOnClose removes configured routes and addresses during device close.
	// Best-effort only and intended for explicit runtime teardown.
	CleanupOnClose bool
}

func validateOpenOptions(opts OpenOptions) error {
	if opts.MTU < 0 || opts.MTU > 65535 {
		return ErrInvalidMTU
	}
	for _, addr := range opts.Addresses {
		addr = strings.TrimSpace(addr)
		if addr == "" {
			return ErrInvalidAddressCIDR
		}
		if _, _, err := net.ParseCIDR(addr); err != nil {
			return ErrInvalidAddressCIDR
		}
	}
	for _, route := range opts.Routes {
		route = strings.TrimSpace(route)
		if route == "" {
			return ErrInvalidRouteCIDR
		}
		if strings.EqualFold(route, "default") {
			continue
		}
		if _, _, err := net.ParseCIDR(route); err != nil {
			return ErrInvalidRouteCIDR
		}
	}
	if mode := normalizeConfigMode(opts.ConfigMode); mode != ConfigModeReplace && mode != ConfigModeAdd {
		return ErrInvalidConfigMode
	}
	return nil
}

func normalizeConfigMode(mode string) string {
	mode = strings.TrimSpace(strings.ToLower(mode))
	if mode == "" {
		return ConfigModeReplace
	}
	return mode
}
