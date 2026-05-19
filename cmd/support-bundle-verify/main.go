package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"tun/internal/runtime"
)

type multiFlag []string

func (m *multiFlag) String() string {
	return strings.Join(*m, ",")
}

func (m *multiFlag) Set(v string) error {
	*m = append(*m, v)
	return nil
}

func main() {
	in := flag.String("in", "", "path to support bundle envelope JSON")
	requireSignature := flag.Bool("require-signature", true, "reject unsigned envelopes")
	var activeKeySpecs multiFlag
	var previousKeySpecs multiFlag
	var retiredKeyIDs multiFlag
	flag.Var(&activeKeySpecs, "active-key", "active key spec: key-id=path")
	flag.Var(&previousKeySpecs, "previous-key", "previous key spec: key-id=path")
	flag.Var(&retiredKeyIDs, "retired-key-id", "retired key id (repeatable)")
	flag.Parse()

	if *in == "" {
		log.Fatal("-in is required")
	}
	raw, err := os.ReadFile(*in)
	if err != nil {
		log.Fatalf("read envelope: %v", err)
	}
	var env runtime.SupportBundleEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		log.Fatalf("parse envelope: %v", err)
	}

	keyring, err := buildKeyring(activeKeySpecs, previousKeySpecs, retiredKeyIDs)
	if err != nil {
		log.Fatalf("keyring: %v", err)
	}
	if err := runtime.VerifyEnvelopeForIngest(env, keyring, runtime.VerificationPolicy{
		RequireSignature: *requireSignature,
	}); err != nil {
		log.Fatalf("verify failed: %v", err)
	}
	fmt.Printf("verified envelope version=%s bundle=%s signed=%t key_id=%s\n",
		env.EnvelopeVersion, env.Bundle.BundleVersion, env.SignatureHMACSHA256 != "", env.SignatureKeyID)
}

func buildKeyring(activeKeySpecs, previousKeySpecs, retiredKeyIDs []string) (runtime.VerificationKeyring, error) {
	keys := map[string]runtime.VerificationKey{}
	if err := loadKeySpecs(keys, activeKeySpecs, runtime.KeyStatusActive); err != nil {
		return runtime.VerificationKeyring{}, err
	}
	if err := loadKeySpecs(keys, previousKeySpecs, runtime.KeyStatusPrevious); err != nil {
		return runtime.VerificationKeyring{}, err
	}
	for _, id := range retiredKeyIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			return runtime.VerificationKeyring{}, fmt.Errorf("empty retired key id")
		}
		keys[id] = runtime.VerificationKey{Status: runtime.KeyStatusRetired}
	}
	return runtime.VerificationKeyring{Keys: keys}, nil
}

func loadKeySpecs(dst map[string]runtime.VerificationKey, specs []string, status runtime.KeyStatus) error {
	for _, spec := range specs {
		id, path, err := splitKeySpec(spec)
		if err != nil {
			return err
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read key %q: %w", id, err)
		}
		dst[id] = runtime.VerificationKey{
			Key:    []byte(strings.TrimSpace(string(raw))),
			Status: status,
		}
	}
	return nil
}

func splitKeySpec(spec string) (id, path string, err error) {
	parts := strings.SplitN(spec, "=", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid key spec %q, expected key-id=path", spec)
	}
	id = strings.TrimSpace(parts[0])
	path = strings.TrimSpace(parts[1])
	if id == "" || path == "" {
		return "", "", fmt.Errorf("invalid key spec %q, expected non-empty key-id and path", spec)
	}
	return id, path, nil
}
