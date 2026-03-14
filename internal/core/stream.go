package core

import (
	"encoding/binary"
	"errors"
	"io"
)

var (
	ErrMsgTooLarge = errors.New("message too large")
	ErrMsgShort    = errors.New("short read")
)

const (
	MaxMsgSize = 1 << 20
)

// WriteMsg writes a length-prefixed message.
func WriteMsg(w io.Writer, b []byte) error {
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(b)))
	if _, err := w.Write(lenBuf[:]); err != nil {
		return err
	}
	_, err := w.Write(b)
	return err
}

// ReadMsg reads a length-prefixed message.
func ReadMsg(r io.Reader) ([]byte, error) {
	var lenBuf [4]byte
	if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(lenBuf[:])
	if n > MaxMsgSize {
		return nil, ErrMsgTooLarge
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	return buf, nil
}
