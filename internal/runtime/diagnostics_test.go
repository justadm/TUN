package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestJSONEventLoggerWritesJSONLines(t *testing.T) {
	var buf bytes.Buffer
	l := NewJSONEventLogger(&buf)
	e := Event{
		State:      StateEstablished,
		ErrorClass: ErrorClassNone,
		Snapshot: Snapshot{
			State:    StateEstablished,
			Attempts: 2,
		},
	}
	if err := l.OnEvent(e); err != nil {
		t.Fatalf("log event: %v", err)
	}
	var obj map[string]any
	if err := json.Unmarshal(buf.Bytes(), &obj); err != nil {
		t.Fatalf("unmarshal line json: %v", err)
	}
	if obj["state"] != string(StateEstablished) {
		t.Fatalf("unexpected state: %v", obj["state"])
	}
}

func TestSupportBundleCollectorKeepsNewestEvents(t *testing.T) {
	c := NewSupportBundleCollector(2)
	c.OnEvent(Event{State: StateIdle, ErrorClass: ErrorClassNone, Snapshot: Snapshot{State: StateIdle}})
	c.OnEvent(Event{State: StateDialing, ErrorClass: ErrorClassNone, Snapshot: Snapshot{State: StateDialing}})
	c.OnEvent(Event{
		State:      StateStopped,
		ErrorClass: ErrorClassContext,
		Cause:      context.Canceled,
		Snapshot:   Snapshot{State: StateStopped},
	})
	b := c.Build()
	if len(b.Events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(b.Events))
	}
	if b.DroppedEvents != 1 {
		t.Fatalf("expected 1 dropped event, got %d", b.DroppedEvents)
	}
	if b.TotalEvents != 3 {
		t.Fatalf("expected total 3 events, got %d", b.TotalEvents)
	}
	if b.FinalSnapshot.State != StateStopped {
		t.Fatalf("expected final state stopped, got %s", b.FinalSnapshot.State)
	}
	if b.Events[1].Cause == "" {
		t.Fatalf("expected final cause to be captured")
	}
}

func TestSupportBundleCollectorExportJSON(t *testing.T) {
	c := NewSupportBundleCollector(4)
	c.OnEvent(Event{
		State:      StateReconnecting,
		ErrorClass: ErrorClassTransport,
		Cause:      errors.New("transport reset"),
		Snapshot: Snapshot{
			State:      StateReconnecting,
			Reconnects: 1,
		},
	})
	raw, err := c.ExportJSON()
	if err != nil {
		t.Fatalf("export json: %v", err)
	}
	var out SupportBundle
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal bundle: %v", err)
	}
	if out.TotalEvents != 1 {
		t.Fatalf("expected total events=1, got %d", out.TotalEvents)
	}
	if out.Events[0].ErrorClass != ErrorClassTransport {
		t.Fatalf("expected transport class, got %s", out.Events[0].ErrorClass)
	}
}

func TestSupportBundleCollectorExportJSONWithConfig(t *testing.T) {
	c := NewSupportBundleCollector(2)
	c.OnEvent(Event{
		State:      StateEstablished,
		ErrorClass: ErrorClassNone,
		Snapshot:   Snapshot{State: StateEstablished, LastError: "token=abc123"},
	})
	raw, err := c.ExportJSONWithConfig(SupportBundleConfig{
		Role:           RoleServer,
		RuntimeVersion: "1.2.3",
		BuildInfo:      "build-abc",
		Ring:           "canary",
		HostID:         "node-01",
	})
	if err != nil {
		t.Fatalf("export with config: %v", err)
	}
	var out SupportBundle
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal bundle: %v", err)
	}
	if out.BundleVersion != SupportBundleVersion {
		t.Fatalf("expected bundle version %s, got %s", SupportBundleVersion, out.BundleVersion)
	}
	if out.Runtime.Version != "1.2.3" || out.Runtime.Build != "build-abc" || out.Runtime.Role != RoleServer {
		t.Fatalf("unexpected runtime metadata: %+v", out.Runtime)
	}
	if out.Environment.Ring != "canary" || out.Environment.HostID != "node-01" {
		t.Fatalf("unexpected environment metadata: %+v", out.Environment)
	}
	if out.FinalSnapshot.LastError != "[redacted]" {
		t.Fatalf("expected redacted final error, got %q", out.FinalSnapshot.LastError)
	}
}

func TestSupportBundleCarriesLinkTelemetryContract(t *testing.T) {
	now := time.Now().UTC()
	c := NewSupportBundleCollector(4)
	c.OnEvent(Event{
		State:      StateEstablished,
		ErrorClass: ErrorClassNone,
		Snapshot: Snapshot{
			State:           StateEstablished,
			LinkID:          "client:dev-01:tun0",
			SessionID:       "sess-123",
			LastHandshakeAt: now,
			LastRxAt:        now.Add(1 * time.Second),
			LastTxAt:        now.Add(2 * time.Second),
			RxBytes:         1024,
			TxBytes:         2048,
		},
	})
	bundle := c.Build()
	if bundle.FinalSnapshot.LinkID != "client:dev-01:tun0" {
		t.Fatalf("unexpected link id: %q", bundle.FinalSnapshot.LinkID)
	}
	if bundle.FinalSnapshot.SessionID != "sess-123" {
		t.Fatalf("unexpected session id: %q", bundle.FinalSnapshot.SessionID)
	}
	if bundle.FinalSnapshot.LastHandshakeAt.IsZero() || bundle.FinalSnapshot.LastRxAt.IsZero() || bundle.FinalSnapshot.LastTxAt.IsZero() {
		t.Fatalf("expected handshake/rx/tx timestamps in final snapshot: %+v", bundle.FinalSnapshot)
	}
	if bundle.FinalSnapshot.RxBytes != 1024 || bundle.FinalSnapshot.TxBytes != 2048 {
		t.Fatalf("unexpected traffic counters in final snapshot: rx=%d tx=%d", bundle.FinalSnapshot.RxBytes, bundle.FinalSnapshot.TxBytes)
	}
	if len(bundle.Events) != 1 {
		t.Fatalf("expected one event, got %d", len(bundle.Events))
	}
	ev := bundle.Events[0].Snapshot
	if ev.LinkID != bundle.FinalSnapshot.LinkID || ev.SessionID != bundle.FinalSnapshot.SessionID {
		t.Fatalf("event snapshot contract mismatch: event=%+v final=%+v", ev, bundle.FinalSnapshot)
	}
}

func TestRotatingFileWriterRotateBySize(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")
	w, err := NewRotatingFileWriter(path, RotationOptions{
		MaxBytes:   20,
		MaxBackups: 2,
	})
	if err != nil {
		t.Fatalf("new rotating writer: %v", err)
	}
	defer w.Close()

	if _, err := w.Write([]byte("1234567890\n")); err != nil {
		t.Fatalf("write1: %v", err)
	}
	if _, err := w.Write([]byte("abcdefghij\n")); err != nil {
		t.Fatalf("write2: %v", err)
	}
	if _, err := w.Write([]byte("KLMNOPQRST\n")); err != nil {
		t.Fatalf("write3: %v", err)
	}

	if _, err := os.Stat(path + ".1"); err != nil {
		t.Fatalf("expected rotated backup .1: %v", err)
	}
	// Ensure we don't exceed backup count after more rotations.
	if _, err := w.Write([]byte("UVWXYZ1234\n")); err != nil {
		t.Fatalf("write4: %v", err)
	}
	if _, err := w.Write([]byte("zzzzzzzzzz\n")); err != nil {
		t.Fatalf("write5: %v", err)
	}
	if _, err := os.Stat(path + ".3"); !os.IsNotExist(err) {
		t.Fatalf("expected no .3 backup, got err=%v", err)
	}
}

func TestRotatingFileWriterRotateByInterval(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events-int.jsonl")
	w, err := NewRotatingFileWriter(path, RotationOptions{
		RotateInterval: 5 * time.Millisecond,
		MaxBackups:     1,
	})
	if err != nil {
		t.Fatalf("new rotating writer: %v", err)
	}
	defer w.Close()

	if _, err := w.Write([]byte("line-1\n")); err != nil {
		t.Fatalf("write1: %v", err)
	}
	time.Sleep(10 * time.Millisecond)
	if _, err := w.Write([]byte("line-2\n")); err != nil {
		t.Fatalf("write2: %v", err)
	}
	if _, err := os.Stat(path + ".1"); err != nil {
		t.Fatalf("expected interval rotation backup .1: %v", err)
	}
}

func TestSupportBundleEnvelopeSignAndVerify(t *testing.T) {
	c := NewSupportBundleCollector(4)
	c.OnEvent(Event{
		State:      StateStopped,
		ErrorClass: ErrorClassTransport,
		Cause:      errors.New("bearer SECRET123"),
		Snapshot:   Snapshot{State: StateStopped},
	})
	key := []byte("super-secret-signing-key")
	raw, err := c.ExportEnvelopeJSONWithConfig(SupportBundleConfig{
		Role: RoleServer,
	}, SigningOptions{
		Key:   key,
		KeyID: "k1",
	})
	if err != nil {
		t.Fatalf("export envelope: %v", err)
	}
	var env SupportBundleEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}
	if env.EnvelopeVersion != SupportBundleEnvelopeVersion {
		t.Fatalf("bad envelope version: %s", env.EnvelopeVersion)
	}
	if env.SignatureHMACSHA256 == "" {
		t.Fatalf("expected signature")
	}
	if err := VerifySupportBundleEnvelope(env, SigningOptions{Key: key}); err != nil {
		t.Fatalf("verify envelope: %v", err)
	}
	// Tamper bundle after export and ensure verify fails.
	env.Bundle.TotalEvents = 99
	if err := VerifySupportBundleEnvelope(env, SigningOptions{Key: key}); !errors.Is(err, ErrBundleIntegrity) {
		t.Fatalf("expected integrity error, got %v", err)
	}
}
