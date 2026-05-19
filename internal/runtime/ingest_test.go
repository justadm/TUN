package runtime

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestVerifyEnvelopeForIngestUnsignedAllowed(t *testing.T) {
	c := NewSupportBundleCollector(2)
	c.OnEvent(Event{State: StateEstablished, ErrorClass: ErrorClassNone, Snapshot: Snapshot{State: StateEstablished}})
	raw, err := c.ExportEnvelopeJSONWithConfig(SupportBundleConfig{Role: RoleClient}, SigningOptions{})
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	var env SupportBundleEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if err := VerifyEnvelopeForIngest(env, VerificationKeyring{}, VerificationPolicy{RequireSignature: false}); err != nil {
		t.Fatalf("verify: %v", err)
	}
}

func TestVerifyEnvelopeForIngestUnsignedRejected(t *testing.T) {
	c := NewSupportBundleCollector(2)
	c.OnEvent(Event{State: StateEstablished, ErrorClass: ErrorClassNone, Snapshot: Snapshot{State: StateEstablished}})
	raw, err := c.ExportEnvelopeJSONWithConfig(SupportBundleConfig{Role: RoleClient}, SigningOptions{})
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	var env SupportBundleEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if err := VerifyEnvelopeForIngest(env, VerificationKeyring{}, VerificationPolicy{RequireSignature: true}); !errors.Is(err, ErrMissingSignature) {
		t.Fatalf("expected missing signature, got %v", err)
	}
}

func TestVerifyEnvelopeForIngestSignedActiveAndPrevious(t *testing.T) {
	c := NewSupportBundleCollector(2)
	c.OnEvent(Event{State: StateEstablished, ErrorClass: ErrorClassNone, Snapshot: Snapshot{State: StateEstablished}})
	key := []byte("rotation-key-active")
	raw, err := c.ExportEnvelopeJSONWithConfig(SupportBundleConfig{Role: RoleServer}, SigningOptions{
		Key:   key,
		KeyID: "k-active",
	})
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	var env SupportBundleEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	keyring := VerificationKeyring{
		Keys: map[string]VerificationKey{
			"k-active": {Key: key, Status: KeyStatusActive},
			"k-prev":   {Key: []byte("legacy"), Status: KeyStatusPrevious},
		},
	}
	if err := VerifyEnvelopeForIngest(env, keyring, VerificationPolicy{RequireSignature: true}); err != nil {
		t.Fatalf("verify active: %v", err)
	}
	keyring.Keys["k-active"] = VerificationKey{Key: key, Status: KeyStatusPrevious}
	if err := VerifyEnvelopeForIngest(env, keyring, VerificationPolicy{RequireSignature: true}); err != nil {
		t.Fatalf("verify previous: %v", err)
	}
}

func TestVerifyEnvelopeForIngestRetiredRejected(t *testing.T) {
	c := NewSupportBundleCollector(2)
	c.OnEvent(Event{State: StateStopped, ErrorClass: ErrorClassTransport, Snapshot: Snapshot{State: StateStopped}})
	key := []byte("rotation-key-retired")
	raw, err := c.ExportEnvelopeJSONWithConfig(SupportBundleConfig{Role: RoleServer}, SigningOptions{
		Key:   key,
		KeyID: "k-old",
	})
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	var env SupportBundleEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	err = VerifyEnvelopeForIngest(env, VerificationKeyring{
		Keys: map[string]VerificationKey{
			"k-old": {Key: key, Status: KeyStatusRetired},
		},
	}, VerificationPolicy{RequireSignature: true})
	if !errors.Is(err, ErrRetiredSigningKey) {
		t.Fatalf("expected retired-key error, got %v", err)
	}
}

func TestVerifyEnvelopeForIngestUnknownKeyRejected(t *testing.T) {
	c := NewSupportBundleCollector(2)
	c.OnEvent(Event{State: StateStopped, ErrorClass: ErrorClassTransport, Snapshot: Snapshot{State: StateStopped}})
	key := []byte("rotation-key-unknown")
	raw, err := c.ExportEnvelopeJSONWithConfig(SupportBundleConfig{Role: RoleServer}, SigningOptions{
		Key:   key,
		KeyID: "k-unknown",
	})
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	var env SupportBundleEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	err = VerifyEnvelopeForIngest(env, VerificationKeyring{
		Keys: map[string]VerificationKey{
			"k-active": {Key: []byte("other"), Status: KeyStatusActive},
		},
	}, VerificationPolicy{RequireSignature: true})
	if !errors.Is(err, ErrUnknownSigningKeyID) {
		t.Fatalf("expected unknown-key error, got %v", err)
	}
}

func TestVerifyEnvelopeForIngestTamperedRejected(t *testing.T) {
	c := NewSupportBundleCollector(2)
	c.OnEvent(Event{State: StateEstablished, ErrorClass: ErrorClassNone, Snapshot: Snapshot{State: StateEstablished}})
	key := []byte("rotation-key-tamper")
	raw, err := c.ExportEnvelopeJSONWithConfig(SupportBundleConfig{Role: RoleClient}, SigningOptions{
		Key:   key,
		KeyID: "k1",
	})
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	var env SupportBundleEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	env.Bundle.TotalEvents = 77
	err = VerifyEnvelopeForIngest(env, VerificationKeyring{
		Keys: map[string]VerificationKey{
			"k1": {Key: key, Status: KeyStatusActive},
		},
	}, VerificationPolicy{RequireSignature: true})
	if !errors.Is(err, ErrBundleIntegrity) {
		t.Fatalf("expected integrity error, got %v", err)
	}
}
