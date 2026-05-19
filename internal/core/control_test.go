package core

import "testing"

func TestControlMessageRoundTrip(t *testing.T) {
	orig := &ControlMessage{
		ControlType: ControlTypeKeepalive,
		Reserved:    0,
		Reserved2:   0,
		Body:        []byte("ping"),
	}
	b, err := orig.MarshalBinary()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var parsed ControlMessage
	if err := parsed.UnmarshalBinary(b); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if parsed.ControlType != orig.ControlType || !bytesEqual(parsed.Body, orig.Body) {
		t.Fatalf("round-trip mismatch")
	}
}

func TestControlMessageRejectsInvalidType(t *testing.T) {
	orig := &ControlMessage{
		ControlType: 0xFF,
		Body:        []byte("x"),
	}
	if _, err := orig.MarshalBinary(); err != ErrInvalidControlMsg {
		t.Fatalf("expected ErrInvalidControlMsg, got %v", err)
	}
}

func TestControlMessageRejectsNonZeroReserved(t *testing.T) {
	raw := []byte{ControlTypeError, 0x01, 0x00, 0x00}
	var msg ControlMessage
	if err := msg.UnmarshalBinary(raw); err != ErrNonZeroReserved {
		t.Fatalf("expected ErrNonZeroReserved, got %v", err)
	}
}
