package core

import "testing"

func TestReplayWindowAccept(t *testing.T) {
	w := NewReplayWindow(64)
	if !w.Accept(1) {
		t.Fatalf("expected accept seq=1")
	}
	if w.Accept(1) {
		t.Fatalf("expected reject duplicate seq=1")
	}
	if !w.Accept(2) {
		t.Fatalf("expected accept seq=2")
	}
}

func TestReplayWindowOld(t *testing.T) {
	w := NewReplayWindow(64)
	w.Accept(100)
	if w.Accept(36) { // 100-36=64, should be rejected
		t.Fatalf("expected reject old seq")
	}
}

func TestReplayWindowShift(t *testing.T) {
	w := NewReplayWindow(64)
	w.Accept(1)
	w.Accept(2)
	w.Accept(70) // shift beyond window
	if w.Accept(1) {
		t.Fatalf("expected reject very old seq")
	}
	if w.Accept(70) {
		t.Fatalf("expected reject duplicate")
	}
	if !w.Accept(71) {
		t.Fatalf("expected accept seq=71")
	}
}
