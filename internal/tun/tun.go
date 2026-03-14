package tun

import "io"

// Device abstracts a TUN interface.
type Device interface {
	io.ReadWriteCloser
	Name() string
	MTU() (int, error)
}
