package main

import "testing"

func TestParseCSVList(t *testing.T) {
	got := parseCSVList("10.0.0.1/24, default , ,fd00::1/64")
	if len(got) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(got))
	}
	if got[0] != "10.0.0.1/24" || got[1] != "default" || got[2] != "fd00::1/64" {
		t.Fatalf("unexpected values: %#v", got)
	}
}
