package core

import (
	"crypto/ecdh"
	"testing"
)

func TestDeriveKeysClientServerMatch(t *testing.T) {
	curve := ecdh.X25519()

	serverStaticPriv, _ := curve.GenerateKey(nil)
	serverStaticPub := serverStaticPriv.PublicKey().Bytes()

	clientEphPriv, _ := curve.GenerateKey(nil)
	clientEphPub := clientEphPriv.PublicKey().Bytes()

	serverEphPriv, _ := curve.GenerateKey(nil)
	serverEphPub := serverEphPriv.PublicKey().Bytes()

	clientHello := []byte("client-hello")
	serverHello := []byte("server-hello")

	kc2s, ks2c, err := DeriveKeysClient(serverStaticPub, clientEphPriv, serverEphPub, clientHello, serverHello)
	if err != nil {
		t.Fatalf("client derive: %v", err)
	}
	kc2s2, ks2c2, err := DeriveKeysServer(serverStaticPriv, serverEphPriv, clientEphPub, clientHello, serverHello)
	if err != nil {
		t.Fatalf("server derive: %v", err)
	}
	if !bytesEqual(kc2s, kc2s2) || !bytesEqual(ks2c, ks2c2) {
		t.Fatalf("keys mismatch")
	}
}
