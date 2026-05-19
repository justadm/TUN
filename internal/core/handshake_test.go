package core

import "testing"

func TestHandshakeMessageRoundTrip(t *testing.T) {
	m := &HandshakeMessage{Type: HSTypeClientHello, Version: VersionV1, Flags: 0, Body: []byte{0x01, 0x02}}
	b, err := m.MarshalBinary()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var parsed HandshakeMessage
	if err := parsed.UnmarshalBinary(b); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if parsed.Type != m.Type || parsed.Version != m.Version || !bytesEqual(parsed.Body, m.Body) {
		t.Fatalf("round-trip mismatch")
	}
}

func TestHandshakeMessageRejectsInvalidType(t *testing.T) {
	m := &HandshakeMessage{Type: 0xFF, Version: VersionV1, Flags: 0, Body: []byte{0x01}}
	if _, err := m.MarshalBinary(); err == nil {
		t.Fatalf("expected marshal error for invalid type")
	}
}

func TestHandshakeMessageRejectsNonZeroFlags(t *testing.T) {
	raw := []byte{HSTypeClientHello, VersionV1, 0x00, 0x01}
	var m HandshakeMessage
	if err := m.UnmarshalBinary(raw); err != ErrNonZeroReserved {
		t.Fatalf("expected ErrNonZeroReserved, got %v", err)
	}
}

func TestClientHelloBodyRoundTrip(t *testing.T) {
	var c ClientHelloBody
	for i := 0; i < 16; i++ {
		c.ClientID[i] = byte(i)
		c.ClientNonce[i] = byte(0xA0 + i)
	}
	for i := 0; i < 32; i++ {
		c.ClientEphemeral[i] = byte(0x10 + i)
	}
	c.AEADPref = 1
	c.KDFPref = 1
	b, _ := c.MarshalBinary()
	var parsed ClientHelloBody
	if err := parsed.UnmarshalBinary(b); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if parsed.AEADPref != c.AEADPref || parsed.KDFPref != c.KDFPref {
		t.Fatalf("prefs mismatch")
	}
}

func TestServerHelloBodyRoundTrip(t *testing.T) {
	var s ServerHelloBody
	for i := 0; i < 16; i++ {
		s.ServerID[i] = byte(0x10 + i)
		s.ServerNonce[i] = byte(0xB0 + i)
	}
	for i := 0; i < 32; i++ {
		s.ServerEphemeral[i] = byte(0x20 + i)
	}
	s.AEADSelected = 1
	s.KDFSelected = 1
	b, _ := s.MarshalBinary()
	var parsed ServerHelloBody
	if err := parsed.UnmarshalBinary(b); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if parsed.AEADSelected != s.AEADSelected || parsed.KDFSelected != s.KDFSelected {
		t.Fatalf("prefs mismatch")
	}
}

func TestClientHelloBodyRejectsZeroIdentity(t *testing.T) {
	var c ClientHelloBody
	for i := 0; i < 32; i++ {
		c.ClientEphemeral[i] = byte(i + 1)
	}
	c.AEADPref = 1
	c.KDFPref = KDFHKDFSHA256
	b, _ := c.MarshalBinary()
	var parsed ClientHelloBody
	if err := parsed.UnmarshalBinary(b); err != ErrBadHello {
		t.Fatalf("expected ErrBadHello, got %v", err)
	}
}
