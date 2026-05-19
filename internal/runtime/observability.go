package runtime

import (
	"context"
	"errors"
	"time"

	"tun/internal/core"
	"tun/internal/engine"
)

type ErrorClass string

const (
	ErrorClassNone         ErrorClass = "none"
	ErrorClassContext      ErrorClass = "context"
	ErrorClassTunOpen      ErrorClass = "tun_open"
	ErrorClassDial         ErrorClass = "dial"
	ErrorClassHandshake    ErrorClass = "handshake"
	ErrorClassEngine       ErrorClass = "engine"
	ErrorClassTransport    ErrorClass = "transport"
	ErrorClassProtocol     ErrorClass = "protocol"
	ErrorClassControl      ErrorClass = "control"
	ErrorClassRetryPolicy  ErrorClass = "retry_policy"
	ErrorClassBackoffSleep ErrorClass = "backoff_sleep"
)

type Snapshot struct {
	State State

	LinkID                string
	SessionID             string
	LastHandshakeAt       time.Time
	LastRekeyAt           time.Time
	LastRxAt              time.Time
	LastTxAt              time.Time
	RxBytes               uint64
	TxBytes               uint64
	RekeyEpoch            uint64
	RekeysInitiated       int
	RekeysCompleted       int
	RekeyFallbacks        int
	RekeyAcksRejected     int
	RekeyAckSendFailures  int
	RekeyInitSendFailures int
	RekeyInitTimeouts     int

	Attempts           int
	Reconnects         int
	RetryDecisions     int
	TunOpenFailures    int
	DialFailures       int
	HandshakeFailures  int
	EngineFailures     int
	SuccessfulSessions int

	LastErrorClass  ErrorClass
	LastError       string
	LastRetryReason string
	LastRetryDelay  time.Duration

	SelectedGatewayID      string
	SelectedGatewayAddr    string
	GatewaySelections      int
	GatewaySwitches        int
	GatewayCooldownSkips   int
	GatewayHysteresisKeeps int
	GatewayAutoSelect      bool

	StartedAt        time.Time
	LastTransitionAt time.Time
}

type Event struct {
	State      State
	Cause      error
	ErrorClass ErrorClass
	Snapshot   Snapshot
}

type EventCallback func(event Event)

type stats struct {
	snapshot Snapshot
}

func newStats(now time.Time) *stats {
	return &stats{
		snapshot: Snapshot{
			State:            StateIdle,
			LastErrorClass:   ErrorClassNone,
			StartedAt:        now,
			LastTransitionAt: now,
		},
	}
}

func (s *stats) snapshotCopy() Snapshot {
	return s.snapshot
}

func (s *stats) transition(now time.Time, state State, cause error, class ErrorClass) Snapshot {
	s.snapshot.State = state
	s.snapshot.LastTransitionAt = now
	if cause != nil {
		s.snapshot.LastError = cause.Error()
		s.snapshot.LastErrorClass = class
	} else {
		s.snapshot.LastError = ""
		s.snapshot.LastErrorClass = ErrorClassNone
	}
	return s.snapshot
}

func classifyError(err error, phase ErrorClass) ErrorClass {
	if err == nil {
		return ErrorClassNone
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return ErrorClassContext
	}
	switch phase {
	case ErrorClassEngine:
		switch {
		case errors.Is(err, engine.ErrTransportClosed):
			return ErrorClassTransport
		case errors.Is(err, engine.ErrUnexpectedMsgType):
			return ErrorClassProtocol
		case errors.Is(err, core.ErrInvalidControlMsg), errors.Is(err, core.ErrNonZeroReserved):
			return ErrorClassControl
		default:
			return ErrorClassEngine
		}
	case ErrorClassTunOpen, ErrorClassDial, ErrorClassHandshake, ErrorClassRetryPolicy, ErrorClassBackoffSleep:
		return phase
	default:
		return ErrorClassEngine
	}
}

// classifyErrorForPhase keeps context cancellation terminal only when the
// parent run context is actually done; operation-scoped timeouts remain phase errors.
func classifyErrorForPhase(ctx context.Context, err error, phase ErrorClass) ErrorClass {
	class := classifyError(err, phase)
	if class == ErrorClassContext && ctx != nil && ctx.Err() == nil {
		return phase
	}
	return class
}
