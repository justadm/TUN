package runtime

import (
	"errors"
	"testing"
	"time"
)

func TestTransportRetryPolicyHandshakeBurst(t *testing.T) {
	p := NewTransportRetryPolicy()
	p.HandshakeFailureLimit = 3
	p.HandshakeFailureWindow = 2 * time.Minute
	p.HandshakeBurstCooldown = 7 * time.Second

	now := time.Unix(1000, 0)
	for i := 0; i < 2; i++ {
		d := p.Decide(RetryInput{
			Error:   errors.New("hs"),
			Class:   ErrorClassHandshake,
			Attempt: i,
			Now:     now.Add(time.Duration(i) * time.Second),
		})
		if !d.Retry || d.ExtraDelay != 0 {
			t.Fatalf("expected retry without burst cooldown, got %+v", d)
		}
	}
	d := p.Decide(RetryInput{
		Error:   errors.New("hs"),
		Class:   ErrorClassHandshake,
		Attempt: 2,
		Now:     now.Add(2 * time.Second),
	})
	if !d.Retry {
		t.Fatalf("expected retry")
	}
	if d.Reason != "handshake_burst" {
		t.Fatalf("expected handshake_burst reason, got %s", d.Reason)
	}
	if d.ExtraDelay != 7*time.Second {
		t.Fatalf("expected burst cooldown, got %s", d.ExtraDelay)
	}
}

func TestTransportRetryPolicyConsecutiveFailuresCooldown(t *testing.T) {
	p := NewTransportRetryPolicy()
	p.MaxConsecutiveFailures = 3
	p.ConsecutiveCooldown = 11 * time.Second

	now := time.Unix(2000, 0)
	for i := 0; i < 2; i++ {
		d := p.Decide(RetryInput{
			Error:   errors.New("dial"),
			Class:   ErrorClassDial,
			Attempt: i,
			Now:     now.Add(time.Duration(i) * time.Second),
		})
		if !d.Retry || d.ExtraDelay != 0 {
			t.Fatalf("expected plain retry, got %+v", d)
		}
	}
	d := p.Decide(RetryInput{
		Error:   errors.New("dial"),
		Class:   ErrorClassDial,
		Attempt: 2,
		Now:     now.Add(2 * time.Second),
	})
	if !d.Retry {
		t.Fatalf("expected retry")
	}
	if d.Reason != "consecutive_failures" {
		t.Fatalf("expected consecutive_failures reason, got %s", d.Reason)
	}
	if d.ExtraDelay != 11*time.Second {
		t.Fatalf("expected cooldown delay, got %s", d.ExtraDelay)
	}
}
