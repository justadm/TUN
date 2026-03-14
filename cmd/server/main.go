package main

import (
	"context"
	"crypto/ecdh"
	"encoding/base64"
	"flag"
	"log"
	"time"

	"tun/internal/core"
	"tun/internal/transport/tlsstream"
)

func main() {
	addr := flag.String("addr", ":8443", "listen address")
	cert := flag.String("cert", "", "path to TLS cert")
	key := flag.String("key", "", "path to TLS key")
	serverID := flag.String("server-id", "", "16-byte hex server id")
	serverStaticPrivB64 := flag.String("server-static-priv", "", "base64 x25519 private key")
	bench := flag.Bool("bench", false, "enable benchmark mode")
	plain := flag.Bool("plain", false, "disable AEAD and send plaintext (testing only)")
	flag.Parse()

	if *cert == "" || *key == "" {
		log.Fatal("cert and key are required")
	}
	if *serverID == "" || *serverStaticPrivB64 == "" {
		log.Fatal("server-id and server-static-priv are required")
	}
	sid, err := parseHex16(*serverID)
	if err != nil {
		log.Fatalf("server-id: %v", err)
	}
	privBytes, err := base64.StdEncoding.DecodeString(*serverStaticPrivB64)
	if err != nil {
		log.Fatalf("server-static-priv: %v", err)
	}
	curve := ecdh.X25519()
	serverStaticPriv, err := curve.NewPrivateKey(privBytes)
	if err != nil {
		log.Fatalf("server-static-priv: %v", err)
	}
	cfg, err := tlsstream.ServerConfig(*cert, *key)
	if err != nil {
		log.Fatalf("tls config: %v", err)
	}
	ln, err := tlsstream.Listen(*addr, cfg)
	if err != nil {
		log.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	log.Printf("listening on %s", *addr)
	for {
		conn, err := ln.Accept(context.Background())
		if err != nil {
			log.Fatalf("accept: %v", err)
		}
		go func() {
			defer conn.Close()
			sess, err := core.ServerHandshakeWithOptions(conn, sid, serverStaticPriv, *plain)
			if err != nil {
				log.Printf("handshake failed: %v", err)
				return
			}
			if *bench {
				var total int
				start := time.Now()
				for {
					wire, err := core.ReadMsg(conn)
					if err != nil {
						log.Printf("read frame: %v", err)
						return
					}
					msgType, pt, err := sess.DecryptFrameWithType(0x00, wire)
					if err != nil {
						log.Printf("decrypt: %v", err)
						return
					}
					if msgType == core.MsgTypeControl && string(pt) == "done" {
						elapsed := time.Since(start)
						log.Printf("bench recv bytes=%d duration=%s", total, elapsed)
						respWire, err := sess.EncryptFrame(0x01, core.MsgTypeControl, []byte("ok"))
						if err != nil {
							log.Printf("encrypt: %v", err)
							return
						}
						_ = core.WriteMsg(conn, respWire)
						return
					}
					total += len(pt)
				}
			} else {
				// Read one encrypted frame and respond with a fixed reply.
				wire, err := core.ReadMsg(conn)
				if err != nil {
					log.Printf("read frame: %v", err)
					return
				}
				_, pt, err := sess.DecryptFrameWithType(0x00, wire)
				if err != nil {
					log.Printf("decrypt: %v", err)
					return
				}
				log.Printf("recv: %s", string(pt))
				respWire, err := sess.EncryptFrame(0x01, core.MsgTypeData, []byte("pong"))
				if err != nil {
					log.Printf("encrypt: %v", err)
					return
				}
				if err := core.WriteMsg(conn, respWire); err != nil {
					log.Printf("write: %v", err)
					return
				}
			}
		}()
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
