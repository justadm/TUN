package main

import (
	"context"
	"encoding/base64"
	"flag"
	"log"
	"strings"
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
	bench := flag.Bool("bench", false, "benchmark mode")
	benchBytes := flag.Int("bench-bytes", 100<<20, "total bytes to send in benchmark")
	benchFrame := flag.Int("bench-frame", 16<<10, "frame payload size in benchmark")
	flag.Parse()

	if *clientID == "" || *serverStaticPubB64 == "" {
		log.Fatal("client-id and server-static-pub are required")
	}
	cid, err := parseHex16(*clientID)
	if err != nil {
		log.Fatalf("client-id: %v", err)
	}
	serverPub, err := base64.StdEncoding.DecodeString(strings.TrimSpace(*serverStaticPubB64))
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
	if *bench {
		total := *benchBytes
		frameSize := *benchFrame
		if frameSize <= 0 {
			frameSize = 16 << 10
		}
		payload := make([]byte, frameSize)
		start := time.Now()
		sent := 0
		for sent < total {
			if total-sent < frameSize {
				payload = payload[:total-sent]
			}
			wire, err := sess.EncryptFrame(0x00, core.MsgTypeData, payload)
			if err != nil {
				log.Fatalf("encrypt: %v", err)
			}
			if err := core.WriteMsg(conn, wire); err != nil {
				log.Fatalf("write: %v", err)
			}
			sent += len(payload)
		}
		doneWire, err := sess.EncryptFrame(0x00, core.MsgTypeControl, []byte("done"))
		if err != nil {
			log.Fatalf("encrypt: %v", err)
		}
		if err := core.WriteMsg(conn, doneWire); err != nil {
			log.Fatalf("write: %v", err)
		}
		respWire, err := core.ReadMsg(conn)
		if err != nil {
			log.Fatalf("read: %v", err)
		}
		_, pt, err := sess.DecryptFrameWithType(0x01, respWire)
		if err != nil {
			log.Fatalf("decrypt: %v", err)
		}
		elapsed := time.Since(start)
		mbps := float64(sent) / elapsed.Seconds() / (1024 * 1024)
		log.Printf("bench recv: %s", string(pt))
		log.Printf("bench sent bytes=%d duration=%s throughput=%.2f MiB/s", sent, elapsed, mbps)
	} else {
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
		_, pt, err := sess.DecryptFrameWithType(0x01, respWire)
		if err != nil {
			log.Fatalf("decrypt: %v", err)
		}
		log.Printf("recv: %s", string(pt))
	}
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
