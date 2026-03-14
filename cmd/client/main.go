package main

import (
	"context"
	"encoding/base64"
	"flag"
	"log"
	"time"

	"tun/internal/core"
	"tun/internal/transport/tlsstream"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8443", "server address")
	serverName := flag.String("server-name", "localhost", "TLS server name")
	insecure := flag.Bool("insecure", true, "skip TLS verification (dev only)")
	clientID := flag.String("client-id", "", "16-byte hex client id")
	serverStaticPubB64 := flag.String("server-static-pub", "", "base64 x25519 public key")
	flag.Parse()

	if *clientID == "" || *serverStaticPubB64 == "" {
		log.Fatal("client-id and server-static-pub are required")
	}
	cid, err := parseHex16(*clientID)
	if err != nil {
		log.Fatalf("client-id: %v", err)
	}
	serverPub, err := base64.StdEncoding.DecodeString(*serverStaticPubB64)
	if err != nil {
		log.Fatalf("server-static-pub: %v", err)
	}

	cfg := tlsstream.ClientConfig(*serverName, *insecure)
	dialer := &tlsstream.Dialer{TLSConfig: cfg, Timeout: 5 * time.Second}
	conn, err := dialer.Dial(context.Background(), *addr)
	if err != nil {
		log.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	log.Printf("connected to %s", *addr)

	sess, err := core.ClientHandshake(conn, cid, serverPub)
	if err != nil {
		log.Fatalf("handshake: %v", err)
	}
	wire, err := sess.EncryptFrame(0x00, core.MsgTypeData, []byte("ping"))
	if err != nil {
		log.Fatalf("encrypt: %v", err)
	}
	if err := core.WriteMsg(conn, wire); err != nil {
		log.Fatalf("write: %v", err)
	}
	respWire, err := core.ReadMsg(conn)
	if err != nil {
		log.Fatalf("read: %v", err)
	}
	pt, err := sess.DecryptFrame(0x01, respWire)
	if err != nil {
		log.Fatalf("decrypt: %v", err)
	}
	log.Printf("recv: %s", string(pt))
}

func parseHex16(s string) ([16]byte, error) {
	var out [16]byte
	if len(s) != 32 {
		return out, core.ErrInvalidHandshake
	}
	for i := 0; i < 16; i++ {
		var v byte
		for j := 0; j < 2; j++ {
			c := s[i*2+j]
			switch {
			case c >= '0' && c <= '9':
				v = v<<4 | byte(c-'0')
			case c >= 'a' && c <= 'f':
				v = v<<4 | byte(c-'a'+10)
			case c >= 'A' && c <= 'F':
				v = v<<4 | byte(c-'A'+10)
			default:
				return out, core.ErrInvalidHandshake
			}
		}
		out[i] = v
	}
	return out, nil
}
