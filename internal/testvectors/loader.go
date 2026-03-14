package testvectors

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// Vector represents an AEAD test vector.
type Vector struct {
	ID            string `json:"id"`
	Algorithm     string `json:"algorithm"`
	KeyHex        string `json:"key_hex"`
	NonceHex      string `json:"nonce_hex"`
	AADHex        string `json:"aad_hex"`
	PlaintextHex  string `json:"plaintext_hex"`
	CiphertextHex string `json:"ciphertext_hex"`
	TagHex        string `json:"tag_hex"`
	Source        string `json:"source"`
	SourceURL     string `json:"source_url"`
}

// File represents the test vectors file.
type File struct {
	FormatVersion string   `json:"format_version"`
	Vectors       []Vector `json:"vectors"`
}

// LoadFromRepo loads test-vectors.json from .docs/spec by walking up to go.mod.
func LoadFromRepo() (*File, error) {
	root, err := findRepoRoot()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(root, ".docs", "spec", "test-vectors.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f File
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, err
	}
	return &f, nil
}

func findRepoRoot() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(wd, "go.mod")); err == nil {
			return wd, nil
		}
		parent := filepath.Dir(wd)
		if parent == wd {
			break
		}
		wd = parent
	}
	return "", errors.New("repo root not found (go.mod missing)")
}
