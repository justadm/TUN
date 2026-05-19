//go:build darwin

package tun

import (
	"net"
	"testing"
)

func TestInferDarwinPeerIPv4_30(t *testing.T) {
	ip := net.ParseIP("10.255.10.2")
	_, n, _ := net.ParseCIDR("10.255.10.2/30")
	peer := inferDarwinPeerIPv4(ip, n)
	if got := peer.String(); got != "10.255.10.1" {
		t.Fatalf("expected peer 10.255.10.1, got %s", got)
	}
}

func TestInferDarwinPeerIPv4_31(t *testing.T) {
	ip := net.ParseIP("10.1.1.4")
	_, n, _ := net.ParseCIDR("10.1.1.4/31")
	peer := inferDarwinPeerIPv4(ip, n)
	if got := peer.String(); got != "10.1.1.5" {
		t.Fatalf("expected peer 10.1.1.5, got %s", got)
	}
}

func TestInferDarwinPeerIPv4_32(t *testing.T) {
	ip := net.ParseIP("10.1.1.9")
	_, n, _ := net.ParseCIDR("10.1.1.9/32")
	peer := inferDarwinPeerIPv4(ip, n)
	if got := peer.String(); got != "10.1.1.9" {
		t.Fatalf("expected peer 10.1.1.9, got %s", got)
	}
}
