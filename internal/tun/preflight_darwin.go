//go:build darwin

package tun

import (
	"fmt"
	"os/exec"
)

func Preflight(opts OpenOptions) error {
	_, err := BuildPreflightReport(opts)
	return err
}

func BuildPreflightReport(opts OpenOptions) (PreflightReport, error) {
	r := newPreflightReport()
	if err := validateOpenOptions(opts); err != nil {
		r.addCheck("options", false, err.Error())
		return r, err
	}
	r.addCheck("options", true, "open options validated")

	if opts.Name != "" {
		if _, err := parseUtunUnit(opts.Name); err != nil {
			r.addCheck("utun-name", false, err.Error())
			return r, err
		}
		r.addCheck("utun-name", true, "utun name validated")
	} else {
		r.addCheck("utun-name", true, "auto-assigned utun name")
	}

	if _, err := exec.LookPath("/sbin/ifconfig"); err != nil {
		werr := fmt.Errorf("tun: preflight failed, ifconfig unavailable: %w", err)
		r.addCheck("ifconfig", false, werr.Error())
		return r, werr
	}
	r.addCheck("ifconfig", true, "ifconfig found")

	if needsRouteCommandDarwin(opts) {
		if _, err := exec.LookPath("/sbin/route"); err != nil {
			werr := fmt.Errorf("tun: preflight failed, route unavailable: %w", err)
			r.addCheck("route", false, werr.Error())
			return r, werr
		}
		r.addCheck("route", true, "route found")
	} else {
		r.addCheck("route", true, "not required by current TUN options")
	}

	r.Success = true
	return r, nil
}

func needsRouteCommandDarwin(opts OpenOptions) bool {
	return len(opts.Routes) > 0 || opts.CleanupOnClose
}
