package core

import "testing"

func TestRekeyInitV1RoundTrip(t *testing.T) {
	var keyID [16]byte
	var nonce [12]byte
	for i := range keyID {
		keyID[i] = byte(i + 1)
	}
	for i := range nonce {
		nonce[i] = byte(i + 2)
	}
	orig := &RekeyInitV1{
		Version:       RekeyVersionV1,
		Flags:         0x01,
		Reserved:      0,
		Epoch:         7,
		OverlapMillis: 3000,
		NotBeforeUnix: 1713000000,
		NewKeyID:      keyID,
		RekeyNonce:    nonce,
	}
	b, err := orig.MarshalBinary()
	if err != nil {
		t.Fatalf("marshal init: %v", err)
	}
	var parsed RekeyInitV1
	if err := parsed.UnmarshalBinary(b); err != nil {
		t.Fatalf("unmarshal init: %v", err)
	}
	if parsed.Epoch != orig.Epoch || parsed.OverlapMillis != orig.OverlapMillis || parsed.NewKeyID != orig.NewKeyID || parsed.RekeyNonce != orig.RekeyNonce {
		t.Fatalf("init roundtrip mismatch: %+v vs %+v", parsed, orig)
	}
}

func TestRekeyInitV1RejectsInvalid(t *testing.T) {
	var msg RekeyInitV1
	if err := msg.UnmarshalBinary([]byte{1, 1, 0, 0}); err != ErrInvalidControlMsg {
		t.Fatalf("expected invalid msg on short payload, got %v", err)
	}
	msg = RekeyInitV1{
		Version: RekeyVersionV1,
		Epoch:   1,
	}
	if _, err := msg.MarshalBinary(); err != ErrInvalidControlMsg {
		t.Fatalf("expected invalid msg on zero key/nonce, got %v", err)
	}
	msg = RekeyInitV1{
		Version:    2,
		Epoch:      1,
		NewKeyID:   [16]byte{1},
		RekeyNonce: [12]byte{1},
	}
	if _, err := msg.MarshalBinary(); err != ErrInvalidControlMsg {
		t.Fatalf("expected invalid msg on wrong version, got %v", err)
	}
}

func TestRekeyAckV1RoundTrip(t *testing.T) {
	orig := &RekeyAckV1{
		Version:        RekeyVersionV1,
		Status:         RekeyAckStatusAccepted,
		Reserved:       0,
		Epoch:          9,
		AcceptedAtUnix: 1713000005,
		ActiveKeyID:    [16]byte{1, 2, 3},
		Proof:          [16]byte{9, 8, 7},
	}
	b, err := orig.MarshalBinary()
	if err != nil {
		t.Fatalf("marshal ack: %v", err)
	}
	var parsed RekeyAckV1
	if err := parsed.UnmarshalBinary(b); err != nil {
		t.Fatalf("unmarshal ack: %v", err)
	}
	if parsed.Status != orig.Status || parsed.Epoch != orig.Epoch || parsed.AcceptedAtUnix != orig.AcceptedAtUnix || parsed.ActiveKeyID != orig.ActiveKeyID {
		t.Fatalf("ack roundtrip mismatch: %+v vs %+v", parsed, orig)
	}
}

func TestRekeyAckV1RejectsInvalid(t *testing.T) {
	msg := RekeyAckV1{
		Version: RekeyVersionV1,
		Status:  RekeyAckStatusAccepted,
		Epoch:   10,
	}
	if _, err := msg.MarshalBinary(); err != ErrInvalidControlMsg {
		t.Fatalf("expected invalid msg on accepted without timestamp, got %v", err)
	}
	msg = RekeyAckV1{
		Version:        RekeyVersionV1,
		Status:         9,
		Epoch:          10,
		AcceptedAtUnix: 1,
	}
	if _, err := msg.MarshalBinary(); err != ErrInvalidControlMsg {
		t.Fatalf("expected invalid msg on bad status, got %v", err)
	}
}
