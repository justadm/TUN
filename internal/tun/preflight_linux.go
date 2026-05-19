//go:build linux

package tun

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
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

	ok, err := hasNetAdminPrivileges()
	if err != nil {
		werr := fmt.Errorf("%w: %v", ErrPreflightPrivileges, err)
		r.addCheck("privileges", false, werr.Error())
		return r, werr
	}
	if !ok {
		r.addCheck("privileges", false, ErrPreflightPrivileges.Error())
		return r, ErrPreflightPrivileges
	}
	r.addCheck("privileges", true, "root or CAP_NET_ADMIN available")

	if err := ensureTunDeviceAccessible(); err != nil {
		r.addCheck("dev-net-tun", false, err.Error())
		return r, err
	}
	r.addCheck("dev-net-tun", true, "/dev/net/tun is accessible")

	if needsIPCommand(opts) {
		p, err := findIPBinaryForPreflight()
		if err != nil {
			werr := fmt.Errorf("%w: %v", ErrPreflightIPCommand, err)
			r.addCheck("ip-command", false, werr.Error())
			return r, werr
		}
		r.addCheck("ip-command", true, "ip binary found: "+p)
	} else {
		r.addCheck("ip-command", true, "not required by current TUN options")
	}
	r.Success = true
	return r, nil
}

func hasNetAdminPrivileges() (bool, error) {
	if os.Geteuid() == 0 {
		return true, nil
	}
	raw, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return false, err
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if !strings.HasPrefix(line, "CapEff:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return false, fmt.Errorf("malformed CapEff line")
		}
		v, err := strconv.ParseUint(fields[1], 16, 64)
		if err != nil {
			return false, err
		}
		const capNetAdminBit = 12
		return (v & (1 << capNetAdminBit)) != 0, nil
	}
	return false, fmt.Errorf("CapEff not found")
}

func ensureTunDeviceAccessible() error {
	if _, err := os.Stat("/dev/net/tun"); err != nil {
		return fmt.Errorf("%w: %v", ErrPreflightTunDevice, err)
	}
	if err := unix.Access("/dev/net/tun", unix.R_OK|unix.W_OK); err != nil {
		return fmt.Errorf("%w: %v", ErrPreflightTunDevice, err)
	}
	return nil
}

func needsIPCommand(opts OpenOptions) bool {
	return len(opts.Addresses) > 0 || len(opts.Routes) > 0 || opts.CleanupOnClose
}

func findIPBinaryForPreflight() (string, error) {
	if p, err := exec.LookPath("ip"); err == nil {
		return p, nil
	}
	candidates := []string{"/sbin/ip", "/usr/sbin/ip"}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c, nil
		}
	}
	return "", fmt.Errorf("iproute2 not found")
}
