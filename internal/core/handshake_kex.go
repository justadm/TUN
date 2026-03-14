package core

import (
	"crypto/ecdh"
	"crypto/hmac"
	"crypto/sha256"
	"errors"
)

var (
	ErrKEX = errors.New("key exchange failed")
)

// DeriveKeysClient derives traffic keys using a Noise NK-style pattern (client knows server static).
func DeriveKeysClient(serverStaticPub []byte, clientEphPriv *ecdh.PrivateKey, serverEphPub []byte, clientHello, serverHello []byte) (kC2S, kS2C []byte, err error) {
	curve := ecdh.X25519()
	spub, err := curve.NewPublicKey(serverStaticPub)
	if err != nil {
		return nil, nil, err
	}
	seph, err := curve.NewPublicKey(serverEphPub)
	if err != nil {
		return nil, nil, err
	}
	es, err := clientEphPriv.ECDH(spub)
	if err != nil {
		return nil, nil, err
	}
	ee, err := clientEphPriv.ECDH(seph)
	if err != nil {
		return nil, nil, err
	}
	return deriveKeys(es, ee, clientHello, serverHello)
}

// DeriveKeysServer derives traffic keys on server side for Noise NK-style pattern.
func DeriveKeysServer(serverStaticPriv *ecdh.PrivateKey, serverEphPriv *ecdh.PrivateKey, clientEphPub []byte, clientHello, serverHello []byte) (kC2S, kS2C []byte, err error) {
	curve := ecdh.X25519()
	cpub, err := curve.NewPublicKey(clientEphPub)
	if err != nil {
		return nil, nil, err
	}
	es, err := serverStaticPriv.ECDH(cpub)
	if err != nil {
		return nil, nil, err
	}
	ee, err := serverEphPriv.ECDH(cpub)
	if err != nil {
		return nil, nil, err
	}
	return deriveKeys(es, ee, clientHello, serverHello)
}

func deriveKeys(es, ee, clientHello, serverHello []byte) ([]byte, []byte, error) {
	// Transcript hash
	h := sha256.Sum256(append(append([]byte{}, clientHello...), serverHello...))
	// HKDF extract/expand
	prk1 := hkdfExtract([]byte("tun-core-v0"), es)
	prk2 := hkdfExtract(prk1, ee)
	kC2S := hkdfExpand(prk2, append(h[:], []byte("c2s")...), 32)
	kS2C := hkdfExpand(prk2, append(h[:], []byte("s2c")...), 32)
	return kC2S, kS2C, nil
}

func hkdfExtract(salt, ikm []byte) []byte {
	mac := hmac.New(sha256.New, salt)
	mac.Write(ikm)
	return mac.Sum(nil)
}

func hkdfExpand(prk, info []byte, length int) []byte {
	var okm []byte
	var t []byte
	counter := byte(1)
	for len(okm) < length {
		mac := hmac.New(sha256.New, prk)
		mac.Write(t)
		mac.Write(info)
		mac.Write([]byte{counter})
		t = mac.Sum(nil)
		okm = append(okm, t...)
		counter++
	}
	return okm[:length]
}
