package core

import (
	"crypto/aes"
	"crypto/cipher"
	"errors"

	"golang.org/x/crypto/chacha20poly1305"
)

const (
	AEADChaCha20Poly1305 = "chacha20-poly1305"
	AEADAES256GCM        = "aes-256-gcm"
)

var (
	ErrUnsupportedAEAD = errors.New("unsupported AEAD")
)

// Encrypt encrypts plaintext using AEAD with given key/nonce/aad.
func Encrypt(alg string, key, nonce, aad, plaintext []byte) ([]byte, error) {
	aead, err := newAEAD(alg, key)
	if err != nil {
		return nil, err
	}
	return aead.Seal(nil, nonce, plaintext, aad), nil
}

// Decrypt decrypts ciphertext using AEAD with given key/nonce/aad.
func Decrypt(alg string, key, nonce, aad, ciphertext []byte) ([]byte, error) {
	aead, err := newAEAD(alg, key)
	if err != nil {
		return nil, err
	}
	return aead.Open(nil, nonce, ciphertext, aad)
}

func newAEAD(alg string, key []byte) (cipher.AEAD, error) {
	switch alg {
	case AEADChaCha20Poly1305:
		return chacha20poly1305.New(key)
	case AEADAES256GCM:
		block, err := aes.NewCipher(key)
		if err != nil {
			return nil, err
		}
		return cipher.NewGCM(block)
	default:
		return nil, ErrUnsupportedAEAD
	}
}
