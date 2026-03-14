package core

import "testing"

func TestNonceFromSeq(t *testing.T) {
	b := NonceFromSeq(0x01, 0x0102030405060708)
	if len(b) != 12 {
		t.Fatalf("nonce size")
	}
	if b[3] != 0x01 {
		t.Fatalf("direction mismatch")
	}
	if b[4] != 0x01 || b[5] != 0x02 || b[6] != 0x03 || b[7] != 0x04 || b[8] != 0x05 || b[9] != 0x06 || b[10] != 0x07 || b[11] != 0x08 {
		t.Fatalf("seq encoding mismatch")
	}
}
