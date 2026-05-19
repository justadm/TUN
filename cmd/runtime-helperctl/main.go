package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

const expectedHelperAPIVersion = "v1"

type schemaResponse struct {
	APIVersion              string                  `json:"apiVersion"`
	GatewayPoolVersion      string                  `json:"gatewayPoolVersion,omitempty"`
	SecurityContractVersion string                  `json:"securityContractVersion,omitempty"`
	ProfileBundleVersion    string                  `json:"profileBundleVersion,omitempty"`
	AuthRequired            bool                    `json:"authRequired"`
	AuthSchemes             []string                `json:"authSchemes"`
	Endpoints               []string                `json:"endpoints"`
	Legacy                  []string                `json:"legacyAliases"`
	RequestID               string                  `json:"requestIdHeader"`
	LeaseHeader             string                  `json:"leaseIdHeader"`
	Bootstrap               schemaBootstrapContract `json:"bootstrap"`
}

type schemaBootstrapContract struct {
	SchemaVersion             string   `json:"schemaVersion"`
	RequiredBootstrapFields   []string `json:"requiredBootstrapFields"`
	OptionalBootstrapFields   []string `json:"optionalBootstrapFields"`
	GatewayPoolSupported      bool     `json:"gatewayPoolSupported"`
	GatewayPolicySupported    bool     `json:"gatewayPolicySupported"`
	RekeyPolicySupported      bool     `json:"rekeyPolicySupported"`
	ProfileBundleSupported    bool     `json:"profileBundleSupported"`
	SecurityProfileSupported  bool     `json:"securityProfileSupported"`
	SupportedSecurityProfiles []string `json:"supportedSecurityProfiles,omitempty"`
}

type leaseStatusResponse struct {
	Active    bool   `json:"active"`
	LeaseID   string `json:"leaseId"`
	Owner     string `json:"owner"`
	ExpiresAt string `json:"expiresAt"`
}

type helperStatusResponse struct {
	Running   bool                `json:"running"`
	LastState string              `json:"lastState"`
	Lease     leaseStatusResponse `json:"lease"`
}

type helperHealthResponse struct {
	Live    bool   `json:"live"`
	Ready   bool   `json:"ready"`
	Running bool   `json:"running"`
	State   string `json:"state"`
}

type bridgeStartupResult struct {
	OK      bool                `json:"ok"`
	Lease   leaseStatusResponse `json:"lease"`
	WaitRaw json.RawMessage     `json:"wait,omitempty"`
}

type bridgeShutdownResult struct {
	OK         bool                `json:"ok"`
	Lease      leaseStatusResponse `json:"lease"`
	Stopped    bool                `json:"stopped"`
	Released   bool                `json:"released"`
	StopError  string              `json:"stopError,omitempty"`
	LeaseError string              `json:"leaseError,omitempty"`
}

type bridgeReconcileResult struct {
	OK     bool                 `json:"ok"`
	Plan   string               `json:"plan"`
	Reason string               `json:"reason,omitempty"`
	Lease  leaseStatusResponse  `json:"lease"`
	Status helperStatusResponse `json:"status"`
	Health helperHealthResponse `json:"health"`
}

type bridgeAutopilotStep struct {
	Iteration int                   `json:"iteration"`
	Plan      string                `json:"plan"`
	Action    string                `json:"action"`
	Reconcile bridgeReconcileResult `json:"reconcile"`
}

type bridgeAutopilotResult struct {
	OK        bool                  `json:"ok"`
	FinalPlan string                `json:"finalPlan"`
	Steps     []bridgeAutopilotStep `json:"steps"`
}

type bridgeAutopilotDaemonTick struct {
	Run       int                   `json:"run"`
	At        time.Time             `json:"at"`
	OK        bool                  `json:"ok"`
	Error     string                `json:"error,omitempty"`
	Autopilot bridgeAutopilotResult `json:"autopilot"`
}

type linkFailoverResult struct {
	OK        bool            `json:"ok"`
	LinkID    string          `json:"linkID"`
	GatewayID string          `json:"gatewayID"`
	Select    json.RawMessage `json:"select"`
	Reconnect json.RawMessage `json:"reconnect"`
}

type bootstrapValidateResponse struct {
	OK bool `json:"ok"`
}

type bridgeBootstrapPayload struct {
	ProfileBootstrap bridgeBootstrapProfile `json:"profileBootstrap"`
}

type bridgeBootstrapProfile struct {
	ClientID        string          `json:"clientID"`
	ServerStaticPub string          `json:"serverStaticPub"`
	ProfileID       string          `json:"profileID,omitempty"`
	Region          string          `json:"region,omitempty"`
	SecurityProfile string          `json:"securityProfile,omitempty"`
	Gateways        json.RawMessage `json:"gateways,omitempty"`
	GatewayPolicy   json.RawMessage `json:"gatewayPolicy,omitempty"`
	RekeyPolicy     json.RawMessage `json:"rekeyPolicy,omitempty"`
	Routing         json.RawMessage `json:"routing,omitempty"`
	DNS             json.RawMessage `json:"dns,omitempty"`
	Bridge          json.RawMessage `json:"bridge,omitempty"`
}

type bridgeStatusStreamOptions struct {
	JSONL           bool
	Retry           bool
	RetryMax        int
	RetryBackoffMin time.Duration
	RetryBackoffMax time.Duration
}

func main() {
	action := flag.String("action", "schema", "helper action: schema|contract.check|health|lease.acquire|lease.ensure|lease.renew|lease.heartbeat|lease.takeover|lease.keepalive|lease.release|lease.status|bridge.startup|bridge.shutdown|bridge.reconcile|bridge.autopilot|bridge.autopilot.once|bridge.autopilot.daemon|bridge.autopilot.daemon.stream|bridge.status.stream|links.health.stream|status|bootstrap.validate|bootstrap.apply|profile.apply|profile.current|tunnel.start|tunnel.stop|tunnel.refresh|stats.read|links|link.read|link.reconnect|link.drain|link.resume|link.gateway.select|link.failover|security.evaluate|security.reputation.upsert|security.policy.upsert|security.policy.get|security.policy.rollout|security.corporate-allow.upsert|security.audit|security.signal.ingest|security.signal.ingest.recent|wait|events|diagnostics.export")
	endpoint := flag.String("endpoint", "http://127.0.0.1:19090", "helper HTTP endpoint")
	unixSocket := flag.String("unix-socket", "", "optional unix socket path for helper API")
	tokenFile := flag.String("token-file", "", "optional path to helper auth token")
	payloadFile := flag.String("payload-file", "", "optional JSON payload file for POST actions")
	requestID := flag.String("request-id", "", "optional X-Request-ID header (recommended for POST idempotency)")
	linkID := flag.String("link-id", "", "for -action link.read: link identifier from /v1/helper/links")
	gatewayID := flag.String("gateway-id", "", "for -action link.gateway.select: gateway identifier from profileBootstrap.gateways")
	leaseID := flag.String("lease-id", "", "optional X-Helper-Lease-ID header for mutating calls under active lease")
	leasePrevID := flag.String("lease-prev-id", "", "previous active lease id for -action lease.takeover")
	leaseOwner := flag.String("lease-owner", "helperctl", "lease owner for -action lease.acquire")
	leaseTTL := flag.Duration("lease-ttl", 60*time.Second, "lease ttl for lease.acquire/lease.renew/lease.heartbeat/lease.keepalive")
	leaseTakeover := flag.Bool("lease-takeover", true, "for -action lease.ensure: allow takeover when active lease belongs to another owner")
	leaseKeepaliveDuration := flag.Duration("lease-keepalive-duration", 0, "total duration for -action lease.keepalive; 0 sends one heartbeat")
	leaseKeepaliveInterval := flag.Duration("lease-keepalive-interval", 20*time.Second, "interval between heartbeat calls for -action lease.keepalive")
	leaseReleaseOnExit := flag.Bool("lease-release-on-exit", true, "for -action lease.keepalive: send lease.release on SIGINT/SIGTERM before exit")
	timeout := flag.Duration("timeout", 5*time.Second, "request timeout")
	waitState := flag.String("wait-state", "established", "target runtime state for -action wait")
	waitTimeout := flag.Duration("wait-timeout", 20*time.Second, "helper-side wait timeout for -action wait")
	eventsDuration := flag.Duration("events-duration", 20*time.Second, "stream duration for -action events")
	bridgeWait := flag.Bool("bridge-wait", true, "for -action bridge.startup: wait for target state after tunnel.start")
	bridgeShutdownBestEffort := flag.Bool("bridge-shutdown-best-effort", true, "for -action bridge.shutdown: return success even if tunnel.stop or lease.release fails")
	bridgeReconcileEnsureLease := flag.Bool("bridge-reconcile-ensure-lease", true, "for -action bridge.reconcile: ensure lease ownership before computing plan")
	bridgeAutopilotMaxSteps := flag.Int("bridge-autopilot-max-steps", 3, "for -action bridge.autopilot: max reconcile/action iterations")
	bridgeAutopilotAllowRestart := flag.Bool("bridge-autopilot-allow-restart", true, "for -action bridge.autopilot: allow restart-needed to run shutdown+startup")
	bridgeAutopilotInterval := flag.Duration("bridge-autopilot-interval", 20*time.Second, "for -action bridge.autopilot.daemon: interval between autopilot runs")
	bridgeAutopilotDuration := flag.Duration("bridge-autopilot-duration", 0, "for -action bridge.autopilot.daemon: total run duration, 0 means run until signal")
	bridgeAutopilotContinueOnError := flag.Bool("bridge-autopilot-continue-on-error", true, "for -action bridge.autopilot.daemon: keep looping after autopilot errors")
	bridgeStatusInterval := flag.Duration("bridge-status-interval", 5*time.Second, "for -action bridge.status.stream: snapshot interval")
	bridgeStatusDuration := flag.Duration("bridge-status-duration", 30*time.Second, "for -action bridge.status.stream: stream duration")
	bridgeStatusJSONL := flag.Bool("bridge-status-jsonl", true, "for -action bridge.status.stream: emit normalized JSONL envelopes")
	bridgeStatusRetry := flag.Bool("bridge-status-retry", true, "for -action bridge.status.stream: reconnect when stream ends without done event")
	bridgeStatusRetryMax := flag.Int("bridge-status-retry-max", 0, "for -action bridge.status.stream: max reconnect attempts (0 means unlimited)")
	bridgeStatusRetryBackoffMin := flag.Duration("bridge-status-retry-backoff-min", 500*time.Millisecond, "for -action bridge.status.stream: minimum reconnect backoff")
	bridgeStatusRetryBackoffMax := flag.Duration("bridge-status-retry-backoff-max", 10*time.Second, "for -action bridge.status.stream: maximum reconnect backoff")
	linksHealthInterval := flag.Duration("links-health-interval", 5*time.Second, "for -action links.health.stream: links snapshot interval")
	linksHealthDuration := flag.Duration("links-health-duration", 30*time.Second, "for -action links.health.stream: stream duration")
	linksHealthJSONL := flag.Bool("links-health-jsonl", true, "for -action links.health.stream: emit normalized JSONL envelopes")
	linksHealthRetry := flag.Bool("links-health-retry", true, "for -action links.health.stream: reconnect when stream ends without done event")
	linksHealthRetryMax := flag.Int("links-health-retry-max", 0, "for -action links.health.stream: max reconnect attempts (0 means unlimited)")
	linksHealthRetryBackoffMin := flag.Duration("links-health-retry-backoff-min", 500*time.Millisecond, "for -action links.health.stream: minimum reconnect backoff")
	linksHealthRetryBackoffMax := flag.Duration("links-health-retry-backoff-max", 10*time.Second, "for -action links.health.stream: maximum reconnect backoff")
	skipSchema := flag.Bool("skip-schema-check", false, "skip /v1/helper/schema validation before action")
	requireGatewayPool := flag.Bool("require-gateway-pool", false, "for -action contract.check: require schema bootstrap.gatewayPoolSupported=true")
	requireGatewayPolicy := flag.Bool("require-gateway-policy", false, "for -action contract.check: require schema bootstrap.gatewayPolicySupported=true")
	requireRekeyPolicy := flag.Bool("require-rekey-policy", false, "for -action contract.check: require schema bootstrap.rekeyPolicySupported=true")
	requireProfileBundle := flag.Bool("require-profile-bundle", false, "for -action contract.check: require schema bootstrap.profileBundleSupported=true")
	requireSecurityProfile := flag.Bool("require-security-profile", false, "for -action contract.check: require schema bootstrap.securityProfileSupported=true")
	requireGatewayPoolVersion := flag.String("require-gateway-pool-version", "", "for -action contract.check: require exact gatewayPoolVersion")
	requireBootstrapSchemaVersion := flag.String("require-bootstrap-schema-version", "", "for -action contract.check: require exact bootstrap.schemaVersion")
	requireProfileBundleVersion := flag.String("require-profile-bundle-version", "", "for -action contract.check: require exact profileBundleVersion")
	flag.Parse()

	token, err := readToken(*tokenFile)
	if err != nil {
		fatalf("read token: %v", err)
	}
	client, baseURL, err := buildClient(*endpoint, *unixSocket, *timeout)
	if err != nil {
		fatalf("build client: %v", err)
	}

	act := strings.TrimSpace(*action)
	if act == "" {
		fatalf("empty action")
	}
	body, err := loadPayload(*payloadFile)
	if err != nil {
		fatalf("payload: %v", err)
	}
	if !*skipSchema && act != "schema" && act != "health" && act != "contract.check" {
		if err := ensureSchema(client, baseURL, token); err != nil {
			fatalf("schema check failed: %v", err)
		}
	}
	if act == "contract.check" {
		schema, err := fetchSchema(client, baseURL, token)
		if err != nil {
			fatalf("contract check failed: %v", err)
		}
		if err := validateSchemaContractRequirements(
			schema,
			*requireGatewayPool,
			*requireGatewayPolicy,
			*requireRekeyPolicy,
			*requireProfileBundle,
			*requireSecurityProfile,
			strings.TrimSpace(*requireGatewayPoolVersion),
			strings.TrimSpace(*requireBootstrapSchemaVersion),
			strings.TrimSpace(*requireProfileBundleVersion),
		); err != nil {
			fatalf("contract check failed: %v", err)
		}
		out := map[string]any{
			"ok":                   true,
			"apiVersion":           schema.APIVersion,
			"gatewayPoolVersion":   schema.GatewayPoolVersion,
			"profileBundleVersion": schema.ProfileBundleVersion,
			"bootstrap":            schema.Bootstrap,
		}
		if len(body) > 0 {
			if err := validateBridgeBootstrapContract(schema, body); err != nil {
				fatalf("contract check failed: %v", err)
			}
			out["payloadValid"] = true
		}
		raw, err := json.Marshal(out)
		if err != nil {
			fatalf("contract check encode failed: %v", err)
		}
		fmt.Println(string(raw))
		return
	}
	if act == "lease.keepalive" {
		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer stop()
		if err := runLeaseKeepalive(ctx, client, baseURL, token, *leaseID, *requestID, *leaseTTL, *leaseKeepaliveInterval, *leaseKeepaliveDuration, *leaseReleaseOnExit); err != nil {
			fatalf("lease keepalive failed: %v", err)
		}
		return
	}
	if act == "lease.ensure" {
		resp, err := ensureLease(client, baseURL, token, *leaseOwner, *leaseTTL, *leaseTakeover, *requestID)
		if err != nil {
			fatalf("lease ensure failed: %v", err)
		}
		raw, err := json.Marshal(resp)
		if err != nil {
			fatalf("lease ensure encode failed: %v", err)
		}
		fmt.Println(string(raw))
		return
	}
	if act == "bridge.startup" {
		payload := map[string]any{
			"lease": map[string]any{
				"owner":    strings.TrimSpace(*leaseOwner),
				"ttl":      leaseTTL.Nanoseconds(),
				"takeover": *leaseTakeover,
			},
			"wait":        *bridgeWait,
			"waitState":   strings.TrimSpace(*waitState),
			"waitTimeout": waitTimeout.Nanoseconds(),
		}
		if len(body) != 0 {
			obj, err := decodeBodyObject(body)
			if err != nil {
				fatalf("bridge startup payload decode failed: %v", err)
			}
			if pb, ok := obj["profileBootstrap"]; ok {
				payload["profileBootstrap"] = pb
			} else if len(obj) != 0 {
				payload["profileBootstrap"] = obj
			}
			if dev, ok := obj["deviceID"]; ok {
				payload["deviceID"] = dev
			}
		}
		rawPayload, err := json.Marshal(payload)
		if err != nil {
			fatalf("bridge startup payload encode failed: %v", err)
		}
		respBody, status, err := doRequest(client, http.MethodPost, baseURL+"/v1/helper/bridge.startup", token, rawPayload, *requestID, "")
		if err != nil {
			fatalf("bridge startup failed: %v", err)
		}
		if status >= 400 {
			fatalf("bridge startup failed status=%d body=%s", status, strings.TrimSpace(string(respBody)))
		}
		fmt.Println(string(respBody))
		return
	}
	if act == "bridge.shutdown" {
		payload := map[string]any{
			"lease": map[string]any{
				"owner":    strings.TrimSpace(*leaseOwner),
				"ttl":      leaseTTL.Nanoseconds(),
				"takeover": *leaseTakeover,
			},
			"bestEffort": *bridgeShutdownBestEffort,
		}
		rawPayload, err := json.Marshal(payload)
		if err != nil {
			fatalf("bridge shutdown payload encode failed: %v", err)
		}
		respBody, status, err := doRequest(client, http.MethodPost, baseURL+"/v1/helper/bridge.shutdown", token, rawPayload, *requestID, "")
		if err != nil {
			fatalf("bridge shutdown failed: %v", err)
		}
		if status >= 400 {
			fatalf("bridge shutdown failed status=%d body=%s", status, strings.TrimSpace(string(respBody)))
		}
		fmt.Println(string(respBody))
		return
	}
	if act == "bridge.reconcile" {
		payload := map[string]any{
			"lease": map[string]any{
				"owner":    strings.TrimSpace(*leaseOwner),
				"ttl":      leaseTTL.Nanoseconds(),
				"takeover": *leaseTakeover,
				"ensure":   *bridgeReconcileEnsureLease,
			},
		}
		rawPayload, err := json.Marshal(payload)
		if err != nil {
			fatalf("bridge reconcile payload encode failed: %v", err)
		}
		respBody, status, err := doRequest(client, http.MethodPost, baseURL+"/v1/helper/bridge.reconcile", token, rawPayload, *requestID, "")
		if err != nil {
			fatalf("bridge reconcile failed: %v", err)
		}
		if status >= 400 {
			fatalf("bridge reconcile failed status=%d body=%s", status, strings.TrimSpace(string(respBody)))
		}
		fmt.Println(string(respBody))
		return
	}
	if act == "bridge.autopilot" || act == "bridge.autopilot.once" {
		startup := map[string]any{
			"lease": map[string]any{
				"owner":    strings.TrimSpace(*leaseOwner),
				"ttl":      leaseTTL.Nanoseconds(),
				"takeover": *leaseTakeover,
			},
			"wait":        *bridgeWait,
			"waitState":   strings.TrimSpace(*waitState),
			"waitTimeout": waitTimeout.Nanoseconds(),
		}
		if len(body) != 0 {
			obj, err := decodeBodyObject(body)
			if err != nil {
				fatalf("bridge autopilot payload decode failed: %v", err)
			}
			if pb, ok := obj["profileBootstrap"]; ok {
				startup["profileBootstrap"] = pb
			} else if len(obj) != 0 {
				startup["profileBootstrap"] = obj
			}
			if dev, ok := obj["deviceID"]; ok {
				startup["deviceID"] = dev
			}
		}
		payload := map[string]any{
			"maxSteps":     *bridgeAutopilotMaxSteps,
			"allowRestart": *bridgeAutopilotAllowRestart,
			"reconcile": map[string]any{
				"lease": map[string]any{
					"owner":    strings.TrimSpace(*leaseOwner),
					"ttl":      leaseTTL.Nanoseconds(),
					"takeover": *leaseTakeover,
					"ensure":   *bridgeReconcileEnsureLease,
				},
			},
			"startup": startup,
			"shutdown": map[string]any{
				"lease": map[string]any{
					"owner":    strings.TrimSpace(*leaseOwner),
					"ttl":      leaseTTL.Nanoseconds(),
					"takeover": *leaseTakeover,
				},
				"bestEffort": *bridgeShutdownBestEffort,
			},
		}
		rawPayload, err := json.Marshal(payload)
		if err != nil {
			fatalf("bridge autopilot payload encode failed: %v", err)
		}
		path := "/v1/helper/bridge.autopilot"
		if act == "bridge.autopilot.once" {
			path = "/v1/helper/bridge.autopilot.once"
		}
		respBody, status, err := doRequest(client, http.MethodPost, baseURL+path, token, rawPayload, *requestID, "")
		if err != nil {
			fatalf("bridge autopilot failed: %v", err)
		}
		if status >= 400 {
			fatalf("bridge autopilot failed status=%d body=%s", status, strings.TrimSpace(string(respBody)))
		}
		fmt.Println(string(respBody))
		return
	}
	if act == "bridge.autopilot.daemon" {
		rawPayload, err := buildBridgeAutopilotDaemonPayload(
			body,
			*leaseOwner, *leaseTTL, *leaseTakeover,
			*bridgeReconcileEnsureLease,
			*bridgeWait, *waitState, *waitTimeout,
			*bridgeShutdownBestEffort,
			*bridgeAutopilotAllowRestart, *bridgeAutopilotMaxSteps,
			*bridgeAutopilotInterval, *bridgeAutopilotDuration, *bridgeAutopilotContinueOnError,
		)
		if err != nil {
			fatalf("bridge autopilot daemon payload build failed: %v", err)
		}
		respBody, status, err := doRequest(client, http.MethodPost, baseURL+"/v1/helper/bridge.autopilot.daemon", token, rawPayload, *requestID, "")
		if err != nil {
			fatalf("bridge autopilot daemon failed: %v", err)
		}
		if status >= 400 {
			fatalf("bridge autopilot daemon failed status=%d body=%s", status, strings.TrimSpace(string(respBody)))
		}
		fmt.Println(string(respBody))
		return
	}
	if act == "bridge.autopilot.daemon.stream" {
		rawPayload, err := buildBridgeAutopilotDaemonPayload(
			body,
			*leaseOwner, *leaseTTL, *leaseTakeover,
			*bridgeReconcileEnsureLease,
			*bridgeWait, *waitState, *waitTimeout,
			*bridgeShutdownBestEffort,
			*bridgeAutopilotAllowRestart, *bridgeAutopilotMaxSteps,
			*bridgeAutopilotInterval, *bridgeAutopilotDuration, *bridgeAutopilotContinueOnError,
		)
		if err != nil {
			fatalf("bridge autopilot daemon stream payload build failed: %v", err)
		}
		if err := streamBridgeAutopilotDaemon(client, baseURL, token, rawPayload, *requestID); err != nil {
			fatalf("bridge autopilot daemon stream failed: %v", err)
		}
		return
	}
	if act == "bridge.status.stream" {
		if err := streamBridgeStatus(client, baseURL, token, *bridgeStatusInterval, *bridgeStatusDuration, *requestID, bridgeStatusStreamOptions{
			JSONL:           *bridgeStatusJSONL,
			Retry:           *bridgeStatusRetry,
			RetryMax:        *bridgeStatusRetryMax,
			RetryBackoffMin: *bridgeStatusRetryBackoffMin,
			RetryBackoffMax: *bridgeStatusRetryBackoffMax,
		}); err != nil {
			fatalf("bridge status stream failed: %v", err)
		}
		return
	}
	if act == "links.health.stream" {
		if err := streamLinksHealth(client, baseURL, token, *linksHealthInterval, *linksHealthDuration, *requestID, bridgeStatusStreamOptions{
			JSONL:           *linksHealthJSONL,
			Retry:           *linksHealthRetry,
			RetryMax:        *linksHealthRetryMax,
			RetryBackoffMin: *linksHealthRetryBackoffMin,
			RetryBackoffMax: *linksHealthRetryBackoffMax,
		}); err != nil {
			fatalf("links health stream failed: %v", err)
		}
		return
	}
	if act == "link.failover" {
		if err := runLinkFailover(client, baseURL, token, *linkID, *gatewayID, *requestID, *leaseID); err != nil {
			fatalf("link failover failed: %v", err)
		}
		return
	}
	if act == "events" {
		if err := streamEvents(client, baseURL, token, *eventsDuration, *requestID); err != nil {
			fatalf("events stream failed: %v", err)
		}
		return
	}

	method, path, err := actionRoute(act, *waitState, *waitTimeout, *linkID)
	if err != nil {
		fatalf("%v", err)
	}
	if len(body) == 0 {
		body, err = defaultPayloadForAction(act, *leaseOwner, *leaseID, *leasePrevID, *leaseTTL, *gatewayID)
		if err != nil {
			fatalf("payload: %v", err)
		}
	}
	respBody, status, err := doRequest(client, method, baseURL+path, token, body, *requestID, *leaseID)
	if err != nil {
		fatalf("request failed: %v", err)
	}
	if status >= 400 {
		fatalf("helper returned status=%d body=%s", status, strings.TrimSpace(string(respBody)))
	}
	if len(respBody) == 0 {
		fmt.Println("{}")
		return
	}
	fmt.Println(string(respBody))
}

func actionRoute(action, waitState string, waitTimeout time.Duration, linkID string) (method, path string, err error) {
	switch action {
	case "schema":
		return http.MethodGet, "/v1/helper/schema", nil
	case "status":
		return http.MethodGet, "/v1/helper/status", nil
	case "contract.check":
		return "", "", nil
	case "lease.acquire":
		return http.MethodPost, "/v1/helper/lease.acquire", nil
	case "lease.ensure":
		return "", "", nil
	case "bridge.startup":
		return "", "", nil
	case "bridge.shutdown":
		return "", "", nil
	case "bridge.reconcile":
		return "", "", nil
	case "bridge.autopilot":
		return "", "", nil
	case "bridge.autopilot.once":
		return "", "", nil
	case "bridge.autopilot.daemon":
		return "", "", nil
	case "bridge.autopilot.daemon.stream":
		return "", "", nil
	case "bridge.status.stream":
		return "", "", nil
	case "links.health.stream":
		return "", "", nil
	case "lease.renew":
		return http.MethodPost, "/v1/helper/lease.renew", nil
	case "lease.heartbeat":
		return http.MethodPost, "/v1/helper/lease.heartbeat", nil
	case "lease.takeover":
		return http.MethodPost, "/v1/helper/lease.takeover", nil
	case "lease.release":
		return http.MethodPost, "/v1/helper/lease.release", nil
	case "lease.status":
		return http.MethodGet, "/v1/helper/lease.status", nil
	case "health":
		return http.MethodGet, "/v1/helper/health", nil
	case "bootstrap.apply":
		return http.MethodPost, "/v1/helper/bootstrap.apply", nil
	case "profile.apply":
		return http.MethodPost, "/v1/helper/profile.apply", nil
	case "profile.current":
		return http.MethodGet, "/v1/helper/profile.current", nil
	case "bootstrap.validate":
		return http.MethodPost, "/v1/helper/bootstrap.validate", nil
	case "tunnel.start":
		return http.MethodPost, "/v1/helper/tunnel.start", nil
	case "tunnel.stop":
		return http.MethodPost, "/v1/helper/tunnel.stop", nil
	case "tunnel.refresh":
		return http.MethodPost, "/v1/helper/tunnel.refresh", nil
	case "stats.read":
		return http.MethodGet, "/v1/helper/stats.read", nil
	case "links":
		return http.MethodGet, "/v1/helper/links", nil
	case "link.read":
		id := strings.TrimSpace(linkID)
		if id == "" {
			return "", "", errors.New("link.read requires -link-id")
		}
		return http.MethodGet, "/v1/helper/links/" + url.PathEscape(id), nil
	case "link.reconnect":
		id := strings.TrimSpace(linkID)
		if id == "" {
			return "", "", errors.New("link.reconnect requires -link-id")
		}
		return http.MethodPost, "/v1/helper/links/" + url.PathEscape(id) + "/reconnect", nil
	case "link.drain":
		id := strings.TrimSpace(linkID)
		if id == "" {
			return "", "", errors.New("link.drain requires -link-id")
		}
		return http.MethodPost, "/v1/helper/links/" + url.PathEscape(id) + "/drain", nil
	case "link.resume":
		id := strings.TrimSpace(linkID)
		if id == "" {
			return "", "", errors.New("link.resume requires -link-id")
		}
		return http.MethodPost, "/v1/helper/links/" + url.PathEscape(id) + "/resume", nil
	case "link.gateway.select":
		id := strings.TrimSpace(linkID)
		if id == "" {
			return "", "", errors.New("link.gateway.select requires -link-id")
		}
		return http.MethodPost, "/v1/helper/links/" + url.PathEscape(id) + "/gateway.select", nil
	case "link.failover":
		return "", "", nil
	case "security.evaluate":
		return http.MethodPost, "/v1/helper/security.evaluate", nil
	case "security.reputation.upsert":
		return http.MethodPost, "/v1/helper/security.reputation.upsert", nil
	case "security.policy.upsert":
		return http.MethodPost, "/v1/helper/security.policy.upsert", nil
	case "security.policy.get":
		return http.MethodGet, "/v1/helper/security.policy.get", nil
	case "security.policy.rollout":
		return http.MethodPost, "/v1/helper/security.policy.rollout", nil
	case "security.corporate-allow.upsert":
		return http.MethodPost, "/v1/helper/security.corporate-allow.upsert", nil
	case "security.audit":
		return http.MethodGet, "/v1/helper/security.audit", nil
	case "security.signal.ingest":
		return http.MethodPost, "/v1/helper/security.signal.ingest", nil
	case "security.signal.ingest.recent":
		return http.MethodGet, "/v1/helper/security.signal.ingest.recent", nil
	case "wait":
		qState := url.QueryEscape(strings.TrimSpace(waitState))
		if qState == "" {
			qState = "established"
		}
		qTimeout := waitTimeout.String()
		if waitTimeout <= 0 {
			qTimeout = (20 * time.Second).String()
		}
		return http.MethodGet, "/v1/helper/wait?state=" + qState + "&timeout=" + url.QueryEscape(qTimeout), nil
	case "events":
		return http.MethodGet, "/v1/helper/events", nil
	case "diagnostics.export":
		return http.MethodPost, "/v1/helper/diagnostics.export", nil
	default:
		return "", "", fmt.Errorf("unknown action %q", action)
	}
}

func streamEvents(client *http.Client, baseURL, token string, duration time.Duration, requestID string) error {
	if duration <= 0 {
		duration = 20 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), duration)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/v1/helper/events", nil)
	if err != nil {
		return err
	}
	token = strings.TrimSpace(token)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if rid := strings.TrimSpace(requestID); rid != "" {
		req.Header.Set("X-Request-ID", rid)
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		line := sc.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		fmt.Println(line)
	}
	if err := sc.Err(); err != nil && !errors.Is(err, context.DeadlineExceeded) && !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return err
	}
	return nil
}

func streamBridgeAutopilotDaemon(client *http.Client, baseURL, token string, payload []byte, requestID string) error {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, baseURL+"/v1/helper/bridge.autopilot.daemon.stream", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	token = strings.TrimSpace(token)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if rid := strings.TrimSpace(requestID); rid != "" {
		req.Header.Set("X-Request-ID", rid)
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		fmt.Println(strings.TrimSpace(strings.TrimPrefix(line, "data: ")))
	}
	if err := sc.Err(); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

func streamBridgeStatus(client *http.Client, baseURL, token string, interval, duration time.Duration, requestID string, opts bridgeStatusStreamOptions) error {
	if opts.RetryBackoffMin <= 0 {
		opts.RetryBackoffMin = 500 * time.Millisecond
	}
	if opts.RetryBackoffMax <= 0 {
		opts.RetryBackoffMax = 10 * time.Second
	}
	if opts.RetryBackoffMax < opts.RetryBackoffMin {
		opts.RetryBackoffMax = opts.RetryBackoffMin
	}

	attempt := 0
	for {
		done, err := streamBridgeStatusOnce(client, baseURL, token, interval, duration, requestID, opts, attempt+1)
		if err == nil {
			if done || !opts.Retry {
				return nil
			}
			err = io.ErrUnexpectedEOF
		}
		if !opts.Retry {
			return err
		}
		attempt++
		if opts.RetryMax > 0 && attempt >= opts.RetryMax {
			return fmt.Errorf("bridge status stream retry limit reached (%d): %w", opts.RetryMax, err)
		}
		backoff := retryBackoff(attempt-1, opts.RetryBackoffMin, opts.RetryBackoffMax)
		time.Sleep(backoff)
	}
}

func streamBridgeStatusOnce(client *http.Client, baseURL, token string, interval, duration time.Duration, requestID string, opts bridgeStatusStreamOptions, attempt int) (bool, error) {
	q := url.Values{}
	if interval > 0 {
		q.Set("interval", interval.String())
	}
	if duration > 0 {
		q.Set("duration", duration.String())
	}
	target := baseURL + "/v1/helper/bridge.status.stream"
	if encoded := q.Encode(); encoded != "" {
		target += "?" + encoded
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, target, nil)
	if err != nil {
		return false, err
	}
	token = strings.TrimSpace(token)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if rid := strings.TrimSpace(requestID); rid != "" {
		req.Header.Set("X-Request-ID", rid)
	}
	resp, err := client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return false, fmt.Errorf("status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	sc := bufio.NewScanner(resp.Body)
	curEvent := ""
	curData := ""
	doneReceived := false
	flush := func() {
		if strings.TrimSpace(curData) == "" {
			curEvent = ""
			curData = ""
			return
		}
		if strings.TrimSpace(curEvent) == "" {
			curEvent = "message"
		}
		if opts.JSONL {
			fmt.Println(formatBridgeStatusJSONL(curEvent, curData, attempt))
		} else {
			fmt.Println(strings.TrimSpace(curData))
		}
		if curEvent == "done" {
			doneReceived = true
		}
		curEvent = ""
		curData = ""
	}
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			flush()
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		if strings.HasPrefix(line, "event:") {
			curEvent = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}
		if strings.HasPrefix(line, "data:") {
			payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if curData == "" {
				curData = payload
			} else {
				curData += "\n" + payload
			}
		}
	}
	flush()
	if err := sc.Err(); err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	if !doneReceived {
		return false, io.ErrUnexpectedEOF
	}
	return true, nil
}

func formatBridgeStatusJSONL(eventName, payload string, attempt int) string {
	data := decodePossibleJSON(strings.TrimSpace(payload))
	envelope := map[string]any{
		"stream":     "bridge.status",
		"event":      strings.TrimSpace(eventName),
		"attempt":    attempt,
		"receivedAt": time.Now().UTC().Format(time.RFC3339Nano),
		"data":       data,
	}
	raw, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Sprintf(`{"stream":"bridge.status","event":"%s","attempt":%d,"receivedAt":"%s","data":"%s"}`,
			strings.TrimSpace(eventName),
			attempt,
			time.Now().UTC().Format(time.RFC3339Nano),
			escapeJSONString(strings.TrimSpace(payload)))
	}
	return string(raw)
}

func streamLinksHealth(client *http.Client, baseURL, token string, interval, duration time.Duration, requestID string, opts bridgeStatusStreamOptions) error {
	if opts.RetryBackoffMin <= 0 {
		opts.RetryBackoffMin = 500 * time.Millisecond
	}
	if opts.RetryBackoffMax <= 0 {
		opts.RetryBackoffMax = 10 * time.Second
	}
	if opts.RetryBackoffMax < opts.RetryBackoffMin {
		opts.RetryBackoffMax = opts.RetryBackoffMin
	}

	attempt := 0
	for {
		done, err := streamLinksHealthOnce(client, baseURL, token, interval, duration, requestID, opts, attempt+1)
		if err == nil {
			if done || !opts.Retry {
				return nil
			}
			err = io.ErrUnexpectedEOF
		}
		if !opts.Retry {
			return err
		}
		attempt++
		if opts.RetryMax > 0 && attempt >= opts.RetryMax {
			return fmt.Errorf("links health stream retry limit reached (%d): %w", opts.RetryMax, err)
		}
		backoff := retryBackoff(attempt-1, opts.RetryBackoffMin, opts.RetryBackoffMax)
		time.Sleep(backoff)
	}
}

func streamLinksHealthOnce(client *http.Client, baseURL, token string, interval, duration time.Duration, requestID string, opts bridgeStatusStreamOptions, attempt int) (bool, error) {
	q := url.Values{}
	if interval > 0 {
		q.Set("interval", interval.String())
	}
	if duration > 0 {
		q.Set("duration", duration.String())
	}
	target := baseURL + "/v1/helper/links/health.stream"
	if encoded := q.Encode(); encoded != "" {
		target += "?" + encoded
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, target, nil)
	if err != nil {
		return false, err
	}
	token = strings.TrimSpace(token)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if rid := strings.TrimSpace(requestID); rid != "" {
		req.Header.Set("X-Request-ID", rid)
	}
	resp, err := client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return false, fmt.Errorf("status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	sc := bufio.NewScanner(resp.Body)
	curEvent := ""
	curData := ""
	doneReceived := false
	flush := func() {
		if strings.TrimSpace(curData) == "" {
			curEvent = ""
			curData = ""
			return
		}
		if strings.TrimSpace(curEvent) == "" {
			curEvent = "message"
		}
		if opts.JSONL {
			fmt.Println(formatLinksHealthJSONL(curEvent, curData, attempt))
		} else {
			fmt.Println(strings.TrimSpace(curData))
		}
		if curEvent == "done" {
			doneReceived = true
		}
		curEvent = ""
		curData = ""
	}
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			flush()
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		if strings.HasPrefix(line, "event:") {
			curEvent = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}
		if strings.HasPrefix(line, "data:") {
			payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if curData == "" {
				curData = payload
			} else {
				curData += "\n" + payload
			}
		}
	}
	flush()
	if err := sc.Err(); err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	if !doneReceived {
		return false, io.ErrUnexpectedEOF
	}
	return true, nil
}

func formatLinksHealthJSONL(eventName, payload string, attempt int) string {
	data := decodePossibleJSON(strings.TrimSpace(payload))
	envelope := map[string]any{
		"stream":     "links.health",
		"event":      strings.TrimSpace(eventName),
		"attempt":    attempt,
		"receivedAt": time.Now().UTC().Format(time.RFC3339Nano),
		"data":       data,
	}
	raw, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Sprintf(`{"stream":"links.health","event":"%s","attempt":%d,"receivedAt":"%s","data":"%s"}`,
			strings.TrimSpace(eventName),
			attempt,
			time.Now().UTC().Format(time.RFC3339Nano),
			escapeJSONString(strings.TrimSpace(payload)))
	}
	return string(raw)
}

func decodePossibleJSON(payload string) any {
	if payload == "" {
		return ""
	}
	var out any
	if err := json.Unmarshal([]byte(payload), &out); err == nil {
		return out
	}
	return payload
}

func escapeJSONString(s string) string {
	raw, err := json.Marshal(s)
	if err != nil || len(raw) < 2 {
		return s
	}
	return string(raw[1 : len(raw)-1])
}

func retryBackoff(attempt int, min, max time.Duration) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	backoff := min
	for i := 0; i < attempt; i++ {
		backoff *= 2
		if backoff >= max {
			return max
		}
	}
	return backoff
}

func buildClient(endpoint, unixSocket string, timeout time.Duration) (*http.Client, string, error) {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	if strings.TrimSpace(unixSocket) != "" {
		sock := strings.TrimSpace(unixSocket)
		tr := &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", sock)
			},
		}
		return &http.Client{
			Timeout:   timeout,
			Transport: tr,
		}, "http://unix", nil
	}
	u, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil {
		return nil, "", err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, "", errors.New("endpoint must be http or https")
	}
	return &http.Client{Timeout: timeout}, strings.TrimRight(u.String(), "/"), nil
}

func ensureSchema(client *http.Client, baseURL, token string) error {
	_, err := fetchSchema(client, baseURL, token)
	return err
}

func fetchSchema(client *http.Client, baseURL, token string) (schemaResponse, error) {
	body, status, err := doRequest(client, http.MethodGet, baseURL+"/v1/helper/schema", token, nil, "", "")
	if err != nil {
		return schemaResponse{}, err
	}
	if status >= 400 {
		return schemaResponse{}, fmt.Errorf("schema status=%d body=%s", status, strings.TrimSpace(string(body)))
	}
	var s schemaResponse
	if err := json.Unmarshal(body, &s); err != nil {
		return schemaResponse{}, err
	}
	if s.APIVersion != expectedHelperAPIVersion {
		return schemaResponse{}, fmt.Errorf("unexpected apiVersion=%q expected=%q", s.APIVersion, expectedHelperAPIVersion)
	}
	return s, nil
}

func validateBridgeBootstrapContract(schema schemaResponse, bootstrapPayload []byte) error {
	var req bridgeBootstrapPayload
	if err := json.Unmarshal(bootstrapPayload, &req); err != nil {
		return fmt.Errorf("bridge.startup payload decode failed: %w", err)
	}
	profile := req.ProfileBootstrap
	for _, field := range schema.Bootstrap.RequiredBootstrapFields {
		switch field {
		case "clientID":
			if strings.TrimSpace(profile.ClientID) == "" {
				return errors.New("bridge.startup payload missing required field profileBootstrap.clientID")
			}
		case "serverStaticPub":
			if strings.TrimSpace(profile.ServerStaticPub) == "" {
				return errors.New("bridge.startup payload missing required field profileBootstrap.serverStaticPub")
			}
		}
	}
	if payloadHasJSONArray(profile.Gateways) && !schema.Bootstrap.GatewayPoolSupported {
		return errors.New("helper schema does not support profileBootstrap.gateways")
	}
	if payloadHasJSONObject(profile.GatewayPolicy) && !schema.Bootstrap.GatewayPolicySupported {
		return errors.New("helper schema does not support profileBootstrap.gatewayPolicy")
	}
	if payloadHasJSONObject(profile.RekeyPolicy) && !schema.Bootstrap.RekeyPolicySupported {
		return errors.New("helper schema does not support profileBootstrap.rekeyPolicy")
	}
	if hasProfileBundlePayload(profile) && !schema.Bootstrap.ProfileBundleSupported {
		return errors.New("helper schema does not support profileBootstrap profile bundle fields (profileID/region/routing/dns/bridge)")
	}
	if strings.TrimSpace(profile.SecurityProfile) != "" && !schema.Bootstrap.SecurityProfileSupported {
		return errors.New("helper schema does not support profileBootstrap.securityProfile")
	}
	return nil
}

func validateSchemaContractRequirements(
	schema schemaResponse,
	requireGatewayPool bool,
	requireGatewayPolicy bool,
	requireRekeyPolicy bool,
	requireProfileBundle bool,
	requireSecurityProfile bool,
	requireGatewayPoolVersion string,
	requireBootstrapSchemaVersion string,
	requireProfileBundleVersion string,
) error {
	if requireGatewayPool && !schema.Bootstrap.GatewayPoolSupported {
		return errors.New("helper schema bootstrap.gatewayPoolSupported=false while --require-gateway-pool is set")
	}
	if requireGatewayPolicy && !schema.Bootstrap.GatewayPolicySupported {
		return errors.New("helper schema bootstrap.gatewayPolicySupported=false while --require-gateway-policy is set")
	}
	if requireRekeyPolicy && !schema.Bootstrap.RekeyPolicySupported {
		return errors.New("helper schema bootstrap.rekeyPolicySupported=false while --require-rekey-policy is set")
	}
	if requireProfileBundle && !schema.Bootstrap.ProfileBundleSupported {
		return errors.New("helper schema bootstrap.profileBundleSupported=false while --require-profile-bundle is set")
	}
	if requireSecurityProfile && !schema.Bootstrap.SecurityProfileSupported {
		return errors.New("helper schema bootstrap.securityProfileSupported=false while --require-security-profile is set")
	}
	if requireGatewayPoolVersion != "" && strings.TrimSpace(schema.GatewayPoolVersion) != requireGatewayPoolVersion {
		return fmt.Errorf(
			"helper schema gatewayPoolVersion=%q expected=%q",
			strings.TrimSpace(schema.GatewayPoolVersion),
			requireGatewayPoolVersion,
		)
	}
	if requireBootstrapSchemaVersion != "" && strings.TrimSpace(schema.Bootstrap.SchemaVersion) != requireBootstrapSchemaVersion {
		return fmt.Errorf(
			"helper schema bootstrap.schemaVersion=%q expected=%q",
			strings.TrimSpace(schema.Bootstrap.SchemaVersion),
			requireBootstrapSchemaVersion,
		)
	}
	if requireProfileBundleVersion != "" && strings.TrimSpace(schema.ProfileBundleVersion) != requireProfileBundleVersion {
		return fmt.Errorf(
			"helper schema profileBundleVersion=%q expected=%q",
			strings.TrimSpace(schema.ProfileBundleVersion),
			requireProfileBundleVersion,
		)
	}
	return nil
}

func hasProfileBundlePayload(profile bridgeBootstrapProfile) bool {
	if strings.TrimSpace(profile.ProfileID) != "" || strings.TrimSpace(profile.Region) != "" {
		return true
	}
	return payloadHasJSONObject(profile.Routing) || payloadHasJSONObject(profile.DNS) || payloadHasJSONObject(profile.Bridge)
}

func payloadHasJSONArray(raw json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" || trimmed == "[]" {
		return false
	}
	return strings.HasPrefix(trimmed, "[")
}

func payloadHasJSONObject(raw json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" || trimmed == "{}" {
		return false
	}
	return strings.HasPrefix(trimmed, "{")
}

func doRequest(client *http.Client, method, targetURL, token string, payload []byte, requestID, leaseID string) ([]byte, int, error) {
	var reader io.Reader
	if len(payload) > 0 {
		reader = bytes.NewReader(payload)
	}
	req, err := http.NewRequest(method, targetURL, reader)
	if err != nil {
		return nil, 0, err
	}
	if len(payload) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	if rid := strings.TrimSpace(requestID); rid != "" {
		req.Header.Set("X-Request-ID", rid)
	}
	if lid := strings.TrimSpace(leaseID); lid != "" {
		req.Header.Set("X-Helper-Lease-ID", lid)
	}
	token = strings.TrimSpace(token)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return raw, resp.StatusCode, nil
}

func defaultPayloadForAction(action, leaseOwner, leaseID, leasePrevID string, leaseTTL time.Duration, gatewayID string) ([]byte, error) {
	switch action {
	case "lease.acquire":
		return json.Marshal(map[string]any{
			"owner": strings.TrimSpace(leaseOwner),
			"ttl":   leaseTTL.Nanoseconds(),
		})
	case "lease.renew":
		if strings.TrimSpace(leaseID) == "" {
			return nil, errors.New("lease.renew requires -lease-id or payload-file")
		}
		return json.Marshal(map[string]any{
			"leaseId": strings.TrimSpace(leaseID),
			"ttl":     leaseTTL.Nanoseconds(),
		})
	case "lease.heartbeat":
		if strings.TrimSpace(leaseID) == "" {
			return nil, errors.New("lease.heartbeat requires -lease-id or payload-file")
		}
		return json.Marshal(map[string]any{
			"leaseId": strings.TrimSpace(leaseID),
			"ttl":     leaseTTL.Nanoseconds(),
		})
	case "lease.takeover":
		prev := strings.TrimSpace(leasePrevID)
		if prev == "" {
			return nil, errors.New("lease.takeover requires -lease-prev-id or payload-file")
		}
		return json.Marshal(map[string]any{
			"owner":       strings.TrimSpace(leaseOwner),
			"ttl":         leaseTTL.Nanoseconds(),
			"prevLeaseId": prev,
		})
	case "lease.release":
		if strings.TrimSpace(leaseID) == "" {
			return nil, errors.New("lease.release requires -lease-id or payload-file")
		}
		return json.Marshal(map[string]any{
			"leaseId": strings.TrimSpace(leaseID),
		})
	case "link.gateway.select":
		if strings.TrimSpace(gatewayID) == "" {
			return nil, errors.New("link.gateway.select requires -gateway-id or payload-file")
		}
		return json.Marshal(map[string]any{
			"gatewayID": strings.TrimSpace(gatewayID),
		})
	default:
		return nil, nil
	}
}

func runLeaseKeepalive(
	ctx context.Context,
	client *http.Client,
	baseURL, token, leaseID, requestID string,
	leaseTTL, interval, duration time.Duration,
	releaseOnExit bool,
) error {
	leaseID = strings.TrimSpace(leaseID)
	if leaseID == "" {
		return errors.New("lease.keepalive requires -lease-id")
	}
	if leaseTTL <= 0 {
		leaseTTL = 60 * time.Second
	}
	if interval <= 0 {
		interval = 20 * time.Second
	}
	if interval > leaseTTL {
		interval = leaseTTL / 2
		if interval <= 0 {
			interval = 5 * time.Second
		}
	}
	if duration < 0 {
		return errors.New("lease-keepalive-duration must be >= 0")
	}

	path := "/v1/helper/lease.heartbeat"
	deadline := time.Now().Add(duration)
	attempt := 0
	for {
		select {
		case <-ctx.Done():
			if releaseOnExit {
				if err := releaseLease(client, baseURL, token, leaseID, requestID); err != nil {
					return fmt.Errorf("context done (%v), lease release failed: %w", ctx.Err(), err)
				}
			}
			return nil
		default:
		}
		attempt++
		rid := strings.TrimSpace(requestID)
		if rid != "" && attempt > 1 {
			rid = fmt.Sprintf("%s-%d", rid, attempt)
		}
		payload, err := json.Marshal(map[string]any{
			"leaseId": leaseID,
			"ttl":     leaseTTL.Nanoseconds(),
		})
		if err != nil {
			return err
		}
		respBody, status, err := doRequest(client, http.MethodPost, baseURL+path, token, payload, rid, leaseID)
		if err != nil {
			return err
		}
		if status >= 400 {
			return fmt.Errorf("status=%d body=%s", status, strings.TrimSpace(string(respBody)))
		}
		if len(respBody) > 0 {
			fmt.Println(string(respBody))
		} else {
			fmt.Println("{}")
		}
		if duration == 0 {
			return nil
		}
		sleepFor := interval
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return nil
		}
		if remaining < sleepFor {
			sleepFor = remaining
		}
		timer := time.NewTimer(sleepFor)
		select {
		case <-ctx.Done():
			timer.Stop()
			if releaseOnExit {
				if err := releaseLease(client, baseURL, token, leaseID, requestID); err != nil {
					return fmt.Errorf("context done (%v), lease release failed: %w", ctx.Err(), err)
				}
			}
			return nil
		case <-timer.C:
		}
	}
}

func ensureLease(
	client *http.Client,
	baseURL, token, owner string,
	ttl time.Duration,
	allowTakeover bool,
	requestID string,
) (leaseStatusResponse, error) {
	statusBody, statusCode, err := doRequest(client, http.MethodGet, baseURL+"/v1/helper/status", token, nil, requestID, "")
	if err != nil {
		return leaseStatusResponse{}, err
	}
	if statusCode >= 400 {
		return leaseStatusResponse{}, fmt.Errorf("status=%d body=%s", statusCode, strings.TrimSpace(string(statusBody)))
	}
	var st helperStatusResponse
	if err := json.Unmarshal(statusBody, &st); err != nil {
		return leaseStatusResponse{}, err
	}
	cur := st.Lease

	owner = strings.TrimSpace(owner)
	if owner == "" {
		owner = "helperctl"
	}
	if ttl <= 0 {
		ttl = 60 * time.Second
	}

	doPost := func(path string, payload map[string]any, leaseHeader string) (leaseStatusResponse, error) {
		rawPayload, err := json.Marshal(payload)
		if err != nil {
			return leaseStatusResponse{}, err
		}
		respBody, code, err := doRequest(client, http.MethodPost, baseURL+path, token, rawPayload, requestID, leaseHeader)
		if err != nil {
			return leaseStatusResponse{}, err
		}
		if code >= 400 {
			return leaseStatusResponse{}, fmt.Errorf("status=%d body=%s", code, strings.TrimSpace(string(respBody)))
		}
		var out leaseStatusResponse
		if err := json.Unmarshal(respBody, &out); err != nil {
			return leaseStatusResponse{}, err
		}
		return out, nil
	}

	if !cur.Active {
		return doPost("/v1/helper/lease.acquire", map[string]any{
			"owner": owner,
			"ttl":   ttl.Nanoseconds(),
		}, "")
	}

	if strings.TrimSpace(cur.Owner) == owner && strings.TrimSpace(cur.LeaseID) != "" {
		return doPost("/v1/helper/lease.heartbeat", map[string]any{
			"leaseId": cur.LeaseID,
			"ttl":     ttl.Nanoseconds(),
		}, cur.LeaseID)
	}

	if !allowTakeover {
		return leaseStatusResponse{}, errors.New("active lease belongs to another owner and takeover is disabled")
	}
	if strings.TrimSpace(cur.LeaseID) == "" {
		return leaseStatusResponse{}, errors.New("active lease is missing leaseId in status")
	}
	return doPost("/v1/helper/lease.takeover", map[string]any{
		"owner":       owner,
		"ttl":         ttl.Nanoseconds(),
		"prevLeaseId": cur.LeaseID,
	}, "")
}

func runBridgeStartup(
	client *http.Client,
	baseURL, token string,
	bootstrapPayload []byte,
	leaseOwner string,
	leaseTTL time.Duration,
	allowTakeover bool,
	waitState string,
	waitTimeout time.Duration,
	waitEnabled bool,
	requestID string,
) (bridgeStartupResult, error) {
	schema, err := fetchSchema(client, baseURL, token)
	if err != nil {
		return bridgeStartupResult{}, err
	}
	if err := validateBridgeBootstrapContract(schema, bootstrapPayload); err != nil {
		return bridgeStartupResult{}, err
	}

	lease, err := ensureLease(client, baseURL, token, leaseOwner, leaseTTL, allowTakeover, requestID)
	if err != nil {
		return bridgeStartupResult{}, err
	}
	leaseID := strings.TrimSpace(lease.LeaseID)
	if leaseID == "" {
		return bridgeStartupResult{}, errors.New("bridge.startup: lease.ensure returned empty leaseId")
	}

	validateBody, validateStatus, err := doRequest(client, http.MethodPost, baseURL+"/v1/helper/bootstrap.validate", token, bootstrapPayload, withRequestAttempt(requestID, "validate"), leaseID)
	if err != nil {
		return bridgeStartupResult{}, err
	}
	if validateStatus >= 400 {
		return bridgeStartupResult{}, fmt.Errorf("bootstrap.validate status=%d body=%s", validateStatus, strings.TrimSpace(string(validateBody)))
	}
	var validateOut bootstrapValidateResponse
	if err := json.Unmarshal(validateBody, &validateOut); err != nil {
		return bridgeStartupResult{}, err
	}
	if !validateOut.OK {
		return bridgeStartupResult{}, fmt.Errorf("bootstrap.validate returned ok=false body=%s", strings.TrimSpace(string(validateBody)))
	}

	applyBody, applyStatus, err := doRequest(client, http.MethodPost, baseURL+"/v1/helper/bootstrap.apply", token, bootstrapPayload, withRequestAttempt(requestID, "apply"), leaseID)
	if err != nil {
		return bridgeStartupResult{}, err
	}
	if applyStatus >= 400 {
		return bridgeStartupResult{}, fmt.Errorf("bootstrap.apply status=%d body=%s", applyStatus, strings.TrimSpace(string(applyBody)))
	}

	startBody, startStatus, err := doRequest(client, http.MethodPost, baseURL+"/v1/helper/tunnel.start", token, nil, withRequestAttempt(requestID, "start"), leaseID)
	if err != nil {
		return bridgeStartupResult{}, err
	}
	if startStatus >= 400 {
		return bridgeStartupResult{}, fmt.Errorf("tunnel.start status=%d body=%s", startStatus, strings.TrimSpace(string(startBody)))
	}

	out := bridgeStartupResult{
		OK:    true,
		Lease: lease,
	}
	if !waitEnabled {
		return out, nil
	}
	targetState := strings.TrimSpace(waitState)
	if targetState == "" {
		targetState = "established"
	}
	if waitTimeout <= 0 {
		waitTimeout = 20 * time.Second
	}
	waitPath := "/v1/helper/wait?state=" + url.QueryEscape(targetState) + "&timeout=" + url.QueryEscape(waitTimeout.String())
	waitBody, waitStatus, err := doRequest(client, http.MethodGet, baseURL+waitPath, token, nil, withRequestAttempt(requestID, "wait"), leaseID)
	if err != nil {
		return bridgeStartupResult{}, err
	}
	if waitStatus >= 400 {
		return bridgeStartupResult{}, fmt.Errorf("wait status=%d body=%s", waitStatus, strings.TrimSpace(string(waitBody)))
	}
	out.WaitRaw = append([]byte(nil), waitBody...)
	return out, nil
}

func runBridgeShutdown(
	client *http.Client,
	baseURL, token string,
	leaseOwner string,
	leaseTTL time.Duration,
	allowTakeover bool,
	requestID string,
	bestEffort bool,
) (bridgeShutdownResult, error) {
	lease, err := ensureLease(client, baseURL, token, leaseOwner, leaseTTL, allowTakeover, requestID)
	if err != nil {
		return bridgeShutdownResult{}, err
	}
	leaseID := strings.TrimSpace(lease.LeaseID)
	if leaseID == "" {
		return bridgeShutdownResult{}, errors.New("bridge.shutdown: lease.ensure returned empty leaseId")
	}

	out := bridgeShutdownResult{
		OK:    true,
		Lease: lease,
	}

	stopBody, stopStatus, stopErr := doRequest(client, http.MethodPost, baseURL+"/v1/helper/tunnel.stop", token, nil, withRequestAttempt(requestID, "stop"), leaseID)
	if stopErr != nil {
		out.StopError = stopErr.Error()
	} else if stopStatus >= 400 {
		out.StopError = fmt.Sprintf("status=%d body=%s", stopStatus, strings.TrimSpace(string(stopBody)))
	} else {
		out.Stopped = true
	}

	releaseErr := releaseLease(client, baseURL, token, leaseID, withRequestAttempt(requestID, "release"))
	if releaseErr != nil {
		out.LeaseError = releaseErr.Error()
	} else {
		out.Released = true
	}

	if out.StopError != "" || out.LeaseError != "" {
		out.OK = false
		if !bestEffort {
			return out, errors.New("bridge.shutdown encountered errors")
		}
	}
	return out, nil
}

func runLinkFailover(
	client *http.Client,
	baseURL, token, linkID, gatewayID, requestID, leaseID string,
) error {
	linkID = strings.TrimSpace(linkID)
	if linkID == "" {
		return errors.New("link.failover requires -link-id")
	}
	gatewayID = strings.TrimSpace(gatewayID)
	if gatewayID == "" {
		return errors.New("link.failover requires -gateway-id")
	}

	selectPayload, err := json.Marshal(map[string]any{
		"gatewayID": gatewayID,
	})
	if err != nil {
		return err
	}
	selectPath := baseURL + "/v1/helper/links/" + url.PathEscape(linkID) + "/gateway.select"
	selectBody, selectStatus, err := doRequest(client, http.MethodPost, selectPath, token, selectPayload, withRequestAttempt(requestID, "failover-select"), leaseID)
	if err != nil {
		return err
	}
	if selectStatus >= 400 {
		return fmt.Errorf("gateway.select status=%d body=%s", selectStatus, strings.TrimSpace(string(selectBody)))
	}

	reconnectPath := baseURL + "/v1/helper/links/" + url.PathEscape(linkID) + "/reconnect"
	reconnectBody, reconnectStatus, err := doRequest(client, http.MethodPost, reconnectPath, token, nil, withRequestAttempt(requestID, "failover-reconnect"), leaseID)
	if err != nil {
		return err
	}
	if reconnectStatus >= 400 {
		return fmt.Errorf("reconnect status=%d body=%s", reconnectStatus, strings.TrimSpace(string(reconnectBody)))
	}

	out := linkFailoverResult{
		OK:        true,
		LinkID:    linkID,
		GatewayID: gatewayID,
		Select:    append([]byte(nil), selectBody...),
		Reconnect: append([]byte(nil), reconnectBody...),
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return err
	}
	fmt.Println(string(raw))
	return nil
}

func runBridgeReconcile(
	client *http.Client,
	baseURL, token string,
	leaseOwner string,
	leaseTTL time.Duration,
	allowTakeover bool,
	ensureLeaseOwnership bool,
	requestID string,
) (bridgeReconcileResult, error) {
	out := bridgeReconcileResult{OK: true}
	if ensureLeaseOwnership {
		lease, err := ensureLease(client, baseURL, token, leaseOwner, leaseTTL, allowTakeover, withRequestAttempt(requestID, "ensure"))
		if err != nil {
			return bridgeReconcileResult{}, err
		}
		out.Lease = lease
	}

	statusBody, statusCode, err := doRequest(client, http.MethodGet, baseURL+"/v1/helper/status", token, nil, withRequestAttempt(requestID, "status"), "")
	if err != nil {
		return bridgeReconcileResult{}, err
	}
	if statusCode >= 400 {
		return bridgeReconcileResult{}, fmt.Errorf("status read failed: status=%d body=%s", statusCode, strings.TrimSpace(string(statusBody)))
	}
	if err := json.Unmarshal(statusBody, &out.Status); err != nil {
		return bridgeReconcileResult{}, err
	}
	if !ensureLeaseOwnership {
		out.Lease = out.Status.Lease
	}

	healthBody, healthCode, err := doRequest(client, http.MethodGet, baseURL+"/v1/helper/health", token, nil, withRequestAttempt(requestID, "health"), "")
	if err != nil {
		return bridgeReconcileResult{}, err
	}
	if healthCode >= 400 {
		return bridgeReconcileResult{}, fmt.Errorf("health read failed: status=%d body=%s", healthCode, strings.TrimSpace(string(healthBody)))
	}
	if err := json.Unmarshal(healthBody, &out.Health); err != nil {
		return bridgeReconcileResult{}, err
	}

	switch {
	case !out.Status.Running:
		out.Plan = "startup-needed"
		out.Reason = "runtime is not running"
	case out.Health.Ready && strings.EqualFold(out.Status.LastState, "established"):
		out.Plan = "running-ok"
		out.Reason = "runtime is running and ready"
	default:
		out.Plan = "restart-needed"
		out.Reason = "runtime running but not ready/established"
	}
	return out, nil
}

func runBridgeAutopilot(
	client *http.Client,
	baseURL, token string,
	bootstrapPayload []byte,
	leaseOwner string,
	leaseTTL time.Duration,
	allowTakeover bool,
	reconcileEnsureLease bool,
	startupWait bool,
	waitState string,
	waitTimeout time.Duration,
	shutdownBestEffort bool,
	allowRestart bool,
	maxSteps int,
	requestID string,
) (bridgeAutopilotResult, error) {
	if maxSteps <= 0 {
		maxSteps = 3
	}
	out := bridgeAutopilotResult{OK: true}
	for i := 1; i <= maxSteps; i++ {
		reconcile, err := runBridgeReconcile(
			client, baseURL, token,
			leaseOwner, leaseTTL, allowTakeover,
			reconcileEnsureLease,
			withRequestAttempt(requestID, fmt.Sprintf("reconcile-%d", i)),
		)
		if err != nil {
			return bridgeAutopilotResult{}, err
		}
		step := bridgeAutopilotStep{
			Iteration: i,
			Plan:      reconcile.Plan,
			Action:    "none",
			Reconcile: reconcile,
		}
		out.FinalPlan = reconcile.Plan
		switch reconcile.Plan {
		case "running-ok":
			step.Action = "noop"
			out.Steps = append(out.Steps, step)
			return out, nil
		case "startup-needed":
			if len(bootstrapPayload) == 0 {
				return bridgeAutopilotResult{}, errors.New("bridge.autopilot requires -payload-file when startup is needed")
			}
			step.Action = "startup"
			if _, err := runBridgeStartup(
				client, baseURL, token, bootstrapPayload,
				leaseOwner, leaseTTL, allowTakeover,
				waitState, waitTimeout, startupWait,
				withRequestAttempt(requestID, fmt.Sprintf("startup-%d", i)),
			); err != nil {
				return bridgeAutopilotResult{}, err
			}
			out.Steps = append(out.Steps, step)
		case "restart-needed":
			if !allowRestart {
				return bridgeAutopilotResult{}, errors.New("bridge.autopilot got restart-needed while restart is disabled")
			}
			if len(bootstrapPayload) == 0 {
				return bridgeAutopilotResult{}, errors.New("bridge.autopilot requires -payload-file when restart is needed")
			}
			step.Action = "restart"
			if _, err := runBridgeShutdown(
				client, baseURL, token,
				leaseOwner, leaseTTL, allowTakeover,
				withRequestAttempt(requestID, fmt.Sprintf("shutdown-%d", i)),
				shutdownBestEffort,
			); err != nil {
				return bridgeAutopilotResult{}, err
			}
			if _, err := runBridgeStartup(
				client, baseURL, token, bootstrapPayload,
				leaseOwner, leaseTTL, allowTakeover,
				waitState, waitTimeout, startupWait,
				withRequestAttempt(requestID, fmt.Sprintf("restart-startup-%d", i)),
			); err != nil {
				return bridgeAutopilotResult{}, err
			}
			out.Steps = append(out.Steps, step)
		default:
			return bridgeAutopilotResult{}, fmt.Errorf("bridge.autopilot got unknown plan %q", reconcile.Plan)
		}
	}
	out.OK = false
	return out, fmt.Errorf("bridge.autopilot exceeded max steps (%d), last plan=%s", maxSteps, out.FinalPlan)
}

func runBridgeAutopilotDaemon(
	ctx context.Context,
	emit func(bridgeAutopilotDaemonTick),
	client *http.Client,
	baseURL, token string,
	bootstrapPayload []byte,
	leaseOwner string,
	leaseTTL time.Duration,
	allowTakeover bool,
	reconcileEnsureLease bool,
	startupWait bool,
	waitState string,
	waitTimeout time.Duration,
	shutdownBestEffort bool,
	allowRestart bool,
	maxSteps int,
	interval time.Duration,
	duration time.Duration,
	continueOnError bool,
	requestID string,
) error {
	if emit == nil {
		return errors.New("bridge.autopilot.daemon requires emit callback")
	}
	if interval <= 0 {
		interval = 20 * time.Second
	}
	if duration < 0 {
		return errors.New("bridge-autopilot-duration must be >= 0")
	}

	started := time.Now()
	run := 0
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		run++
		result, err := runBridgeAutopilot(
			client, baseURL, token, bootstrapPayload,
			leaseOwner, leaseTTL, allowTakeover,
			reconcileEnsureLease,
			startupWait, waitState, waitTimeout,
			shutdownBestEffort,
			allowRestart,
			maxSteps,
			withRequestAttempt(requestID, fmt.Sprintf("daemon-%d", run)),
		)
		tick := bridgeAutopilotDaemonTick{
			Run:       run,
			At:        time.Now().UTC(),
			OK:        err == nil && result.OK,
			Autopilot: result,
		}
		if err != nil {
			tick.Error = err.Error()
		}
		emit(tick)

		if err != nil && !continueOnError {
			return err
		}
		if duration > 0 && time.Since(started) >= duration {
			return nil
		}

		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
}

func withRequestAttempt(requestID, suffix string) string {
	rid := strings.TrimSpace(requestID)
	if rid == "" {
		return ""
	}
	s := strings.TrimSpace(suffix)
	if s == "" {
		return rid
	}
	return rid + "-" + s
}

func releaseLease(client *http.Client, baseURL, token, leaseID, requestID string) error {
	payload, err := json.Marshal(map[string]any{"leaseId": leaseID})
	if err != nil {
		return err
	}
	rid := strings.TrimSpace(requestID)
	if rid != "" {
		rid += "-release"
	}
	body, status, err := doRequest(client, http.MethodPost, baseURL+"/v1/helper/lease.release", token, payload, rid, leaseID)
	if err != nil {
		return err
	}
	if status >= 400 {
		return fmt.Errorf("status=%d body=%s", status, strings.TrimSpace(string(body)))
	}
	return nil
}

func loadPayload(path string) ([]byte, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return nil, errors.New("payload file is empty")
	}
	return raw, nil
}

func decodeBodyObject(raw []byte) (map[string]any, error) {
	var out map[string]any
	if len(raw) == 0 {
		return map[string]any{}, nil
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	if out == nil {
		return map[string]any{}, nil
	}
	return out, nil
}

func buildBridgeAutopilotDaemonPayload(
	body []byte,
	leaseOwner string,
	leaseTTL time.Duration,
	leaseTakeover bool,
	reconcileEnsureLease bool,
	bridgeWait bool,
	waitState string,
	waitTimeout time.Duration,
	bridgeShutdownBestEffort bool,
	bridgeAutopilotAllowRestart bool,
	bridgeAutopilotMaxSteps int,
	bridgeAutopilotInterval time.Duration,
	bridgeAutopilotDuration time.Duration,
	bridgeAutopilotContinueOnError bool,
) ([]byte, error) {
	startup := map[string]any{
		"lease": map[string]any{
			"owner":    strings.TrimSpace(leaseOwner),
			"ttl":      leaseTTL.Nanoseconds(),
			"takeover": leaseTakeover,
		},
		"wait":        bridgeWait,
		"waitState":   strings.TrimSpace(waitState),
		"waitTimeout": waitTimeout.Nanoseconds(),
	}
	if len(body) != 0 {
		obj, err := decodeBodyObject(body)
		if err != nil {
			return nil, err
		}
		if pb, ok := obj["profileBootstrap"]; ok {
			startup["profileBootstrap"] = pb
		} else if len(obj) != 0 {
			startup["profileBootstrap"] = obj
		}
		if dev, ok := obj["deviceID"]; ok {
			startup["deviceID"] = dev
		}
	}
	payload := map[string]any{
		"interval":        bridgeAutopilotInterval.Nanoseconds(),
		"duration":        bridgeAutopilotDuration.Nanoseconds(),
		"continueOnError": bridgeAutopilotContinueOnError,
		"autopilot": map[string]any{
			"maxSteps":     bridgeAutopilotMaxSteps,
			"allowRestart": bridgeAutopilotAllowRestart,
			"reconcile": map[string]any{
				"lease": map[string]any{
					"owner":    strings.TrimSpace(leaseOwner),
					"ttl":      leaseTTL.Nanoseconds(),
					"takeover": leaseTakeover,
					"ensure":   reconcileEnsureLease,
				},
			},
			"startup": startup,
			"shutdown": map[string]any{
				"lease": map[string]any{
					"owner":    strings.TrimSpace(leaseOwner),
					"ttl":      leaseTTL.Nanoseconds(),
					"takeover": leaseTakeover,
				},
				"bestEffort": bridgeShutdownBestEffort,
			},
		},
	}
	return json.Marshal(payload)
}

func readToken(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	token := strings.TrimSpace(string(raw))
	if token == "" {
		return "", errors.New("empty token")
	}
	return token, nil
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
