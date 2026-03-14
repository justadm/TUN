package main

import (
	"context"
	"flag"
	"log"

	"tun/internal/transport/tlsstream"
)

func main() {
	addr := flag.String("addr", ":8443", "listen address")
	cert := flag.String("cert", "", "path to TLS cert")
	key := flag.String("key", "", "path to TLS key")
	flag.Parse()

	if *cert == "" || *key == "" {
		log.Fatal("cert and key are required")
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
		_ = conn.Close()
	}
}
