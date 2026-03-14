package tlsstream

import (
	"crypto/tls"
)

// ServerConfig loads server TLS config from cert/key paths.
func ServerConfig(certFile, keyFile string) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, err
	}
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS13,
		NextProtos:   []string{"h2"},
	}, nil
}

// ClientConfig returns a client TLS config.
// serverName is required for SNI and verification.
func ClientConfig(serverName string, insecure bool) *tls.Config {
	return &tls.Config{
		ServerName:         serverName,
		InsecureSkipVerify: insecure,
		MinVersion:         tls.VersionTLS13,
		NextProtos:         []string{"h2"},
	}
}
