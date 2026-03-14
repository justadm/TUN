package main

import (
	"context"
	"encoding/base64"
	"flag"
	"log"
	"strings"
	"sync"
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
	benchConns := flag.Int("bench-conns", 1, "number of parallel connections in benchmark")
	plain := flag.Bool("plain", false, "disable AEAD and send plaintext (testing only)")
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
	if *bench {
		conns := *benchConns
		if conns < 1 {
			conns = 1
		}
		total := *benchBytes
		if total < conns {
			total = conns
		}
		perConn := total / conns
		rem := total % conns

		type result struct {
			sent int
			dur  time.Duration
			err  error
		}
		results := make(chan result, conns)
		var wg sync.WaitGroup
		startAll := time.Now()
		for i := 0; i < conns; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				bytesToSend := perConn
				if idx == 0 {
					bytesToSend += rem
				}
				sent, dur, err := runBenchConn(dialer, *addr, cid, serverPub, bytesToSend, *benchFrame, *plain)
				results <- result{sent: sent, dur: dur, err: err}
			}(i)
		}
		wg.Wait()
		close(results)

		totalSent := 0
		var maxDur time.Duration
		for r := range results {
			if r.err != nil {
				log.Fatalf("bench error: %v", r.err)
			}
			totalSent += r.sent
			if r.dur > maxDur {
				maxDur = r.dur
			}
		}
		elapsed := time.Since(startAll)
		mbps := float64(totalSent) / elapsed.Seconds() / (1024 * 1024)
		log.Printf("bench sent bytes=%d duration=%s throughput=%.2f MiB/s", totalSent, elapsed, mbps)
	} else {
		conn, err := dialer.Dial(context.Background(), *addr)
		if err != nil {
			log.Fatalf("dial: %v", err)
		}
		defer conn.Close()
		log.Printf("connected to %s", *addr)

		sess, err := core.ClientHandshakeWithOptions(conn, cid, serverPub, *plain)
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
		_, pt, err := sess.DecryptFrameWithType(0x01, respWire)
		if err != nil {
			log.Fatalf("decrypt: %v", err)
		}
		log.Printf("recv: %s", string(pt))
	}
}

func runBenchConn(dialer *tlsstream.Dialer, addr string, cid [16]byte, serverPub []byte, total, frameSize int, plain bool) (int, time.Duration, error) {
	if frameSize <= 0 {
		frameSize = 16 << 10
	}
	conn, err := dialer.Dial(context.Background(), addr)
	if err != nil {
		return 0, 0, err
	}
	defer conn.Close()

	sess, err := core.ClientHandshakeWithOptions(conn, cid, serverPub, plain)
	if err != nil {
		return 0, 0, err
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
			return sent, time.Since(start), err
		}
		if err := core.WriteMsg(conn, wire); err != nil {
			return sent, time.Since(start), err
		}
		sent += len(payload)
	}
	doneWire, err := sess.EncryptFrame(0x00, core.MsgTypeControl, []byte("done"))
	if err != nil {
		return sent, time.Since(start), err
	}
	if err := core.WriteMsg(conn, doneWire); err != nil {
		return sent, time.Since(start), err
	}
	respWire, err := core.ReadMsg(conn)
	if err != nil {
		return sent, time.Since(start), err
	}
	_, pt, err := sess.DecryptFrameWithType(0x01, respWire)
	if err != nil {
		return sent, time.Since(start), err
	}
	if string(pt) != "ok" {
		return sent, time.Since(start), core.ErrBadHello
	}
	return sent, time.Since(start), nil
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
