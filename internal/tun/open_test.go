package tun

import (
	"errors"
	"runtime"
	"testing"
)

func TestOpenUnsupportedOnNonLinux(t *testing.T) {
	if runtime.GOOS == "linux" || runtime.GOOS == "darwin" {
		t.Skip("this test checks unsupported-platform behavior")
	}
	_, err := Open(OpenOptions{})
	if !errors.Is(err, ErrUnsupportedPlatform) {
		t.Fatalf("expected ErrUnsupportedPlatform, got %v", err)
	}
}

func TestPreflightUnsupportedOnNonLinux(t *testing.T) {
	if runtime.GOOS == "linux" || runtime.GOOS == "darwin" {
		t.Skip("this test checks unsupported-platform behavior")
	}
	err := Preflight(OpenOptions{})
	if !errors.Is(err, ErrUnsupportedPlatform) {
		t.Fatalf("expected ErrUnsupportedPlatform, got %v", err)
	}
}

func TestValidateOpenOptionsInvalidMTU(t *testing.T) {
	if err := validateOpenOptions(OpenOptions{MTU: -1}); !errors.Is(err, ErrInvalidMTU) {
		t.Fatalf("expected ErrInvalidMTU for negative MTU, got %v", err)
	}
	if err := validateOpenOptions(OpenOptions{MTU: 70000}); !errors.Is(err, ErrInvalidMTU) {
		t.Fatalf("expected ErrInvalidMTU for oversize MTU, got %v", err)
	}
	if err := validateOpenOptions(OpenOptions{MTU: 1500}); err != nil {
		t.Fatalf("expected valid MTU, got %v", err)
	}
}

func TestValidateOpenOptionsInvalidAddressOrRoute(t *testing.T) {
	if err := validateOpenOptions(OpenOptions{Addresses: []string{"not-a-cidr"}}); !errors.Is(err, ErrInvalidAddressCIDR) {
		t.Fatalf("expected ErrInvalidAddressCIDR, got %v", err)
	}
	if err := validateOpenOptions(OpenOptions{Routes: []string{"also-not-a-cidr"}}); !errors.Is(err, ErrInvalidRouteCIDR) {
		t.Fatalf("expected ErrInvalidRouteCIDR, got %v", err)
	}
	if err := validateOpenOptions(OpenOptions{
		Addresses: []string{"10.10.0.2/24", "fd00::2/64"},
		Routes:    []string{"10.20.0.0/16", "default", "fd10::/64"},
	}); err != nil {
		t.Fatalf("expected valid addresses/routes, got %v", err)
	}
}

func TestValidateOpenOptionsConfigMode(t *testing.T) {
	if err := validateOpenOptions(OpenOptions{ConfigMode: "weird"}); !errors.Is(err, ErrInvalidConfigMode) {
		t.Fatalf("expected ErrInvalidConfigMode, got %v", err)
	}
	if err := validateOpenOptions(OpenOptions{ConfigMode: ConfigModeReplace}); err != nil {
		t.Fatalf("expected valid replace mode, got %v", err)
	}
	if err := validateOpenOptions(OpenOptions{ConfigMode: ConfigModeAdd}); err != nil {
		t.Fatalf("expected valid add mode, got %v", err)
	}
	if err := validateOpenOptions(OpenOptions{}); err != nil {
		t.Fatalf("expected default mode to be valid, got %v", err)
	}
}
