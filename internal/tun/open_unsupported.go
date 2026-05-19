//go:build !linux && !darwin

package tun

func Open(_ OpenOptions) (Device, error) {
	return nil, ErrUnsupportedPlatform
}
