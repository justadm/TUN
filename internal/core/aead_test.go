package core

import (
	"encoding/hex"
	"testing"

	"tun/internal/testvectors"
)

func TestAEADVectors(t *testing.T) {
	f, err := testvectors.LoadFromRepo()
	if err != nil {
		t.Fatalf("load vectors: %v", err)
	}

	for _, v := range f.Vectors {
		if v.KeyHex == "" || v.NonceHex == "" || v.PlaintextHex == "" && v.CiphertextHex == "" {
			continue
		}
		alg := mapAlgorithm(v.Algorithm)
		if alg == "" {
			continue
		}

		key := mustHex(t, v.KeyHex)
		nonce := mustHex(t, v.NonceHex)
		aad := mustHex(t, v.AADHex)
		pt := mustHex(t, v.PlaintextHex)
		ct := mustHex(t, v.CiphertextHex)
		tag := mustHex(t, v.TagHex)

		ciphertext, err := Encrypt(alg, key, nonce, aad, pt)
		if err != nil {
			t.Fatalf("%s encrypt: %v", v.ID, err)
		}
		if len(tag) > 0 {
			// AEAD Seal returns ciphertext || tag
			ctExpected := append([]byte{}, ct...)
			ctExpected = append(ctExpected, tag...)
			if !bytesEqual(ciphertext, ctExpected) {
				t.Fatalf("%s encrypt mismatch", v.ID)
			}
		}

		plaintext, err := Decrypt(alg, key, nonce, aad, ciphertext)
		if err != nil {
			t.Fatalf("%s decrypt: %v", v.ID, err)
		}
		if !bytesEqual(plaintext, pt) {
			t.Fatalf("%s decrypt mismatch", v.ID)
		}
	}
}

func mapAlgorithm(s string) string {
	switch s {
	case "CHACHA20-POLY1305":
		return AEADChaCha20Poly1305
	case "AES-256-GCM":
		return AEADAES256GCM
	default:
		return ""
	}
}

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	if s == "" {
		return nil
	}
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("hex decode: %v", err)
	}
	return b
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
