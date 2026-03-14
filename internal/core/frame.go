package core

import (
	"encoding/binary"
	"errors"
)

const (
	FrameHeaderSize = 20
	VersionV1       = 0x01

	MsgTypeData      = 0
	MsgTypeControl   = 1
	MsgTypeHandshake = 2
)

var (
	ErrShortFrame   = errors.New("frame too short")
	ErrLengthMismatch = errors.New("payload length mismatch")
	ErrUnsupportedVersion = errors.New("unsupported version")
)

// Frame represents a VPN frame on the wire.
type Frame struct {
	Version    uint8
	MsgType    uint8
	Flags      uint16
	Reserved   uint32
	Seq        uint64
	PayloadLen uint32
	Payload    []byte
}

// MarshalBinary serializes the frame into a byte slice.
func (f *Frame) MarshalBinary() ([]byte, error) {
	payloadLen := uint32(len(f.Payload))
	f.PayloadLen = payloadLen
	buf := make([]byte, FrameHeaderSize+payloadLen)
	buf[0] = f.Version
	buf[1] = f.MsgType
	binary.BigEndian.PutUint16(buf[2:4], f.Flags)
	binary.BigEndian.PutUint32(buf[4:8], f.Reserved)
	binary.BigEndian.PutUint64(buf[8:16], f.Seq)
	binary.BigEndian.PutUint32(buf[16:20], f.PayloadLen)
	copy(buf[20:], f.Payload)
	return buf, nil
}

// UnmarshalBinary parses a frame from the given bytes.
func (f *Frame) UnmarshalBinary(b []byte) error {
	if len(b) < FrameHeaderSize {
		return ErrShortFrame
	}
	f.Version = b[0]
	if f.Version != VersionV1 {
		return ErrUnsupportedVersion
	}
	f.MsgType = b[1]
	f.Flags = binary.BigEndian.Uint16(b[2:4])
	f.Reserved = binary.BigEndian.Uint32(b[4:8])
	f.Seq = binary.BigEndian.Uint64(b[8:16])
	f.PayloadLen = binary.BigEndian.Uint32(b[16:20])

	if int(f.PayloadLen) != len(b)-FrameHeaderSize {
		return ErrLengthMismatch
	}
	if f.PayloadLen > 0 {
		f.Payload = make([]byte, f.PayloadLen)
		copy(f.Payload, b[20:])
	} else {
		f.Payload = nil
	}
	return nil
}
