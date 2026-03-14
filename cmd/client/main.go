package main

import (
	"context"
	"flag"
	"log"
	"time"

	"tun/internal/transport/tlsstream"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8443", "server address")
	serverName := flag.String("server-name", "localhost", "TLS server name")
	insecure := flag.Bool("insecure", true, "skip TLS verification (dev only)")
	flag.Parse()

	cfg := tlsstream.ClientConfig(*serverName, *insecure)
	dialer := &tlsstream.Dialer{TLSConfig: cfg, Timeout: 5 * time.Second}
	conn, err := dialer.Dial(context.Background(), *addr)
	if err != nil {
		log.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	log.Printf("connected to %s", *addr)
}
