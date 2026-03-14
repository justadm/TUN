package core

import (
	"encoding/binary"
	"errors"
)

var (
	ErrInvalidHandshake = errors.New("invalid handshake")
)

const (
	HSTypeClientHello = 1
	HSTypeServerHello = 2
)

type HandshakeMessage struct {
	Type    uint8
	Version uint8
	Flags   uint16
	Body    []byte
}

func (m *HandshakeMessage) MarshalBinary() ([]byte, error) {
	buf := make([]byte, 4+len(m.Body))
	buf[0] = m.Type
	buf[1] = m.Version
	binary.BigEndian.PutUint16(buf[2:4], m.Flags)
	copy(buf[4:], m.Body)
	return buf, nil
}

func (m *HandshakeMessage) UnmarshalBinary(b []byte) error {
	if len(b) < 4 {
		return ErrInvalidHandshake
	}
	m.Type = b[0]
	m.Version = b[1]
	m.Flags = binary.BigEndian.Uint16(b[2:4])
	m.Body = append([]byte{}, b[4:]...)
	return nil
}

// ClientHelloBody is the v0 ClientHello body (no client_static_pub in v0).
type ClientHelloBody struct {
	ClientID        [16]byte
	ClientNonce     [16]byte
	ClientEphemeral [32]byte
	AEADPref        uint8
	KDFPref         uint8
	Reserved        uint16
}

func (c *ClientHelloBody) MarshalBinary() ([]byte, error) {
	buf := make([]byte, 16+16+32+1+1+2)
	copy(buf[0:16], c.ClientID[:])
	copy(buf[16:32], c.ClientNonce[:])
	copy(buf[32:64], c.ClientEphemeral[:])
	buf[64] = c.AEADPref
	buf[65] = c.KDFPref
	binary.BigEndian.PutUint16(buf[66:68], c.Reserved)
	return buf, nil
}

func (c *ClientHelloBody) UnmarshalBinary(b []byte) error {
	if len(b) != 68 {
		return ErrInvalidHandshake
	}
	copy(c.ClientID[:], b[0:16])
	copy(c.ClientNonce[:], b[16:32])
	copy(c.ClientEphemeral[:], b[32:64])
	c.AEADPref = b[64]
	c.KDFPref = b[65]
	c.Reserved = binary.BigEndian.Uint16(b[66:68])
	return nil
}

// ServerHelloBody is the v0 ServerHello body.
type ServerHelloBody struct {
	ServerID        [16]byte
	ServerNonce     [16]byte
	ServerEphemeral [32]byte
	AEADSelected    uint8
	KDFSelected     uint8
	Reserved        uint16
}

func (s *ServerHelloBody) MarshalBinary() ([]byte, error) {
	buf := make([]byte, 16+16+32+1+1+2)
	copy(buf[0:16], s.ServerID[:])
	copy(buf[16:32], s.ServerNonce[:])
	copy(buf[32:64], s.ServerEphemeral[:])
	buf[64] = s.AEADSelected
	buf[65] = s.KDFSelected
	binary.BigEndian.PutUint16(buf[66:68], s.Reserved)
	return buf, nil
}

func (s *ServerHelloBody) UnmarshalBinary(b []byte) error {
	if len(b) != 68 {
		return ErrInvalidHandshake
	}
	copy(s.ServerID[:], b[0:16])
	copy(s.ServerNonce[:], b[16:32])
	copy(s.ServerEphemeral[:], b[32:64])
	s.AEADSelected = b[64]
	s.KDFSelected = b[65]
	s.Reserved = binary.BigEndian.Uint16(b[66:68])
	return nil
}
