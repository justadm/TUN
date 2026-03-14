package core

import "testing"

func TestSessionEncryptDecrypt(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	s := NewSession(AEADChaCha20Poly1305, key, key)
	payload := []byte("hello")

	frameBytes, err := s.EncryptFrame(0x00, MsgTypeData, payload)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	pt, err := s.DecryptFrame(0x00, frameBytes)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if !bytesEqual(pt, payload) {
		t.Fatalf("payload mismatch")
	}
}

func TestSessionReplay(t *testing.T) {
	key := make([]byte, 32)
	s := NewSession(AEADChaCha20Poly1305, key, key)
	payload := []byte("hello")
	frameBytes, err := s.EncryptFrame(0x00, MsgTypeData, payload)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if _, err := s.DecryptFrame(0x00, frameBytes); err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if _, err := s.DecryptFrame(0x00, frameBytes); err == nil {
		t.Fatalf("expected replay error")
	}
}
