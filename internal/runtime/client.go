package runtime

import (
	"context"
	"crypto/rand"
	"errors"
	"sync"
	"time"

	"tun/internal/core"
	"tun/internal/engine"
	"tun/internal/transport"
	"tun/internal/tun"
)

type rekeyObserver struct {
	mu      sync.Mutex
	enabled bool
	opts    ClientOptions
	st      *stats
	sess    *core.Session

	initiatorStarted bool
	initiatorStopCh  chan struct{}
	nextInitEpoch    uint64
	ackWaiters       map[uint64]chan uint8
}

func newRekeyObserver(enabled bool, opts ClientOptions, st *stats, sess *core.Session) *rekeyObserver {
	return &rekeyObserver{
		enabled: enabled,
		opts:    opts,
		st:      st,
		sess:    sess,
	}
}

func (o *rekeyObserver) onControl(msg core.ControlMessage, sendControl func(msg core.ControlMessage) error) {
	if o == nil || !o.enabled {
		return
	}
	now := o.opts.Now()
	switch msg.ControlType {
	case core.ControlTypeRekeyInit:
		var init core.RekeyInitV1
		if err := init.UnmarshalBinary(msg.Body); err != nil {
			return
		}
		ackStatus := uint8(core.RekeyAckStatusAccepted)
		ackKeyID := init.NewKeyID
		if o.sess != nil {
			if err := o.sess.InstallRekeyV1(init, now); err != nil {
				ackStatus = core.RekeyAckStatusRejected
				ackKeyID = [16]byte{}
				o.mu.Lock()
				o.st.snapshot.RekeyFallbacks++
				o.st.snapshot.RekeyAcksRejected++
				o.mu.Unlock()
			}
		}
		if sendControl != nil {
			ack := core.RekeyAckV1{
				Version:        core.RekeyVersionV1,
				Status:         ackStatus,
				Epoch:          init.Epoch,
				AcceptedAtUnix: uint64(now.UTC().Unix()),
				ActiveKeyID:    ackKeyID,
			}
			if ackStatus != core.RekeyAckStatusAccepted {
				ack.AcceptedAtUnix = 0
			}
			ackBody, err := ack.MarshalBinary()
			if err == nil {
				_ = o.sendControlWithRetry(sendControl, core.ControlMessage{
					ControlType: core.ControlTypeRekeyAck,
					Body:        ackBody,
				})
			}
		}
		if ackStatus != core.RekeyAckStatusAccepted {
			return
		}
		o.mu.Lock()
		if init.Epoch > o.st.snapshot.RekeyEpoch {
			o.st.snapshot.RekeyEpoch = init.Epoch
			o.st.snapshot.RekeysInitiated++
			snapPending := o.st.transition(now, StateRekeyPending, nil, ErrorClassNone)
			snapOverlap := o.st.transition(now, StateRekeyOverlap, nil, ErrorClassNone)
			o.mu.Unlock()
			emit(o.opts, snapPending, nil, ErrorClassNone)
			emit(o.opts, snapOverlap, nil, ErrorClassNone)
			return
		}
		o.mu.Unlock()
	case core.ControlTypeRekeyAck:
		var ack core.RekeyAckV1
		if err := ack.UnmarshalBinary(msg.Body); err != nil {
			return
		}
		o.mu.Lock()
		o.notifyAckWaiterLocked(ack.Epoch, ack.Status)
		if ack.Status == core.RekeyAckStatusAccepted && ack.Epoch >= o.st.snapshot.RekeyEpoch {
			if o.sess != nil {
				if err := o.sess.CutoverRekeyV1(ack, now); err != nil {
					o.st.snapshot.RekeyFallbacks++
					o.mu.Unlock()
					return
				}
			}
			o.st.snapshot.RekeyEpoch = ack.Epoch
			o.st.snapshot.RekeysCompleted++
			o.st.snapshot.LastRekeyAt = now
			snapCutover := o.st.transition(now, StateRekeyCutover, nil, ErrorClassNone)
			o.mu.Unlock()
			emit(o.opts, snapCutover, nil, ErrorClassNone)
			return
		}
		if ack.Status == core.RekeyAckStatusRejected {
			o.st.snapshot.RekeyFallbacks++
			o.st.snapshot.RekeyAcksRejected++
		}
		o.mu.Unlock()
	}
}

func (o *rekeyObserver) notifyAckWaiterLocked(epoch uint64, status uint8) {
	if o.ackWaiters == nil {
		return
	}
	ch, ok := o.ackWaiters[epoch]
	if !ok {
		return
	}
	select {
	case ch <- status:
	default:
	}
}

func (o *rekeyObserver) addAckWaiter(epoch uint64) chan uint8 {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.ackWaiters == nil {
		o.ackWaiters = make(map[uint64]chan uint8)
	}
	ch := make(chan uint8, 1)
	o.ackWaiters[epoch] = ch
	return ch
}

func (o *rekeyObserver) removeAckWaiter(epoch uint64) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.ackWaiters == nil {
		return
	}
	delete(o.ackWaiters, epoch)
}

func (o *rekeyObserver) sendControlWithRetry(sendControl func(msg core.ControlMessage) error, msg core.ControlMessage) error {
	attempts := o.opts.RekeyAckRetries + 1
	if attempts < 1 {
		attempts = 1
	}
	var lastErr error
	for i := 0; i < attempts; i++ {
		lastErr = sendControl(msg)
		if lastErr == nil {
			return nil
		}
		if i+1 < attempts && o.opts.RekeyAckRetryDelay > 0 {
			time.Sleep(o.opts.RekeyAckRetryDelay)
		}
	}
	o.mu.Lock()
	o.st.snapshot.RekeyFallbacks++
	o.st.snapshot.RekeyAckSendFailures++
	o.mu.Unlock()
	return lastErr
}

func (o *rekeyObserver) startInitiator(sendControl func(msg core.ControlMessage) error) {
	if o == nil || !o.enabled || sendControl == nil || o.opts.RekeyInitInterval <= 0 {
		return
	}
	o.mu.Lock()
	if o.initiatorStarted {
		o.mu.Unlock()
		return
	}
	o.initiatorStarted = true
	o.initiatorStopCh = make(chan struct{})
	if o.nextInitEpoch <= o.st.snapshot.RekeyEpoch {
		o.nextInitEpoch = o.st.snapshot.RekeyEpoch
	}
	stopCh := o.initiatorStopCh
	o.mu.Unlock()

	go o.runInitiator(stopCh, sendControl)
}

func (o *rekeyObserver) stopInitiator() {
	o.mu.Lock()
	defer o.mu.Unlock()
	if !o.initiatorStarted {
		return
	}
	close(o.initiatorStopCh)
	o.initiatorStarted = false
	o.initiatorStopCh = nil
}

func (o *rekeyObserver) runInitiator(stopCh <-chan struct{}, sendControl func(msg core.ControlMessage) error) {
	timer := time.NewTimer(o.opts.RekeyInitInterval)
	defer timer.Stop()
	for {
		select {
		case <-stopCh:
			return
		case <-timer.C:
			o.initiateRekeyOnce(stopCh, sendControl)
			timer.Reset(o.opts.RekeyInitInterval)
		}
	}
}

func (o *rekeyObserver) initiateRekeyOnce(stopCh <-chan struct{}, sendControl func(msg core.ControlMessage) error) {
	init, err := o.buildInitiatorRekeyInit()
	if err != nil {
		o.bumpFallback()
		return
	}
	if o.sess != nil {
		if err := o.sess.InstallRekeyV1(init, o.opts.Now()); err != nil {
			o.bumpFallback()
			return
		}
	}
	initBody, err := init.MarshalBinary()
	if err != nil {
		o.bumpFallback()
		return
	}
	msg := core.ControlMessage{
		ControlType: core.ControlTypeRekeyInit,
		Body:        initBody,
	}
	if err := o.sendControlRetryPolicy(sendControl, msg, o.opts.RekeyInitRetries, o.opts.RekeyInitRetryDelay); err != nil {
		o.mu.Lock()
		o.st.snapshot.RekeyInitSendFailures++
		o.st.snapshot.RekeyFallbacks++
		o.mu.Unlock()
		return
	}

	wait := o.addAckWaiter(init.Epoch)
	defer o.removeAckWaiter(init.Epoch)

	ackTimer := time.NewTimer(o.opts.RekeyInitAckTimeout)
	defer ackTimer.Stop()
	select {
	case <-stopCh:
		return
	case status := <-wait:
		if status != core.RekeyAckStatusAccepted {
			o.mu.Lock()
			o.st.snapshot.RekeyAcksRejected++
			o.st.snapshot.RekeyFallbacks++
			o.mu.Unlock()
		}
	case <-ackTimer.C:
		o.mu.Lock()
		o.st.snapshot.RekeyInitTimeouts++
		o.st.snapshot.RekeyFallbacks++
		o.mu.Unlock()
	}
}

func (o *rekeyObserver) sendControlRetryPolicy(sendControl func(msg core.ControlMessage) error, msg core.ControlMessage, retries int, delay time.Duration) error {
	attempts := retries + 1
	if attempts < 1 {
		attempts = 1
	}
	var lastErr error
	for i := 0; i < attempts; i++ {
		lastErr = sendControl(msg)
		if lastErr == nil {
			return nil
		}
		if i+1 < attempts && delay > 0 {
			time.Sleep(delay)
		}
	}
	return lastErr
}

func (o *rekeyObserver) bumpFallback() {
	o.mu.Lock()
	o.st.snapshot.RekeyFallbacks++
	o.mu.Unlock()
}

func (o *rekeyObserver) buildInitiatorRekeyInit() (core.RekeyInitV1, error) {
	o.mu.Lock()
	o.nextInitEpoch++
	epoch := o.nextInitEpoch
	o.mu.Unlock()

	var keyID [16]byte
	var nonce [12]byte
	if _, err := rand.Read(keyID[:]); err != nil {
		return core.RekeyInitV1{}, err
	}
	if _, err := rand.Read(nonce[:]); err != nil {
		return core.RekeyInitV1{}, err
	}
	return core.RekeyInitV1{
		Version:       core.RekeyVersionV1,
		Epoch:         epoch,
		OverlapMillis: uint32(o.opts.RekeyInitOverlap / time.Millisecond),
		NewKeyID:      keyID,
		RekeyNonce:    nonce,
	}, nil
}

type State string

const (
	StateIdle         State = "idle"
	StateListening    State = "listening"
	StateAccepted     State = "accepted"
	StateDialing      State = "dialing"
	StateHandshaking  State = "handshaking"
	StateEstablished  State = "established"
	StateRekeyPending State = "rekey_pending"
	StateRekeyOverlap State = "rekey_overlap"
	StateRekeyCutover State = "rekey_cutover"
	StateReconnecting State = "reconnecting"
	StateStopped      State = "stopped"
)

type TunnelFactory func(ctx context.Context) (tun.Device, error)
type StreamDialer func(ctx context.Context) (transport.Stream, error)
type SessionHandshake func(stream transport.Stream) (*core.Session, error)
type StateCallback func(state State, cause error)
type SleepFunc func(ctx context.Context, d time.Duration) error
type ShouldReconnectFunc func(err error, attempt int) bool
type EngineRunFunc func(ctx context.Context, dev tun.Device, stream transport.Stream, sess *core.Session, opts engine.Options) error

type ClientOptions struct {
	EngineOptions engine.Options

	BackoffInitial time.Duration
	BackoffMax     time.Duration
	BackoffFactor  float64

	MaxRetries int

	LinkID              string
	RekeyEnabled        bool
	RekeyAckRetries     int
	RekeyAckRetryDelay  time.Duration
	RekeyInitInterval   time.Duration
	RekeyInitAckTimeout time.Duration
	RekeyInitRetries    int
	RekeyInitRetryDelay time.Duration
	RekeyInitOverlap    time.Duration

	OnStateChange   StateCallback
	OnEvent         EventCallback
	Sleep           SleepFunc
	ShouldReconnect ShouldReconnectFunc
	RetryPolicy     RetryPolicy
	RunEngine       EngineRunFunc
	Now             func() time.Time
}

func RunClient(ctx context.Context, openTun TunnelFactory, dial StreamDialer, handshake SessionHandshake, opts ClientOptions) error {
	if openTun == nil || dial == nil || handshake == nil {
		return errors.New("runtime: openTun, dial, and handshake are required")
	}
	fillDefaults(&opts)
	st := newStats(opts.Now())
	st.snapshot.LinkID = opts.LinkID
	emit(opts, st.transition(opts.Now(), StateIdle, nil, ErrorClassNone), nil, ErrorClassNone)

	attempt := 0
	for {
		if err := ctx.Err(); err != nil {
			class := classifyError(err, ErrorClassContext)
			emit(opts, st.transition(opts.Now(), StateStopped, err, class), err, class)
			return err
		}

		st.snapshot.Attempts++
		emit(opts, st.transition(opts.Now(), StateDialing, nil, ErrorClassNone), nil, ErrorClassNone)
		dev, err := openTun(ctx)
		if err != nil {
			st.snapshot.TunOpenFailures++
			class := classifyErrorForPhase(ctx, err, ErrorClassTunOpen)
			decision := nextRetryDecision(ctx, opts, err, class, attempt)
			if !decision.Retry {
				emit(opts, st.transition(opts.Now(), StateStopped, err, class), err, class)
				return err
			}
			st.snapshot.RetryDecisions++
			st.snapshot.LastRetryReason = decision.Reason
			st.snapshot.LastRetryDelay = decision.ExtraDelay
			st.snapshot.Reconnects++
			emit(opts, st.transition(opts.Now(), StateReconnecting, err, class), err, class)
			wait := backoff(attempt, opts.BackoffInitial, opts.BackoffMax, opts.BackoffFactor) + decision.ExtraDelay
			if err := opts.Sleep(ctx, wait); err != nil {
				sleepClass := classifyError(err, ErrorClassBackoffSleep)
				emit(opts, st.transition(opts.Now(), StateStopped, err, sleepClass), err, sleepClass)
				return err
			}
			attempt++
			continue
		}

		stream, err := dial(ctx)
		if err != nil {
			st.snapshot.DialFailures++
			_ = dev.Close()
			class := classifyErrorForPhase(ctx, err, ErrorClassDial)
			decision := nextRetryDecision(ctx, opts, err, class, attempt)
			if !decision.Retry {
				emit(opts, st.transition(opts.Now(), StateStopped, err, class), err, class)
				return err
			}
			st.snapshot.RetryDecisions++
			st.snapshot.LastRetryReason = decision.Reason
			st.snapshot.LastRetryDelay = decision.ExtraDelay
			st.snapshot.Reconnects++
			emit(opts, st.transition(opts.Now(), StateReconnecting, err, class), err, class)
			wait := backoff(attempt, opts.BackoffInitial, opts.BackoffMax, opts.BackoffFactor) + decision.ExtraDelay
			if err := opts.Sleep(ctx, wait); err != nil {
				sleepClass := classifyError(err, ErrorClassBackoffSleep)
				emit(opts, st.transition(opts.Now(), StateStopped, err, sleepClass), err, sleepClass)
				return err
			}
			attempt++
			continue
		}

		emit(opts, st.transition(opts.Now(), StateHandshaking, nil, ErrorClassNone), nil, ErrorClassNone)
		sess, err := handshake(stream)
		if err != nil {
			st.snapshot.HandshakeFailures++
			_ = stream.Close()
			_ = dev.Close()
			class := classifyErrorForPhase(ctx, err, ErrorClassHandshake)
			decision := nextRetryDecision(ctx, opts, err, class, attempt)
			if !decision.Retry {
				emit(opts, st.transition(opts.Now(), StateStopped, err, class), err, class)
				return err
			}
			st.snapshot.RetryDecisions++
			st.snapshot.LastRetryReason = decision.Reason
			st.snapshot.LastRetryDelay = decision.ExtraDelay
			st.snapshot.Reconnects++
			emit(opts, st.transition(opts.Now(), StateReconnecting, err, class), err, class)
			wait := backoff(attempt, opts.BackoffInitial, opts.BackoffMax, opts.BackoffFactor) + decision.ExtraDelay
			if err := opts.Sleep(ctx, wait); err != nil {
				sleepClass := classifyError(err, ErrorClassBackoffSleep)
				emit(opts, st.transition(opts.Now(), StateStopped, err, sleepClass), err, sleepClass)
				return err
			}
			attempt++
			continue
		}

		st.snapshot.SuccessfulSessions++
		st.snapshot.SessionID = newSessionID()
		st.snapshot.LastHandshakeAt = opts.Now()
		if opts.RetryPolicy != nil {
			opts.RetryPolicy.OnSuccess(opts.Now())
		}
		emit(opts, st.transition(opts.Now(), StateEstablished, nil, ErrorClassNone), nil, ErrorClassNone)
		engineOpts := opts.EngineOptions
		rekeyObs := newRekeyObserver(opts.RekeyEnabled, opts, st, sess)
		prevOnControl := engineOpts.OnControl
		prevOnControlWithSend := engineOpts.OnControlWithSend
		prevOnControlSenderReady := engineOpts.OnControlSenderReady
		engineOpts.OnControl = func(msg core.ControlMessage) error {
			rekeyObs.onControl(msg, nil)
			if prevOnControl != nil {
				return prevOnControl(msg)
			}
			return nil
		}
		engineOpts.OnControlWithSend = func(msg core.ControlMessage, send func(msg core.ControlMessage) error) error {
			rekeyObs.onControl(msg, send)
			if prevOnControlWithSend != nil {
				return prevOnControlWithSend(msg, send)
			}
			return nil
		}
		engineOpts.OnControlSenderReady = func(send func(msg core.ControlMessage) error) {
			rekeyObs.startInitiator(send)
			if prevOnControlSenderReady != nil {
				prevOnControlSenderReady(send)
			}
		}
		runErr := opts.RunEngine(ctx, dev, stream, sess, engineOpts)
		rekeyObs.stopInitiator()
		if runErr == nil {
			emit(opts, st.transition(opts.Now(), StateStopped, nil, ErrorClassNone), nil, ErrorClassNone)
			return nil
		}
		st.snapshot.EngineFailures++
		class := classifyErrorForPhase(ctx, runErr, ErrorClassEngine)
		if errors.Is(runErr, context.Canceled) || errors.Is(runErr, context.DeadlineExceeded) {
			emit(opts, st.transition(opts.Now(), StateStopped, runErr, class), runErr, class)
			return runErr
		}
		decision := nextRetryDecision(ctx, opts, runErr, class, attempt)
		if !decision.Retry {
			policyClass := classifyError(runErr, ErrorClassRetryPolicy)
			emit(opts, st.transition(opts.Now(), StateStopped, runErr, policyClass), runErr, policyClass)
			return runErr
		}
		st.snapshot.RetryDecisions++
		st.snapshot.LastRetryReason = decision.Reason
		st.snapshot.LastRetryDelay = decision.ExtraDelay
		st.snapshot.Reconnects++
		emit(opts, st.transition(opts.Now(), StateReconnecting, runErr, class), runErr, class)
		wait := backoff(attempt, opts.BackoffInitial, opts.BackoffMax, opts.BackoffFactor) + decision.ExtraDelay
		if err := opts.Sleep(ctx, wait); err != nil {
			sleepClass := classifyError(err, ErrorClassBackoffSleep)
			emit(opts, st.transition(opts.Now(), StateStopped, err, sleepClass), err, sleepClass)
			return err
		}
		attempt++
	}
}

func fillDefaults(opts *ClientOptions) {
	if opts.BackoffInitial <= 0 {
		opts.BackoffInitial = 200 * time.Millisecond
	}
	if opts.BackoffMax <= 0 {
		opts.BackoffMax = 5 * time.Second
	}
	if opts.BackoffFactor < 1 {
		opts.BackoffFactor = 2
	}
	if opts.Sleep == nil {
		opts.Sleep = sleepWithContext
	}
	if opts.ShouldReconnect == nil {
		opts.ShouldReconnect = defaultShouldReconnect
	}
	if opts.RunEngine == nil {
		opts.RunEngine = engine.Run
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.RekeyAckRetries < 0 {
		opts.RekeyAckRetries = 0
	}
	if opts.RekeyAckRetryDelay <= 0 {
		opts.RekeyAckRetryDelay = 200 * time.Millisecond
	}
	if opts.RekeyInitAckTimeout <= 0 {
		opts.RekeyInitAckTimeout = 3 * time.Second
	}
	if opts.RekeyInitRetries < 0 {
		opts.RekeyInitRetries = 0
	}
	if opts.RekeyInitRetryDelay <= 0 {
		opts.RekeyInitRetryDelay = 200 * time.Millisecond
	}
	if opts.RekeyInitOverlap <= 0 {
		opts.RekeyInitOverlap = 2500 * time.Millisecond
	}
}

func newSessionID() string {
	return time.Now().UTC().Format("20060102T150405.000000000Z07:00")
}

func emit(opts ClientOptions, snapshot Snapshot, cause error, class ErrorClass) {
	if opts.OnStateChange != nil {
		opts.OnStateChange(snapshot.State, cause)
	}
	if opts.OnEvent != nil {
		opts.OnEvent(Event{
			State:      snapshot.State,
			Cause:      cause,
			ErrorClass: class,
			Snapshot:   snapshot,
		})
	}
}

func defaultShouldReconnect(err error, _ int) bool {
	return !errors.Is(err, context.Canceled)
}

func nextRetryDecision(ctx context.Context, opts ClientOptions, err error, class ErrorClass, attempt int) RetryDecision {
	if ctx.Err() != nil {
		return RetryDecision{Retry: false, Reason: "context_done"}
	}
	if opts.MaxRetries > 0 && attempt >= opts.MaxRetries {
		return RetryDecision{Retry: false, Reason: "max_retries"}
	}
	if opts.RetryPolicy != nil {
		return opts.RetryPolicy.Decide(RetryInput{
			Error:   err,
			Class:   class,
			Attempt: attempt,
			Now:     opts.Now(),
		})
	}
	if opts.ShouldReconnect(err, attempt) {
		return RetryDecision{Retry: true, Reason: "callback_retry"}
	}
	return RetryDecision{Retry: false, Reason: "callback_stop"}
}

func sleepWithContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func backoff(attempt int, initial, max time.Duration, factor float64) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	v := float64(initial)
	for i := 0; i < attempt; i++ {
		v *= factor
		if time.Duration(v) >= max {
			return max
		}
	}
	out := time.Duration(v)
	if out > max {
		return max
	}
	return out
}
