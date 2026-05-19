package tun

import (
	"errors"
	"runtime"
)

var (
	ErrPreflightPrivileges = errors.New("tun: preflight failed, CAP_NET_ADMIN or root privileges required")
	ErrPreflightTunDevice  = errors.New("tun: preflight failed, /dev/net/tun unavailable")
	ErrPreflightIPCommand  = errors.New("tun: preflight failed, ip command unavailable")
)

type PreflightCheck struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
}

type PreflightReport struct {
	Platform string           `json:"platform"`
	Success  bool             `json:"success"`
	Checks   []PreflightCheck `json:"checks"`
}

func newPreflightReport() PreflightReport {
	return PreflightReport{
		Platform: runtime.GOOS,
		Success:  true,
		Checks:   make([]PreflightCheck, 0, 4),
	}
}

func (r *PreflightReport) addCheck(name string, ok bool, detail string) {
	r.Checks = append(r.Checks, PreflightCheck{
		Name:   name,
		OK:     ok,
		Detail: detail,
	})
	if !ok {
		r.Success = false
	}
}
