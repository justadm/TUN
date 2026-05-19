package core

import "encoding/binary"

const (
	ControlTypeKeepalive = 1
	ControlTypeRekeyInit = 2
	ControlTypeRekeyAck  = 3
	ControlTypeError     = 4
)

// ControlMessage is the plaintext payload for MsgTypeControl frames.
type ControlMessage struct {
	ControlType uint8
	Reserved    uint8
	Reserved2   uint16
	Body        []byte
}

func (m *ControlMessage) MarshalBinary() ([]byte, error) {
	if !isValidControlType(m.ControlType) {
		return nil, ErrInvalidControlMsg
	}
	if m.Reserved != 0 || m.Reserved2 != 0 {
		return nil, ErrNonZeroReserved
	}
	buf := make([]byte, 4+len(m.Body))
	buf[0] = m.ControlType
	buf[1] = m.Reserved
	binary.BigEndian.PutUint16(buf[2:4], m.Reserved2)
	copy(buf[4:], m.Body)
	return buf, nil
}

func (m *ControlMessage) UnmarshalBinary(b []byte) error {
	if len(b) < 4 {
		return ErrInvalidControlMsg
	}
	m.ControlType = b[0]
	if !isValidControlType(m.ControlType) {
		return ErrInvalidControlMsg
	}
	m.Reserved = b[1]
	m.Reserved2 = binary.BigEndian.Uint16(b[2:4])
	if m.Reserved != 0 || m.Reserved2 != 0 {
		return ErrNonZeroReserved
	}
	m.Body = append([]byte{}, b[4:]...)
	return nil
}

func isValidControlType(v uint8) bool {
	switch v {
	case ControlTypeKeepalive, ControlTypeRekeyInit, ControlTypeRekeyAck, ControlTypeError:
		return true
	default:
		return false
	}
}
