package main

import (
	"context"
	"crypto/ecdh"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"tun/internal/core"
	"tun/internal/engine"
	"tun/internal/runtime"
	"tun/internal/transport"
	"tun/internal/transport/tlsstream"
	"tun/internal/tun"
)

func main() {
	addr := flag.String("addr", ":8443", "listen address")
	cert := flag.String("cert", "", "path to TLS cert")
	key := flag.String("key", "", "path to TLS key")
	serverID := flag.String("server-id", "", "16-byte hex server id")
	serverStaticPrivB64 := flag.String("server-static-priv", "", "base64 x25519 private key")
	tunName := flag.String("tun-name", "", "desired TUN interface name (linux only)")
	tunMTU := flag.Int("tun-mtu", 0, "optional TUN MTU (0 keeps system default)")
	tunSkipUp := flag.Bool("tun-skip-up", false, "do not set TUN interface UP on open")
	tunAddresses := flag.String("tun-addresses", "", "optional comma-separated interface CIDRs (linux only)")
	tunRoutes := flag.String("tun-routes", "", "optional comma-separated route CIDRs or 'default' (linux only)")
	tunConfigMode := flag.String("tun-config-mode", "replace", "TUN address/route apply mode: replace|add")
	tunCleanupOnClose := flag.Bool("tun-cleanup-on-close", false, "remove configured addresses/routes on device close")
	healthAddr := flag.String("health-addr", "127.0.0.1:18081", "health listen address")
	eventJSONLog := flag.String("event-json-log", "", "optional path to write JSON event logs")
	eventLogRotateBytes := flag.Int64("event-log-rotate-bytes", 0, "rotate event log when size exceeds bytes (0 disables)")
	eventLogRotateInterval := flag.Duration("event-log-rotate-interval", 0, "rotate event log at interval (0 disables)")
	eventLogMaxBackups := flag.Int("event-log-max-backups", 5, "max rotated event log backups to keep")
	supportBundleOut := flag.String("support-bundle-out", "", "optional path to write support bundle JSON on exit")
	supportRing := flag.String("support-ring", "", "optional deployment ring in support bundle")
	supportHostID := flag.String("support-host-id", "", "optional host id in support bundle")
	runtimeVersion := flag.String("runtime-version", "", "optional runtime version for support bundle metadata")
	buildInfo := flag.String("build-info", "", "optional build info for support bundle metadata")
	signingKeyFile := flag.String("support-signing-key-file", "", "optional path to HMAC signing key for support bundle envelope")
	signingKeyID := flag.String("support-signing-key-id", "", "optional signing key id stored in support bundle envelope")
	plain := flag.Bool("plain", false, "disable AEAD and send plaintext (testing only)")
	maxRetries := flag.Int("max-retries", 0, "max retries after runtime failures (0 = unlimited)")
	notify := flag.Bool("notify", true, "send systemd notifications via NOTIFY_SOCKET")
	flag.Parse()

	if *cert == "" || *key == "" {
		log.Fatal("cert and key are required")
	}
	if *serverID == "" || *serverStaticPrivB64 == "" {
		log.Fatal("server-id and server-static-priv are required")
	}
	tunOpts := tun.OpenOptions{
		Name:           *tunName,
		MTU:            *tunMTU,
		SkipUp:         *tunSkipUp,
		Addresses:      parseCSVList(*tunAddresses),
		Routes:         parseCSVList(*tunRoutes),
		ConfigMode:     *tunConfigMode,
		CleanupOnClose: *tunCleanupOnClose,
	}
	if err := tun.Preflight(tunOpts); err != nil {
		log.Fatalf("tun preflight failed: %v", err)
	}
	sid, err := parseHex16(*serverID)
	if err != nil {
		log.Fatalf("server-id: %v", err)
	}
	privBytes, err := base64.StdEncoding.DecodeString(strings.TrimSpace(*serverStaticPrivB64))
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

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()

	tracker := runtime.NewServiceStatusTracker(runtime.RoleServer)
	collector := runtime.NewSupportBundleCollector(5000)
	var eventLogger *runtime.JSONEventLogger
	var rotatingWriter *runtime.RotatingFileWriter
	if *eventJSONLog != "" {
		w, err := runtime.NewRotatingFileWriter(*eventJSONLog, runtime.RotationOptions{
			MaxBytes:       *eventLogRotateBytes,
			RotateInterval: *eventLogRotateInterval,
			MaxBackups:     *eventLogMaxBackups,
		})
		if err != nil {
			log.Fatalf("open event json log: %v", err)
		}
		rotatingWriter = w
		defer rotatingWriter.Close()
		eventLogger = runtime.NewJSONEventLogger(rotatingWriter)
	}
	if *healthAddr != "" {
		startHealthServer(ctx, *healthAddr, tracker.Handler())
	}

	if *notify {
		_ = runtime.SdNotify("STATUS=starting runtime-server-systemd")
		_ = runtime.SdNotify(fmt.Sprintf("MAINPID=%d", syscall.Getpid()))
		defer func() {
			_ = runtime.SdNotify("STOPPING=1")
		}()
	}

	retryPolicy := runtime.NewTransportRetryPolicy()
	// Keep server accept/rehandshake loop responsive under intermittent peer reconnects.
	retryPolicy.MaxConsecutiveFailures = 0
	var readySent atomic.Bool
	log.Printf("runtime-systemd server listening on %s tun=%q", *addr, *tunName)
	err = runtime.RunServer(
		ctx,
		func(_ context.Context) (tun.Device, error) {
			dev, err := tun.Open(tunOpts)
			if err != nil {
				return nil, err
			}
			mtu, mtuErr := dev.MTU()
			if mtuErr != nil {
				log.Printf("tun opened name=%s mtu=unknown err=%v", dev.Name(), mtuErr)
			} else {
				log.Printf("tun opened name=%s mtu=%d", dev.Name(), mtu)
			}
			return dev, nil
		},
		ln,
		func(stream transport.Stream) (*core.Session, error) {
			return core.ServerHandshakeWithOptions(stream, sid, serverStaticPriv, *plain)
		},
		runtime.ClientOptions{
			MaxRetries:  *maxRetries,
			RetryPolicy: retryPolicy,
			OnStateChange: func(state runtime.State, cause error) {
				if cause != nil {
					log.Printf("state=%s cause=%v", state, cause)
				} else {
					log.Printf("state=%s", state)
				}
			},
			OnEvent: func(e runtime.Event) {
				tracker.OnEvent(e)
				collector.OnEvent(e)
				if eventLogger != nil {
					if err := eventLogger.OnEvent(e); err != nil {
						log.Printf("event logger error: %v", err)
					}
				}
				log.Printf("event state=%s class=%s attempts=%d reconnects=%d last_retry_reason=%s last_retry_delay=%s",
					e.State, e.ErrorClass, e.Snapshot.Attempts, e.Snapshot.Reconnects, e.Snapshot.LastRetryReason, e.Snapshot.LastRetryDelay)
				if *notify {
					_ = runtime.SdNotify(fmt.Sprintf("STATUS=state=%s class=%s reconnects=%d", e.State, e.ErrorClass, e.Snapshot.Reconnects))
					if !readySent.Load() && (e.State == runtime.StateListening || e.State == runtime.StateEstablished) {
						_ = runtime.SdNotify("READY=1")
						readySent.Store(true)
					}
				}
			},
			EngineOptions: engine.Options{
				OutDirection: 0x01,
				InDirection:  0x00,
			},
		},
	)
	if err != nil && err != context.Canceled && !errors.Is(err, net.ErrClosed) {
		log.Fatalf("runtime-systemd server exited with error: %v", err)
	}
	if *supportBundleOut != "" {
		hostID := *supportHostID
		if hostID == "" {
			if h, err := os.Hostname(); err == nil {
				hostID = h
			}
		}
		var signingKey []byte
		if *signingKeyFile != "" {
			rawKey, keyErr := os.ReadFile(*signingKeyFile)
			if keyErr != nil {
				log.Printf("support bundle signing key read failed: %v", keyErr)
			} else {
				signingKey = []byte(strings.TrimSpace(string(rawKey)))
			}
		}
		raw, expErr := collector.ExportEnvelopeJSONWithConfig(runtime.SupportBundleConfig{
			Role:           runtime.RoleServer,
			RuntimeVersion: *runtimeVersion,
			BuildInfo:      *buildInfo,
			Ring:           *supportRing,
			HostID:         hostID,
		}, runtime.SigningOptions{
			Key:   signingKey,
			KeyID: *signingKeyID,
		})
		if expErr != nil {
			log.Printf("support bundle export failed: %v", expErr)
		} else if wrErr := os.WriteFile(*supportBundleOut, raw, 0o600); wrErr != nil {
			log.Printf("support bundle write failed: %v", wrErr)
		} else {
			log.Printf("support bundle written to %s", *supportBundleOut)
		}
	}
	log.Printf("runtime-systemd server stopped")
}

func startHealthServer(ctx context.Context, addr string, handler http.Handler) {
	srv := &http.Server{
		Addr:    addr,
		Handler: handler,
	}
	go func() {
		log.Printf("health server listening on %s", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("health server error: %v", err)
		}
	}()
	go func() {
		<-ctx.Done()
		stopCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(stopCtx)
	}()
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

func parseCSVList(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out = append(out, p)
	}
	return out
}
