package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestActionRoute(t *testing.T) {
	method, path, err := actionRoute("schema", "established", 20*time.Second, "")
	if err != nil {
		t.Fatalf("action route: %v", err)
	}
	if method != http.MethodGet || path != "/v1/helper/schema" {
		t.Fatalf("unexpected route: %s %s", method, path)
	}
	if _, _, err := actionRoute("bad.action", "established", 20*time.Second, ""); err == nil {
		t.Fatalf("expected unknown action error")
	}

	method, path, err = actionRoute("lease.acquire", "established", 15*time.Second, "")
	if err != nil {
		t.Fatalf("lease.acquire route: %v", err)
	}
	if method != http.MethodPost || path != "/v1/helper/lease.acquire" {
		t.Fatalf("unexpected lease.acquire route: %s %s", method, path)
	}
	method, path, err = actionRoute("lease.ensure", "established", 15*time.Second, "")
	if err != nil {
		t.Fatalf("lease.ensure route: %v", err)
	}
	if method != "" || path != "" {
		t.Fatalf("unexpected lease.ensure route: %q %q", method, path)
	}
	method, path, err = actionRoute("bridge.startup", "established", 15*time.Second, "")
	if err != nil {
		t.Fatalf("bridge.startup route: %v", err)
	}
	if method != "" || path != "" {
		t.Fatalf("unexpected bridge.startup route: %q %q", method, path)
	}
	method, path, err = actionRoute("contract.check", "established", 15*time.Second, "")
	if err != nil {
		t.Fatalf("contract.check route: %v", err)
	}
	if method != "" || path != "" {
		t.Fatalf("unexpected contract.check route: %q %q", method, path)
	}
	method, path, err = actionRoute("bridge.shutdown", "established", 15*time.Second, "")
	if err != nil {
		t.Fatalf("bridge.shutdown route: %v", err)
	}
	if method != "" || path != "" {
		t.Fatalf("unexpected bridge.shutdown route: %q %q", method, path)
	}
	method, path, err = actionRoute("bridge.reconcile", "established", 15*time.Second, "")
	if err != nil {
		t.Fatalf("bridge.reconcile route: %v", err)
	}
	if method != "" || path != "" {
		t.Fatalf("unexpected bridge.reconcile route: %q %q", method, path)
	}
	method, path, err = actionRoute("bridge.autopilot", "established", 15*time.Second, "")
	if err != nil {
		t.Fatalf("bridge.autopilot route: %v", err)
	}
	if method != "" || path != "" {
		t.Fatalf("unexpected bridge.autopilot route: %q %q", method, path)
	}
	method, path, err = actionRoute("bridge.autopilot.once", "established", 15*time.Second, "")
	if err != nil {
		t.Fatalf("bridge.autopilot.once route: %v", err)
	}
	if method != "" || path != "" {
		t.Fatalf("unexpected bridge.autopilot.once route: %q %q", method, path)
	}
	method, path, err = actionRoute("bridge.autopilot.daemon", "established", 15*time.Second, "")
	if err != nil {
		t.Fatalf("bridge.autopilot.daemon route: %v", err)
	}
	if method != "" || path != "" {
		t.Fatalf("unexpected bridge.autopilot.daemon route: %q %q", method, path)
	}
	method, path, err = actionRoute("bridge.autopilot.daemon.stream", "established", 15*time.Second, "")
	if err != nil {
		t.Fatalf("bridge.autopilot.daemon.stream route: %v", err)
	}
	if method != "" || path != "" {
		t.Fatalf("unexpected bridge.autopilot.daemon.stream route: %q %q", method, path)
	}
	method, path, err = actionRoute("bridge.status.stream", "established", 15*time.Second, "")
	if err != nil {
		t.Fatalf("bridge.status.stream route: %v", err)
	}
	if method != "" || path != "" {
		t.Fatalf("unexpected bridge.status.stream route: %q %q", method, path)
	}
	method, path, err = actionRoute("links.health.stream", "established", 15*time.Second, "")
	if err != nil {
		t.Fatalf("links.health.stream route: %v", err)
	}
	if method != "" || path != "" {
		t.Fatalf("unexpected links.health.stream route: %q %q", method, path)
	}

	method, path, err = actionRoute("lease.status", "established", 15*time.Second, "")
	if err != nil {
		t.Fatalf("lease.status route: %v", err)
	}
	if method != http.MethodGet || path != "/v1/helper/lease.status" {
		t.Fatalf("unexpected lease.status route: %s %s", method, path)
	}

	method, path, err = actionRoute("lease.heartbeat", "established", 15*time.Second, "")
	if err != nil {
		t.Fatalf("lease.heartbeat route: %v", err)
	}
	if method != http.MethodPost || path != "/v1/helper/lease.heartbeat" {
		t.Fatalf("unexpected lease.heartbeat route: %s %s", method, path)
	}

	method, path, err = actionRoute("lease.takeover", "established", 15*time.Second, "")
	if err != nil {
		t.Fatalf("lease.takeover route: %v", err)
	}
	if method != http.MethodPost || path != "/v1/helper/lease.takeover" {
		t.Fatalf("unexpected lease.takeover route: %s %s", method, path)
	}

	method, path, err = actionRoute("wait", "established", 15*time.Second, "")
	if err != nil {
		t.Fatalf("wait route: %v", err)
	}
	if method != http.MethodGet {
		t.Fatalf("unexpected wait method: %s", method)
	}
	if path != "/v1/helper/wait?state=established&timeout=15s" {
		t.Fatalf("unexpected wait path: %s", path)
	}

	method, path, err = actionRoute("events", "established", 15*time.Second, "")
	if err != nil {
		t.Fatalf("events route: %v", err)
	}
	if method != http.MethodGet || path != "/v1/helper/events" {
		t.Fatalf("unexpected events route: %s %s", method, path)
	}

	method, path, err = actionRoute("bootstrap.validate", "established", 15*time.Second, "")
	if err != nil {
		t.Fatalf("validate route: %v", err)
	}
	if method != http.MethodPost || path != "/v1/helper/bootstrap.validate" {
		t.Fatalf("unexpected validate route: %s %s", method, path)
	}
	method, path, err = actionRoute("profile.apply", "established", 15*time.Second, "")
	if err != nil {
		t.Fatalf("profile.apply route: %v", err)
	}
	if method != http.MethodPost || path != "/v1/helper/profile.apply" {
		t.Fatalf("unexpected profile.apply route: %s %s", method, path)
	}
	method, path, err = actionRoute("profile.current", "established", 15*time.Second, "")
	if err != nil {
		t.Fatalf("profile.current route: %v", err)
	}
	if method != http.MethodGet || path != "/v1/helper/profile.current" {
		t.Fatalf("unexpected profile.current route: %s %s", method, path)
	}

	method, path, err = actionRoute("links", "established", 15*time.Second, "")
	if err != nil {
		t.Fatalf("links route: %v", err)
	}
	if method != http.MethodGet || path != "/v1/helper/links" {
		t.Fatalf("unexpected links route: %s %s", method, path)
	}

	if _, _, err = actionRoute("link.read", "established", 15*time.Second, ""); err == nil {
		t.Fatalf("expected link.read to require -link-id")
	}
	method, path, err = actionRoute("link.read", "established", 15*time.Second, "client:dev-01:default")
	if err != nil {
		t.Fatalf("link.read route: %v", err)
	}
	if method != http.MethodGet || path != "/v1/helper/links/client:dev-01:default" {
		t.Fatalf("unexpected link.read route: %s %s", method, path)
	}
	if _, _, err = actionRoute("link.reconnect", "established", 15*time.Second, ""); err == nil {
		t.Fatalf("expected link.reconnect to require -link-id")
	}
	method, path, err = actionRoute("link.reconnect", "established", 15*time.Second, "client:dev-01:default")
	if err != nil {
		t.Fatalf("link.reconnect route: %v", err)
	}
	if method != http.MethodPost || path != "/v1/helper/links/client:dev-01:default/reconnect" {
		t.Fatalf("unexpected link.reconnect route: %s %s", method, path)
	}
	method, path, err = actionRoute("link.drain", "established", 15*time.Second, "client:dev-01:default")
	if err != nil {
		t.Fatalf("link.drain route: %v", err)
	}
	if method != http.MethodPost || path != "/v1/helper/links/client:dev-01:default/drain" {
		t.Fatalf("unexpected link.drain route: %s %s", method, path)
	}
	method, path, err = actionRoute("link.resume", "established", 15*time.Second, "client:dev-01:default")
	if err != nil {
		t.Fatalf("link.resume route: %v", err)
	}
	if method != http.MethodPost || path != "/v1/helper/links/client:dev-01:default/resume" {
		t.Fatalf("unexpected link.resume route: %s %s", method, path)
	}
	method, path, err = actionRoute("link.gateway.select", "established", 15*time.Second, "client:dev-01:default")
	if err != nil {
		t.Fatalf("link.gateway.select route: %v", err)
	}
	if method != http.MethodPost || path != "/v1/helper/links/client:dev-01:default/gateway.select" {
		t.Fatalf("unexpected link.gateway.select route: %s %s", method, path)
	}
	method, path, err = actionRoute("link.failover", "established", 15*time.Second, "client:dev-01:default")
	if err != nil {
		t.Fatalf("link.failover route: %v", err)
	}
	if method != "" || path != "" {
		t.Fatalf("unexpected link.failover route: %q %q", method, path)
	}
	method, path, err = actionRoute("security.evaluate", "established", 15*time.Second, "")
	if err != nil {
		t.Fatalf("security.evaluate route: %v", err)
	}
	if method != http.MethodPost || path != "/v1/helper/security.evaluate" {
		t.Fatalf("unexpected security.evaluate route: %s %s", method, path)
	}
	method, path, err = actionRoute("security.reputation.upsert", "established", 15*time.Second, "")
	if err != nil {
		t.Fatalf("security.reputation.upsert route: %v", err)
	}
	if method != http.MethodPost || path != "/v1/helper/security.reputation.upsert" {
		t.Fatalf("unexpected security.reputation.upsert route: %s %s", method, path)
	}
	method, path, err = actionRoute("security.policy.upsert", "established", 15*time.Second, "")
	if err != nil {
		t.Fatalf("security.policy.upsert route: %v", err)
	}
	if method != http.MethodPost || path != "/v1/helper/security.policy.upsert" {
		t.Fatalf("unexpected security.policy.upsert route: %s %s", method, path)
	}
	method, path, err = actionRoute("security.policy.get", "established", 15*time.Second, "")
	if err != nil {
		t.Fatalf("security.policy.get route: %v", err)
	}
	if method != http.MethodGet || path != "/v1/helper/security.policy.get" {
		t.Fatalf("unexpected security.policy.get route: %s %s", method, path)
	}
	method, path, err = actionRoute("security.policy.rollout", "established", 15*time.Second, "")
	if err != nil {
		t.Fatalf("security.policy.rollout route: %v", err)
	}
	if method != http.MethodPost || path != "/v1/helper/security.policy.rollout" {
		t.Fatalf("unexpected security.policy.rollout route: %s %s", method, path)
	}
	method, path, err = actionRoute("security.corporate-allow.upsert", "established", 15*time.Second, "")
	if err != nil {
		t.Fatalf("security.corporate-allow.upsert route: %v", err)
	}
	if method != http.MethodPost || path != "/v1/helper/security.corporate-allow.upsert" {
		t.Fatalf("unexpected security.corporate-allow.upsert route: %s %s", method, path)
	}
	method, path, err = actionRoute("security.audit", "established", 15*time.Second, "")
	if err != nil {
		t.Fatalf("security.audit route: %v", err)
	}
	if method != http.MethodGet || path != "/v1/helper/security.audit" {
		t.Fatalf("unexpected security.audit route: %s %s", method, path)
	}
	method, path, err = actionRoute("security.signal.ingest", "established", 15*time.Second, "")
	if err != nil {
		t.Fatalf("security.signal.ingest route: %v", err)
	}
	if method != http.MethodPost || path != "/v1/helper/security.signal.ingest" {
		t.Fatalf("unexpected security.signal.ingest route: %s %s", method, path)
	}
	method, path, err = actionRoute("security.signal.ingest.recent", "established", 15*time.Second, "")
	if err != nil {
		t.Fatalf("security.signal.ingest.recent route: %v", err)
	}
	if method != http.MethodGet || path != "/v1/helper/security.signal.ingest.recent" {
		t.Fatalf("unexpected security.signal.ingest.recent route: %s %s", method, path)
	}
}

func TestValidateBridgeBootstrapContract(t *testing.T) {
	schema := schemaResponse{
		APIVersion: "v1",
		Bootstrap: schemaBootstrapContract{
			RequiredBootstrapFields:  []string{"clientID", "serverStaticPub"},
			GatewayPoolSupported:     true,
			GatewayPolicySupported:   false,
			RekeyPolicySupported:     false,
			ProfileBundleSupported:   false,
			SecurityProfileSupported: false,
		},
	}
	payload := []byte(`{
	  "profileBootstrap":{
	    "clientID":"00112233445566778899aabbccddeeff",
	    "serverStaticPub":"MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=",
	    "gateways":[{"gatewayID":"gw-1","endpoints":[{"addr":"10.0.0.1:443"}]}],
	    "gatewayPolicy":{"forceGatewayID":"gw-1"},
	    "rekeyPolicy":{"enabled":true}
	  }
	}`)
	if err := validateBridgeBootstrapContract(schema, payload); err == nil || !strings.Contains(err.Error(), "gatewayPolicy") {
		t.Fatalf("expected gatewayPolicy unsupported error, got %v", err)
	}

	schema.Bootstrap.GatewayPolicySupported = true
	if err := validateBridgeBootstrapContract(schema, payload); err == nil || !strings.Contains(err.Error(), "rekeyPolicy") {
		t.Fatalf("expected rekeyPolicy unsupported error, got %v", err)
	}
	schema.Bootstrap.RekeyPolicySupported = true
	if err := validateBridgeBootstrapContract(schema, payload); err != nil {
		t.Fatalf("expected valid payload, got %v", err)
	}

	badPayload := []byte(`{"profileBootstrap":{"serverStaticPub":"x"}}`)
	if err := validateBridgeBootstrapContract(schema, badPayload); err == nil || !strings.Contains(err.Error(), "clientID") {
		t.Fatalf("expected missing clientID error, got %v", err)
	}

	profilePayload := []byte(`{
	  "profileBootstrap":{
	    "clientID":"00112233445566778899aabbccddeeff",
	    "serverStaticPub":"MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=",
	    "securityProfile":"high_risk",
	    "routing":{"strategy":"geo_split"}
	  }
	}`)
	if err := validateBridgeBootstrapContract(schema, profilePayload); err == nil || !strings.Contains(err.Error(), "profile bundle") {
		t.Fatalf("expected profile bundle unsupported error, got %v", err)
	}
	schema.Bootstrap.ProfileBundleSupported = true
	if err := validateBridgeBootstrapContract(schema, profilePayload); err == nil || !strings.Contains(err.Error(), "securityProfile") {
		t.Fatalf("expected security profile unsupported error, got %v", err)
	}
	schema.Bootstrap.SecurityProfileSupported = true
	if err := validateBridgeBootstrapContract(schema, profilePayload); err != nil {
		t.Fatalf("expected valid security profile payload, got %v", err)
	}
}

func TestValidateSchemaContractRequirements(t *testing.T) {
	schema := schemaResponse{
		APIVersion:           "v1",
		GatewayPoolVersion:   "2026-04-10",
		ProfileBundleVersion: "2026-04-14",
		Bootstrap: schemaBootstrapContract{
			SchemaVersion:          "2026-04-10",
			GatewayPoolSupported:   false,
			ProfileBundleSupported: false,
		},
	}
	if err := validateSchemaContractRequirements(schema, false, false, false, false, false, "", "", ""); err != nil {
		t.Fatalf("unexpected error without strict requirement: %v", err)
	}
	if err := validateSchemaContractRequirements(schema, true, false, false, false, false, "", "", ""); err == nil {
		t.Fatalf("expected error when require-gateway-pool=true and schema unsupported")
	}
	schema.Bootstrap.GatewayPoolSupported = true
	schema.Bootstrap.GatewayPolicySupported = false
	schema.Bootstrap.RekeyPolicySupported = false
	if err := validateSchemaContractRequirements(schema, true, false, false, false, false, "2026-04-10", "", ""); err != nil {
		t.Fatalf("expected pass when gateway pool is supported: %v", err)
	}
	if err := validateSchemaContractRequirements(schema, true, true, false, false, false, "2026-04-10", "", ""); err == nil {
		t.Fatalf("expected error when require-gateway-policy=true and schema unsupported")
	}
	schema.Bootstrap.GatewayPolicySupported = true
	if err := validateSchemaContractRequirements(schema, true, true, true, false, false, "2026-04-10", "2026-04-10", ""); err == nil {
		t.Fatalf("expected error when require-rekey-policy=true and schema unsupported")
	}
	schema.Bootstrap.RekeyPolicySupported = true
	if err := validateSchemaContractRequirements(schema, true, true, true, false, false, "2026-04-10", "2026-04-10", ""); err != nil {
		t.Fatalf("expected pass for matching schema version: %v", err)
	}
	if err := validateSchemaContractRequirements(schema, true, true, true, false, false, "2026-04-11", "2026-04-10", ""); err == nil {
		t.Fatalf("expected error for gateway pool version mismatch")
	}
	if err := validateSchemaContractRequirements(schema, true, true, true, false, false, "2026-04-10", "2026-04-11", ""); err == nil {
		t.Fatalf("expected error for schema version mismatch")
	}
	if err := validateSchemaContractRequirements(schema, true, true, true, true, false, "2026-04-10", "2026-04-10", "2026-04-14"); err == nil {
		t.Fatalf("expected error when require-profile-bundle=true and schema unsupported")
	}
	schema.Bootstrap.ProfileBundleSupported = true
	if err := validateSchemaContractRequirements(schema, true, true, true, true, false, "2026-04-10", "2026-04-10", "2026-04-14"); err != nil {
		t.Fatalf("expected pass for profile bundle support: %v", err)
	}
	if err := validateSchemaContractRequirements(schema, true, true, true, true, true, "2026-04-10", "2026-04-10", "2026-04-14"); err == nil {
		t.Fatalf("expected error for missing security profile support")
	}
	schema.Bootstrap.SecurityProfileSupported = true
	if err := validateSchemaContractRequirements(schema, true, true, true, true, true, "2026-04-10", "2026-04-10", "2026-04-14"); err != nil {
		t.Fatalf("expected pass for security profile support: %v", err)
	}
	if err := validateSchemaContractRequirements(schema, true, true, true, true, true, "2026-04-10", "2026-04-10", "2026-04-15"); err == nil {
		t.Fatalf("expected error for profile bundle version mismatch")
	}
}

func TestDefaultPayloadForAction(t *testing.T) {
	raw, err := defaultPayloadForAction("lease.acquire", "owner-1", "", "", 30*time.Second, "")
	if err != nil {
		t.Fatalf("lease.acquire payload: %v", err)
	}
	if len(raw) == 0 {
		t.Fatalf("expected acquire payload")
	}
	if _, err := defaultPayloadForAction("lease.renew", "", "", "", 30*time.Second, ""); err == nil {
		t.Fatalf("expected renew lease-id error")
	}
	if _, err := defaultPayloadForAction("lease.heartbeat", "", "", "", 30*time.Second, ""); err == nil {
		t.Fatalf("expected heartbeat lease-id error")
	}
	if _, err := defaultPayloadForAction("lease.takeover", "owner-2", "", "", 30*time.Second, ""); err == nil {
		t.Fatalf("expected takeover prev-lease-id error")
	}
	raw, err = defaultPayloadForAction("lease.takeover", "owner-2", "", "prev-lease", 30*time.Second, "")
	if err != nil {
		t.Fatalf("lease.takeover payload: %v", err)
	}
	if len(raw) == 0 {
		t.Fatalf("expected takeover payload")
	}
	if _, err := defaultPayloadForAction("link.gateway.select", "", "", "", 30*time.Second, ""); err == nil {
		t.Fatalf("expected gateway.select gateway-id error")
	}
	raw, err = defaultPayloadForAction("link.gateway.select", "", "", "", 30*time.Second, "gw-1")
	if err != nil {
		t.Fatalf("link.gateway.select payload: %v", err)
	}
	if len(raw) == 0 {
		t.Fatalf("expected gateway.select payload")
	}
}

func TestEnsureSchema(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/helper/schema" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"apiVersion":"v1"}`))
	}))
	defer srv.Close()

	client, baseURL, err := buildClient(srv.URL, "", 2*time.Second)
	if err != nil {
		t.Fatalf("build client: %v", err)
	}
	if err := ensureSchema(client, baseURL, ""); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}
}

func TestRunLeaseKeepalive(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/helper/schema" {
			_, _ = w.Write([]byte(`{"apiVersion":"v1"}`))
			return
		}
		if r.URL.Path != "/v1/helper/lease.heartbeat" {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected method: %s", r.Method)
		}
		if strings.TrimSpace(r.Header.Get("X-Helper-Lease-ID")) == "" {
			t.Fatalf("missing lease header")
		}
		atomic.AddInt32(&calls, 1)
		_, _ = w.Write([]byte(`{"active":true}`))
	}))
	defer srv.Close()

	client, baseURL, err := buildClient(srv.URL, "", 2*time.Second)
	if err != nil {
		t.Fatalf("build client: %v", err)
	}
	if err := runLeaseKeepalive(context.Background(), client, baseURL, "", "lease-1", "rid", 60*time.Second, 10*time.Millisecond, 25*time.Millisecond, false); err != nil {
		t.Fatalf("run keepalive: %v", err)
	}
	if got := atomic.LoadInt32(&calls); got < 2 {
		t.Fatalf("expected at least 2 heartbeat calls, got %d", got)
	}
}

func TestRunLinkFailover(t *testing.T) {
	var sawSelect int32
	var sawReconnect int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/helper/links/client:dev-01:default/gateway.select":
			atomic.AddInt32(&sawSelect, 1)
			if got := strings.TrimSpace(r.Header.Get("X-Request-ID")); got != "rid-failover-select" {
				t.Fatalf("unexpected request id for gateway.select: %q", got)
			}
			_, _ = w.Write([]byte(`{"ok":true,"action":"gateway.select"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/helper/links/client:dev-01:default/reconnect":
			atomic.AddInt32(&sawReconnect, 1)
			if got := strings.TrimSpace(r.Header.Get("X-Request-ID")); got != "rid-failover-reconnect" {
				t.Fatalf("unexpected request id for reconnect: %q", got)
			}
			_, _ = w.Write([]byte(`{"ok":true,"action":"reconnect"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	client, baseURL, err := buildClient(srv.URL, "", 2*time.Second)
	if err != nil {
		t.Fatalf("build client: %v", err)
	}
	if err := runLinkFailover(client, baseURL, "", "client:dev-01:default", "gw-b", "rid", ""); err != nil {
		t.Fatalf("runLinkFailover: %v", err)
	}
	if atomic.LoadInt32(&sawSelect) != 1 || atomic.LoadInt32(&sawReconnect) != 1 {
		t.Fatalf("unexpected call counters select=%d reconnect=%d", sawSelect, sawReconnect)
	}
}

func TestRunLinkFailoverRequiresIDs(t *testing.T) {
	if err := runLinkFailover(&http.Client{}, "http://127.0.0.1", "", "", "gw-b", "rid", ""); err == nil {
		t.Fatalf("expected link-id validation error")
	}
	if err := runLinkFailover(&http.Client{}, "http://127.0.0.1", "", "client:dev-01:default", "", "rid", ""); err == nil {
		t.Fatalf("expected gateway-id validation error")
	}
}

func TestEnsureLeaseAcquireWhenNoActiveLease(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/helper/schema":
			_, _ = w.Write([]byte(`{"apiVersion":"v1","bootstrap":{"requiredBootstrapFields":["clientID","serverStaticPub"],"gatewayPoolSupported":true,"gatewayPolicySupported":true}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/helper/status":
			_, _ = w.Write([]byte(`{"lease":{"active":false}}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/helper/lease.acquire":
			_, _ = w.Write([]byte(`{"active":true,"leaseId":"lease-a","owner":"owner-a"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	client, baseURL, err := buildClient(srv.URL, "", 2*time.Second)
	if err != nil {
		t.Fatalf("build client: %v", err)
	}
	out, err := ensureLease(client, baseURL, "", "owner-a", 60*time.Second, true, "rid")
	if err != nil {
		t.Fatalf("ensureLease: %v", err)
	}
	if !out.Active || out.LeaseID != "lease-a" || out.Owner != "owner-a" {
		t.Fatalf("unexpected ensure result: %+v", out)
	}
}

func TestEnsureLeaseTakeoverWhenOtherOwner(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/helper/status":
			_, _ = w.Write([]byte(`{"lease":{"active":true,"leaseId":"lease-old","owner":"owner-old"}}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/helper/lease.takeover":
			_, _ = w.Write([]byte(`{"active":true,"leaseId":"lease-new","owner":"owner-new"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	client, baseURL, err := buildClient(srv.URL, "", 2*time.Second)
	if err != nil {
		t.Fatalf("build client: %v", err)
	}
	out, err := ensureLease(client, baseURL, "", "owner-new", 60*time.Second, true, "rid")
	if err != nil {
		t.Fatalf("ensureLease: %v", err)
	}
	if out.LeaseID != "lease-new" || out.Owner != "owner-new" {
		t.Fatalf("unexpected ensure result: %+v", out)
	}
}

func TestEnsureLeaseRejectsWithoutTakeover(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/v1/helper/status" {
			_, _ = w.Write([]byte(`{"lease":{"active":true,"leaseId":"lease-old","owner":"owner-old"}}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	client, baseURL, err := buildClient(srv.URL, "", 2*time.Second)
	if err != nil {
		t.Fatalf("build client: %v", err)
	}
	if _, err := ensureLease(client, baseURL, "", "owner-new", 60*time.Second, false, "rid"); err == nil {
		t.Fatalf("expected ensureLease error when takeover disabled")
	}
}

func TestRunBridgeStartup(t *testing.T) {
	var sawAcquire int32
	var sawValidate int32
	var sawApply int32
	var sawStart int32
	var sawWait int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/helper/schema":
			_, _ = w.Write([]byte(`{"apiVersion":"v1","bootstrap":{"requiredBootstrapFields":["clientID","serverStaticPub"],"gatewayPoolSupported":true,"gatewayPolicySupported":true}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/helper/status":
			_, _ = w.Write([]byte(`{"lease":{"active":false}}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/helper/lease.acquire":
			atomic.AddInt32(&sawAcquire, 1)
			_, _ = w.Write([]byte(`{"active":true,"leaseId":"lease-bridge","owner":"bridge-owner"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/helper/bootstrap.validate":
			if strings.TrimSpace(r.Header.Get("X-Helper-Lease-ID")) != "lease-bridge" {
				t.Fatalf("missing lease header on validate")
			}
			atomic.AddInt32(&sawValidate, 1)
			_, _ = w.Write([]byte(`{"ok":true}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/helper/bootstrap.apply":
			if strings.TrimSpace(r.Header.Get("X-Helper-Lease-ID")) != "lease-bridge" {
				t.Fatalf("missing lease header on apply")
			}
			atomic.AddInt32(&sawApply, 1)
			_, _ = w.Write([]byte(`{"ok":true}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/helper/tunnel.start":
			if strings.TrimSpace(r.Header.Get("X-Helper-Lease-ID")) != "lease-bridge" {
				t.Fatalf("missing lease header on start")
			}
			atomic.AddInt32(&sawStart, 1)
			_, _ = w.Write([]byte(`{"ok":true}`))
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.RequestURI(), "/v1/helper/wait?"):
			atomic.AddInt32(&sawWait, 1)
			_, _ = w.Write([]byte(`{"running":true,"lastState":"established"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	client, baseURL, err := buildClient(srv.URL, "", 2*time.Second)
	if err != nil {
		t.Fatalf("build client: %v", err)
	}
	out, err := runBridgeStartup(client, baseURL, "", []byte(`{"profileBootstrap":{"addr":"127.0.0.1:8443","serverName":"localhost","clientID":"00112233445566778899aabbccddeeff","serverStaticPub":"MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="}}`), "bridge-owner", 60*time.Second, true, "established", 20*time.Second, true, "rid")
	if err != nil {
		t.Fatalf("runBridgeStartup: %v", err)
	}
	if !out.OK || out.Lease.LeaseID != "lease-bridge" {
		t.Fatalf("unexpected bridge startup result: %+v", out)
	}
	if atomic.LoadInt32(&sawAcquire) != 1 || atomic.LoadInt32(&sawValidate) != 1 || atomic.LoadInt32(&sawApply) != 1 || atomic.LoadInt32(&sawStart) != 1 || atomic.LoadInt32(&sawWait) != 1 {
		t.Fatalf("unexpected call counters acquire=%d validate=%d apply=%d start=%d wait=%d", sawAcquire, sawValidate, sawApply, sawStart, sawWait)
	}
}

func TestRunBridgeStartupRejectsUnsupportedGatewayPolicyContract(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/helper/schema":
			_, _ = w.Write([]byte(`{"apiVersion":"v1","bootstrap":{"requiredBootstrapFields":["clientID","serverStaticPub"],"gatewayPoolSupported":true,"gatewayPolicySupported":false}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/helper/status":
			_, _ = w.Write([]byte(`{"lease":{"active":false}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	client, baseURL, err := buildClient(srv.URL, "", 2*time.Second)
	if err != nil {
		t.Fatalf("build client: %v", err)
	}
	payload := []byte(`{
	  "profileBootstrap":{
	    "clientID":"00112233445566778899aabbccddeeff",
	    "serverStaticPub":"MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=",
	    "gatewayPolicy":{"forceGatewayID":"gw-1"}
	  }
	}`)
	_, err = runBridgeStartup(client, baseURL, "", payload, "bridge-owner", 60*time.Second, true, "established", 20*time.Second, true, "rid")
	if err == nil || !strings.Contains(err.Error(), "gatewayPolicy") {
		t.Fatalf("expected gatewayPolicy contract error, got %v", err)
	}
}

func TestRunBridgeShutdownSuccess(t *testing.T) {
	var sawAcquire int32
	var sawStop int32
	var sawRelease int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/helper/status":
			_, _ = w.Write([]byte(`{"lease":{"active":false}}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/helper/lease.acquire":
			atomic.AddInt32(&sawAcquire, 1)
			_, _ = w.Write([]byte(`{"active":true,"leaseId":"lease-shutdown","owner":"bridge-owner"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/helper/tunnel.stop":
			atomic.AddInt32(&sawStop, 1)
			_, _ = w.Write([]byte(`{"ok":true}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/helper/lease.release":
			atomic.AddInt32(&sawRelease, 1)
			_, _ = w.Write([]byte(`{"active":false}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	client, baseURL, err := buildClient(srv.URL, "", 2*time.Second)
	if err != nil {
		t.Fatalf("build client: %v", err)
	}
	out, err := runBridgeShutdown(client, baseURL, "", "bridge-owner", 60*time.Second, true, "rid", true)
	if err != nil {
		t.Fatalf("runBridgeShutdown: %v", err)
	}
	if !out.OK || !out.Stopped || !out.Released {
		t.Fatalf("unexpected shutdown result: %+v", out)
	}
	if atomic.LoadInt32(&sawAcquire) != 1 || atomic.LoadInt32(&sawStop) != 1 || atomic.LoadInt32(&sawRelease) != 1 {
		t.Fatalf("unexpected call counters acquire=%d stop=%d release=%d", sawAcquire, sawStop, sawRelease)
	}
}

func TestRunBridgeShutdownBestEffort(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/helper/status":
			_, _ = w.Write([]byte(`{"lease":{"active":false}}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/helper/lease.acquire":
			_, _ = w.Write([]byte(`{"active":true,"leaseId":"lease-shutdown","owner":"bridge-owner"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/helper/tunnel.stop":
			http.Error(w, "stop failed", http.StatusConflict)
		case r.Method == http.MethodPost && r.URL.Path == "/v1/helper/lease.release":
			_, _ = w.Write([]byte(`{"active":false}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	client, baseURL, err := buildClient(srv.URL, "", 2*time.Second)
	if err != nil {
		t.Fatalf("build client: %v", err)
	}
	out, err := runBridgeShutdown(client, baseURL, "", "bridge-owner", 60*time.Second, true, "rid", true)
	if err != nil {
		t.Fatalf("runBridgeShutdown: %v", err)
	}
	if out.OK {
		t.Fatalf("expected not-ok result in best-effort partial failure")
	}
	if out.StopError == "" {
		t.Fatalf("expected stop error to be populated")
	}
	if !out.Released {
		t.Fatalf("expected release to succeed despite stop failure")
	}
}

func TestRunBridgeReconcileStartupNeeded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/helper/status":
			_, _ = w.Write([]byte(`{"running":false,"lastState":"idle","lease":{"active":false}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/helper/health":
			_, _ = w.Write([]byte(`{"live":true,"ready":false,"running":false,"state":"idle"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	client, baseURL, err := buildClient(srv.URL, "", 2*time.Second)
	if err != nil {
		t.Fatalf("build client: %v", err)
	}
	out, err := runBridgeReconcile(client, baseURL, "", "bridge-owner", 60*time.Second, true, false, "rid")
	if err != nil {
		t.Fatalf("runBridgeReconcile: %v", err)
	}
	if out.Plan != "startup-needed" {
		t.Fatalf("unexpected plan: %s", out.Plan)
	}
}

func TestRunBridgeReconcileRunningOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/helper/status":
			_, _ = w.Write([]byte(`{"running":true,"lastState":"established","lease":{"active":true,"leaseId":"l1","owner":"bridge-owner"}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/helper/health":
			_, _ = w.Write([]byte(`{"live":true,"ready":true,"running":true,"state":"established"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	client, baseURL, err := buildClient(srv.URL, "", 2*time.Second)
	if err != nil {
		t.Fatalf("build client: %v", err)
	}
	out, err := runBridgeReconcile(client, baseURL, "", "bridge-owner", 60*time.Second, true, false, "rid")
	if err != nil {
		t.Fatalf("runBridgeReconcile: %v", err)
	}
	if out.Plan != "running-ok" {
		t.Fatalf("unexpected plan: %s", out.Plan)
	}
}

func TestRunBridgeReconcileRestartNeeded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/helper/status":
			_, _ = w.Write([]byte(`{"running":true,"lastState":"reconnecting","lease":{"active":true,"leaseId":"l1","owner":"bridge-owner"}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/helper/health":
			_, _ = w.Write([]byte(`{"live":true,"ready":false,"running":true,"state":"reconnecting"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	client, baseURL, err := buildClient(srv.URL, "", 2*time.Second)
	if err != nil {
		t.Fatalf("build client: %v", err)
	}
	out, err := runBridgeReconcile(client, baseURL, "", "bridge-owner", 60*time.Second, true, false, "rid")
	if err != nil {
		t.Fatalf("runBridgeReconcile: %v", err)
	}
	if out.Plan != "restart-needed" {
		t.Fatalf("unexpected plan: %s", out.Plan)
	}
}

func TestRunBridgeAutopilotStartupThenRunningOK(t *testing.T) {
	var phase int32
	var startupCalls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/helper/schema":
			_, _ = w.Write([]byte(`{"apiVersion":"v1","bootstrap":{"requiredBootstrapFields":["clientID","serverStaticPub"],"gatewayPoolSupported":true,"gatewayPolicySupported":true}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/helper/status":
			if atomic.LoadInt32(&phase) == 0 {
				_, _ = w.Write([]byte(`{"running":false,"lastState":"idle","lease":{"active":false}}`))
			} else {
				_, _ = w.Write([]byte(`{"running":true,"lastState":"established","lease":{"active":true,"leaseId":"lease-1","owner":"bridge-owner"}}`))
			}
		case r.Method == http.MethodGet && r.URL.Path == "/v1/helper/health":
			if atomic.LoadInt32(&phase) == 0 {
				_, _ = w.Write([]byte(`{"live":true,"ready":false,"running":false,"state":"idle"}`))
			} else {
				_, _ = w.Write([]byte(`{"live":true,"ready":true,"running":true,"state":"established"}`))
			}
		case r.Method == http.MethodPost && r.URL.Path == "/v1/helper/lease.acquire":
			_, _ = w.Write([]byte(`{"active":true,"leaseId":"lease-1","owner":"bridge-owner"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/helper/lease.heartbeat":
			_, _ = w.Write([]byte(`{"active":true,"leaseId":"lease-1","owner":"bridge-owner"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/helper/bootstrap.validate":
			atomic.AddInt32(&startupCalls, 1)
			_, _ = w.Write([]byte(`{"ok":true}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/helper/bootstrap.apply":
			_, _ = w.Write([]byte(`{"ok":true}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/helper/tunnel.start":
			atomic.StoreInt32(&phase, 1)
			_, _ = w.Write([]byte(`{"ok":true}`))
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.RequestURI(), "/v1/helper/wait?"):
			_, _ = w.Write([]byte(`{"running":true,"lastState":"established"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	client, baseURL, err := buildClient(srv.URL, "", 2*time.Second)
	if err != nil {
		t.Fatalf("build client: %v", err)
	}
	out, err := runBridgeAutopilot(
		client, baseURL, "", []byte(`{"profileBootstrap":{"addr":"127.0.0.1:8443","serverName":"localhost","clientID":"00112233445566778899aabbccddeeff","serverStaticPub":"MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="}}`),
		"bridge-owner", 60*time.Second, true, true, true, "established", 2*time.Second, true, true, 3, "rid",
	)
	if err != nil {
		t.Fatalf("runBridgeAutopilot: %v", err)
	}
	if !out.OK || out.FinalPlan != "running-ok" {
		t.Fatalf("unexpected autopilot result: %+v", out)
	}
	if got := atomic.LoadInt32(&startupCalls); got != 1 {
		t.Fatalf("expected one startup call, got %d", got)
	}
}

func TestRunBridgeAutopilotRestartThenRunningOK(t *testing.T) {
	var phase int32
	var shutdownCalls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/helper/schema":
			_, _ = w.Write([]byte(`{"apiVersion":"v1","bootstrap":{"requiredBootstrapFields":["clientID","serverStaticPub"],"gatewayPoolSupported":true,"gatewayPolicySupported":true}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/helper/status":
			p := atomic.LoadInt32(&phase)
			switch p {
			case 0:
				_, _ = w.Write([]byte(`{"running":true,"lastState":"reconnecting","lease":{"active":false}}`))
			default:
				_, _ = w.Write([]byte(`{"running":true,"lastState":"established","lease":{"active":true,"leaseId":"lease-2","owner":"bridge-owner"}}`))
			}
		case r.Method == http.MethodGet && r.URL.Path == "/v1/helper/health":
			if atomic.LoadInt32(&phase) == 0 {
				_, _ = w.Write([]byte(`{"live":true,"ready":false,"running":true,"state":"reconnecting"}`))
			} else {
				_, _ = w.Write([]byte(`{"live":true,"ready":true,"running":true,"state":"established"}`))
			}
		case r.Method == http.MethodPost && r.URL.Path == "/v1/helper/lease.acquire":
			_, _ = w.Write([]byte(`{"active":true,"leaseId":"lease-2","owner":"bridge-owner"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/helper/lease.heartbeat":
			_, _ = w.Write([]byte(`{"active":true,"leaseId":"lease-2","owner":"bridge-owner"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/helper/tunnel.stop":
			atomic.AddInt32(&shutdownCalls, 1)
			_, _ = w.Write([]byte(`{"ok":true}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/helper/lease.release":
			_, _ = w.Write([]byte(`{"active":false}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/helper/bootstrap.validate":
			_, _ = w.Write([]byte(`{"ok":true}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/helper/bootstrap.apply":
			_, _ = w.Write([]byte(`{"ok":true}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/helper/tunnel.start":
			atomic.StoreInt32(&phase, 1)
			_, _ = w.Write([]byte(`{"ok":true}`))
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.RequestURI(), "/v1/helper/wait?"):
			_, _ = w.Write([]byte(`{"running":true,"lastState":"established"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	client, baseURL, err := buildClient(srv.URL, "", 2*time.Second)
	if err != nil {
		t.Fatalf("build client: %v", err)
	}
	out, err := runBridgeAutopilot(
		client, baseURL, "", []byte(`{"profileBootstrap":{"addr":"127.0.0.1:8443","serverName":"localhost","clientID":"00112233445566778899aabbccddeeff","serverStaticPub":"MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="}}`),
		"bridge-owner", 60*time.Second, true, true, true, "established", 2*time.Second, true, true, 3, "rid",
	)
	if err != nil {
		t.Fatalf("runBridgeAutopilot: %v", err)
	}
	if !out.OK || out.FinalPlan != "running-ok" {
		t.Fatalf("unexpected autopilot result: %+v", out)
	}
	if got := atomic.LoadInt32(&shutdownCalls); got != 1 {
		t.Fatalf("expected one shutdown call, got %d", got)
	}
}

func TestRunBridgeAutopilotRestartDisabled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/helper/status":
			_, _ = w.Write([]byte(`{"running":true,"lastState":"reconnecting","lease":{"active":false}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/helper/health":
			_, _ = w.Write([]byte(`{"live":true,"ready":false,"running":true,"state":"reconnecting"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/helper/lease.acquire":
			_, _ = w.Write([]byte(`{"active":true,"leaseId":"lease-3","owner":"bridge-owner"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	client, baseURL, err := buildClient(srv.URL, "", 2*time.Second)
	if err != nil {
		t.Fatalf("build client: %v", err)
	}
	_, err = runBridgeAutopilot(
		client, baseURL, "", []byte(`{"profileBootstrap":{"addr":"127.0.0.1:8443","serverName":"localhost","clientID":"00112233445566778899aabbccddeeff","serverStaticPub":"MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="}}`),
		"bridge-owner", 60*time.Second, true, true, true, "established", 2*time.Second, true, false, 2, "rid",
	)
	if err == nil {
		t.Fatalf("expected restart-disabled error")
	}
}

func TestRunBridgeAutopilotDaemonLoops(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/helper/status":
			_, _ = w.Write([]byte(`{"running":true,"lastState":"established","lease":{"active":true,"leaseId":"l1","owner":"bridge-owner"}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/helper/health":
			_, _ = w.Write([]byte(`{"live":true,"ready":true,"running":true,"state":"established"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/helper/lease.acquire":
			_, _ = w.Write([]byte(`{"active":true,"leaseId":"l1","owner":"bridge-owner"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/helper/lease.heartbeat":
			_, _ = w.Write([]byte(`{"active":true,"leaseId":"l1","owner":"bridge-owner"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	client, baseURL, err := buildClient(srv.URL, "", 2*time.Second)
	if err != nil {
		t.Fatalf("build client: %v", err)
	}
	var ticks []bridgeAutopilotDaemonTick
	emit := func(t bridgeAutopilotDaemonTick) {
		ticks = append(ticks, t)
	}
	if err := runBridgeAutopilotDaemon(
		context.Background(), emit,
		client, baseURL, "", nil,
		"bridge-owner", 60*time.Second, true,
		false,
		true, "established", 2*time.Second,
		true, true, 2,
		10*time.Millisecond, 35*time.Millisecond, true,
		"rid",
	); err != nil {
		t.Fatalf("runBridgeAutopilotDaemon: %v", err)
	}
	if len(ticks) < 2 {
		t.Fatalf("expected at least 2 daemon ticks, got %d", len(ticks))
	}
}

func TestRunBridgeAutopilotDaemonStopsOnErrorWhenConfigured(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/helper/status":
			_, _ = w.Write([]byte(`{"running":false,"lastState":"idle","lease":{"active":false}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/helper/health":
			_, _ = w.Write([]byte(`{"live":true,"ready":false,"running":false,"state":"idle"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/helper/lease.acquire":
			_, _ = w.Write([]byte(`{"active":true,"leaseId":"l1","owner":"bridge-owner"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	client, baseURL, err := buildClient(srv.URL, "", 2*time.Second)
	if err != nil {
		t.Fatalf("build client: %v", err)
	}
	emit := func(_ bridgeAutopilotDaemonTick) {}
	err = runBridgeAutopilotDaemon(
		context.Background(), emit,
		client, baseURL, "", nil,
		"bridge-owner", 60*time.Second, true,
		false,
		true, "established", 2*time.Second,
		true, true, 2,
		10*time.Millisecond, 50*time.Millisecond, false,
		"rid",
	)
	if err == nil {
		t.Fatalf("expected daemon error when continue-on-error=false and startup needs payload")
	}
}

func TestRunBridgeAutopilotDaemonContinuesOnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/helper/status":
			_, _ = w.Write([]byte(`{"running":false,"lastState":"idle","lease":{"active":false}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/helper/health":
			_, _ = w.Write([]byte(`{"live":true,"ready":false,"running":false,"state":"idle"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/helper/lease.acquire":
			_, _ = w.Write([]byte(`{"active":true,"leaseId":"l1","owner":"bridge-owner"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	client, baseURL, err := buildClient(srv.URL, "", 2*time.Second)
	if err != nil {
		t.Fatalf("build client: %v", err)
	}
	var ticks []bridgeAutopilotDaemonTick
	emit := func(tick bridgeAutopilotDaemonTick) {
		ticks = append(ticks, tick)
	}
	if err := runBridgeAutopilotDaemon(
		context.Background(), emit,
		client, baseURL, "", nil,
		"bridge-owner", 60*time.Second, true,
		false,
		true, "established", 2*time.Second,
		true, true, 2,
		10*time.Millisecond, 35*time.Millisecond, true,
		"rid",
	); err != nil {
		t.Fatalf("runBridgeAutopilotDaemon: %v", err)
	}
	if len(ticks) < 2 {
		t.Fatalf("expected daemon to continue on errors and emit multiple ticks, got %d", len(ticks))
	}
	for i, tick := range ticks {
		if tick.OK {
			t.Fatalf("expected error tick at index %d, got ok=true", i)
		}
		if tick.Error == "" {
			t.Fatalf("expected error message in tick %d", i)
		}
	}
}

func TestRunLeaseKeepaliveReleaseOnCancel(t *testing.T) {
	var heartbeatCalls int32
	var releaseCalls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/helper/lease.heartbeat":
			atomic.AddInt32(&heartbeatCalls, 1)
			_, _ = w.Write([]byte(`{"active":true}`))
		case "/v1/helper/lease.release":
			atomic.AddInt32(&releaseCalls, 1)
			_, _ = w.Write([]byte(`{"active":false}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	client, baseURL, err := buildClient(srv.URL, "", 2*time.Second)
	if err != nil {
		t.Fatalf("build client: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(30 * time.Millisecond)
		cancel()
	}()
	if err := runLeaseKeepalive(ctx, client, baseURL, "", "lease-cancel", "rid", 60*time.Second, 10*time.Millisecond, 5*time.Second, true); err != nil {
		t.Fatalf("run keepalive: %v", err)
	}
	if got := atomic.LoadInt32(&heartbeatCalls); got == 0 {
		t.Fatalf("expected heartbeat calls before cancel")
	}
	if got := atomic.LoadInt32(&releaseCalls); got != 1 {
		t.Fatalf("expected exactly one release call, got %d", got)
	}
}

func TestStreamEvents(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/helper/events" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("event: runtime\n"))
		_, _ = w.Write([]byte("data: {\"state\":\"established\"}\n\n"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		time.Sleep(50 * time.Millisecond)
	}))
	defer srv.Close()

	client, baseURL, err := buildClient(srv.URL, "", 2*time.Second)
	if err != nil {
		t.Fatalf("build client: %v", err)
	}
	if err := streamEvents(client, baseURL, "", 100*time.Millisecond, ""); err != nil {
		t.Fatalf("stream events: %v", err)
	}
}

func TestStreamBridgeAutopilotDaemon(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/helper/bridge.autopilot.daemon.stream" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("event: begin\n"))
		_, _ = w.Write([]byte("data: {\"ok\":true}\n\n"))
		_, _ = w.Write([]byte("event: tick\n"))
		_, _ = w.Write([]byte("data: {\"run\":1,\"ok\":true}\n\n"))
		_, _ = w.Write([]byte("event: done\n"))
		_, _ = w.Write([]byte("data: {\"ok\":true,\"runs\":1}\n\n"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}))
	defer srv.Close()

	client, baseURL, err := buildClient(srv.URL, "", 2*time.Second)
	if err != nil {
		t.Fatalf("build client: %v", err)
	}
	if err := streamBridgeAutopilotDaemon(client, baseURL, "", []byte(`{}`), ""); err != nil {
		t.Fatalf("stream daemon: %v", err)
	}
}

func TestStreamBridgeStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/helper/bridge.status.stream" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("event: begin\n"))
		_, _ = w.Write([]byte("data: {\"ok\":true}\n\n"))
		_, _ = w.Write([]byte("event: status\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"snapshot\"}\n\n"))
		_, _ = w.Write([]byte("event: done\n"))
		_, _ = w.Write([]byte("data: {\"ok\":true}\n\n"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}))
	defer srv.Close()

	client, baseURL, err := buildClient(srv.URL, "", 2*time.Second)
	if err != nil {
		t.Fatalf("build client: %v", err)
	}
	if err := streamBridgeStatus(client, baseURL, "", 100*time.Millisecond, 200*time.Millisecond, "", bridgeStatusStreamOptions{
		JSONL: true,
		Retry: false,
	}); err != nil {
		t.Fatalf("stream bridge status: %v", err)
	}
}

func TestStreamBridgeStatusRetryOnUnexpectedEOF(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/helper/bridge.status.stream" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		n := atomic.AddInt32(&calls, 1)
		_, _ = w.Write([]byte("event: status\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"snapshot\"}\n\n"))
		if n >= 2 {
			_, _ = w.Write([]byte("event: done\n"))
			_, _ = w.Write([]byte("data: {\"ok\":true}\n\n"))
		}
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}))
	defer srv.Close()

	client, baseURL, err := buildClient(srv.URL, "", 2*time.Second)
	if err != nil {
		t.Fatalf("build client: %v", err)
	}
	err = streamBridgeStatus(client, baseURL, "", 100*time.Millisecond, 200*time.Millisecond, "", bridgeStatusStreamOptions{
		JSONL:           true,
		Retry:           true,
		RetryMax:        3,
		RetryBackoffMin: 1 * time.Millisecond,
		RetryBackoffMax: 5 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("stream bridge status with retry: %v", err)
	}
	if got := atomic.LoadInt32(&calls); got < 2 {
		t.Fatalf("expected at least 2 stream attempts, got %d", got)
	}
}

func TestStreamLinksHealth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/helper/links/health.stream" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("event: begin\n"))
		_, _ = w.Write([]byte("data: {\"ok\":true}\n\n"))
		_, _ = w.Write([]byte("event: links\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"links\",\"links\":[]}\n\n"))
		_, _ = w.Write([]byte("event: done\n"))
		_, _ = w.Write([]byte("data: {\"ok\":true}\n\n"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}))
	defer srv.Close()

	client, baseURL, err := buildClient(srv.URL, "", 2*time.Second)
	if err != nil {
		t.Fatalf("build client: %v", err)
	}
	if err := streamLinksHealth(client, baseURL, "", 100*time.Millisecond, 200*time.Millisecond, "", bridgeStatusStreamOptions{
		JSONL: true,
		Retry: false,
	}); err != nil {
		t.Fatalf("stream links health: %v", err)
	}
}

func TestFormatLinksHealthJSONL(t *testing.T) {
	raw := formatLinksHealthJSONL("links", `{"type":"links","links":[]}`, 3)
	if !strings.Contains(raw, `"stream":"links.health"`) {
		t.Fatalf("unexpected envelope: %s", raw)
	}
	if !strings.Contains(raw, `"event":"links"`) {
		t.Fatalf("missing event in envelope: %s", raw)
	}
	if !strings.Contains(raw, `"attempt":3`) {
		t.Fatalf("missing attempt in envelope: %s", raw)
	}
}

func TestFormatBridgeStatusJSONL(t *testing.T) {
	raw := formatBridgeStatusJSONL("status", `{"type":"snapshot"}`, 2)
	if !strings.Contains(raw, `"stream":"bridge.status"`) {
		t.Fatalf("unexpected envelope: %s", raw)
	}
	if !strings.Contains(raw, `"event":"status"`) {
		t.Fatalf("missing event in envelope: %s", raw)
	}
	if !strings.Contains(raw, `"attempt":2`) {
		t.Fatalf("missing attempt in envelope: %s", raw)
	}
	if !strings.Contains(raw, `"type":"snapshot"`) {
		t.Fatalf("missing decoded payload in envelope: %s", raw)
	}
}
