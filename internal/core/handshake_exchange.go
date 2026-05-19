package core

import (
	"crypto/ecdh"
	"crypto/rand"
	"errors"
	"io"
	"time"
)

var (
	ErrHandshake = errors.New("handshake failed")
)

type deadlineSetter interface {
	SetDeadline(time.Time) error
}

var (
	HandshakeDeadline          = 8 * time.Second
	serverClientHelloReplaySet = newClientHelloReplayCache(defaultHandshakeReplayTTL, defaultHandshakeReplayCapacity)
)

type ClientHandshakeOptions struct {
	Plain            bool
	ExpectedServerID *[16]byte
}

func applyHandshakeDeadline(rw io.ReadWriter) func() {
	if HandshakeDeadline <= 0 {
		return func() {}
	}
	ds, ok := rw.(deadlineSetter)
	if !ok {
		return func() {}
	}
	_ = ds.SetDeadline(time.Now().Add(HandshakeDeadline))
	return func() {
		_ = ds.SetDeadline(time.Time{})
	}
}

// ClientHandshake performs a basic client-side handshake over a stream.
func ClientHandshake(rw io.ReadWriter, clientID [16]byte, serverStaticPub []byte) (*Session, error) {
	return ClientHandshakeWithOptions(rw, clientID, serverStaticPub, false)
}

func ClientHandshakeWithOptions(rw io.ReadWriter, clientID [16]byte, serverStaticPub []byte, plain bool) (*Session, error) {
	return ClientHandshakeWithConfig(rw, clientID, serverStaticPub, ClientHandshakeOptions{Plain: plain})
}

func ClientHandshakeWithConfig(rw io.ReadWriter, clientID [16]byte, serverStaticPub []byte, opts ClientHandshakeOptions) (*Session, error) {
	clearDeadline := applyHandshakeDeadline(rw)
	defer clearDeadline()

	curve := ecdh.X25519()
	clientEphPriv, err := curve.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	var ephPub [32]byte
	copy(ephPub[:], clientEphPriv.PublicKey().Bytes())

	chBody, err := BuildClientHello(clientID, 1, KDFHKDFSHA256, ephPub)
	if err != nil {
		return nil, err
	}
	chBodyBytes, err := chBody.MarshalBinary()
	if err != nil {
		return nil, err
	}
	chMsg := HandshakeMessage{
		Type:    HSTypeClientHello,
		Version: VersionV1,
		Flags:   0,
		Body:    chBodyBytes,
	}
	chWire, err := chMsg.MarshalBinary()
	if err != nil {
		return nil, err
	}
	if err := WriteMsg(rw, chWire); err != nil {
		return nil, err
	}

	resp, err := ReadMsg(rw)
	if err != nil {
		return nil, err
	}
	var shMsg HandshakeMessage
	if err := shMsg.UnmarshalBinary(resp); err != nil {
		return nil, err
	}
	if shMsg.Type != HSTypeServerHello || shMsg.Version != VersionV1 {
		return nil, ErrBadHello
	}
	var shBody ServerHelloBody
	if err := shBody.UnmarshalBinary(shMsg.Body); err != nil {
		return nil, err
	}
	if !ValidateServerHello(&shBody, chBody.AEADPref, chBody.KDFPref) {
		return nil, ErrBadHello
	}
	if opts.ExpectedServerID != nil && shBody.ServerID != *opts.ExpectedServerID {
		return nil, ErrServerIDMismatch
	}

	kc2s, ks2c, err := DeriveKeysClient(serverStaticPub, clientEphPriv, shBody.ServerEphemeral[:], chWire, resp)
	if err != nil {
		return nil, err
	}
	sess := NewSession(AEADChaCha20Poly1305, kc2s, ks2c)
	sess.Plain = opts.Plain
	return sess, nil
}

// ServerHandshake performs a basic server-side handshake over a stream.
func ServerHandshake(rw io.ReadWriter, serverID [16]byte, serverStaticPriv *ecdh.PrivateKey) (*Session, error) {
	return ServerHandshakeWithOptions(rw, serverID, serverStaticPriv, false)
}

func ServerHandshakeWithOptions(rw io.ReadWriter, serverID [16]byte, serverStaticPriv *ecdh.PrivateKey, plain bool) (*Session, error) {
	clearDeadline := applyHandshakeDeadline(rw)
	defer clearDeadline()

	req, err := ReadMsg(rw)
	if err != nil {
		return nil, err
	}
	var chMsg HandshakeMessage
	if err := chMsg.UnmarshalBinary(req); err != nil {
		return nil, err
	}
	if chMsg.Type != HSTypeClientHello || chMsg.Version != VersionV1 {
		return nil, ErrBadHello
	}
	var chBody ClientHelloBody
	if err := chBody.UnmarshalBinary(chMsg.Body); err != nil {
		return nil, err
	}
	if !ValidateClientHello(&chBody) {
		return nil, ErrBadHello
	}
	if serverClientHelloReplaySet.seenOrAdd(chBody.ClientNonce, time.Now()) {
		return nil, ErrHandshakeReplay
	}
	if chBody.AEADPref != 1 || chBody.KDFPref != KDFHKDFSHA256 {
		return nil, ErrUnsupportedAlgo
	}

	curve := ecdh.X25519()
	serverEphPriv, err := curve.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	var ephPub [32]byte
	copy(ephPub[:], serverEphPriv.PublicKey().Bytes())
	shBody, err := BuildServerHello(serverID, chBody.AEADPref, chBody.KDFPref, ephPub)
	if err != nil {
		return nil, err
	}
	shBodyBytes, err := shBody.MarshalBinary()
	if err != nil {
		return nil, err
	}
	shMsg := HandshakeMessage{
		Type:    HSTypeServerHello,
		Version: VersionV1,
		Flags:   0,
		Body:    shBodyBytes,
	}
	shWire, err := shMsg.MarshalBinary()
	if err != nil {
		return nil, err
	}
	if err := WriteMsg(rw, shWire); err != nil {
		return nil, err
	}

	kc2s, ks2c, err := DeriveKeysServer(serverStaticPriv, serverEphPriv, chBody.ClientEphemeral[:], req, shWire)
	if err != nil {
		return nil, err
	}
	sess := NewSession(AEADChaCha20Poly1305, kc2s, ks2c)
	sess.Plain = plain
	return sess, nil
}
