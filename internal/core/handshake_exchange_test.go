package core

import (
	"bytes"
	"crypto/ecdh"
	"net"
	"testing"
	"time"
)

type rwBuffer struct {
	r *bytes.Reader
	w bytes.Buffer
}

func (rw *rwBuffer) Read(p []byte) (int, error)  { return rw.r.Read(p) }
func (rw *rwBuffer) Write(p []byte) (int, error) { return rw.w.Write(p) }

func TestServerHandshakeRejectsDuplicateClientNonce(t *testing.T) {
	prevCache := serverClientHelloReplaySet
	serverClientHelloReplaySet = newClientHelloReplayCache(time.Minute, 1024)
	t.Cleanup(func() { serverClientHelloReplaySet = prevCache })

	prevDeadline := HandshakeDeadline
	HandshakeDeadline = 0
	t.Cleanup(func() { HandshakeDeadline = prevDeadline })

	curve := ecdh.X25519()
	serverStaticPriv, err := curve.GenerateKey(bytes.NewReader(bytes.Repeat([]byte{0x11}, 64)))
	if err != nil {
		t.Fatalf("server keygen: %v", err)
	}
	clientEphPriv, err := curve.GenerateKey(bytes.NewReader(bytes.Repeat([]byte{0x22}, 64)))
	if err != nil {
		t.Fatalf("client eph keygen: %v", err)
	}

	var clientID [16]byte
	var nonce [16]byte
	var ephPub [32]byte
	for i := 0; i < 16; i++ {
		clientID[i] = byte(i + 1)
		nonce[i] = byte(0xA0 + i)
	}
	copy(ephPub[:], clientEphPriv.PublicKey().Bytes())

	chBody := ClientHelloBody{
		ClientID:        clientID,
		ClientNonce:     nonce,
		ClientEphemeral: ephPub,
		AEADPref:        1,
		KDFPref:         KDFHKDFSHA256,
	}
	chBodyWire, err := chBody.MarshalBinary()
	if err != nil {
		t.Fatalf("marshal client hello body: %v", err)
	}
	chMsg := HandshakeMessage{
		Type:    HSTypeClientHello,
		Version: VersionV1,
		Flags:   0,
		Body:    chBodyWire,
	}
	chWire, err := chMsg.MarshalBinary()
	if err != nil {
		t.Fatalf("marshal client hello: %v", err)
	}
	reqFrame := make([]byte, 4+len(chWire))
	reqFrame[0] = 0
	reqFrame[1] = 0
	reqFrame[2] = byte(len(chWire) >> 8)
	reqFrame[3] = byte(len(chWire))
	copy(reqFrame[4:], chWire)

	var serverID [16]byte
	for i := 0; i < 16; i++ {
		serverID[i] = byte(0x10 + i)
	}

	rw1 := &rwBuffer{r: bytes.NewReader(reqFrame)}
	if _, err := ServerHandshakeWithOptions(rw1, serverID, serverStaticPriv, false); err != nil {
		t.Fatalf("first handshake unexpectedly failed: %v", err)
	}

	rw2 := &rwBuffer{r: bytes.NewReader(reqFrame)}
	if _, err := ServerHandshakeWithOptions(rw2, serverID, serverStaticPriv, false); err != ErrHandshakeReplay {
		t.Fatalf("expected ErrHandshakeReplay, got %v", err)
	}
}

func TestClientHandshakeRespectsHandshakeDeadline(t *testing.T) {
	prevDeadline := HandshakeDeadline
	HandshakeDeadline = 100 * time.Millisecond
	t.Cleanup(func() { HandshakeDeadline = prevDeadline })

	curve := ecdh.X25519()
	serverStaticPriv, err := curve.GenerateKey(bytes.NewReader(bytes.Repeat([]byte{0x33}, 64)))
	if err != nil {
		t.Fatalf("server static keygen: %v", err)
	}
	var clientID [16]byte
	for i := 0; i < 16; i++ {
		clientID[i] = byte(i + 1)
	}

	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()

	done := make(chan error, 1)
	go func() {
		_, err := ClientHandshakeWithOptions(c1, clientID, serverStaticPriv.PublicKey().Bytes(), false)
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatalf("expected handshake timeout error")
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("client handshake did not respect deadline")
	}
}

func TestClientHandshakeWithConfigRejectsServerIDMismatch(t *testing.T) {
	prevDeadline := HandshakeDeadline
	HandshakeDeadline = 2 * time.Second
	t.Cleanup(func() { HandshakeDeadline = prevDeadline })

	curve := ecdh.X25519()
	serverStaticPriv, err := curve.GenerateKey(bytes.NewReader(bytes.Repeat([]byte{0x44}, 64)))
	if err != nil {
		t.Fatalf("server static keygen: %v", err)
	}
	var actualServerID [16]byte
	var expectedServerID [16]byte
	var clientID [16]byte
	for i := 0; i < 16; i++ {
		actualServerID[i] = byte(0x20 + i)
		expectedServerID[i] = byte(0x40 + i)
		clientID[i] = byte(0x60 + i)
	}

	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()

	serverDone := make(chan error, 1)
	go func() {
		_, err := ServerHandshakeWithOptions(c2, actualServerID, serverStaticPriv, false)
		serverDone <- err
	}()

	_, err = ClientHandshakeWithConfig(c1, clientID, serverStaticPriv.PublicKey().Bytes(), ClientHandshakeOptions{
		ExpectedServerID: &expectedServerID,
	})
	if err != ErrServerIDMismatch {
		t.Fatalf("expected ErrServerIDMismatch, got %v", err)
	}

	_ = c2.Close()
	select {
	case <-serverDone:
	case <-time.After(2 * time.Second):
		t.Fatalf("server handshake goroutine did not finish")
	}
}

func TestClientHandshakeWithConfigAcceptsExpectedServerID(t *testing.T) {
	prevDeadline := HandshakeDeadline
	HandshakeDeadline = 2 * time.Second
	t.Cleanup(func() { HandshakeDeadline = prevDeadline })

	curve := ecdh.X25519()
	serverStaticPriv, err := curve.GenerateKey(bytes.NewReader(bytes.Repeat([]byte{0x55}, 64)))
	if err != nil {
		t.Fatalf("server static keygen: %v", err)
	}
	var serverID [16]byte
	var clientID [16]byte
	for i := 0; i < 16; i++ {
		serverID[i] = byte(0x70 + i)
		clientID[i] = byte(0x80 + i)
	}

	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()

	serverDone := make(chan error, 1)
	go func() {
		_, err := ServerHandshakeWithOptions(c2, serverID, serverStaticPriv, false)
		serverDone <- err
	}()

	if _, err := ClientHandshakeWithConfig(c1, clientID, serverStaticPriv.PublicKey().Bytes(), ClientHandshakeOptions{
		ExpectedServerID: &serverID,
	}); err != nil {
		t.Fatalf("expected handshake success, got %v", err)
	}

	select {
	case err := <-serverDone:
		if err != nil {
			t.Fatalf("server handshake failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("server handshake goroutine did not finish")
	}
}
