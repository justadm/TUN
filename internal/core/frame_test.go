package core

import "testing"

func TestFrameRoundTrip(t *testing.T) {
	orig := &Frame{
		Version:  VersionV1,
		MsgType:  MsgTypeData,
		Flags:    0,
		Reserved: 0,
		Seq:      1,
		Payload:  []byte{0x01, 0x02, 0x03},
	}
	b, err := orig.MarshalBinary()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var parsed Frame
	if err := parsed.UnmarshalBinary(b); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if parsed.Version != orig.Version || parsed.MsgType != orig.MsgType || parsed.Seq != orig.Seq {
		t.Fatalf("header mismatch")
	}
	if !bytesEqual(parsed.Payload, orig.Payload) {
		t.Fatalf("payload mismatch")
	}
}

func TestFrameLengthMismatch(t *testing.T) {
	b := make([]byte, FrameHeaderSize)
	b[0] = VersionV1
	b[1] = MsgTypeData
	// payload_len set to 1, but no payload
	b[16] = 0
	b[17] = 0
	b[18] = 0
	b[19] = 1
	var f Frame
	if err := f.UnmarshalBinary(b); err != ErrLengthMismatch {
		t.Fatalf("expected ErrLengthMismatch, got %v", err)
	}
}

func TestFrameRejectsInvalidMsgType(t *testing.T) {
	b := make([]byte, FrameHeaderSize)
	b[0] = VersionV1
	b[1] = 0xFF
	var f Frame
	if err := f.UnmarshalBinary(b); err != ErrInvalidMsgType {
		t.Fatalf("expected ErrInvalidMsgType, got %v", err)
	}
}

func TestFrameRejectsNonZeroReserved(t *testing.T) {
	b := make([]byte, FrameHeaderSize)
	b[0] = VersionV1
	b[1] = MsgTypeData
	b[7] = 1
	var f Frame
	if err := f.UnmarshalBinary(b); err != ErrNonZeroReserved {
		t.Fatalf("expected ErrNonZeroReserved, got %v", err)
	}
}
