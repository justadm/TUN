package runtime

import (
	"context"
	"sync"
	"time"
)

type RetryInput struct {
	Error   error
	Class   ErrorClass
	Attempt int
	Now     time.Time
}

type RetryDecision struct {
	Retry      bool
	ExtraDelay time.Duration
	Reason     string
}

type RetryPolicy interface {
	Decide(input RetryInput) RetryDecision
	OnSuccess(now time.Time)
}

type TransportRetryPolicy struct {
	MaxConsecutiveFailures int
	ConsecutiveCooldown    time.Duration
	HandshakeFailureLimit  int
	HandshakeFailureWindow time.Duration
	HandshakeBurstCooldown time.Duration

	mu               sync.Mutex
	consecutiveFails int
	handshakeFails   []time.Time
}

func NewTransportRetryPolicy() *TransportRetryPolicy {
	return &TransportRetryPolicy{
		MaxConsecutiveFailures: 5,
		ConsecutiveCooldown:    5 * time.Minute,
		HandshakeFailureLimit:  3,
		HandshakeFailureWindow: 2 * time.Minute,
		HandshakeBurstCooldown: 30 * time.Second,
	}
}

func (p *TransportRetryPolicy) OnSuccess(_ time.Time) {
	p.mu.Lock()
	p.consecutiveFails = 0
	p.handshakeFails = p.handshakeFails[:0]
	p.mu.Unlock()
}

func (p *TransportRetryPolicy) Decide(in RetryInput) RetryDecision {
	if in.Error == nil {
		return RetryDecision{Retry: false, Reason: "no_error"}
	}
	if in.Class == ErrorClassContext || in.Class == ErrorClassRetryPolicy || in.Class == ErrorClassBackoffSleep {
		return RetryDecision{Retry: false, Reason: "terminal_class"}
	}
	if in.Class == ErrorClassTunOpen || in.Class == ErrorClassDial || in.Class == ErrorClassHandshake || in.Class == ErrorClassEngine || in.Class == ErrorClassTransport || in.Class == ErrorClassProtocol || in.Class == ErrorClassControl {
		// retryable classes, continue below
	} else if in.Class == ErrorClassNone {
		return RetryDecision{Retry: false, Reason: "no_error_class"}
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	p.consecutiveFails++
	now := in.Now

	if in.Class == ErrorClassHandshake {
		p.handshakeFails = append(p.handshakeFails, now)
		p.handshakeFails = pruneOlderThan(p.handshakeFails, now.Add(-p.HandshakeFailureWindow))
		if p.HandshakeFailureLimit > 0 && len(p.handshakeFails) >= p.HandshakeFailureLimit {
			p.handshakeFails = p.handshakeFails[:0]
			return RetryDecision{
				Retry:      true,
				ExtraDelay: p.HandshakeBurstCooldown,
				Reason:     "handshake_burst",
			}
		}
	}

	if p.MaxConsecutiveFailures > 0 && p.consecutiveFails >= p.MaxConsecutiveFailures {
		p.consecutiveFails = 0
		return RetryDecision{
			Retry:      true,
			ExtraDelay: p.ConsecutiveCooldown,
			Reason:     "consecutive_failures",
		}
	}

	return RetryDecision{
		Retry:      true,
		ExtraDelay: 0,
		Reason:     "retry",
	}
}

func pruneOlderThan(values []time.Time, cutoff time.Time) []time.Time {
	n := 0
	for _, v := range values {
		if !v.Before(cutoff) {
			values[n] = v
			n++
		}
	}
	return values[:n]
}

func defaultRetryDecision(err error, attempt int) RetryDecision {
	if err == nil {
		return RetryDecision{Retry: false, Reason: "no_error"}
	}
	if context.Canceled == err {
		return RetryDecision{Retry: false, Reason: "context_terminal"}
	}
	if defaultShouldReconnect(err, attempt) {
		return RetryDecision{Retry: true, Reason: "callback_retry"}
	}
	return RetryDecision{Retry: false, Reason: "callback_stop"}
}
