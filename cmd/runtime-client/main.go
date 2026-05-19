package main

import (
	"context"
	"encoding/base64"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"tun/internal/core"
	"tun/internal/runtime"
	"tun/internal/transport"
	"tun/internal/transport/tlsstream"
	"tun/internal/tun"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8443", "server address")
	serverName := flag.String("server-name", "localhost", "TLS server name")
	insecure := flag.Bool("insecure", true, "skip TLS verification (dev only)")
	dialTimeout := flag.Duration("dial-timeout", 15*time.Second, "transport dial+TLS timeout")
	clientID := flag.String("client-id", "", "16-byte hex client id")
	serverStaticPubB64 := flag.String("server-static-pub", "", "base64 x25519 public key")
	tunName := flag.String("tun-name", "", "desired TUN interface name (linux only)")
	tunMTU := flag.Int("tun-mtu", 0, "optional TUN MTU (0 keeps system default)")
	tunSkipUp := flag.Bool("tun-skip-up", false, "do not set TUN interface UP on open")
	tunAddresses := flag.String("tun-addresses", "", "optional comma-separated interface CIDRs (linux only)")
	tunRoutes := flag.String("tun-routes", "", "optional comma-separated route CIDRs or 'default' (linux only)")
	tunConfigMode := flag.String("tun-config-mode", "replace", "TUN address/route apply mode: replace|add")
	tunCleanupOnClose := flag.Bool("tun-cleanup-on-close", false, "remove configured addresses/routes on device close")
	healthAddr := flag.String("health-addr", "", "optional health listen address, for example 127.0.0.1:18080")
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
	maxRetries := flag.Int("max-retries", 0, "max reconnect attempts (0 = unlimited)")
	rekeyEnabled := flag.Bool("rekey-enabled", false, "enable in-session rekey control loop")
	rekeyAckRetries := flag.Int("rekey-ack-retries", 0, "rekey ack send retries")
	rekeyAckRetryDelay := flag.Duration("rekey-ack-retry-delay", 200*time.Millisecond, "rekey ack retry delay")
	rekeyInitInterval := flag.Duration("rekey-init-interval", 0, "rekey init interval; 0 disables autonomous initiator")
	rekeyInitAckTimeout := flag.Duration("rekey-init-ack-timeout", 3*time.Second, "rekey init ack timeout")
	rekeyInitRetries := flag.Int("rekey-init-retries", 0, "rekey init send retries")
	rekeyInitRetryDelay := flag.Duration("rekey-init-retry-delay", 200*time.Millisecond, "rekey init retry delay")
	rekeyInitOverlap := flag.Duration("rekey-init-overlap", 2500*time.Millisecond, "rekey init overlap window")
	flag.Parse()

	if *clientID == "" || *serverStaticPubB64 == "" {
		log.Fatal("client-id and server-static-pub are required")
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
	cid, err := parseHex16(*clientID)
	if err != nil {
		log.Fatalf("client-id: %v", err)
	}
	serverPub, err := base64.StdEncoding.DecodeString(strings.TrimSpace(*serverStaticPubB64))
	if err != nil {
		log.Fatalf("server-static-pub: %v", err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	cfg := tlsstream.ClientConfig(*serverName, *insecure)
	dialer := &tlsstream.Dialer{TLSConfig: cfg, Timeout: *dialTimeout}
	retryPolicy := runtime.NewTransportRetryPolicy()
	// Keep client recovery responsive under transient uplink stalls.
	// Long consecutive-failure cooldown is better handled by external rollout policy.
	retryPolicy.MaxConsecutiveFailures = 0
	tracker := runtime.NewServiceStatusTracker(runtime.RoleClient)
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

	log.Printf("runtime client starting, addr=%s tun=%q", *addr, *tunName)
	err = runtime.RunClient(
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
		func(ctx context.Context) (transport.Stream, error) {
			return dialer.Dial(ctx, *addr)
		},
		func(stream transport.Stream) (*core.Session, error) {
			return core.ClientHandshakeWithOptions(stream, cid, serverPub, *plain)
		},
		runtime.ClientOptions{
			MaxRetries:          *maxRetries,
			RetryPolicy:         retryPolicy,
			RekeyEnabled:        *rekeyEnabled || *rekeyInitInterval > 0,
			RekeyAckRetries:     *rekeyAckRetries,
			RekeyAckRetryDelay:  *rekeyAckRetryDelay,
			RekeyInitInterval:   *rekeyInitInterval,
			RekeyInitAckTimeout: *rekeyInitAckTimeout,
			RekeyInitRetries:    *rekeyInitRetries,
			RekeyInitRetryDelay: *rekeyInitRetryDelay,
			RekeyInitOverlap:    *rekeyInitOverlap,
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
			},
		},
	)
	if err != nil && err != context.Canceled {
		log.Fatalf("runtime client exited with error: %v", err)
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
			Role:           runtime.RoleClient,
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
	log.Printf("runtime client stopped")
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
