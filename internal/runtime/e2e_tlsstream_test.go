package runtime

import (
	"context"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"os"
	"testing"
	"time"

	"tun/internal/core"
	"tun/internal/engine"
	"tun/internal/transport"
	"tun/internal/transport/tlsstream"
	"tun/internal/tun"
)

func TestClientServerTLSStreamDataPath(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	certPath, keyPath := makeTempCertPair(t)
	cfg, err := tlsstream.ServerConfig(certPath, keyPath)
	if err != nil {
		t.Fatalf("server tls config: %v", err)
	}
	ln, err := tlsstream.Listen("127.0.0.1:0", cfg)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	curve := ecdh.X25519()
	serverStaticPriv, err := curve.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate static server key: %v", err)
	}
	serverStaticPub := serverStaticPriv.PublicKey().Bytes()
	var sid [16]byte
	copy(sid[:], []byte("srv-tls-00000000"))
	var cid [16]byte
	copy(cid[:], []byte("cli-tls-00000000"))

	serverTun := newLoopTun("stls0", 1400)
	clientTun := newLoopTun("ctls0", 1400)

	serverErrCh := make(chan error, 1)
	go func() {
		serverErrCh <- RunServer(
			ctx,
			func(_ context.Context) (tun.Device, error) { return serverTun, nil },
			ln,
			func(stream transport.Stream) (*core.Session, error) {
				return core.ServerHandshakeWithOptions(stream, sid, serverStaticPriv, false)
			},
			ClientOptions{
				Sleep: func(_ context.Context, _ time.Duration) error { return nil },
			},
		)
	}()

	dialer := &tlsstream.Dialer{
		TLSConfig: tlsstream.ClientConfig("localhost", true),
		Timeout:   5 * time.Second,
	}
	addr := ln.Addr().String()

	clientErrCh := make(chan error, 1)
	go func() {
		clientErrCh <- RunClient(
			ctx,
			func(_ context.Context) (tun.Device, error) { return clientTun, nil },
			func(c context.Context) (transport.Stream, error) {
				return dialer.Dial(c, addr)
			},
			func(stream transport.Stream) (*core.Session, error) {
				return core.ClientHandshakeWithOptions(stream, cid, serverStaticPub, false)
			},
			ClientOptions{
				Sleep: func(_ context.Context, _ time.Duration) error { return nil },
			},
		)
	}()

	up := []byte{0x45, 0x00, 0x00, 0x18, 0x51}
	clientTun.injectRead(up)
	if got, ok := serverTun.waitWrite(3 * time.Second); !ok || !bytesEqual(got, up) {
		t.Fatalf("tls client->server packet mismatch")
	}

	down := []byte{0x60, 0x00, 0x00, 0x00, 0x61}
	serverTun.injectRead(down)
	if got, ok := clientTun.waitWrite(3 * time.Second); !ok || !bytesEqual(got, down) {
		t.Fatalf("tls server->client packet mismatch")
	}

	cancel()
	clientErr := <-clientErrCh
	serverErr := <-serverErrCh
	if !errors.Is(clientErr, context.Canceled) && !errors.Is(clientErr, engine.ErrTransportClosed) {
		t.Fatalf("unexpected client error: %v", clientErr)
	}
	if !errors.Is(serverErr, context.Canceled) && !errors.Is(serverErr, engine.ErrTransportClosed) {
		t.Fatalf("unexpected server error: %v", serverErr)
	}
}

func makeTempCertPair(t *testing.T) (certFile, keyFile string) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate ecdsa key: %v", err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 62))
	if err != nil {
		t.Fatalf("serial: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName: "localhost",
		},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}

	certOut, err := os.CreateTemp(t.TempDir(), "tls-cert-*.pem")
	if err != nil {
		t.Fatalf("create cert file: %v", err)
	}
	defer certOut.Close()
	keyOut, err := os.CreateTemp(t.TempDir(), "tls-key-*.pem")
	if err != nil {
		t.Fatalf("create key file: %v", err)
	}
	defer keyOut.Close()

	if err := pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: der}); err != nil {
		t.Fatalf("write cert: %v", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	if err := pem.Encode(keyOut, &pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}); err != nil {
		t.Fatalf("write key: %v", err)
	}

	if _, err := tls.LoadX509KeyPair(certOut.Name(), keyOut.Name()); err != nil {
		t.Fatalf("validate keypair: %v", err)
	}
	return certOut.Name(), keyOut.Name()
}
