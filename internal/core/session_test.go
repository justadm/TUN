package core

import (
	"errors"
	"testing"
	"time"
)

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

func TestSessionDecryptsWithPreparedNextKeyDuringOverlap(t *testing.T) {
	oldC2S := fillBytes(0x11, 32)
	oldS2C := fillBytes(0x22, 32)
	s := NewSession(AEADChaCha20Poly1305, oldC2S, oldS2C)

	var keyID [16]byte
	var nonce [12]byte
	keyID[0] = 1
	nonce[0] = 2
	init := RekeyInitV1{
		Version:       RekeyVersionV1,
		Epoch:         7,
		OverlapMillis: 30000,
		NewKeyID:      keyID,
		RekeyNonce:    nonce,
	}
	now := time.Now().UTC()
	if err := s.InstallRekeyV1(init, now); err != nil {
		t.Fatalf("install rekey: %v", err)
	}

	nextC2S, _ := deriveRekeyKeysV1(oldC2S, oldS2C, init)
	seq := uint64(1)
	ct, err := Encrypt(AEADChaCha20Poly1305, nextC2S, NonceFromSeq(0x00, seq), nil, []byte("next-key-payload"))
	if err != nil {
		t.Fatalf("encrypt with next key: %v", err)
	}
	wire, err := (&Frame{
		Version:  VersionV1,
		MsgType:  MsgTypeData,
		Flags:    0,
		Reserved: 0,
		Seq:      seq,
		Payload:  ct,
	}).MarshalBinary()
	if err != nil {
		t.Fatalf("marshal frame: %v", err)
	}

	pt, err := s.DecryptFrame(0x00, wire)
	if err != nil {
		t.Fatalf("decrypt with overlap fallback: %v", err)
	}
	if !bytesEqual(pt, []byte("next-key-payload")) {
		t.Fatalf("payload mismatch: %q", string(pt))
	}
}

func TestSessionCutoverRekeyPromotesPreparedKeys(t *testing.T) {
	oldC2S := fillBytes(0x31, 32)
	oldS2C := fillBytes(0x41, 32)
	s := NewSession(AEADChaCha20Poly1305, oldC2S, oldS2C)

	var keyID [16]byte
	var nonce [12]byte
	keyID[0] = 9
	nonce[0] = 7
	init := RekeyInitV1{
		Version:       RekeyVersionV1,
		Epoch:         11,
		OverlapMillis: 5000,
		NewKeyID:      keyID,
		RekeyNonce:    nonce,
	}
	now := time.Now().UTC()
	if err := s.InstallRekeyV1(init, now); err != nil {
		t.Fatalf("install rekey: %v", err)
	}
	ack := RekeyAckV1{
		Version:        RekeyVersionV1,
		Status:         RekeyAckStatusAccepted,
		Epoch:          init.Epoch,
		AcceptedAtUnix: uint64(now.Unix()),
		ActiveKeyID:    keyID,
	}
	if err := s.CutoverRekeyV1(ack, now); err != nil {
		t.Fatalf("cutover rekey: %v", err)
	}

	nextC2S, _ := deriveRekeyKeysV1(oldC2S, oldS2C, init)
	frameBytes, err := (&Session{
		KeyC2S:  nextC2S,
		KeyS2C:  fillBytes(0x99, 32),
		AEADAlg: AEADChaCha20Poly1305,
	}).EncryptFrame(0x00, MsgTypeData, []byte("after-cutover"))
	if err != nil {
		t.Fatalf("encrypt post-cutover fixture: %v", err)
	}
	pt, err := s.DecryptFrame(0x00, frameBytes)
	if err != nil {
		t.Fatalf("decrypt after cutover: %v", err)
	}
	if !bytesEqual(pt, []byte("after-cutover")) {
		t.Fatalf("payload mismatch: %q", string(pt))
	}
}

func TestSessionCutoverRejectsMismatchedAck(t *testing.T) {
	s := NewSession(AEADChaCha20Poly1305, fillBytes(0x51, 32), fillBytes(0x61, 32))

	var keyID [16]byte
	var nonce [12]byte
	keyID[0] = 1
	nonce[0] = 1
	if err := s.InstallRekeyV1(RekeyInitV1{
		Version:       RekeyVersionV1,
		Epoch:         3,
		OverlapMillis: 1000,
		NewKeyID:      keyID,
		RekeyNonce:    nonce,
	}, time.Now().UTC()); err != nil {
		t.Fatalf("install rekey: %v", err)
	}

	var wrongKeyID [16]byte
	wrongKeyID[0] = 2
	err := s.CutoverRekeyV1(RekeyAckV1{
		Version:        RekeyVersionV1,
		Status:         RekeyAckStatusAccepted,
		Epoch:          3,
		AcceptedAtUnix: uint64(time.Now().UTC().Unix()),
		ActiveKeyID:    wrongKeyID,
	}, time.Now().UTC())
	if err == nil {
		t.Fatalf("expected cutover mismatch error")
	}
	if !errors.Is(err, ErrRekeyAckMismatch) {
		t.Fatalf("expected ErrRekeyAckMismatch, got %v", err)
	}
}

func TestSessionInstallRejectsStaleEpochAfterCutover(t *testing.T) {
	s := NewSession(AEADChaCha20Poly1305, fillBytes(0x71, 32), fillBytes(0x81, 32))
	now := time.Now().UTC()

	var keyID [16]byte
	var nonce [12]byte
	keyID[0] = 5
	nonce[0] = 6
	init := RekeyInitV1{
		Version:       RekeyVersionV1,
		Epoch:         9,
		OverlapMillis: 2000,
		NewKeyID:      keyID,
		RekeyNonce:    nonce,
	}
	if err := s.InstallRekeyV1(init, now); err != nil {
		t.Fatalf("install rekey: %v", err)
	}
	if err := s.CutoverRekeyV1(RekeyAckV1{
		Version:        RekeyVersionV1,
		Status:         RekeyAckStatusAccepted,
		Epoch:          9,
		AcceptedAtUnix: uint64(now.Unix()),
		ActiveKeyID:    keyID,
	}, now); err != nil {
		t.Fatalf("cutover: %v", err)
	}

	err := s.InstallRekeyV1(RekeyInitV1{
		Version:       RekeyVersionV1,
		Epoch:         9,
		OverlapMillis: 2000,
		NewKeyID:      keyID,
		RekeyNonce:    nonce,
	}, now.Add(time.Second))
	if !errors.Is(err, ErrRekeyStaleEpoch) {
		t.Fatalf("expected ErrRekeyStaleEpoch, got %v", err)
	}
}

func fillBytes(v byte, n int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = v
	}
	return out
}
