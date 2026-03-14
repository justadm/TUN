package core

// NonceFromSeq derives a 12-byte nonce from direction and seq.
// direction: 0x00 for c2s, 0x01 for s2c.
func NonceFromSeq(direction byte, seq uint64) []byte {
	nonce := make([]byte, 12)
	nonce[3] = direction
	// seq in big-endian at the end
	nonce[4] = byte(seq >> 56)
	nonce[5] = byte(seq >> 48)
	nonce[6] = byte(seq >> 40)
	nonce[7] = byte(seq >> 32)
	nonce[8] = byte(seq >> 24)
	nonce[9] = byte(seq >> 16)
	nonce[10] = byte(seq >> 8)
	nonce[11] = byte(seq)
	return nonce
}
