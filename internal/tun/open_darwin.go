//go:build darwin

package tun

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

const (
	utunControlName = "com.apple.net.utun_control"
	utunOptIfName   = 2
	sysProtoControl = 2
)

type darwinDevice struct {
	file           *os.File
	name           string
	addresses      []string
	routes         []string
	configMode     string
	cleanupOnClose bool
}

func Open(opts OpenOptions) (Device, error) {
	if err := validateOpenOptions(opts); err != nil {
		return nil, err
	}
	mode := normalizeConfigMode(opts.ConfigMode)

	fd, err := unix.Socket(unix.AF_SYSTEM, unix.SOCK_DGRAM, sysProtoControl)
	if err != nil {
		return nil, err
	}

	var ci unix.CtlInfo
	copy(ci.Name[:], []byte(utunControlName))
	if err := unix.IoctlCtlInfo(fd, &ci); err != nil {
		_ = unix.Close(fd)
		return nil, err
	}

	sa := &unix.SockaddrCtl{ID: ci.Id}
	if opts.Name != "" {
		unit, err := parseUtunUnit(opts.Name)
		if err != nil {
			_ = unix.Close(fd)
			return nil, err
		}
		sa.Unit = unit
	}
	if err := unix.Connect(fd, sa); err != nil {
		_ = unix.Close(fd)
		return nil, err
	}

	name, err := unix.GetsockoptString(fd, sysProtoControl, utunOptIfName)
	if err != nil || strings.TrimSpace(name) == "" {
		name = strings.TrimSpace(opts.Name)
	}
	name = strings.Trim(name, "\x00 \t\r\n")
	if name == "" {
		name = "utun"
	}

	if opts.MTU > 0 {
		if err := runIfconfig(name, "mtu", strconv.Itoa(opts.MTU)); err != nil {
			_ = unix.Close(fd)
			return nil, err
		}
	}
	if !opts.SkipUp {
		if err := runIfconfig(name, "up"); err != nil {
			_ = unix.Close(fd)
			return nil, err
		}
	}
	if err := applyDarwinAddresses(name, opts.Addresses, mode); err != nil {
		_ = unix.Close(fd)
		return nil, err
	}
	if err := applyDarwinRoutes(name, opts.Routes, mode); err != nil {
		_ = unix.Close(fd)
		return nil, err
	}

	addresses := sanitizeStringList(opts.Addresses)
	routes := sanitizeStringList(opts.Routes)
	return &darwinDevice{
		file:           os.NewFile(uintptr(fd), "/dev/utun"),
		name:           name,
		addresses:      addresses,
		routes:         routes,
		configMode:     mode,
		cleanupOnClose: opts.CleanupOnClose,
	}, nil
}

func (d *darwinDevice) Read(p []byte) (int, error) {
	// utun prepends 4-byte address family header.
	buf := make([]byte, len(p)+4)
	for {
		n, err := d.file.Read(buf)
		if err != nil {
			return 0, err
		}
		if n <= 4 {
			continue
		}
		payload := buf[4:n]
		if len(payload) > len(p) {
			return 0, io.ErrShortBuffer
		}
		copy(p, payload)
		return len(payload), nil
	}
}

func (d *darwinDevice) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	var family uint32 = unix.AF_INET
	if (p[0] >> 4) == 6 {
		family = unix.AF_INET6
	}
	frame := make([]byte, 4+len(p))
	binary.BigEndian.PutUint32(frame[:4], family)
	copy(frame[4:], p)
	n, err := d.file.Write(frame)
	if err != nil {
		return 0, err
	}
	if n <= 4 {
		return 0, io.ErrShortWrite
	}
	return n - 4, nil
}

func (d *darwinDevice) Close() error {
	var errs []error
	if d.cleanupOnClose {
		if err := cleanupDarwinRoutes(d.name, d.routes); err != nil {
			errs = append(errs, err)
		}
		if err := cleanupDarwinAddresses(d.name, d.addresses); err != nil {
			errs = append(errs, err)
		}
	}
	if d.file != nil {
		if err := d.file.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) == 0 {
		return nil
	}
	return errors.Join(errs...)
}

func (d *darwinDevice) Name() string { return d.name }

func (d *darwinDevice) MTU() (int, error) {
	out, err := exec.Command("/sbin/ifconfig", d.name).CombinedOutput()
	if err != nil {
		return 0, fmt.Errorf("ifconfig %s: %w (%s)", d.name, err, strings.TrimSpace(string(out)))
	}
	fields := strings.Fields(string(out))
	for i := 0; i < len(fields)-1; i++ {
		if fields[i] == "mtu" {
			v, convErr := strconv.Atoi(fields[i+1])
			if convErr == nil {
				return v, nil
			}
		}
	}
	return 0, fmt.Errorf("mtu not found in ifconfig output")
}

func parseUtunUnit(name string) (uint32, error) {
	name = strings.TrimSpace(strings.ToLower(name))
	if !strings.HasPrefix(name, "utun") {
		return 0, fmt.Errorf("darwin requires utun<N> name, got %q", name)
	}
	n, err := strconv.Atoi(strings.TrimPrefix(name, "utun"))
	if err != nil || n < 0 {
		return 0, fmt.Errorf("invalid utun unit in %q", name)
	}
	// Darwin uses 1-based utun unit in SockaddrCtl.
	return uint32(n + 1), nil
}

func sanitizeStringList(in []string) []string {
	out := make([]string, 0, len(in))
	for _, v := range in {
		v = strings.TrimSpace(v)
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}

func runIfconfig(name string, args ...string) error {
	cmd := exec.Command("/sbin/ifconfig", append([]string{name}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ifconfig %s %s: %w (%s)", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func runRoute(args ...string) error {
	cmd := exec.Command("/sbin/route", append([]string{"-n"}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("route -n %s: %w (%s)", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func applyDarwinAddresses(name string, addrs []string, mode string) error {
	for _, raw := range addrs {
		cidr := strings.TrimSpace(raw)
		if cidr == "" {
			continue
		}
		ip, ipNet, err := net.ParseCIDR(cidr)
		if err != nil {
			return ErrInvalidAddressCIDR
		}
		if ip.To4() == nil {
			args := []string{"inet6", cidr}
			if mode == ConfigModeAdd {
				args = append(args, "alias")
			}
			if err := runIfconfig(name, args...); err != nil {
				return err
			}
			continue
		}
		mask := net.IP(ipNet.Mask).String()
		peer := inferDarwinPeerIPv4(ip, ipNet)
		args := []string{"inet", ip.String(), peer.String(), "netmask", mask}
		if mode == ConfigModeAdd {
			args = append(args, "alias")
		}
		if err := runIfconfig(name, args...); err != nil {
			return err
		}
	}
	return nil
}

func inferDarwinPeerIPv4(ip net.IP, ipNet *net.IPNet) net.IP {
	local := ip.To4()
	if local == nil || ipNet == nil {
		return ip
	}
	ones, bits := ipNet.Mask.Size()
	if ones < 0 || bits != 32 {
		return local
	}
	// /32 has no peer choice.
	if ones >= 32 {
		return local
	}
	localU := ipv4ToUint32(local)
	netU := ipv4ToUint32(ipNet.IP.To4())
	maskU := ipv4ToUint32(net.IP(ipNet.Mask).To4())
	bcastU := netU | ^maskU

	// /31 point-to-point pair: flip the least-significant host bit.
	if ones == 31 {
		return uint32ToIPv4(localU ^ 1)
	}

	firstHost := netU + 1
	lastHost := bcastU - 1
	if firstHost > lastHost {
		return local
	}
	if localU == firstHost {
		if firstHost < lastHost {
			return uint32ToIPv4(firstHost + 1)
		}
		return local
	}
	return uint32ToIPv4(firstHost)
}

func ipv4ToUint32(ip net.IP) uint32 {
	v := ip.To4()
	if v == nil {
		return 0
	}
	return uint32(v[0])<<24 | uint32(v[1])<<16 | uint32(v[2])<<8 | uint32(v[3])
}

func uint32ToIPv4(v uint32) net.IP {
	return net.IPv4(byte(v>>24), byte(v>>16), byte(v>>8), byte(v))
}

func applyDarwinRoutes(name string, routes []string, mode string) error {
	for _, raw := range routes {
		route := strings.TrimSpace(raw)
		if route == "" {
			continue
		}
		verb := "add"
		if mode == ConfigModeReplace {
			verb = "change"
		}
		if strings.EqualFold(route, "default") {
			if err := runRoute(verb, "default", "-interface", name); err != nil {
				// fallback to add if change failed because route does not exist
				if mode == ConfigModeReplace {
					_ = runRoute("add", "default", "-interface", name)
				}
			}
			continue
		}
		ip, ipNet, err := net.ParseCIDR(route)
		if err != nil {
			return ErrInvalidRouteCIDR
		}
		if ip.To4() == nil {
			if err := runRoute(verb, "-inet6", route, "-ifscope", name); err != nil && mode == ConfigModeReplace {
				_ = runRoute("add", "-inet6", route, "-ifscope", name)
			}
			continue
		}
		network := ipNet.IP.String()
		mask := net.IP(ipNet.Mask).String()
		if err := runRoute(verb, "-net", network, "-netmask", mask, "-ifscope", name); err != nil && mode == ConfigModeReplace {
			_ = runRoute("add", "-net", network, "-netmask", mask, "-ifscope", name)
		}
	}
	return nil
}

func cleanupDarwinRoutes(name string, routes []string) error {
	var errs []error
	for _, raw := range routes {
		route := strings.TrimSpace(raw)
		if route == "" {
			continue
		}
		if strings.EqualFold(route, "default") {
			_ = runRoute("delete", "default", "-interface", name)
			continue
		}
		ip, ipNet, err := net.ParseCIDR(route)
		if err != nil {
			continue
		}
		if ip.To4() == nil {
			_ = runRoute("delete", "-inet6", route, "-ifscope", name)
			continue
		}
		network := ipNet.IP.String()
		mask := net.IP(ipNet.Mask).String()
		if err := runRoute("delete", "-net", network, "-netmask", mask, "-ifscope", name); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func cleanupDarwinAddresses(name string, addrs []string) error {
	var errs []error
	for _, raw := range addrs {
		cidr := strings.TrimSpace(raw)
		if cidr == "" {
			continue
		}
		ip, ipNet, err := net.ParseCIDR(cidr)
		if err != nil {
			continue
		}
		if ip.To4() == nil {
			_ = runIfconfig(name, "inet6", cidr, "-alias")
			continue
		}
		mask := net.IP(ipNet.Mask).String()
		if err := runIfconfig(name, "inet", ip.String(), "-alias"); err != nil {
			// fallback form
			if err2 := runIfconfig(name, "inet", ip.String(), ip.String(), "netmask", mask, "-alias"); err2 != nil {
				errs = append(errs, err2)
			}
		}
	}
	return errors.Join(errs...)
}
