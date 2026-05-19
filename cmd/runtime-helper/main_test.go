package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tun/internal/runtime"
)

func TestParseHex16(t *testing.T) {
	v, err := parseHex16("00112233445566778899aabbccddeeff")
	if err != nil {
		t.Fatalf("parse hex16: %v", err)
	}
	if v[0] != 0x00 || v[1] != 0x11 || v[15] != 0xff {
		t.Fatalf("unexpected value: %#v", v)
	}
}

func TestNormalizeBootstrapRejectsInsecureModesByDefault(t *testing.T) {
	t.Setenv("ALLOW_INSECURE_TUNNEL_FOR_TESTS", "")
	pb := ProfileBootstrap{
		Addr:               "127.0.0.1:8443",
		ServerName:         "localhost",
		ClientID:           "00112233445566778899aabbccddeeff",
		ServerStaticPubB64: "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=",
		Insecure:           true,
	}
	if err := normalizeBootstrap(&pb); err == nil {
		t.Fatalf("expected insecure bootstrap rejection")
	}
}

func TestNormalizeBootstrapAllowsInsecureModesWithEnv(t *testing.T) {
	t.Setenv("ALLOW_INSECURE_TUNNEL_FOR_TESTS", "1")
	pb := ProfileBootstrap{
		Addr:               "127.0.0.1:8443",
		ServerName:         "localhost",
		ClientID:           "00112233445566778899aabbccddeeff",
		ServerStaticPubB64: "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=",
		Plain:              true,
	}
	if err := normalizeBootstrap(&pb); err != nil {
		t.Fatalf("expected insecure bootstrap allowed in test env, got %v", err)
	}
}

func TestNormalizeBootstrapAllowsGatewayPoolWithoutLegacyAddr(t *testing.T) {
	pb := ProfileBootstrap{
		ClientID:           "00112233445566778899aabbccddeeff",
		ServerStaticPubB64: "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=",
		ServerName:         "gw.local",
		Gateways: []GatewayConfig{
			{
				GatewayID: "gw-msk-d-1",
				Health:    "healthy",
				Endpoints: []GatewayEndpointConfig{
					{Addr: "10.0.0.10:443"},
				},
				Hints: GatewayHints{Priority: 10, LoadScore: 35, RTTScore: 20},
			},
		},
	}
	if err := normalizeBootstrap(&pb); err != nil {
		t.Fatalf("normalize with gateways: %v", err)
	}
	if pb.Addr != "" {
		t.Fatalf("expected legacy addr to stay empty when gateways are provided, got %q", pb.Addr)
	}
	if got := pb.Gateways[0].Endpoints[0].ServerName; got != "gw.local" {
		t.Fatalf("expected endpoint serverName defaulted from bootstrap, got %q", got)
	}
}

func TestNormalizeBootstrapRejectsGatewayPoolWithoutEndpoints(t *testing.T) {
	pb := ProfileBootstrap{
		ClientID:           "00112233445566778899aabbccddeeff",
		ServerStaticPubB64: "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=",
		Gateways: []GatewayConfig{
			{
				GatewayID: "gw-empty",
				Health:    "healthy",
				Endpoints: []GatewayEndpointConfig{{Addr: "   "}},
			},
		},
	}
	if err := normalizeBootstrap(&pb); err == nil {
		t.Fatalf("expected gateway validation error for empty endpoints")
	}
}

func TestNormalizeBootstrapRejectsUnknownForceGatewayID(t *testing.T) {
	force := "gw-missing"
	pb := ProfileBootstrap{
		ClientID:           "00112233445566778899aabbccddeeff",
		ServerStaticPubB64: "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=",
		GatewayPolicy: GatewayPolicy{
			ForceGatewayID: force,
		},
		Gateways: []GatewayConfig{
			{
				GatewayID: "gw-1",
				Endpoints: []GatewayEndpointConfig{{Addr: "10.0.0.1:443"}},
			},
		},
	}
	if err := normalizeBootstrap(&pb); err == nil {
		t.Fatalf("expected error for unknown forceGatewayID")
	}
}

func TestNormalizeBootstrapAllowsForceGatewayIDAndAutoSelectDisable(t *testing.T) {
	auto := false
	pb := ProfileBootstrap{
		ClientID:           "00112233445566778899aabbccddeeff",
		ServerStaticPubB64: "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=",
		GatewayPolicy: GatewayPolicy{
			AutoSelectEnabled: &auto,
			ForceGatewayID:    "gw-2",
			StickyDuration:    5 * time.Second,
			CooldownMin:       2 * time.Second,
			CooldownMax:       4 * time.Second,
		},
		Gateways: []GatewayConfig{
			{
				GatewayID: "gw-1",
				Endpoints: []GatewayEndpointConfig{{Addr: "10.0.0.1:443"}},
			},
			{
				GatewayID: "gw-2",
				Endpoints: []GatewayEndpointConfig{{Addr: "10.0.0.2:443"}},
			},
		},
	}
	if err := normalizeBootstrap(&pb); err != nil {
		t.Fatalf("normalize with forceGatewayID: %v", err)
	}
	if pb.GatewayPolicy.AutoSelectEnabled == nil || *pb.GatewayPolicy.AutoSelectEnabled {
		t.Fatalf("expected autoSelectEnabled=false to be preserved")
	}
}

func TestNormalizeBootstrapRejectsNegativeGatewaySwitchHysteresis(t *testing.T) {
	pb := ProfileBootstrap{
		ClientID:           "00112233445566778899aabbccddeeff",
		ServerStaticPubB64: "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=",
		ServerName:         "gw.local",
		GatewayPolicy: GatewayPolicy{
			SwitchHysteresis: -1,
		},
		Gateways: []GatewayConfig{
			{
				GatewayID: "gw-1",
				Endpoints: []GatewayEndpointConfig{{Addr: "10.0.0.1:443"}},
			},
		},
	}
	if err := normalizeBootstrap(&pb); err == nil || !strings.Contains(err.Error(), "switchHysteresis") {
		t.Fatalf("expected switchHysteresis validation error, got %v", err)
	}
}

func TestNormalizeBootstrapRejectsNegativeRekeyPolicyValues(t *testing.T) {
	pb := ProfileBootstrap{
		Addr:               "127.0.0.1:8443",
		ServerName:         "localhost",
		ClientID:           "00112233445566778899aabbccddeeff",
		ServerStaticPubB64: "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=",
		RekeyPolicy: RekeyPolicy{
			AckRetries: -1,
		},
	}
	if err := normalizeBootstrap(&pb); err == nil {
		t.Fatalf("expected normalize error for negative rekey ack retries")
	}
}

func TestNormalizeBootstrapDefaultsSecurityProfile(t *testing.T) {
	pb := ProfileBootstrap{
		Addr:               "127.0.0.1:8443",
		ServerName:         "localhost",
		ClientID:           "00112233445566778899aabbccddeeff",
		ServerStaticPubB64: "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=",
	}
	if err := normalizeBootstrap(&pb); err != nil {
		t.Fatalf("normalize bootstrap: %v", err)
	}
	if pb.SecurityProfile != "balanced" {
		t.Fatalf("expected default balanced security profile, got %q", pb.SecurityProfile)
	}
	if pb.Bridge.AllowLocalTCPBridge == nil || *pb.Bridge.AllowLocalTCPBridge {
		t.Fatalf("expected balanced default allowLocalTCPBridge=false")
	}
	if pb.Bridge.AllowLocalControlAPI == nil || *pb.Bridge.AllowLocalControlAPI {
		t.Fatalf("expected balanced default allowLocalControlAPI=false")
	}
}

func TestNormalizeBootstrapRejectsUnknownSecurityProfile(t *testing.T) {
	pb := ProfileBootstrap{
		Addr:               "127.0.0.1:8443",
		ServerName:         "localhost",
		ClientID:           "00112233445566778899aabbccddeeff",
		ServerStaticPubB64: "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=",
		SecurityProfile:    "ultra_secret",
	}
	if err := normalizeBootstrap(&pb); err == nil || !strings.Contains(err.Error(), "securityProfile") {
		t.Fatalf("expected unsupported securityProfile error, got %v", err)
	}
}

func TestNormalizeBootstrapRejectsHighRiskLocalBridgeEnable(t *testing.T) {
	allow := true
	pb := ProfileBootstrap{
		Addr:               "127.0.0.1:8443",
		ServerName:         "localhost",
		ClientID:           "00112233445566778899aabbccddeeff",
		ServerStaticPubB64: "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=",
		SecurityProfile:    "high_risk",
		Bridge: BridgePolicy{
			AllowLocalTCPBridge: &allow,
		},
	}
	if err := normalizeBootstrap(&pb); err == nil || !strings.Contains(err.Error(), "allowLocalTCPBridge") {
		t.Fatalf("expected high_risk local bridge error, got %v", err)
	}
}

func TestNormalizeBootstrapRoutingBGPDefaults(t *testing.T) {
	pb := ProfileBootstrap{
		Addr:               "127.0.0.1:8443",
		ServerName:         "localhost",
		ClientID:           "00112233445566778899aabbccddeeff",
		ServerStaticPubB64: "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=",
		Routing: RoutePolicy{
			Source: "bgp",
		},
	}
	if err := normalizeBootstrap(&pb); err != nil {
		t.Fatalf("normalize bootstrap: %v", err)
	}
	if pb.Routing.BGP.Neighbor != "45.154.73.71" {
		t.Fatalf("unexpected bgp neighbor default: %q", pb.Routing.BGP.Neighbor)
	}
	if pb.Routing.BGP.NeighborAS != 65432 {
		t.Fatalf("unexpected bgp neighborAs default: %d", pb.Routing.BGP.NeighborAS)
	}
	if pb.Routing.BGP.HoldTimeSec != 240 || pb.Routing.BGP.KeepaliveSec != 80 {
		t.Fatalf("unexpected bgp timer defaults: hold=%d keepalive=%d", pb.Routing.BGP.HoldTimeSec, pb.Routing.BGP.KeepaliveSec)
	}
	if pb.Routing.BGP.Enabled == nil || !*pb.Routing.BGP.Enabled {
		t.Fatalf("expected bgp enabled default=true")
	}
}

func TestNormalizeBootstrapRejectsUnknownRoutingSource(t *testing.T) {
	pb := ProfileBootstrap{
		Addr:               "127.0.0.1:8443",
		ServerName:         "localhost",
		ClientID:           "00112233445566778899aabbccddeeff",
		ServerStaticPubB64: "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=",
		Routing: RoutePolicy{
			Source: "magic",
		},
	}
	if err := normalizeBootstrap(&pb); err == nil || !strings.Contains(err.Error(), "routing.source") {
		t.Fatalf("expected routing.source validation error, got %v", err)
	}
}

func TestAllowTCPHelperNoAuthForTests(t *testing.T) {
	t.Setenv("ALLOW_HELPER_TCP_NOAUTH_FOR_TESTS", "1")
	if !allowTCPHelperNoAuthForTests() {
		t.Fatalf("expected tcp no-auth override to be enabled")
	}
	t.Setenv("ALLOW_HELPER_TCP_NOAUTH_FOR_TESTS", "")
	if allowTCPHelperNoAuthForTests() {
		t.Fatalf("expected tcp no-auth override to be disabled")
	}
}

func TestHelperManagerStartStop(t *testing.T) {
	m := newHelperManager("")
	m.runFn = func(ctx context.Context, _ ProfileBootstrap, onEvent func(runtime.Event)) error {
		onEvent(runtime.Event{
			State:      runtime.StateEstablished,
			ErrorClass: runtime.ErrorClassNone,
			Snapshot: runtime.Snapshot{
				State:           runtime.StateEstablished,
				LinkID:          "client:dev-01:default",
				SessionID:       "sess-1",
				LastHandshakeAt: time.Now().UTC(),
			},
		})
		<-ctx.Done()
		return ctx.Err()
	}
	req := StartRequest{
		DeviceID: "dev-01",
		ProfileBootstrap: ProfileBootstrap{
			Addr:               "127.0.0.1:8443",
			ServerName:         "localhost",
			ClientID:           "00112233445566778899aabbccddeeff",
			ServerStaticPubB64: "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=", // test string
		},
	}
	if err := m.start(req); err != nil {
		t.Fatalf("start: %v", err)
	}
	st := m.status()
	if !st.Running {
		t.Fatalf("expected running status")
	}
	h := m.health()
	if !h.Running {
		t.Fatalf("expected running health")
	}
	deadline := time.Now().Add(500 * time.Millisecond)
	var links []LinkStatusResponse
	for time.Now().Before(deadline) {
		links = m.links()
		if len(links) == 1 && links[0].LinkID != "" && links[0].SessionID != "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(links) != 1 || links[0].LinkID == "" || links[0].SessionID == "" {
		t.Fatalf("expected link and session ids in link status: %+v", links)
	}
	if err := m.stop(2 * time.Second); err != nil {
		t.Fatalf("stop: %v", err)
	}
	st = m.status()
	if st.Running {
		t.Fatalf("expected stopped status")
	}
}

func TestLinksEndpoint(t *testing.T) {
	manager := newHelperManager("")
	manager.mu.Lock()
	manager.deviceID = "dev-links"
	manager.bootstrap = ProfileBootstrap{
		ClientID: "00112233445566778899aabbccddeeff",
		Tun:      TunConfig{Name: "tun-links"},
	}
	manager.hasBootstrap = true
	manager.linkID = helperLinkID(manager.deviceID, manager.bootstrap)
	now := time.Now().UTC()
	manager.running = true
	manager.lastEvent = runtime.Event{
		State:      runtime.StateEstablished,
		ErrorClass: runtime.ErrorClassNone,
		Snapshot: runtime.Snapshot{
			State:            runtime.StateEstablished,
			LinkID:           manager.linkID,
			SessionID:        "sess-links",
			LastHandshakeAt:  now,
			LastTransitionAt: now,
			LastRxAt:         now,
			LastTxAt:         now,
			RxBytes:          10,
			TxBytes:          20,
		},
	}
	manager.lastEventAt = &now
	manager.mu.Unlock()

	mux := newHelperMux(manager, "")

	req := httptest.NewRequest(http.MethodGet, "/v1/helper/links", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var listOut struct {
		Links []LinkStatusResponse `json:"links"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listOut); err != nil {
		t.Fatalf("decode links list: %v", err)
	}
	if len(listOut.Links) != 1 {
		t.Fatalf("expected one link in list, got %d", len(listOut.Links))
	}
	if listOut.Links[0].SessionID != "sess-links" {
		t.Fatalf("unexpected session id: %+v", listOut.Links[0])
	}

	req = httptest.NewRequest(http.MethodGet, "/v1/helper/links/"+manager.linkID, nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for link detail, got %d body=%s", rec.Code, rec.Body.String())
	}
	var linkOut LinkStatusResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &linkOut); err != nil {
		t.Fatalf("decode link detail: %v", err)
	}
	if linkOut.LinkID != manager.linkID || linkOut.RxBytes != 10 || linkOut.TxBytes != 20 {
		t.Fatalf("unexpected link detail: %+v", linkOut)
	}
	if !linkOut.Readiness.Ready {
		t.Fatalf("expected readiness=true for established running link: %+v", linkOut.Readiness)
	}
	if linkOut.Readiness.ContractVersion == "" {
		t.Fatalf("expected readiness contract version to be set")
	}

	req = httptest.NewRequest(http.MethodGet, "/v1/helper/links/missing-link", nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for missing link, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestStatsIncludesRekeyAggregateBlock(t *testing.T) {
	manager := newHelperManager("")
	now := time.Now().UTC()
	manager.mu.Lock()
	manager.lastEvent = runtime.Event{
		State:      runtime.StateEstablished,
		ErrorClass: runtime.ErrorClassNone,
		Snapshot: runtime.Snapshot{
			State:                 runtime.StateEstablished,
			RekeyEpoch:            7,
			RekeysInitiated:       3,
			RekeysCompleted:       2,
			RekeyFallbacks:        1,
			RekeyAcksRejected:     1,
			RekeyAckSendFailures:  1,
			RekeyInitSendFailures: 1,
			RekeyInitTimeouts:     1,
			LastRekeyAt:           now,
		},
	}
	manager.lastEventAt = &now
	manager.mu.Unlock()

	stats := manager.stats()
	rekey, ok := stats["rekey"].(map[string]any)
	if !ok {
		t.Fatalf("expected rekey aggregate map in stats, got: %+v", stats)
	}
	if got, ok := rekey["health"].(string); !ok || got == "" {
		t.Fatalf("expected rekey health string, got %+v", rekey["health"])
	}
}

func TestLinkActionEndpoints(t *testing.T) {
	manager := newHelperManager("")
	manager.runFn = func(ctx context.Context, _ ProfileBootstrap, onEvent func(runtime.Event)) error {
		now := time.Now().UTC()
		onEvent(runtime.Event{
			State:      runtime.StateEstablished,
			ErrorClass: runtime.ErrorClassNone,
			Snapshot: runtime.Snapshot{
				State:               runtime.StateEstablished,
				LinkID:              manager.linkID,
				SessionID:           now.Format("20060102T150405.000000000Z07:00"),
				LastHandshakeAt:     now,
				LastTransitionAt:    now,
				SelectedGatewayID:   "gw-a",
				SelectedGatewayAddr: "10.0.0.1:443",
			},
		})
		<-ctx.Done()
		return ctx.Err()
	}
	req := StartRequest{
		DeviceID: "dev-action",
		ProfileBootstrap: ProfileBootstrap{
			ClientID:           "00112233445566778899aabbccddeeff",
			ServerStaticPubB64: "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=",
			ServerName:         "gw.local",
			Gateways: []GatewayConfig{
				{
					GatewayID: "gw-a",
					Endpoints: []GatewayEndpointConfig{{Addr: "10.0.0.1:443"}},
				},
				{
					GatewayID: "gw-b",
					Endpoints: []GatewayEndpointConfig{{Addr: "10.0.0.2:443"}},
				},
			},
		},
	}
	if err := manager.start(req); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() {
		_ = manager.stop(2 * time.Second)
	})
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		link := manager.linkStatus()
		if link.Running && link.SessionID != "" && link.GatewayID == "gw-a" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	mux := newHelperMux(manager, "")
	linkID := helperLinkID(req.DeviceID, req.ProfileBootstrap)

	reconnectReq := httptest.NewRequest(http.MethodPost, "/v1/helper/links/"+linkID+"/reconnect", strings.NewReader(`{}`))
	reconnectReq.Header.Set("Content-Type", "application/json")
	reconnectRec := httptest.NewRecorder()
	mux.ServeHTTP(reconnectRec, reconnectReq)
	if reconnectRec.Code != http.StatusOK {
		t.Fatalf("expected 200 on reconnect, got %d body=%s", reconnectRec.Code, reconnectRec.Body.String())
	}

	selectReq := httptest.NewRequest(http.MethodPost, "/v1/helper/links/"+linkID+"/gateway.select", strings.NewReader(`{"gatewayID":"gw-b"}`))
	selectReq.Header.Set("Content-Type", "application/json")
	selectRec := httptest.NewRecorder()
	mux.ServeHTTP(selectRec, selectReq)
	if selectRec.Code != http.StatusOK {
		t.Fatalf("expected 200 on gateway.select, got %d body=%s", selectRec.Code, selectRec.Body.String())
	}
	var selectOut LinkActionResponse
	if err := json.Unmarshal(selectRec.Body.Bytes(), &selectOut); err != nil {
		t.Fatalf("decode gateway.select response: %v", err)
	}
	if selectOut.Link.GatewayID != "gw-b" {
		t.Fatalf("expected forced gateway gw-b, got %+v", selectOut.Link)
	}

	invalidSelectReq := httptest.NewRequest(http.MethodPost, "/v1/helper/links/"+linkID+"/gateway.select", strings.NewReader(`{}`))
	invalidSelectReq.Header.Set("Content-Type", "application/json")
	invalidSelectRec := httptest.NewRecorder()
	mux.ServeHTTP(invalidSelectRec, invalidSelectReq)
	if invalidSelectRec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing gatewayID, got %d body=%s", invalidSelectRec.Code, invalidSelectRec.Body.String())
	}

	drainReq := httptest.NewRequest(http.MethodPost, "/v1/helper/links/"+linkID+"/drain", strings.NewReader(`{}`))
	drainReq.Header.Set("Content-Type", "application/json")
	drainRec := httptest.NewRecorder()
	mux.ServeHTTP(drainRec, drainReq)
	if drainRec.Code != http.StatusOK {
		t.Fatalf("expected 200 on drain, got %d body=%s", drainRec.Code, drainRec.Body.String())
	}
	if manager.status().Running {
		t.Fatalf("expected runtime stopped after drain")
	}

	reconnectStoppedReq := httptest.NewRequest(http.MethodPost, "/v1/helper/links/"+linkID+"/reconnect", strings.NewReader(`{}`))
	reconnectStoppedReq.Header.Set("Content-Type", "application/json")
	reconnectStoppedRec := httptest.NewRecorder()
	mux.ServeHTTP(reconnectStoppedRec, reconnectStoppedReq)
	if reconnectStoppedRec.Code != http.StatusConflict {
		t.Fatalf("expected 409 for reconnect on stopped runtime, got %d body=%s", reconnectStoppedRec.Code, reconnectStoppedRec.Body.String())
	}
	var reconnectErr apiErrorEnvelope
	if err := json.Unmarshal(reconnectStoppedRec.Body.Bytes(), &reconnectErr); err != nil {
		t.Fatalf("decode reconnect conflict: %v", err)
	}
	if reconnectErr.Error.Code != "runtime_not_running" {
		t.Fatalf("unexpected reconnect conflict code: %q", reconnectErr.Error.Code)
	}

	resumeReq := httptest.NewRequest(http.MethodPost, "/v1/helper/links/"+linkID+"/resume", strings.NewReader(`{}`))
	resumeReq.Header.Set("Content-Type", "application/json")
	resumeRec := httptest.NewRecorder()
	mux.ServeHTTP(resumeRec, resumeReq)
	if resumeRec.Code != http.StatusOK {
		t.Fatalf("expected 200 on resume, got %d body=%s", resumeRec.Code, resumeRec.Body.String())
	}

	missingReq := httptest.NewRequest(http.MethodPost, "/v1/helper/links/missing-link/drain", strings.NewReader(`{}`))
	missingReq.Header.Set("Content-Type", "application/json")
	missingRec := httptest.NewRecorder()
	mux.ServeHTTP(missingRec, missingReq)
	if missingRec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for missing link action, got %d body=%s", missingRec.Code, missingRec.Body.String())
	}
}

func TestSchemaEndpoint(t *testing.T) {
	mux := newHelperMux(newHelperManager(""), "")
	req := httptest.NewRequest(http.MethodGet, "/v1/helper/schema", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var out HelperSchemaResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal schema: %v", err)
	}
	if out.APIVersion != helperAPIVersion {
		t.Fatalf("unexpected api version: %q", out.APIVersion)
	}
	if out.GatewayPoolVersion != helperGatewayPoolVersion {
		t.Fatalf("unexpected gateway pool version: %q", out.GatewayPoolVersion)
	}
	if out.SecurityContractVersion != helperSecurityContractVersion {
		t.Fatalf("unexpected security contract version: %q", out.SecurityContractVersion)
	}
	if out.ProfileBundleVersion != helperProfileBundleVersion {
		t.Fatalf("unexpected profile bundle version: %q", out.ProfileBundleVersion)
	}
	if out.Bootstrap.SchemaVersion == "" {
		t.Fatalf("expected bootstrap schema version")
	}
	if out.Bootstrap.SchemaVersion != helperBootstrapSchemaVersion {
		t.Fatalf("unexpected bootstrap schema version: %q", out.Bootstrap.SchemaVersion)
	}
	if !out.Bootstrap.GatewayPoolSupported || !out.Bootstrap.GatewayPolicySupported {
		t.Fatalf("expected gateway bootstrap contract flags enabled")
	}
	if !out.Bootstrap.RekeyPolicySupported {
		t.Fatalf("expected rekey policy support flag enabled")
	}
	if !out.Bootstrap.ProfileBundleSupported {
		t.Fatalf("expected profile bundle support flag enabled")
	}
	if !out.Bootstrap.SecurityProfileSupported {
		t.Fatalf("expected security profile support flag enabled")
	}
	if len(out.Bootstrap.SupportedSecurityProfiles) == 0 {
		t.Fatalf("expected supported security profile list")
	}
	if len(out.Endpoints) == 0 {
		t.Fatalf("expected endpoints list")
	}
	var hasLinkReconnect bool
	var hasLinksHealthStream bool
	var hasProfileApply bool
	var hasProfileCurrent bool
	for _, ep := range out.Endpoints {
		if ep == "POST /v1/helper/links/<linkID>/reconnect" {
			hasLinkReconnect = true
		}
		if ep == "GET /v1/helper/links/health.stream" {
			hasLinksHealthStream = true
		}
		if ep == "POST /v1/helper/profile.apply" {
			hasProfileApply = true
		}
		if ep == "GET /v1/helper/profile.current" {
			hasProfileCurrent = true
		}
		if hasLinkReconnect && hasLinksHealthStream && hasProfileApply && hasProfileCurrent {
			break
		}
	}
	if !hasLinkReconnect {
		t.Fatalf("expected link reconnect endpoint in schema: %v", out.Endpoints)
	}
	if !hasLinksHealthStream {
		t.Fatalf("expected links health stream endpoint in schema: %v", out.Endpoints)
	}
	if !hasProfileApply {
		t.Fatalf("expected profile.apply endpoint in schema: %v", out.Endpoints)
	}
	if !hasProfileCurrent {
		t.Fatalf("expected profile.current endpoint in schema: %v", out.Endpoints)
	}
	var hasSecurityEvaluate bool
	for _, ep := range out.Endpoints {
		if ep == "POST /v1/helper/security.evaluate" {
			hasSecurityEvaluate = true
			break
		}
	}
	if !hasSecurityEvaluate {
		t.Fatalf("expected security.evaluate endpoint in schema: %v", out.Endpoints)
	}
}

func TestProfileApplyAndCurrentEndpoint(t *testing.T) {
	mux := newHelperMux(newHelperManager(""), "")
	applyBody := `{
	  "bundle":{
	    "apiVersion":"2026-04-14",
	    "version":"v1",
	    "profiles":[
	      {
	        "id":"ru-high",
	        "region":"RU",
	        "securityProfile":"high_risk",
	        "revision":1,
	        "tun":{"mode":"full","lockdown":true},
	        "bridge":{"allowLocalTCPBridge":false,"allowLocalControlAPI":false}
	      }
	    ]
	  }
	}`
	applyReq := httptest.NewRequest(http.MethodPost, "/v1/helper/profile.apply", strings.NewReader(applyBody))
	applyReq.Header.Set("Content-Type", "application/json")
	applyRec := httptest.NewRecorder()
	mux.ServeHTTP(applyRec, applyReq)
	if applyRec.Code != http.StatusOK {
		t.Fatalf("expected 200 on profile.apply, got %d body=%s", applyRec.Code, applyRec.Body.String())
	}

	currentReq := httptest.NewRequest(http.MethodGet, "/v1/helper/profile.current", nil)
	currentRec := httptest.NewRecorder()
	mux.ServeHTTP(currentRec, currentReq)
	if currentRec.Code != http.StatusOK {
		t.Fatalf("expected 200 on profile.current, got %d body=%s", currentRec.Code, currentRec.Body.String())
	}
	var out struct {
		OK      bool                  `json:"ok"`
		Current runtime.ProfileBundle `json:"current"`
	}
	if err := json.Unmarshal(currentRec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode profile.current response: %v", err)
	}
	if !out.OK {
		t.Fatalf("expected ok=true from profile.current")
	}
	if len(out.Current.Profiles) != 1 || out.Current.Profiles[0].SecurityProfile != runtime.SecurityProfileHighRisk {
		t.Fatalf("unexpected current profile bundle: %+v", out.Current)
	}
}

func TestSecurityEvaluateEndpoint(t *testing.T) {
	mux := newHelperMux(newHelperManager(""), "")
	body := `{
	  "geoipDetected": true,
	  "directDetected": true,
	  "indirectDetected": false,
	  "hostingRisk": true,
	  "serverCountry": "DE",
	  "clientCountry": "RU"
	}`
	req := httptest.NewRequest(http.MethodPost, "/v1/helper/security.evaluate", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var out SecurityEvaluateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode security response: %v", err)
	}
	if !out.OK || out.Decision != "detected" {
		t.Fatalf("unexpected decision: %+v", out)
	}
	if len(out.ProtectionPlan) == 0 || out.ProtectionPlan[0] != "deny_sensitive_actions" {
		t.Fatalf("unexpected protection plan: %+v", out.ProtectionPlan)
	}
}

func TestEvaluateSecuritySignalsDampeners(t *testing.T) {
	out := evaluateSecuritySignalsBase(SecurityEvaluateRequest{
		GeoIPDetected:        true,
		HostingRisk:          true,
		CorporateWhitelisted: true,
	})
	if out.Decision != "additional_check" {
		t.Fatalf("expected additional_check for corporate whitelist dampener, got %+v", out)
	}
	if len(out.ProtectionPlan) == 0 || out.ProtectionPlan[0] != "allow_with_limits" {
		t.Fatalf("unexpected protection plan: %+v", out.ProtectionPlan)
	}
}

func TestSecurityEvaluateSignatureAndReplay(t *testing.T) {
	manager := newHelperManager("")
	manager.security.signalHMACKey = []byte("test-hmac-key")
	mux := newHelperMux(manager, "")

	ts := time.Now().UTC().Unix()
	nonce := "nonce-1"
	base := fmt.Sprintf("%s|%s|%s|%t|%t|%t|%d", "dev-sec", "default", nonce, true, false, true, ts)
	mac := hmac.New(sha256.New, []byte("test-hmac-key"))
	_, _ = mac.Write([]byte(base))
	sig := hex.EncodeToString(mac.Sum(nil))

	body := fmt.Sprintf(`{"deviceID":"dev-sec","tenantID":"default","geoipDetected":true,"directDetected":true,"signalTimestamp":%d,"signalNonce":"%s","signalSignature":"%s"}`, ts, nonce, sig)
	req := httptest.NewRequest(http.MethodPost, "/v1/helper/security.evaluate", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	// Replay same nonce must fail.
	req2 := httptest.NewRequest(http.MethodPost, "/v1/helper/security.evaluate", strings.NewReader(body))
	req2.Header.Set("Content-Type", "application/json")
	rec2 := httptest.NewRecorder()
	mux.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 on replay, got %d body=%s", rec2.Code, rec2.Body.String())
	}
}

func TestSecurityPolicyCorporateAllowAndReputation(t *testing.T) {
	manager := newHelperManager("")
	manager.security.signalHMACKey = nil // disable signature for this test
	mux := newHelperMux(manager, "")

	policyReq := httptest.NewRequest(http.MethodPost, "/v1/helper/security.policy.upsert", strings.NewReader(`{"tenantID":"t1","profile":"strict","enforce":true,"hysteresisThreshold":2,"hysteresisWindowSec":3600}`))
	policyReq.Header.Set("Content-Type", "application/json")
	policyRec := httptest.NewRecorder()
	mux.ServeHTTP(policyRec, policyReq)
	if policyRec.Code != http.StatusOK {
		t.Fatalf("policy upsert failed: %d body=%s", policyRec.Code, policyRec.Body.String())
	}

	allowReq := httptest.NewRequest(http.MethodPost, "/v1/helper/security.corporate-allow.upsert", strings.NewReader(`{"tenantID":"t1","asn":"AS12345","ttl":60000000000}`))
	allowReq.Header.Set("Content-Type", "application/json")
	allowRec := httptest.NewRecorder()
	mux.ServeHTTP(allowRec, allowReq)
	if allowRec.Code != http.StatusOK {
		t.Fatalf("corporate allow upsert failed: %d body=%s", allowRec.Code, allowRec.Body.String())
	}

	repReq := httptest.NewRequest(http.MethodPost, "/v1/helper/security.reputation.upsert", strings.NewReader(`{"tenantID":"t1","ip":"1.2.3.4","source":"ranr","riskType":"vpn","confidence":80,"ttl":60000000000}`))
	repReq.Header.Set("Content-Type", "application/json")
	repRec := httptest.NewRecorder()
	mux.ServeHTTP(repRec, repReq)
	if repRec.Code != http.StatusOK {
		t.Fatalf("reputation upsert failed: %d body=%s", repRec.Code, repRec.Body.String())
	}

	// First detected hit should not hard-block yet (threshold=2).
	eval1 := httptest.NewRequest(http.MethodPost, "/v1/helper/security.evaluate", strings.NewReader(`{"tenantID":"t1","deviceID":"dev1","asn":"AS12345","clientIP":"1.2.3.4","geoipDetected":true}`))
	eval1.Header.Set("Content-Type", "application/json")
	evalRec1 := httptest.NewRecorder()
	mux.ServeHTTP(evalRec1, eval1)
	if evalRec1.Code != http.StatusOK {
		t.Fatalf("eval1 failed: %d body=%s", evalRec1.Code, evalRec1.Body.String())
	}
	var out1 SecurityEvaluateResponse
	if err := json.Unmarshal(evalRec1.Body.Bytes(), &out1); err != nil {
		t.Fatalf("decode eval1: %v", err)
	}
	if out1.PolicyProfile != "strict" {
		t.Fatalf("expected strict profile, got %+v", out1)
	}
	if out1.HardBlock {
		t.Fatalf("first offense should not hard block: %+v", out1)
	}

	// Second detected hit should hard-block by hysteresis threshold.
	eval2 := httptest.NewRequest(http.MethodPost, "/v1/helper/security.evaluate", strings.NewReader(`{"tenantID":"t1","deviceID":"dev1","asn":"AS12345","clientIP":"1.2.3.4","geoipDetected":true}`))
	eval2.Header.Set("Content-Type", "application/json")
	evalRec2 := httptest.NewRecorder()
	mux.ServeHTTP(evalRec2, eval2)
	if evalRec2.Code != http.StatusOK {
		t.Fatalf("eval2 failed: %d body=%s", evalRec2.Code, evalRec2.Body.String())
	}
	var out2 SecurityEvaluateResponse
	if err := json.Unmarshal(evalRec2.Body.Bytes(), &out2); err != nil {
		t.Fatalf("decode eval2: %v", err)
	}
	if !out2.HardBlock {
		t.Fatalf("second offense should hard block: %+v", out2)
	}

	auditReq := httptest.NewRequest(http.MethodGet, "/v1/helper/security.audit?limit=5", nil)
	auditRec := httptest.NewRecorder()
	mux.ServeHTTP(auditRec, auditReq)
	if auditRec.Code != http.StatusOK {
		t.Fatalf("security.audit failed: %d body=%s", auditRec.Code, auditRec.Body.String())
	}
}

func TestSecurityPolicyRolloutAndSignalIngest(t *testing.T) {
	manager := newHelperManager("")
	manager.security.signalHMACKey = nil
	mux := newHelperMux(manager, "")

	rolloutReq := httptest.NewRequest(http.MethodPost, "/v1/helper/security.policy.rollout", strings.NewReader(`{"defaultProfile":"balanced","strictTenants":["risk-a","risk-b"]}`))
	rolloutReq.Header.Set("Content-Type", "application/json")
	rolloutRec := httptest.NewRecorder()
	mux.ServeHTTP(rolloutRec, rolloutReq)
	if rolloutRec.Code != http.StatusOK {
		t.Fatalf("security.policy.rollout failed: %d body=%s", rolloutRec.Code, rolloutRec.Body.String())
	}

	ingestReq := httptest.NewRequest(http.MethodPost, "/v1/helper/security.signal.ingest", strings.NewReader(`{"signal":{"tenantID":"risk-a","deviceID":"dev-ingest","geoipDetected":true}}`))
	ingestReq.Header.Set("Content-Type", "application/json")
	ingestRec := httptest.NewRecorder()
	mux.ServeHTTP(ingestRec, ingestReq)
	if ingestRec.Code != http.StatusOK {
		t.Fatalf("security.signal.ingest failed: %d body=%s", ingestRec.Code, ingestRec.Body.String())
	}
	var ingestOut SecuritySignalIngestResponse
	if err := json.Unmarshal(ingestRec.Body.Bytes(), &ingestOut); err != nil {
		t.Fatalf("decode ingest response: %v", err)
	}
	if !ingestOut.Evaluated || ingestOut.Result == nil {
		t.Fatalf("expected evaluated ingest result: %+v", ingestOut)
	}
	if ingestOut.Result.PolicyProfile != "strict" {
		t.Fatalf("expected strict profile after rollout, got %+v", ingestOut.Result)
	}

	recentReq := httptest.NewRequest(http.MethodGet, "/v1/helper/security.signal.ingest.recent?limit=10", nil)
	recentRec := httptest.NewRecorder()
	mux.ServeHTTP(recentRec, recentReq)
	if recentRec.Code != http.StatusOK {
		t.Fatalf("security.signal.ingest.recent failed: %d body=%s", recentRec.Code, recentRec.Body.String())
	}
}

func TestListenUnixSocket(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "helper.sock")
	ln, got, err := listenUnixSocket(sock)
	if err != nil {
		t.Fatalf("listen unix socket: %v", err)
	}
	defer func() {
		_ = ln.Close()
		_ = os.Remove(got)
	}()
	if got != sock {
		t.Fatalf("unexpected socket path: got=%q want=%q", got, sock)
	}
	st, err := os.Stat(sock)
	if err != nil {
		t.Fatalf("stat socket: %v", err)
	}
	if (st.Mode() & os.ModeSocket) == 0 {
		t.Fatalf("expected socket mode, got %v", st.Mode())
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("expected 0600 mode, got %o", st.Mode().Perm())
	}
	// Ensure it accepts local dials.
	c, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatalf("dial socket: %v", err)
	}
	_ = c.Close()
}

func TestProtectedEndpointRequiresToken(t *testing.T) {
	mux := newHelperMux(newHelperManager(""), "secret-token")
	req := httptest.NewRequest(http.MethodGet, "/v1/helper/status", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for missing token, got %d", rec.Code)
	}

	req2 := httptest.NewRequest(http.MethodGet, "/v1/helper/status", nil)
	req2.Header.Set("Authorization", "Bearer secret-token")
	rec2 := httptest.NewRecorder()
	mux.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("expected 200 for valid token, got %d", rec2.Code)
	}
}

func TestPersistedStateLoad(t *testing.T) {
	dir := t.TempDir()
	stateFile := filepath.Join(dir, "state.json")
	m := newHelperManager(stateFile)
	req := StartRequest{
		DeviceID: "dev-42",
		ProfileBootstrap: ProfileBootstrap{
			ProfileID:          "ru-balanced",
			SecurityProfile:    "balanced",
			Addr:               "127.0.0.1:8443",
			ServerName:         "localhost",
			ClientID:           "00112233445566778899aabbccddeeff",
			ServerStaticPubB64: "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=",
		},
	}
	if err := m.applyBootstrap(req); err != nil {
		t.Fatalf("apply bootstrap: %v", err)
	}

	loaded := newHelperManager(stateFile)
	if !loaded.hasBootstrap {
		t.Fatalf("expected hasBootstrap=true after load")
	}
	if loaded.deviceID != "dev-42" {
		t.Fatalf("expected deviceID persisted, got %q", loaded.deviceID)
	}
	if loaded.bootstrap.ClientID != req.ProfileBootstrap.ClientID {
		t.Fatalf("expected clientID persisted, got %q", loaded.bootstrap.ClientID)
	}
	st := loaded.status()
	if st.ProfileID != "ru-balanced" {
		t.Fatalf("expected profileID in status, got %q", st.ProfileID)
	}
	if st.SecurityProfile != "balanced" {
		t.Fatalf("expected securityProfile in status, got %q", st.SecurityProfile)
	}
}

func TestWaitForStateTimeout(t *testing.T) {
	m := newHelperManager("")
	_, err := m.waitForState(runtime.StateEstablished, 50*time.Millisecond)
	if err == nil {
		t.Fatalf("expected timeout error")
	}
}

func TestUnauthorizedErrorEnvelopeIncludesRequestID(t *testing.T) {
	mux := newHelperMux(newHelperManager(""), "secret-token")
	req := httptest.NewRequest(http.MethodGet, "/v1/helper/status", nil)
	req.Header.Set("X-Request-ID", "req-123")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
	var out apiErrorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if out.Error.Code != "unauthorized" {
		t.Fatalf("unexpected error code: %q", out.Error.Code)
	}
	if out.Error.RequestID != "req-123" {
		t.Fatalf("expected request id req-123, got %q", out.Error.RequestID)
	}
	if rec.Header().Get("X-Request-ID") != "req-123" {
		t.Fatalf("expected response request id header")
	}
}

func TestIdempotentReplayOnPost(t *testing.T) {
	mux := newHelperMux(newHelperManager(""), "")
	body := `{
	  "deviceID": "dev-55",
	  "profileBootstrap": {
	    "addr": "127.0.0.1:8443",
	    "serverName": "localhost",
	    "clientID": "00112233445566778899aabbccddeeff",
	    "serverStaticPub": "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="
	  }
	}`
	req1 := httptest.NewRequest(http.MethodPost, "/v1/helper/bootstrap.apply", strings.NewReader(body))
	req1.Header.Set("Content-Type", "application/json")
	req1.Header.Set("X-Request-ID", "idem-1")
	rec1 := httptest.NewRecorder()
	mux.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusOK {
		t.Fatalf("first request expected 200, got %d", rec1.Code)
	}
	firstBody, _ := io.ReadAll(rec1.Body)

	req2 := httptest.NewRequest(http.MethodPost, "/v1/helper/bootstrap.apply", strings.NewReader(body))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("X-Request-ID", "idem-1")
	rec2 := httptest.NewRecorder()
	mux.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("replay request expected 200, got %d", rec2.Code)
	}
	if rec2.Header().Get("X-Idempotent-Replay") != "true" {
		t.Fatalf("expected idempotent replay header")
	}
	secondBody, _ := io.ReadAll(rec2.Body)
	if string(firstBody) != string(secondBody) {
		t.Fatalf("expected same response body on replay")
	}
}

func TestValidateBootstrapEndpoint(t *testing.T) {
	mux := newHelperMux(newHelperManager(""), "")
	body := `{
	  "profileBootstrap": {
	    "addr": "127.0.0.1:8443",
	    "serverName": "localhost",
	    "clientID": "00112233445566778899aabbccddeeff",
	    "serverStaticPub": "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="
	  }
	}`
	req := httptest.NewRequest(http.MethodPost, "/v1/helper/bootstrap.validate", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest && rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d", rec.Code)
	}
	var out ValidateBootstrapResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode validate response: %v", err)
	}
	if out.Normalized.ClientID == "" {
		t.Fatalf("expected normalized bootstrap in response")
	}
}

func TestLeaseEnforcementOnMutatingCalls(t *testing.T) {
	mux := newHelperMux(newHelperManager(""), "")

	// Acquire lease.
	acqReq := httptest.NewRequest(http.MethodPost, "/v1/helper/lease.acquire", strings.NewReader(`{"owner":"tester","ttl":60000000000}`))
	acqReq.Header.Set("Content-Type", "application/json")
	acqRec := httptest.NewRecorder()
	mux.ServeHTTP(acqRec, acqReq)
	if acqRec.Code != http.StatusOK {
		t.Fatalf("expected 200 on lease.acquire, got %d body=%s", acqRec.Code, acqRec.Body.String())
	}
	var leaseOut LeaseStatusResponse
	if err := json.Unmarshal(acqRec.Body.Bytes(), &leaseOut); err != nil {
		t.Fatalf("decode lease.acquire: %v", err)
	}
	if leaseOut.LeaseID == "" {
		t.Fatalf("expected lease id")
	}

	body := `{
	  "deviceID": "dev-lease",
	  "profileBootstrap": {
	    "addr": "127.0.0.1:8443",
	    "serverName": "localhost",
	    "clientID": "00112233445566778899aabbccddeeff",
	    "serverStaticPub": "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="
	  }
	}`

	// Without lease header should fail.
	reqNoLease := httptest.NewRequest(http.MethodPost, "/v1/helper/bootstrap.apply", strings.NewReader(body))
	reqNoLease.Header.Set("Content-Type", "application/json")
	recNoLease := httptest.NewRecorder()
	mux.ServeHTTP(recNoLease, reqNoLease)
	if recNoLease.Code != http.StatusLocked {
		t.Fatalf("expected 423 without lease header, got %d", recNoLease.Code)
	}

	// With matching lease header should pass.
	reqLease := httptest.NewRequest(http.MethodPost, "/v1/helper/bootstrap.apply", strings.NewReader(body))
	reqLease.Header.Set("Content-Type", "application/json")
	reqLease.Header.Set("X-Helper-Lease-ID", leaseOut.LeaseID)
	recLease := httptest.NewRecorder()
	mux.ServeHTTP(recLease, reqLease)
	if recLease.Code != http.StatusOK {
		t.Fatalf("expected 200 with lease header, got %d body=%s", recLease.Code, recLease.Body.String())
	}
}

func TestLeaseHeartbeatRenewsWithHeaderLeaseID(t *testing.T) {
	mux := newHelperMux(newHelperManager(""), "")

	acqReq := httptest.NewRequest(http.MethodPost, "/v1/helper/lease.acquire", strings.NewReader(`{"owner":"tester","ttl":5000000000}`))
	acqReq.Header.Set("Content-Type", "application/json")
	acqRec := httptest.NewRecorder()
	mux.ServeHTTP(acqRec, acqReq)
	if acqRec.Code != http.StatusOK {
		t.Fatalf("expected 200 on lease.acquire, got %d body=%s", acqRec.Code, acqRec.Body.String())
	}
	var leaseOut LeaseStatusResponse
	if err := json.Unmarshal(acqRec.Body.Bytes(), &leaseOut); err != nil {
		t.Fatalf("decode lease.acquire: %v", err)
	}
	if leaseOut.LeaseID == "" || leaseOut.ExpiresAt == nil {
		t.Fatalf("expected lease id and expiresAt")
	}
	previousExpiry := leaseOut.ExpiresAt.UTC()

	hbReq := httptest.NewRequest(http.MethodPost, "/v1/helper/lease.heartbeat", strings.NewReader(`{"ttl":60000000000}`))
	hbReq.Header.Set("Content-Type", "application/json")
	hbReq.Header.Set("X-Helper-Lease-ID", leaseOut.LeaseID)
	hbRec := httptest.NewRecorder()
	mux.ServeHTTP(hbRec, hbReq)
	if hbRec.Code != http.StatusOK {
		t.Fatalf("expected 200 on lease.heartbeat, got %d body=%s", hbRec.Code, hbRec.Body.String())
	}
	var hbOut LeaseStatusResponse
	if err := json.Unmarshal(hbRec.Body.Bytes(), &hbOut); err != nil {
		t.Fatalf("decode lease.heartbeat: %v", err)
	}
	if hbOut.ExpiresAt == nil {
		t.Fatalf("expected renewed expiresAt")
	}
	if !hbOut.ExpiresAt.After(previousExpiry) {
		t.Fatalf("expected heartbeat to extend lease expiry: before=%s after=%s", previousExpiry, hbOut.ExpiresAt.UTC())
	}
}

func TestExpiredLeaseNoLongerBlocksMutatingCalls(t *testing.T) {
	manager := newHelperManager("")
	manager.mu.Lock()
	manager.lease = &leaseState{
		ID:        "expired-lease",
		Owner:     "tester",
		ExpiresAt: time.Now().UTC().Add(-1 * time.Minute),
	}
	manager.mu.Unlock()

	mux := newHelperMux(manager, "")
	body := `{
	  "deviceID": "dev-expired-lease",
	  "profileBootstrap": {
	    "addr": "127.0.0.1:8443",
	    "serverName": "localhost",
	    "clientID": "00112233445566778899aabbccddeeff",
	    "serverStaticPub": "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="
	  }
	}`
	req := httptest.NewRequest(http.MethodPost, "/v1/helper/bootstrap.apply", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for mutating call when lease is expired, got %d body=%s", rec.Code, rec.Body.String())
	}

	status := manager.leaseStatus()
	if status.Active {
		t.Fatalf("expected lease to be expired and inactive")
	}
}

func TestStatusIncludesLeaseSnapshot(t *testing.T) {
	manager := newHelperManager("")
	resp, err := manager.leaseAcquire(LeaseAcquireRequest{
		Owner: "status-owner",
		TTL:   60 * time.Second,
	})
	if err != nil {
		t.Fatalf("leaseAcquire: %v", err)
	}
	st := manager.status()
	if !st.Lease.Active {
		t.Fatalf("expected status lease active")
	}
	if st.Lease.LeaseID != resp.LeaseID {
		t.Fatalf("unexpected lease id in status: got=%q want=%q", st.Lease.LeaseID, resp.LeaseID)
	}
	if st.Lease.Owner != "status-owner" {
		t.Fatalf("unexpected lease owner in status: %q", st.Lease.Owner)
	}
}

func TestLeaseTakeoverRequiresPrevLeaseMatch(t *testing.T) {
	manager := newHelperManager("")
	original, err := manager.leaseAcquire(LeaseAcquireRequest{
		Owner: "owner-a",
		TTL:   60 * time.Second,
	})
	if err != nil {
		t.Fatalf("leaseAcquire: %v", err)
	}
	if original.LeaseID == "" {
		t.Fatalf("expected original lease id")
	}

	_, err = manager.leaseTakeover(LeaseTakeoverRequest{
		Owner:       "owner-b",
		TTL:         60 * time.Second,
		PrevLeaseID: "wrong-lease-id",
	})
	if err == nil {
		t.Fatalf("expected takeover mismatch error")
	}

	taken, err := manager.leaseTakeover(LeaseTakeoverRequest{
		Owner:       "owner-b",
		TTL:         60 * time.Second,
		PrevLeaseID: original.LeaseID,
	})
	if err != nil {
		t.Fatalf("leaseTakeover: %v", err)
	}
	if !taken.Active {
		t.Fatalf("expected active lease after takeover")
	}
	if taken.LeaseID == original.LeaseID {
		t.Fatalf("expected new lease id on takeover")
	}
	if taken.Owner != "owner-b" {
		t.Fatalf("unexpected takeover owner: %q", taken.Owner)
	}
}

func TestBridgeReconcileEndpoint(t *testing.T) {
	mux := newHelperMux(newHelperManager(""), "")
	req := httptest.NewRequest(http.MethodPost, "/v1/helper/bridge.reconcile", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var out BridgeReconcileResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode reconcile response: %v", err)
	}
	if out.Plan != "startup-needed" {
		t.Fatalf("expected startup-needed plan, got %q", out.Plan)
	}
	if !out.Lease.Active {
		t.Fatalf("expected reconcile to ensure lease by default")
	}
}

func TestBridgeReconcilePlanGatewayPolicyTuneNeeded(t *testing.T) {
	manager := newHelperManager("")
	now := time.Now().UTC()
	manager.mu.Lock()
	manager.deviceID = "dev-tune"
	manager.bootstrap = ProfileBootstrap{
		ClientID: "00112233445566778899aabbccddeeff",
		Gateways: []GatewayConfig{
			{GatewayID: "gw-1", Endpoints: []GatewayEndpointConfig{{Addr: "10.0.0.1:443"}}},
			{GatewayID: "gw-2", Endpoints: []GatewayEndpointConfig{{Addr: "10.0.0.2:443"}}},
		},
		GatewayPolicy: GatewayPolicy{
			SwitchHysteresis: 0,
			CooldownMin:      2 * time.Second,
			CooldownMax:      4 * time.Second,
		},
	}
	manager.hasBootstrap = true
	manager.running = true
	manager.linkID = helperLinkID(manager.deviceID, manager.bootstrap)
	manager.lastEvent = runtime.Event{
		State:      runtime.StateEstablished,
		ErrorClass: runtime.ErrorClassNone,
		Snapshot: runtime.Snapshot{
			State:                  runtime.StateEstablished,
			LinkID:                 manager.linkID,
			GatewaySelections:      12,
			GatewaySwitches:        6,
			GatewayCooldownSkips:   0,
			GatewayHysteresisKeeps: 0,
			LastTransitionAt:       now,
		},
	}
	manager.lastEventAt = &now
	manager.mu.Unlock()

	ensure := false
	out, err := manager.bridgeReconcile(BridgeReconcileRequest{
		Lease: BridgeLeaseConfig{Ensure: &ensure},
	})
	if err != nil {
		t.Fatalf("bridgeReconcile: %v", err)
	}
	if out.Plan != "gateway-policy-tune-needed" {
		t.Fatalf("expected gateway-policy-tune-needed, got %q", out.Plan)
	}
}

func TestBridgeStartupAndAutopilotEndpoints(t *testing.T) {
	manager := newHelperManager("")
	manager.runFn = func(ctx context.Context, _ ProfileBootstrap, onEvent func(runtime.Event)) error {
		onEvent(runtime.Event{
			State:      runtime.StateEstablished,
			ErrorClass: runtime.ErrorClassNone,
			Snapshot:   runtime.Snapshot{State: runtime.StateEstablished},
		})
		<-ctx.Done()
		return ctx.Err()
	}
	if err := manager.applyBootstrap(StartRequest{
		DeviceID: "dev-bridge",
		ProfileBootstrap: ProfileBootstrap{
			Addr:               "127.0.0.1:8443",
			ServerName:         "localhost",
			ClientID:           "00112233445566778899aabbccddeeff",
			ServerStaticPubB64: "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=",
		},
	}); err != nil {
		t.Fatalf("apply bootstrap: %v", err)
	}
	mux := newHelperMux(manager, "")
	startBody := `{"deviceID":"dev-bridge"}`
	startReq := httptest.NewRequest(http.MethodPost, "/v1/helper/bridge.startup", strings.NewReader(startBody))
	startReq.Header.Set("Content-Type", "application/json")
	startRec := httptest.NewRecorder()
	mux.ServeHTTP(startRec, startReq)
	if startRec.Code != http.StatusOK {
		t.Fatalf("expected 200 on bridge.startup, got %d body=%s", startRec.Code, startRec.Body.String())
	}
	var startupOut BridgeStartupResponse
	if err := json.Unmarshal(startRec.Body.Bytes(), &startupOut); err != nil {
		t.Fatalf("decode startup response: %v", err)
	}
	if !startupOut.OK || !startupOut.Started {
		t.Fatalf("unexpected startup response: %+v", startupOut)
	}
	if startupOut.Profile == nil || len(startupOut.Profile.Profiles) == 0 {
		t.Fatalf("expected startup response to include current profile bundle, got %+v", startupOut)
	}

	apReq := httptest.NewRequest(http.MethodPost, "/v1/helper/bridge.autopilot", strings.NewReader(`{"maxSteps":2}`))
	apReq.Header.Set("Content-Type", "application/json")
	apRec := httptest.NewRecorder()
	mux.ServeHTTP(apRec, apReq)
	if apRec.Code != http.StatusOK {
		t.Fatalf("expected 200 on bridge.autopilot, got %d body=%s", apRec.Code, apRec.Body.String())
	}
	var apOut BridgeAutopilotResponse
	if err := json.Unmarshal(apRec.Body.Bytes(), &apOut); err != nil {
		t.Fatalf("decode autopilot response: %v", err)
	}
	if !apOut.OK || apOut.FinalPlan != "running-ok" {
		t.Fatalf("unexpected autopilot response: %+v", apOut)
	}
	if len(apOut.Steps) == 0 || apOut.Steps[0].Action != "noop" {
		t.Fatalf("unexpected autopilot steps: %+v", apOut.Steps)
	}
	if err := manager.stop(2 * time.Second); err != nil {
		t.Fatalf("stop manager: %v", err)
	}
}

func TestBridgeAutopilotPolicyTuneRestart(t *testing.T) {
	manager := newHelperManager("")
	manager.runFn = func(ctx context.Context, _ ProfileBootstrap, onEvent func(runtime.Event)) error {
		onEvent(runtime.Event{
			State:      runtime.StateEstablished,
			ErrorClass: runtime.ErrorClassNone,
			Snapshot: runtime.Snapshot{
				State: runtime.StateEstablished,
			},
		})
		<-ctx.Done()
		return ctx.Err()
	}
	if err := manager.applyBootstrap(StartRequest{
		DeviceID: "dev-policy-tune",
		ProfileBootstrap: ProfileBootstrap{
			Addr:               "127.0.0.1:8443",
			ServerName:         "localhost",
			ClientID:           "00112233445566778899aabbccddeeff",
			ServerStaticPubB64: "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=",
			Gateways: []GatewayConfig{
				{GatewayID: "gw-1", Endpoints: []GatewayEndpointConfig{{Addr: "10.0.0.1:443"}}},
				{GatewayID: "gw-2", Endpoints: []GatewayEndpointConfig{{Addr: "10.0.0.2:443"}}},
			},
			GatewayPolicy: GatewayPolicy{
				SwitchHysteresis: 0,
				CooldownMin:      2 * time.Second,
				CooldownMax:      4 * time.Second,
			},
		},
	}); err != nil {
		t.Fatalf("apply bootstrap: %v", err)
	}
	if err := manager.startStored(); err != nil {
		t.Fatalf("startStored: %v", err)
	}
	defer func() {
		_ = manager.stop(2 * time.Second)
	}()
	manager.mu.Lock()
	now := time.Now().UTC()
	manager.lastEvent = runtime.Event{
		State:      runtime.StateEstablished,
		ErrorClass: runtime.ErrorClassNone,
		Snapshot: runtime.Snapshot{
			State:                  runtime.StateEstablished,
			LinkID:                 manager.linkID,
			GatewaySelections:      12,
			GatewaySwitches:        6,
			GatewayCooldownSkips:   0,
			GatewayHysteresisKeeps: 0,
			LastTransitionAt:       now,
		},
	}
	manager.lastEventAt = &now
	manager.mu.Unlock()

	ensure := false
	out, err := manager.bridgeAutopilot(BridgeAutopilotRequest{
		MaxSteps: 3,
		Startup: BridgeStartupRequest{
			DeviceID: "dev-policy-tune",
		},
		Shutdown: BridgeShutdownRequest{
			Timeout: 2 * time.Second,
		},
		Reconcile: BridgeReconcileRequest{
			Lease: BridgeLeaseConfig{Ensure: &ensure},
		},
	})
	if err != nil {
		t.Fatalf("bridgeAutopilot: %v", err)
	}
	if !out.OK || out.FinalPlan != "running-ok" {
		t.Fatalf("unexpected autopilot output: %+v", out)
	}
	if len(out.Steps) == 0 || out.Steps[0].Action != "policy-tune-restart" {
		t.Fatalf("expected first step policy-tune-restart, got %+v", out.Steps)
	}
	if manager.bootstrap.GatewayPolicy.SwitchHysteresis < 10 {
		t.Fatalf("expected tuned switch hysteresis, got %+v", manager.bootstrap.GatewayPolicy)
	}
	if manager.bootstrap.GatewayPolicy.CooldownMin < 4*time.Second {
		t.Fatalf("expected tuned cooldownMin >= 4s, got %+v", manager.bootstrap.GatewayPolicy)
	}
}

func TestBridgeStartupRollsBackBootstrapOnStartFailure(t *testing.T) {
	manager := newHelperManager("")
	manager.runFn = func(ctx context.Context, _ ProfileBootstrap, onEvent func(runtime.Event)) error {
		onEvent(runtime.Event{
			State:      runtime.StateEstablished,
			ErrorClass: runtime.ErrorClassNone,
			Snapshot:   runtime.Snapshot{State: runtime.StateEstablished},
		})
		<-ctx.Done()
		return ctx.Err()
	}

	original := StartRequest{
		DeviceID: "dev-orig",
		ProfileBootstrap: ProfileBootstrap{
			Addr:               "127.0.0.1:8443",
			ServerName:         "localhost",
			ClientID:           "00112233445566778899aabbccddeeff",
			ServerStaticPubB64: "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=",
		},
	}
	if err := manager.applyBootstrap(original); err != nil {
		t.Fatalf("apply original bootstrap: %v", err)
	}
	if err := manager.start(original); err != nil {
		t.Fatalf("start original runtime: %v", err)
	}
	defer func() {
		_ = manager.stop(2 * time.Second)
	}()

	mux := newHelperMux(manager, "")
	body := `{
	  "deviceID":"dev-new",
	  "profileBootstrap":{
	    "addr":"127.0.0.1:9443",
	    "serverName":"new.example",
	    "clientID":"ffeeddccbbaa99887766554433221100",
	    "serverStaticPub":"MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="
	  }
	}`
	req := httptest.NewRequest(http.MethodPost, "/v1/helper/bridge.startup", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 when startup called while running, got %d body=%s", rec.Code, rec.Body.String())
	}

	st := manager.status()
	if st.DeviceID != original.DeviceID {
		t.Fatalf("expected device rollback to %q, got %q", original.DeviceID, st.DeviceID)
	}
	if manager.bootstrap.ClientID != original.ProfileBootstrap.ClientID {
		t.Fatalf("expected bootstrap rollback clientID=%q got=%q", original.ProfileBootstrap.ClientID, manager.bootstrap.ClientID)
	}
}

func TestBridgeStartupRejectsProfileBundleBootstrapMismatch(t *testing.T) {
	manager := newHelperManager("")
	mux := newHelperMux(manager, "")
	body := `{
	  "deviceID":"dev-bundle-mismatch",
	  "profileBootstrap":{
	    "profileID":"ru-balanced",
	    "securityProfile":"balanced",
	    "addr":"127.0.0.1:8443",
	    "serverName":"localhost",
	    "clientID":"00112233445566778899aabbccddeeff",
	    "serverStaticPub":"MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="
	  },
	  "profileBundle":{
	    "apiVersion":"2026-04-14",
	    "version":"v1",
	    "profiles":[
	      {
	        "id":"eu-compat",
	        "region":"EU",
	        "securityProfile":"compat",
	        "revision":1
	      }
	    ]
	  }
	}`
	req := httptest.NewRequest(http.MethodPost, "/v1/helper/bridge.startup", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 on profile mismatch, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "does not match profileBundle.profiles[0]") {
		t.Fatalf("expected mismatch details in response body, got %s", rec.Body.String())
	}
	if _, ok := manager.currentProfileBundle(); ok {
		t.Fatalf("expected profile bundle to remain unset after mismatch")
	}
}

func TestBridgeStartupWithProfileBundleKeepsBundleAsAuthoritative(t *testing.T) {
	manager := newHelperManager("")
	manager.runFn = func(ctx context.Context, _ ProfileBootstrap, onEvent func(runtime.Event)) error {
		onEvent(runtime.Event{
			State:      runtime.StateEstablished,
			ErrorClass: runtime.ErrorClassNone,
			Snapshot:   runtime.Snapshot{State: runtime.StateEstablished},
		})
		<-ctx.Done()
		return ctx.Err()
	}
	mux := newHelperMux(manager, "")
	body := `{
	  "deviceID":"dev-bundle-ok",
	  "profileBootstrap":{
	    "addr":"127.0.0.1:8443",
	    "serverName":"localhost",
	    "clientID":"00112233445566778899aabbccddeeff",
	    "serverStaticPub":"MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="
	  },
	  "profileBundle":{
	    "apiVersion":"2026-04-14",
	    "version":"v1",
	    "profiles":[
	      {
	        "id":"ru-high",
	        "region":"RU",
	        "securityProfile":"high_risk",
	        "revision":2,
	        "tun":{"mode":"full","lockdown":true},
	        "bridge":{"allowLocalTCPBridge":false,"allowLocalControlAPI":false}
	      }
	    ]
	  }
	}`
	req := httptest.NewRequest(http.MethodPost, "/v1/helper/bridge.startup", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 with profile bundle, got %d body=%s", rec.Code, rec.Body.String())
	}
	var startupOut BridgeStartupResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &startupOut); err != nil {
		t.Fatalf("decode startup response: %v", err)
	}
	if startupOut.Profile == nil || len(startupOut.Profile.Profiles) == 0 {
		t.Fatalf("expected startup profile in response, got %+v", startupOut)
	}
	if startupOut.Profile.Profiles[0].ID != "ru-high" {
		t.Fatalf("expected profile id ru-high, got %+v", startupOut.Profile.Profiles[0])
	}
	cur, ok := manager.currentProfileBundle()
	if !ok || len(cur.Profiles) == 0 {
		t.Fatalf("expected current profile bundle in manager")
	}
	if cur.Profiles[0].ID != "ru-high" || cur.Profiles[0].SecurityProfile != runtime.SecurityProfileHighRisk {
		t.Fatalf("expected manager current profile ru-high/high_risk, got %+v", cur.Profiles[0])
	}
	if manager.bootstrap.ProfileID != "ru-high" || manager.bootstrap.SecurityProfile != runtime.SecurityProfileHighRisk {
		t.Fatalf("expected bootstrap profile fields to follow bundle, got profileID=%q securityProfile=%q", manager.bootstrap.ProfileID, manager.bootstrap.SecurityProfile)
	}
	if err := manager.stop(2 * time.Second); err != nil {
		t.Fatalf("stop manager: %v", err)
	}
}

func TestBridgeAutopilotDaemonEndpoint(t *testing.T) {
	manager := newHelperManager("")
	manager.runFn = func(ctx context.Context, _ ProfileBootstrap, onEvent func(runtime.Event)) error {
		onEvent(runtime.Event{
			State:      runtime.StateEstablished,
			ErrorClass: runtime.ErrorClassNone,
			Snapshot:   runtime.Snapshot{State: runtime.StateEstablished},
		})
		<-ctx.Done()
		return ctx.Err()
	}
	if err := manager.applyBootstrap(StartRequest{
		DeviceID: "dev-daemon",
		ProfileBootstrap: ProfileBootstrap{
			Addr:               "127.0.0.1:8443",
			ServerName:         "localhost",
			ClientID:           "00112233445566778899aabbccddeeff",
			ServerStaticPubB64: "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=",
		},
	}); err != nil {
		t.Fatalf("apply bootstrap: %v", err)
	}
	mux := newHelperMux(manager, "")
	body := `{
	  "interval": 100000000,
	  "duration": 0,
	  "continueOnError": true,
	  "autopilot": {
	    "maxSteps": 2
	  }
	}`
	req := httptest.NewRequest(http.MethodPost, "/v1/helper/bridge.autopilot.daemon", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var out BridgeAutopilotDaemonResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode daemon response: %v", err)
	}
	if out.Runs < 1 || len(out.Ticks) < 1 {
		t.Fatalf("expected at least one tick, got runs=%d ticks=%d", out.Runs, len(out.Ticks))
	}
	if err := manager.stop(2 * time.Second); err != nil {
		t.Fatalf("stop manager: %v", err)
	}
}

func TestBridgeStatusStreamEndpoint(t *testing.T) {
	manager := newHelperManager("")
	mux := newHelperMux(manager, "")
	req := httptest.NewRequest(http.MethodGet, "/v1/helper/bridge.status.stream?interval=20ms&duration=80ms", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "event: status") {
		t.Fatalf("expected status events in stream body")
	}
	if !strings.Contains(body, "event: done") {
		t.Fatalf("expected done event in stream body")
	}
}

func TestLinksHealthStreamEndpoint(t *testing.T) {
	manager := newHelperManager("")
	mux := newHelperMux(manager, "")
	req := httptest.NewRequest(http.MethodGet, "/v1/helper/links/health.stream?interval=20ms&duration=80ms", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "event: links") {
		t.Fatalf("expected links events in stream body")
	}
	if !strings.Contains(body, "event: done") {
		t.Fatalf("expected done event in stream body")
	}
}
