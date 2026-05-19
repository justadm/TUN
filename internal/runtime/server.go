package runtime

import (
	"context"
	"errors"

	"tun/internal/core"
	"tun/internal/transport"
)

type SessionHandshakeServer func(stream transport.Stream) (*core.Session, error)

func RunServer(ctx context.Context, openTun TunnelFactory, listener transport.Listener, handshake SessionHandshakeServer, opts ClientOptions) error {
	if openTun == nil || listener == nil || handshake == nil {
		return errors.New("runtime: openTun, listener, and handshake are required")
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

		emit(opts, st.transition(opts.Now(), StateListening, nil, ErrorClassNone), nil, ErrorClassNone)
		stream, err := listener.Accept(ctx)
		if err != nil {
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
		emit(opts, st.transition(opts.Now(), StateAccepted, nil, ErrorClassNone), nil, ErrorClassNone)

		st.snapshot.Attempts++
		emit(opts, st.transition(opts.Now(), StateHandshaking, nil, ErrorClassNone), nil, ErrorClassNone)
		sess, err := handshake(stream)
		if err != nil {
			st.snapshot.HandshakeFailures++
			_ = stream.Close()
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

		dev, err := openTun(ctx)
		if err != nil {
			st.snapshot.TunOpenFailures++
			_ = stream.Close()
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

		st.snapshot.SuccessfulSessions++
		st.snapshot.SessionID = newSessionID()
		st.snapshot.LastHandshakeAt = opts.Now()
		if opts.RetryPolicy != nil {
			opts.RetryPolicy.OnSuccess(opts.Now())
		}
		emit(opts, st.transition(opts.Now(), StateEstablished, nil, ErrorClassNone), nil, ErrorClassNone)

		runOpts := opts.EngineOptions
		if runOpts.OutDirection == 0 && runOpts.InDirection == 0 {
			// Server defaults are opposite of client defaults.
			runOpts.OutDirection = 0x01
			runOpts.InDirection = 0x00
		}
		rekeyObs := newRekeyObserver(opts.RekeyEnabled, opts, st, sess)
		prevOnControl := runOpts.OnControl
		prevOnControlWithSend := runOpts.OnControlWithSend
		prevOnControlSenderReady := runOpts.OnControlSenderReady
		runOpts.OnControl = func(msg core.ControlMessage) error {
			rekeyObs.onControl(msg, nil)
			if prevOnControl != nil {
				return prevOnControl(msg)
			}
			return nil
		}
		runOpts.OnControlWithSend = func(msg core.ControlMessage, send func(msg core.ControlMessage) error) error {
			rekeyObs.onControl(msg, send)
			if prevOnControlWithSend != nil {
				return prevOnControlWithSend(msg, send)
			}
			return nil
		}
		runOpts.OnControlSenderReady = func(send func(msg core.ControlMessage) error) {
			rekeyObs.startInitiator(send)
			if prevOnControlSenderReady != nil {
				prevOnControlSenderReady(send)
			}
		}
		runErr := opts.RunEngine(ctx, dev, stream, sess, runOpts)
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

type ServerFactory func(ctx context.Context) (transport.Listener, error)

func RunServerWithFactory(ctx context.Context, openTun TunnelFactory, openListener ServerFactory, handshake SessionHandshakeServer, opts ClientOptions) error {
	if openListener == nil {
		return errors.New("runtime: openListener is required")
	}
	fillDefaults(&opts)
	attempt := 0
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		listener, err := openListener(ctx)
		if err != nil {
			decision := nextRetryDecision(ctx, opts, err, ErrorClassDial, attempt)
			if !decision.Retry {
				return err
			}
			wait := backoff(attempt, opts.BackoffInitial, opts.BackoffMax, opts.BackoffFactor) + decision.ExtraDelay
			if err := opts.Sleep(ctx, wait); err != nil {
				return err
			}
			attempt++
			continue
		}
		defer listener.Close()
		return RunServer(ctx, openTun, listener, handshake, opts)
	}
}
