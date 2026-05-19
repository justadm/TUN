//go:build !linux && !darwin

package tun

func Preflight(_ OpenOptions) error {
	return ErrUnsupportedPlatform
}

func BuildPreflightReport(_ OpenOptions) (PreflightReport, error) {
	r := newPreflightReport()
	r.addCheck("platform", false, ErrUnsupportedPlatform.Error())
	return r, ErrUnsupportedPlatform
}
