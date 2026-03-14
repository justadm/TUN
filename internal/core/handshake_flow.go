package core

import (
	"crypto/rand"
	"encoding/binary"
)

// BuildClientHello creates a basic ClientHello body.
func BuildClientHello(clientID [16]byte, aeadPref, kdfPref uint8, ephPub [32]byte) (ClientHelloBody, error) {
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return ClientHelloBody{}, err
	}
	return ClientHelloBody{
		ClientID:        clientID,
		ClientNonce:     nonce,
		ClientEphemeral: ephPub,
		AEADPref:        aeadPref,
		KDFPref:         kdfPref,
		Reserved:        0,
	}, nil
}

// BuildServerHello creates a basic ServerHello body.
func BuildServerHello(serverID [16]byte, aeadSel, kdfSel uint8, ephPub [32]byte) (ServerHelloBody, error) {
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return ServerHelloBody{}, err
	}
	return ServerHelloBody{
		ServerID:        serverID,
		ServerNonce:     nonce,
		ServerEphemeral: ephPub,
		AEADSelected:    aeadSel,
		KDFSelected:     kdfSel,
		Reserved:        0,
	}, nil
}

// ValidateClientHello performs basic validation.
func ValidateClientHello(c *ClientHelloBody) bool {
	if c.Reserved != 0 {
		return false
	}
	return true
}

// ValidateServerHello performs basic validation.
func ValidateServerHello(s *ServerHelloBody, aeadPref, kdfPref uint8) bool {
	if s.Reserved != 0 {
		return false
	}
	if s.AEADSelected != aeadPref || s.KDFSelected != kdfPref {
		return false
	}
	return true
}

const (
	KDFHKDFSHA256 = 1
)

// Helpers for ID parsing.
func ParseID(b []byte) [16]byte {
	var id [16]byte
	copy(id[:], b)
	return id
}

func Uint16ToBytes(v uint16) []byte {
	b := make([]byte, 2)
	binary.BigEndian.PutUint16(b, v)
	return b
}
