package core

import (
	"encoding/binary"
)

const (
	RekeyVersionV1 = 1

	RekeyAckStatusAccepted   = 0
	RekeyAckStatusRejected   = 1
	RekeyAckStatusRetryLater = 2

	rekeyInitV1Size = 52
	rekeyAckV1Size  = 50
)

type RekeyInitV1 struct {
	Version       uint8
	Flags         uint8
	Reserved      uint16
	Epoch         uint64
	OverlapMillis uint32
	NotBeforeUnix uint64
	NewKeyID      [16]byte
	RekeyNonce    [12]byte
}

func (m *RekeyInitV1) MarshalBinary() ([]byte, error) {
	if m == nil {
		return nil, ErrInvalidControlMsg
	}
	if m.Version != RekeyVersionV1 {
		return nil, ErrInvalidControlMsg
	}
	if m.Reserved != 0 {
		return nil, ErrNonZeroReserved
	}
	if m.Epoch == 0 || allZero(m.NewKeyID[:]) || allZero(m.RekeyNonce[:]) {
		return nil, ErrInvalidControlMsg
	}
	out := make([]byte, rekeyInitV1Size)
	out[0] = m.Version
	out[1] = m.Flags
	binary.BigEndian.PutUint16(out[2:4], m.Reserved)
	binary.BigEndian.PutUint64(out[4:12], m.Epoch)
	binary.BigEndian.PutUint32(out[12:16], m.OverlapMillis)
	binary.BigEndian.PutUint64(out[16:24], m.NotBeforeUnix)
	copy(out[24:40], m.NewKeyID[:])
	copy(out[40:52], m.RekeyNonce[:])
	return out, nil
}

func (m *RekeyInitV1) UnmarshalBinary(b []byte) error {
	if len(b) != rekeyInitV1Size {
		return ErrInvalidControlMsg
	}
	m.Version = b[0]
	m.Flags = b[1]
	m.Reserved = binary.BigEndian.Uint16(b[2:4])
	m.Epoch = binary.BigEndian.Uint64(b[4:12])
	m.OverlapMillis = binary.BigEndian.Uint32(b[12:16])
	m.NotBeforeUnix = binary.BigEndian.Uint64(b[16:24])
	copy(m.NewKeyID[:], b[24:40])
	copy(m.RekeyNonce[:], b[40:52])
	if m.Version != RekeyVersionV1 {
		return ErrInvalidControlMsg
	}
	if m.Reserved != 0 {
		return ErrNonZeroReserved
	}
	if m.Epoch == 0 || allZero(m.NewKeyID[:]) || allZero(m.RekeyNonce[:]) {
		return ErrInvalidControlMsg
	}
	return nil
}

type RekeyAckV1 struct {
	Version        uint8
	Status         uint8
	Reserved       uint16
	Epoch          uint64
	AcceptedAtUnix uint64
	ActiveKeyID    [16]byte
	Proof          [16]byte
}

func (m *RekeyAckV1) MarshalBinary() ([]byte, error) {
	if m == nil {
		return nil, ErrInvalidControlMsg
	}
	if m.Version != RekeyVersionV1 || !isValidRekeyAckStatus(m.Status) || m.Reserved != 0 || m.Epoch == 0 {
		return nil, ErrInvalidControlMsg
	}
	if m.Status == RekeyAckStatusAccepted && m.AcceptedAtUnix == 0 {
		return nil, ErrInvalidControlMsg
	}
	out := make([]byte, rekeyAckV1Size)
	out[0] = m.Version
	out[1] = m.Status
	binary.BigEndian.PutUint16(out[2:4], m.Reserved)
	binary.BigEndian.PutUint64(out[4:12], m.Epoch)
	binary.BigEndian.PutUint64(out[12:20], m.AcceptedAtUnix)
	copy(out[20:36], m.ActiveKeyID[:])
	copy(out[36:50], m.Proof[:])
	return out, nil
}

func (m *RekeyAckV1) UnmarshalBinary(b []byte) error {
	if len(b) != rekeyAckV1Size {
		return ErrInvalidControlMsg
	}
	m.Version = b[0]
	m.Status = b[1]
	m.Reserved = binary.BigEndian.Uint16(b[2:4])
	m.Epoch = binary.BigEndian.Uint64(b[4:12])
	m.AcceptedAtUnix = binary.BigEndian.Uint64(b[12:20])
	copy(m.ActiveKeyID[:], b[20:36])
	copy(m.Proof[:], b[36:50])
	if m.Version != RekeyVersionV1 || !isValidRekeyAckStatus(m.Status) || m.Reserved != 0 || m.Epoch == 0 {
		return ErrInvalidControlMsg
	}
	if m.Status == RekeyAckStatusAccepted && m.AcceptedAtUnix == 0 {
		return ErrInvalidControlMsg
	}
	return nil
}

func isValidRekeyAckStatus(v uint8) bool {
	switch v {
	case RekeyAckStatusAccepted, RekeyAckStatusRejected, RekeyAckStatusRetryLater:
		return true
	default:
		return false
	}
}

func allZero(b []byte) bool {
	for _, v := range b {
		if v != 0 {
			return false
		}
	}
	return true
}
