package runtime

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"tun/internal/core"
	"tun/internal/engine"
	"tun/internal/transport"
	"tun/internal/tun"
)

func TestRunClientReconnectsAndEmitsStates(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var stateMu sync.Mutex
	var states []State
	record := func(s State, _ error) {
		stateMu.Lock()
		states = append(states, s)
		stateMu.Unlock()
	}

	openCalls := 0
	openTun := func(_ context.Context) (tun.Device, error) {
		openCalls++
		return &noopTun{}, nil
	}
	dial := func(_ context.Context) (transport.Stream, error) {
		return &noopStream{}, nil
	}
	handshake := func(_ transport.Stream) (*core.Session, error) {
		return core.NewSession(core.AEADChaCha20Poly1305, fillBytes(0x01, 32), fillBytes(0x02, 32)), nil
	}

	runCalls := 0
	runEngine := func(_ context.Context, _ tun.Device, _ transport.Stream, _ *core.Session, _ engine.Options) error {
		runCalls++
		if runCalls == 1 {
			return engine.ErrTransportClosed
		}
		cancel()
		return context.Canceled
	}

	sleepCalls := 0
	err := RunClient(ctx, openTun, dial, handshake, ClientOptions{
		OnStateChange: record,
		Sleep: func(_ context.Context, _ time.Duration) error {
			sleepCalls++
			return nil
		},
		RunEngine: runEngine,
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if runCalls != 2 {
		t.Fatalf("expected 2 run attempts, got %d", runCalls)
	}
	if sleepCalls != 1 {
		t.Fatalf("expected 1 backoff sleep, got %d", sleepCalls)
	}
	if openCalls != 2 {
		t.Fatalf("expected 2 tunnel opens, got %d", openCalls)
	}

	stateMu.Lock()
	defer stateMu.Unlock()
	if len(states) < 7 {
		t.Fatalf("expected state transitions, got %v", states)
	}
	if states[0] != StateIdle {
		t.Fatalf("expected first state idle, got %s", states[0])
	}
	if !containsState(states, StateReconnecting) {
		t.Fatalf("expected reconnecting state, got %v", states)
	}
	if states[len(states)-1] != StateStopped {
		t.Fatalf("expected final state stopped, got %s", states[len(states)-1])
	}
}

func TestRunClientEmitsEventSnapshots(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var events []Event
	openCalls := 0
	openTun := func(_ context.Context) (tun.Device, error) {
		openCalls++
		if openCalls == 1 {
			return nil, errors.New("tun open failed")
		}
		return &noopTun{}, nil
	}
	dial := func(_ context.Context) (transport.Stream, error) {
		return &noopStream{}, nil
	}
	handshake := func(_ transport.Stream) (*core.Session, error) {
		return core.NewSession(core.AEADChaCha20Poly1305, fillBytes(0x10, 32), fillBytes(0x20, 32)), nil
	}
	runCalls := 0
	runEngine := func(_ context.Context, _ tun.Device, _ transport.Stream, _ *core.Session, _ engine.Options) error {
		runCalls++
		cancel()
		return context.Canceled
	}

	err := RunClient(ctx, openTun, dial, handshake, ClientOptions{
		OnEvent: func(e Event) {
			events = append(events, e)
		},
		Sleep: func(_ context.Context, _ time.Duration) error {
			return nil
		},
		RunEngine: runEngine,
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if len(events) == 0 {
		t.Fatalf("expected runtime events")
	}
	var sawTunOpenClass bool
	var final Event
	for _, e := range events {
		if e.ErrorClass == ErrorClassTunOpen {
			sawTunOpenClass = true
		}
		final = e
	}
	if !sawTunOpenClass {
		t.Fatalf("expected tun_open error class in events")
	}
	if final.State != StateStopped {
		t.Fatalf("expected final stopped state, got %s", final.State)
	}
	if final.Snapshot.Attempts != 2 {
		t.Fatalf("expected attempts=2, got %d", final.Snapshot.Attempts)
	}
	if final.Snapshot.Reconnects != 1 {
		t.Fatalf("expected reconnects=1, got %d", final.Snapshot.Reconnects)
	}
	if final.Snapshot.RetryDecisions != 1 {
		t.Fatalf("expected retry decisions=1, got %d", final.Snapshot.RetryDecisions)
	}
	if final.Snapshot.LastRetryReason == "" {
		t.Fatalf("expected last retry reason to be set")
	}
	if final.Snapshot.TunOpenFailures != 1 {
		t.Fatalf("expected tun open failures=1, got %d", final.Snapshot.TunOpenFailures)
	}
	if final.Snapshot.SuccessfulSessions != 1 {
		t.Fatalf("expected successful sessions=1, got %d", final.Snapshot.SuccessfulSessions)
	}
}

func TestRunClientReconnectProducesNewSessionID(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	openTun := func(_ context.Context) (tun.Device, error) { return &noopTun{}, nil }
	dial := func(_ context.Context) (transport.Stream, error) { return &noopStream{}, nil }
	handshake := func(_ transport.Stream) (*core.Session, error) {
		return core.NewSession(core.AEADChaCha20Poly1305, fillBytes(0x10, 32), fillBytes(0x20, 32)), nil
	}

	var establishedSessionIDs []string
	runCalls := 0
	runEngine := func(_ context.Context, _ tun.Device, _ transport.Stream, _ *core.Session, _ engine.Options) error {
		runCalls++
		if runCalls == 1 {
			return engine.ErrTransportClosed
		}
		cancel()
		return context.Canceled
	}

	err := RunClient(ctx, openTun, dial, handshake, ClientOptions{
		OnEvent: func(e Event) {
			if e.State == StateEstablished {
				establishedSessionIDs = append(establishedSessionIDs, e.Snapshot.SessionID)
			}
		},
		Sleep: func(_ context.Context, _ time.Duration) error {
			return nil
		},
		RunEngine: runEngine,
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if len(establishedSessionIDs) != 2 {
		t.Fatalf("expected two established sessions, got %d (%v)", len(establishedSessionIDs), establishedSessionIDs)
	}
	if establishedSessionIDs[0] == "" || establishedSessionIDs[1] == "" {
		t.Fatalf("expected non-empty session ids, got %v", establishedSessionIDs)
	}
	if establishedSessionIDs[0] == establishedSessionIDs[1] {
		t.Fatalf("expected new session id after reconnect, got %v", establishedSessionIDs)
	}
}

func TestRunClientRekeyStateTransitionsFromControlMessages(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	openTun := func(_ context.Context) (tun.Device, error) { return &noopTun{}, nil }
	dial := func(_ context.Context) (transport.Stream, error) { return &noopStream{}, nil }
	handshake := func(_ transport.Stream) (*core.Session, error) {
		return core.NewSession(core.AEADChaCha20Poly1305, fillBytes(0x10, 32), fillBytes(0x20, 32)), nil
	}

	var states []State
	runEngine := func(_ context.Context, _ tun.Device, _ transport.Stream, _ *core.Session, opts engine.Options) error {
		var keyID [16]byte
		var nonce [12]byte
		keyID[0] = 1
		nonce[0] = 2
		initMsg := core.RekeyInitV1{
			Version:       core.RekeyVersionV1,
			Epoch:         1,
			OverlapMillis: 2500,
			NewKeyID:      keyID,
			RekeyNonce:    nonce,
		}
		initBody, err := initMsg.MarshalBinary()
		if err != nil {
			t.Fatalf("marshal init: %v", err)
		}
		if opts.OnControl != nil {
			if err := opts.OnControl(core.ControlMessage{ControlType: core.ControlTypeRekeyInit, Body: initBody}); err != nil {
				t.Fatalf("on control init: %v", err)
			}
		}

		ack := core.RekeyAckV1{
			Version:        core.RekeyVersionV1,
			Status:         core.RekeyAckStatusAccepted,
			Epoch:          1,
			AcceptedAtUnix: uint64(time.Now().UTC().Unix()),
			ActiveKeyID:    keyID,
		}
		ackBody, err := ack.MarshalBinary()
		if err != nil {
			t.Fatalf("marshal ack: %v", err)
		}
		if opts.OnControl != nil {
			if err := opts.OnControl(core.ControlMessage{ControlType: core.ControlTypeRekeyAck, Body: ackBody}); err != nil {
				t.Fatalf("on control ack: %v", err)
			}
		}
		cancel()
		return context.Canceled
	}

	err := RunClient(ctx, openTun, dial, handshake, ClientOptions{
		RekeyEnabled: true,
		OnEvent: func(e Event) {
			states = append(states, e.State)
		},
		RunEngine: runEngine,
		Sleep: func(_ context.Context, _ time.Duration) error {
			return nil
		},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context canceled, got %v", err)
	}
	if !containsState(states, StateRekeyPending) {
		t.Fatalf("expected rekey_pending state, got %v", states)
	}
	if !containsState(states, StateRekeyOverlap) {
		t.Fatalf("expected rekey_overlap state, got %v", states)
	}
	if !containsState(states, StateRekeyCutover) {
		t.Fatalf("expected rekey_cutover state, got %v", states)
	}
}

func TestRunClientRekeyInitSendsAckWithRetryPolicy(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	openTun := func(_ context.Context) (tun.Device, error) { return &noopTun{}, nil }
	dial := func(_ context.Context) (transport.Stream, error) { return &noopStream{}, nil }
	handshake := func(_ transport.Stream) (*core.Session, error) {
		return core.NewSession(core.AEADChaCha20Poly1305, fillBytes(0x10, 32), fillBytes(0x20, 32)), nil
	}

	sendCalls := 0
	var lastSent core.ControlMessage
	runEngine := func(_ context.Context, _ tun.Device, _ transport.Stream, _ *core.Session, opts engine.Options) error {
		var keyID [16]byte
		var nonce [12]byte
		keyID[0] = 1
		nonce[0] = 2
		initMsg := core.RekeyInitV1{
			Version:       core.RekeyVersionV1,
			Epoch:         5,
			OverlapMillis: 3000,
			NewKeyID:      keyID,
			RekeyNonce:    nonce,
		}
		initBody, err := initMsg.MarshalBinary()
		if err != nil {
			t.Fatalf("marshal init: %v", err)
		}
		if opts.OnControlWithSend != nil {
			err = opts.OnControlWithSend(core.ControlMessage{ControlType: core.ControlTypeRekeyInit, Body: initBody}, func(msg core.ControlMessage) error {
				sendCalls++
				lastSent = msg
				if sendCalls == 1 {
					return errors.New("send failed once")
				}
				return nil
			})
			if err != nil {
				t.Fatalf("on control with send: %v", err)
			}
		}
		cancel()
		return context.Canceled
	}

	err := RunClient(ctx, openTun, dial, handshake, ClientOptions{
		RekeyEnabled:       true,
		RekeyAckRetries:    2,
		RekeyAckRetryDelay: 0,
		RunEngine:          runEngine,
		Sleep: func(_ context.Context, _ time.Duration) error {
			return nil
		},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context canceled, got %v", err)
	}
	if sendCalls != 2 {
		t.Fatalf("expected 2 send attempts, got %d", sendCalls)
	}
	if lastSent.ControlType != core.ControlTypeRekeyAck {
		t.Fatalf("expected rekey ack sent, got %d", lastSent.ControlType)
	}
}

func TestRunClientInitiatorSendsRekeyInitAndHandlesAck(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	openTun := func(_ context.Context) (tun.Device, error) { return &noopTun{}, nil }
	dial := func(_ context.Context) (transport.Stream, error) { return &noopStream{}, nil }
	handshake := func(_ transport.Stream) (*core.Session, error) {
		return core.NewSession(core.AEADChaCha20Poly1305, fillBytes(0x30, 32), fillBytes(0x40, 32)), nil
	}

	var mu sync.Mutex
	sentInits := 0
	var states []State
	runEngine := func(_ context.Context, _ tun.Device, _ transport.Stream, _ *core.Session, opts engine.Options) error {
		if opts.OnControlSenderReady != nil {
			opts.OnControlSenderReady(func(msg core.ControlMessage) error {
				if msg.ControlType != core.ControlTypeRekeyInit {
					return nil
				}
				mu.Lock()
				sentInits++
				mu.Unlock()
				var init core.RekeyInitV1
				if err := init.UnmarshalBinary(msg.Body); err != nil {
					return err
				}
				ack := core.RekeyAckV1{
					Version:        core.RekeyVersionV1,
					Status:         core.RekeyAckStatusAccepted,
					Epoch:          init.Epoch,
					AcceptedAtUnix: uint64(time.Now().UTC().Unix()),
					ActiveKeyID:    init.NewKeyID,
				}
				ackBody, err := ack.MarshalBinary()
				if err != nil {
					return err
				}
				if opts.OnControlWithSend != nil {
					return opts.OnControlWithSend(core.ControlMessage{
						ControlType: core.ControlTypeRekeyAck,
						Body:        ackBody,
					}, nil)
				}
				if opts.OnControl != nil {
					return opts.OnControl(core.ControlMessage{
						ControlType: core.ControlTypeRekeyAck,
						Body:        ackBody,
					})
				}
				return nil
			})
		}
		time.Sleep(40 * time.Millisecond)
		cancel()
		return context.Canceled
	}

	err := RunClient(ctx, openTun, dial, handshake, ClientOptions{
		RekeyEnabled:        true,
		RekeyInitInterval:   10 * time.Millisecond,
		RekeyInitAckTimeout: 500 * time.Millisecond,
		RekeyInitRetries:    0,
		RekeyInitOverlap:    2 * time.Second,
		RunEngine:           runEngine,
		Sleep:               func(_ context.Context, _ time.Duration) error { return nil },
		OnEvent: func(e Event) {
			states = append(states, e.State)
		},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context canceled, got %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if sentInits == 0 {
		t.Fatalf("expected at least one sent rekey init")
	}
	if !containsState(states, StateRekeyCutover) {
		t.Fatalf("expected rekey cutover state, got %v", states)
	}
}

func TestRunClientInitiatorTimeoutIncrementsRekeyCounters(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	openTun := func(_ context.Context) (tun.Device, error) { return &noopTun{}, nil }
	dial := func(_ context.Context) (transport.Stream, error) { return &noopStream{}, nil }
	handshake := func(_ transport.Stream) (*core.Session, error) {
		return core.NewSession(core.AEADChaCha20Poly1305, fillBytes(0x50, 32), fillBytes(0x60, 32)), nil
	}

	var final Snapshot
	runEngine := func(_ context.Context, _ tun.Device, _ transport.Stream, _ *core.Session, opts engine.Options) error {
		if opts.OnControlSenderReady != nil {
			opts.OnControlSenderReady(func(core.ControlMessage) error { return nil })
		}
		time.Sleep(40 * time.Millisecond)
		cancel()
		return context.Canceled
	}

	err := RunClient(ctx, openTun, dial, handshake, ClientOptions{
		RekeyEnabled:        true,
		RekeyInitInterval:   5 * time.Millisecond,
		RekeyInitAckTimeout: 10 * time.Millisecond,
		RekeyInitRetries:    0,
		RekeyInitOverlap:    1500 * time.Millisecond,
		RunEngine:           runEngine,
		Sleep:               func(_ context.Context, _ time.Duration) error { return nil },
		OnEvent: func(e Event) {
			final = e.Snapshot
		},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context canceled, got %v", err)
	}
	if final.RekeyInitTimeouts == 0 {
		t.Fatalf("expected rekey init timeout counter > 0, got %+v", final)
	}
	if final.RekeyFallbacks == 0 {
		t.Fatalf("expected rekey fallback counter > 0, got %+v", final)
	}
}

func TestRunClientUsesPolicyExtraDelay(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	openCalls := 0
	openTun := func(_ context.Context) (tun.Device, error) {
		openCalls++
		if openCalls == 1 {
			return nil, errors.New("dial like failure")
		}
		return &noopTun{}, nil
	}
	dial := func(_ context.Context) (transport.Stream, error) {
		return &noopStream{}, nil
	}
	handshake := func(_ transport.Stream) (*core.Session, error) {
		return core.NewSession(core.AEADChaCha20Poly1305, fillBytes(0x10, 32), fillBytes(0x20, 32)), nil
	}
	runEngine := func(_ context.Context, _ tun.Device, _ transport.Stream, _ *core.Session, _ engine.Options) error {
		cancel()
		return context.Canceled
	}

	var waits []time.Duration
	policy := &fixedRetryPolicy{
		decision: RetryDecision{
			Retry:      true,
			ExtraDelay: 3 * time.Second,
			Reason:     "test_policy_delay",
		},
	}

	err := RunClient(ctx, openTun, dial, handshake, ClientOptions{
		RetryPolicy: policy,
		RunEngine:   runEngine,
		Sleep: func(_ context.Context, d time.Duration) error {
			waits = append(waits, d)
			return nil
		},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context canceled, got %v", err)
	}
	if len(waits) != 1 {
		t.Fatalf("expected one wait, got %d", len(waits))
	}
	if waits[0] < 3*time.Second {
		t.Fatalf("expected wait to include policy delay, got %s", waits[0])
	}
}

func TestRunClientRetriesOnDialDeadlineExceeded(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	openCalls := 0
	openTun := func(_ context.Context) (tun.Device, error) {
		openCalls++
		return &noopTun{}, nil
	}
	dialCalls := 0
	dial := func(_ context.Context) (transport.Stream, error) {
		dialCalls++
		if dialCalls == 1 {
			return nil, context.DeadlineExceeded
		}
		return &noopStream{}, nil
	}
	handshake := func(_ transport.Stream) (*core.Session, error) {
		return core.NewSession(core.AEADChaCha20Poly1305, fillBytes(0x10, 32), fillBytes(0x20, 32)), nil
	}
	runCalls := 0
	runEngine := func(_ context.Context, _ tun.Device, _ transport.Stream, _ *core.Session, _ engine.Options) error {
		runCalls++
		cancel()
		return context.Canceled
	}
	sleepCalls := 0

	err := RunClient(ctx, openTun, dial, handshake, ClientOptions{
		Sleep: func(_ context.Context, _ time.Duration) error {
			sleepCalls++
			return nil
		},
		RunEngine: runEngine,
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if dialCalls != 2 {
		t.Fatalf("expected 2 dial attempts, got %d", dialCalls)
	}
	if sleepCalls != 1 {
		t.Fatalf("expected one retry sleep, got %d", sleepCalls)
	}
	if runCalls != 1 {
		t.Fatalf("expected one engine run, got %d", runCalls)
	}
}

func TestRunClientStopsWhenRetryPolicyRejects(t *testing.T) {
	ctx := context.Background()
	openTun := func(_ context.Context) (tun.Device, error) {
		return &noopTun{}, nil
	}
	dial := func(_ context.Context) (transport.Stream, error) {
		return &noopStream{}, nil
	}
	handshake := func(_ transport.Stream) (*core.Session, error) {
		return nil, core.ErrUnsupportedAlgo
	}
	shouldReconnect := func(err error, _ int) bool {
		return !errors.Is(err, core.ErrUnsupportedAlgo)
	}

	err := RunClient(ctx, openTun, dial, handshake, ClientOptions{
		ShouldReconnect: shouldReconnect,
		Sleep: func(_ context.Context, _ time.Duration) error {
			t.Fatalf("sleep should not be called")
			return nil
		},
	})
	if !errors.Is(err, core.ErrUnsupportedAlgo) {
		t.Fatalf("expected ErrUnsupportedAlgo, got %v", err)
	}
}

func TestRunClientStopsAtMaxRetries(t *testing.T) {
	ctx := context.Background()
	openTun := func(_ context.Context) (tun.Device, error) {
		return nil, errors.New("open failed")
	}
	dial := func(_ context.Context) (transport.Stream, error) {
		return &noopStream{}, nil
	}
	handshake := func(_ transport.Stream) (*core.Session, error) {
		return nil, nil
	}
	sleepCalls := 0
	err := RunClient(ctx, openTun, dial, handshake, ClientOptions{
		MaxRetries: 2,
		Sleep: func(_ context.Context, _ time.Duration) error {
			sleepCalls++
			return nil
		},
	})
	if err == nil || err.Error() != "open failed" {
		t.Fatalf("expected open failed, got %v", err)
	}
	if sleepCalls != 2 {
		t.Fatalf("expected 2 sleeps before stop, got %d", sleepCalls)
	}
}

func TestRunClientReconnectsOnSessionBytesLimit(t *testing.T) {
	ctx := context.Background()

	openCalls := 0
	openTun := func(_ context.Context) (tun.Device, error) {
		openCalls++
		tunDev := newLimitTun()
		tunDev.injectRead([]byte{0x45, 0x00, 0x00, 0x01, 0xaa})
		return tunDev, nil
	}
	dial := func(_ context.Context) (transport.Stream, error) {
		return &limitStream{readCh: make(chan []byte)}, nil
	}
	handshake := func(_ transport.Stream) (*core.Session, error) {
		return core.NewSession(core.AEADChaCha20Poly1305, fillBytes(0x31, 32), fillBytes(0x32, 32)), nil
	}

	var states []State
	err := RunClient(ctx, openTun, dial, handshake, ClientOptions{
		MaxRetries: 1,
		EngineOptions: engine.Options{
			MaxSessionBytes: 4,
		},
		OnStateChange: func(s State, _ error) {
			states = append(states, s)
		},
		Sleep: func(_ context.Context, _ time.Duration) error { return nil },
	})
	if !errors.Is(err, engine.ErrSessionBytesLimitExceeded) {
		t.Fatalf("expected ErrSessionBytesLimitExceeded, got %v", err)
	}
	if openCalls != 2 {
		t.Fatalf("expected 2 attempts/openTun calls, got %d", openCalls)
	}
	if !containsState(states, StateReconnecting) {
		t.Fatalf("expected reconnecting state transition, got %v", states)
	}
}

func containsState(states []State, want State) bool {
	for _, s := range states {
		if s == want {
			return true
		}
	}
	return false
}

type noopTun struct{}

func (n *noopTun) Read(_ []byte) (int, error)  { return 0, io.EOF }
func (n *noopTun) Write(p []byte) (int, error) { return len(p), nil }
func (n *noopTun) Close() error                { return nil }
func (n *noopTun) Name() string                { return "noop0" }
func (n *noopTun) MTU() (int, error)           { return 1500, nil }

type noopStream struct{}

func (n *noopStream) Read(_ []byte) (int, error)  { return 0, io.EOF }
func (n *noopStream) Write(p []byte) (int, error) { return len(p), nil }
func (n *noopStream) Close() error                { return nil }

type limitStream struct {
	readCh chan []byte
	closed bool
	mu     sync.Mutex
}

func (s *limitStream) Read(p []byte) (int, error) {
	for {
		s.mu.Lock()
		closed := s.closed
		s.mu.Unlock()
		if closed {
			return 0, io.EOF
		}
		select {
		case b := <-s.readCh:
			n := copy(p, b)
			return n, nil
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func (s *limitStream) Write(p []byte) (int, error) { return len(p), nil }

func (s *limitStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return nil
}

type limitTun struct {
	readCh chan []byte
	closed bool
	mu     sync.Mutex
}

func newLimitTun() *limitTun {
	return &limitTun{readCh: make(chan []byte, 4)}
}

func (d *limitTun) injectRead(b []byte) {
	d.readCh <- append([]byte{}, b...)
}

func (d *limitTun) Read(p []byte) (int, error) {
	for {
		d.mu.Lock()
		closed := d.closed
		d.mu.Unlock()
		if closed {
			return 0, io.EOF
		}
		select {
		case b := <-d.readCh:
			n := copy(p, b)
			return n, nil
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func (d *limitTun) Write(p []byte) (int, error) { return len(p), nil }

func (d *limitTun) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.closed = true
	return nil
}

func (d *limitTun) Name() string      { return "limit0" }
func (d *limitTun) MTU() (int, error) { return 1500, nil }

func fillBytes(seed byte, n int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = seed
	}
	return out
}

type fixedRetryPolicy struct {
	decision RetryDecision
}

func (f *fixedRetryPolicy) Decide(_ RetryInput) RetryDecision {
	return f.decision
}

func (f *fixedRetryPolicy) OnSuccess(_ time.Time) {}
