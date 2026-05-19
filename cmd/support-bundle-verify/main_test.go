package main

import (
	"os"
	"path/filepath"
	"testing"

	"tun/internal/runtime"
)

func TestSplitKeySpec(t *testing.T) {
	id, path, err := splitKeySpec("k1=/tmp/key")
	if err != nil {
		t.Fatalf("split: %v", err)
	}
	if id != "k1" || path != "/tmp/key" {
		t.Fatalf("unexpected split result id=%q path=%q", id, path)
	}
}

func TestBuildKeyring(t *testing.T) {
	dir := t.TempDir()
	activePath := filepath.Join(dir, "active.key")
	prevPath := filepath.Join(dir, "prev.key")
	if err := os.WriteFile(activePath, []byte("active-secret\n"), 0o600); err != nil {
		t.Fatalf("write active key: %v", err)
	}
	if err := os.WriteFile(prevPath, []byte("prev-secret"), 0o600); err != nil {
		t.Fatalf("write previous key: %v", err)
	}
	keyring, err := buildKeyring(
		[]string{"k-active=" + activePath},
		[]string{"k-prev=" + prevPath},
		[]string{"k-old"},
	)
	if err != nil {
		t.Fatalf("build keyring: %v", err)
	}
	if keyring.Keys["k-active"].Status != runtime.KeyStatusActive {
		t.Fatalf("expected active status")
	}
	if string(keyring.Keys["k-active"].Key) != "active-secret" {
		t.Fatalf("unexpected active key value")
	}
	if keyring.Keys["k-prev"].Status != runtime.KeyStatusPrevious {
		t.Fatalf("expected previous status")
	}
	if keyring.Keys["k-old"].Status != runtime.KeyStatusRetired {
		t.Fatalf("expected retired status")
	}
}
