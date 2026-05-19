//go:build linux

package tun

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"unsafe"

	"golang.org/x/sys/unix"
)

type linuxDevice struct {
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
	fd, err := unix.Open("/dev/net/tun", unix.O_RDWR|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	ifr := ifreq{}
	if opts.Name != "" {
		if err := setIfName(&ifr, opts.Name); err != nil {
			_ = unix.Close(fd)
			return nil, err
		}
	}
	// Allocate a pure L3 tunnel interface without packet information prefix.
	flags := uint16(unix.IFF_TUN | unix.IFF_NO_PI)
	*(*uint16)(unsafe.Pointer(&ifr.Data[0])) = flags
	if _, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), uintptr(unix.TUNSETIFF), uintptr(unsafe.Pointer(&ifr))); errno != 0 {
		_ = unix.Close(fd)
		return nil, errno
	}
	name := bytesToString(ifr.Name[:])
	if opts.MTU > 0 {
		if err := setInterfaceMTU(name, opts.MTU); err != nil {
			_ = unix.Close(fd)
			return nil, err
		}
	}
	if !opts.SkipUp {
		if err := bringInterfaceUp(name); err != nil {
			_ = unix.Close(fd)
			return nil, err
		}
	}
	if err := applyInterfaceAddresses(name, opts.Addresses, mode); err != nil {
		_ = unix.Close(fd)
		return nil, err
	}
	if err := applyInterfaceRoutes(name, opts.Routes, mode); err != nil {
		_ = unix.Close(fd)
		return nil, err
	}
	addresses := make([]string, 0, len(opts.Addresses))
	for _, a := range opts.Addresses {
		a = strings.TrimSpace(a)
		if a != "" {
			addresses = append(addresses, a)
		}
	}
	routes := make([]string, 0, len(opts.Routes))
	for _, r := range opts.Routes {
		r = strings.TrimSpace(r)
		if r != "" {
			routes = append(routes, r)
		}
	}
	return &linuxDevice{
		file:           os.NewFile(uintptr(fd), "/dev/net/tun"),
		name:           name,
		addresses:      addresses,
		routes:         routes,
		configMode:     mode,
		cleanupOnClose: opts.CleanupOnClose,
	}, nil
}

func (d *linuxDevice) Read(p []byte) (int, error) {
	return d.file.Read(p)
}

func (d *linuxDevice) Write(p []byte) (int, error) {
	return d.file.Write(p)
}

func (d *linuxDevice) Close() error {
	var errs []error
	if d.cleanupOnClose {
		if err := cleanupInterfaceRoutes(d.name, d.routes); err != nil {
			errs = append(errs, err)
		}
		if err := cleanupInterfaceAddresses(d.name, d.addresses); err != nil {
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

func (d *linuxDevice) Name() string {
	return d.name
}

func (d *linuxDevice) MTU() (int, error) {
	sock, err := unix.Socket(unix.AF_INET, unix.SOCK_DGRAM, 0)
	if err != nil {
		return 0, err
	}
	defer unix.Close(sock)

	ifr := ifreq{}
	if err := setIfName(&ifr, d.name); err != nil {
		return 0, err
	}
	if _, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(sock), uintptr(unix.SIOCGIFMTU), uintptr(unsafe.Pointer(&ifr))); errno != 0 {
		return 0, errno
	}
	mtu := *(*int32)(unsafe.Pointer(&ifr.Data[0]))
	return int(mtu), nil
}

type ifreq struct {
	Name [unix.IFNAMSIZ]byte
	Data [24]byte
}

func setIfName(ifr *ifreq, name string) error {
	if len(name) >= len(ifr.Name) {
		return ErrDeviceNameTooLong
	}
	for i := range ifr.Name {
		ifr.Name[i] = 0
	}
	copy(ifr.Name[:], []byte(name))
	return nil
}

func bytesToString(b []byte) string {
	n := 0
	for n < len(b) && b[n] != 0 {
		n++
	}
	return string(b[:n])
}

func bringInterfaceUp(name string) error {
	sock, err := unix.Socket(unix.AF_INET, unix.SOCK_DGRAM, 0)
	if err != nil {
		return err
	}
	defer unix.Close(sock)

	ifr := ifreq{}
	if err := setIfName(&ifr, name); err != nil {
		return err
	}
	if _, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(sock), uintptr(unix.SIOCGIFFLAGS), uintptr(unsafe.Pointer(&ifr))); errno != 0 {
		return errno
	}
	flags := *(*uint16)(unsafe.Pointer(&ifr.Data[0]))
	flags |= unix.IFF_UP
	*(*uint16)(unsafe.Pointer(&ifr.Data[0])) = flags
	if _, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(sock), uintptr(unix.SIOCSIFFLAGS), uintptr(unsafe.Pointer(&ifr))); errno != 0 {
		return errno
	}
	return nil
}

func setInterfaceMTU(name string, mtu int) error {
	sock, err := unix.Socket(unix.AF_INET, unix.SOCK_DGRAM, 0)
	if err != nil {
		return err
	}
	defer unix.Close(sock)

	ifr := ifreq{}
	if err := setIfName(&ifr, name); err != nil {
		return err
	}
	*(*int32)(unsafe.Pointer(&ifr.Data[0])) = int32(mtu)
	if _, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(sock), uintptr(unix.SIOCSIFMTU), uintptr(unsafe.Pointer(&ifr))); errno != 0 {
		return errno
	}
	return nil
}

func applyInterfaceAddresses(name string, addrs []string, mode string) error {
	action := "replace"
	if mode == ConfigModeAdd {
		action = "add"
	}
	for _, raw := range addrs {
		addr := strings.TrimSpace(raw)
		if addr == "" {
			continue
		}
		ip, _, err := net.ParseCIDR(addr)
		if err != nil {
			return ErrInvalidAddressCIDR
		}
		args := []string{"-4", "addr", action, addr, "dev", name}
		if ip.To4() == nil {
			args[0] = "-6"
		}
		if err := runIP(args...); err != nil {
			return err
		}
	}
	return nil
}

func applyInterfaceRoutes(name string, routes []string, mode string) error {
	action := "replace"
	if mode == ConfigModeAdd {
		action = "add"
	}
	for _, raw := range routes {
		route := strings.TrimSpace(raw)
		if route == "" {
			continue
		}
		if strings.EqualFold(route, "default") {
			if err := runIP("-4", "route", action, "default", "dev", name); err != nil {
				return err
			}
			continue
		}
		ip, _, err := net.ParseCIDR(route)
		if err != nil {
			return ErrInvalidRouteCIDR
		}
		args := []string{"-4", "route", action, route, "dev", name}
		if ip.To4() == nil {
			args[0] = "-6"
		}
		if err := runIP(args...); err != nil {
			return err
		}
	}
	return nil
}

func cleanupInterfaceAddresses(name string, addrs []string) error {
	var errs []error
	for _, raw := range addrs {
		addr := strings.TrimSpace(raw)
		if addr == "" {
			continue
		}
		ip, _, err := net.ParseCIDR(addr)
		if err != nil {
			continue
		}
		args := []string{"-4", "addr", "del", addr, "dev", name}
		if ip.To4() == nil {
			args[0] = "-6"
		}
		if err := runIPAllowMissing(args...); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) == 0 {
		return nil
	}
	return errors.Join(errs...)
}

func cleanupInterfaceRoutes(name string, routes []string) error {
	var errs []error
	for _, raw := range routes {
		route := strings.TrimSpace(raw)
		if route == "" {
			continue
		}
		if strings.EqualFold(route, "default") {
			if err := runIPAllowMissing("-4", "route", "del", "default", "dev", name); err != nil {
				errs = append(errs, err)
			}
			continue
		}
		ip, _, err := net.ParseCIDR(route)
		if err != nil {
			continue
		}
		args := []string{"-4", "route", "del", route, "dev", name}
		if ip.To4() == nil {
			args[0] = "-6"
		}
		if err := runIPAllowMissing(args...); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) == 0 {
		return nil
	}
	return errors.Join(errs...)
}

func runIP(args ...string) error {
	bin, err := findIPBinary()
	if err != nil {
		return err
	}
	cmd := exec.Command(bin, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			return fmt.Errorf("ip %s: %w", strings.Join(args, " "), err)
		}
		return fmt.Errorf("ip %s: %w: %s", strings.Join(args, " "), err, msg)
	}
	return nil
}

func runIPAllowMissing(args ...string) error {
	err := runIP(args...)
	if err == nil {
		return nil
	}
	msg := err.Error()
	if strings.Contains(msg, "Cannot find device") ||
		strings.Contains(msg, "No such process") ||
		strings.Contains(msg, "Cannot assign requested address") ||
		strings.Contains(msg, "No such file or directory") {
		return nil
	}
	return err
}

func findIPBinary() (string, error) {
	if p, err := exec.LookPath("ip"); err == nil {
		return p, nil
	}
	candidates := []string{"/sbin/ip", "/usr/sbin/ip"}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c, nil
		}
	}
	return "", errors.New("tun: ip command not found (iproute2 is required for address/route config)")
}
