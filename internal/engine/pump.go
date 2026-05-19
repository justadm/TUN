package engine

import (
	"context"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"tun/internal/core"
	"tun/internal/transport"
	"tun/internal/tun"
)

var (
	ErrPacketTooLarge               = errors.New("packet too large")
	ErrUnexpectedMsgType            = errors.New("unexpected frame message type")
	ErrTransportClosed              = errors.New("transport closed")
	ErrSessionDurationLimitExceeded = errors.New("session duration limit exceeded")
	ErrSessionBytesLimitExceeded    = errors.New("session traffic volume limit exceeded")
)

type Options struct {
	// MaxPacketSize is the maximum plaintext packet size accepted from TUN.
	// If zero, default is 65535.
	MaxPacketSize int
	// BufferSize controls the TUN read buffer size.
	// If zero, it defaults to MaxPacketSize.
	BufferSize int
	// OnControl is called for decrypted CONTROL messages.
	OnControl func(msg core.ControlMessage) error
	// OnControlWithSend is called for decrypted CONTROL messages and receives
	// a safe sender function for emitting CONTROL frames back to peer.
	// If set, it has precedence over OnControl.
	OnControlWithSend func(msg core.ControlMessage, send func(msg core.ControlMessage) error) error
	// OnControlSenderReady is called once when CONTROL sender is ready.
	OnControlSenderReady func(send func(msg core.ControlMessage) error)
	// OutDirection is the frame direction used when writing data from TUN to stream.
	// If zero, defaults to 0x00 (client->server).
	OutDirection byte
	// InDirection is the frame direction used when decrypting data from stream to TUN.
	// If zero, defaults to 0x01 (server->client).
	InDirection byte
	// MaxSessionDuration closes the session when elapsed time reaches this limit.
	// If zero, no duration limit is enforced.
	MaxSessionDuration time.Duration
	// MaxSessionBytes closes the session when total DATA payload bytes
	// (both directions combined) reaches this limit.
	// If zero, no byte limit is enforced.
	MaxSessionBytes uint64
	// TrafficObserver is called after successful DATA transfer.
	TrafficObserver func(sample TrafficSample)
}

type TrafficDirection string

const (
	TrafficDirectionTx TrafficDirection = "tx"
	TrafficDirectionRx TrafficDirection = "rx"
)

type TrafficSample struct {
	Direction TrafficDirection
	Bytes     int
	At        time.Time
}

type sessionBudget struct {
	start       time.Time
	maxDuration time.Duration
	maxBytes    uint64
	c2sBytes    atomic.Uint64
	s2cBytes    atomic.Uint64
}

func newSessionBudget(opts Options) *sessionBudget {
	if opts.MaxSessionDuration <= 0 && opts.MaxSessionBytes == 0 {
		return nil
	}
	return &sessionBudget{
		start:       time.Now(),
		maxDuration: opts.MaxSessionDuration,
		maxBytes:    opts.MaxSessionBytes,
	}
}

func (b *sessionBudget) addC2S(n int) {
	if b == nil || n <= 0 {
		return
	}
	b.c2sBytes.Add(uint64(n))
}

func (b *sessionBudget) addS2C(n int) {
	if b == nil || n <= 0 {
		return
	}
	b.s2cBytes.Add(uint64(n))
}

func (b *sessionBudget) exceeded(now time.Time) error {
	if b == nil {
		return nil
	}
	if b.maxDuration > 0 && now.Sub(b.start) >= b.maxDuration {
		return ErrSessionDurationLimitExceeded
	}
	if b.maxBytes > 0 && (b.c2sBytes.Load()+b.s2cBytes.Load()) >= b.maxBytes {
		return ErrSessionBytesLimitExceeded
	}
	return nil
}

// Run starts bidirectional packet pumping between a TUN device and encrypted stream.
//
// Direction mapping:
// - TUN -> stream uses c2s direction (0x00)
// - stream -> TUN uses s2c direction (0x01)
func Run(ctx context.Context, dev tun.Device, stream transport.Stream, sess *core.Session, opts Options) error {
	maxPacket := opts.MaxPacketSize
	if maxPacket <= 0 {
		maxPacket = 65535
	}
	bufSize := opts.BufferSize
	if bufSize <= 0 {
		bufSize = maxPacket
	}
	if bufSize < maxPacket {
		bufSize = maxPacket
	}
	outDir := opts.OutDirection
	inDir := opts.InDirection
	if outDir == 0 && inDir == 0 {
		outDir = 0x00
		inDir = 0x01
	}
	budget := newSessionBudget(opts)
	var writeMu sync.Mutex
	writeMsgLocked := func(wire []byte) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		return core.WriteMsg(stream, wire)
	}
	sendControl := func(msg core.ControlMessage) error {
		body, err := msg.MarshalBinary()
		if err != nil {
			return err
		}
		wire, err := sess.EncryptFrame(outDir, core.MsgTypeControl, body)
		if err != nil {
			return err
		}
		return writeMsgLocked(wire)
	}
	if opts.OnControlSenderReady != nil {
		opts.OnControlSenderReady(sendControl)
	}

	var closeOnce sync.Once
	closeAll := func() {
		closeOnce.Do(func() {
			// Close transport first so peer-side sockets do not linger in CLOSE-WAIT
			// when device close is slow or temporarily blocked.
			_ = stream.Close()
			go func() {
				_ = dev.Close()
			}()
		})
	}

	results := make(chan error, 2)
	go func() {
		results <- pumpFromTun(dev, writeMsgLocked, sess, maxPacket, bufSize, outDir, budget, opts.TrafficObserver)
	}()
	go func() {
		results <- pumpFromStream(dev, stream, sess, opts.OnControl, opts.OnControlWithSend, sendControl, inDir, budget, opts.TrafficObserver)
	}()

	var firstErr error
	done := 0
	ctxDone := ctx.Done()
	limitTicker := time.NewTicker(100 * time.Millisecond)
	defer limitTicker.Stop()
	for done < 2 {
		select {
		case <-ctxDone:
			// Fast shutdown path: return immediately on cancellation.
			// Close in background so a slow/blocked device close does not delay process stop.
			go closeAll()
			if firstErr == nil {
				firstErr = ctx.Err()
			}
			return firstErr
		case err := <-results:
			done++
			if err == nil {
				continue
			}
			if firstErr == nil {
				firstErr = err
			}
			closeAll()
			// After an error we still drain both workers.
		case <-limitTicker.C:
			if limitErr := budget.exceeded(time.Now()); limitErr != nil {
				if firstErr == nil {
					firstErr = limitErr
				}
				closeAll()
			}
		}
	}
	return firstErr
}

func pumpFromTun(dev tun.Device, writeWire func(wire []byte) error, sess *core.Session, maxPacket, bufSize int, outDirection byte, budget *sessionBudget, observer func(TrafficSample)) error {
	buf := make([]byte, bufSize)
	for {
		n, err := dev.Read(buf)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		if n == 0 {
			continue
		}
		if n > maxPacket {
			return ErrPacketTooLarge
		}
		payload := append([]byte{}, buf[:n]...)
		wire, err := sess.EncryptFrame(outDirection, core.MsgTypeData, payload)
		if err != nil {
			return err
		}
		if err := writeWire(wire); err != nil {
			return err
		}
		budget.addC2S(n)
		if observer != nil {
			observer(TrafficSample{
				Direction: TrafficDirectionTx,
				Bytes:     n,
				At:        time.Now(),
			})
		}
	}
}

func pumpFromStream(
	dev tun.Device,
	stream transport.Stream,
	sess *core.Session,
	onControl func(msg core.ControlMessage) error,
	onControlWithSend func(msg core.ControlMessage, send func(msg core.ControlMessage) error) error,
	sendControl func(msg core.ControlMessage) error,
	inDirection byte,
	budget *sessionBudget,
	observer func(TrafficSample),
) error {
	for {
		wire, err := core.ReadMsg(stream)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return ErrTransportClosed
			}
			return err
		}
		msgType, pt, err := sess.DecryptFrameWithType(inDirection, wire)
		if err != nil {
			return err
		}
		switch msgType {
		case core.MsgTypeData:
			if err := writeAll(dev, pt); err != nil {
				return err
			}
			budget.addS2C(len(pt))
			if observer != nil {
				observer(TrafficSample{
					Direction: TrafficDirectionRx,
					Bytes:     len(pt),
					At:        time.Now(),
				})
			}
		case core.MsgTypeControl:
			var ctrl core.ControlMessage
			if err := ctrl.UnmarshalBinary(pt); err != nil {
				return err
			}
			if onControlWithSend != nil {
				if err := onControlWithSend(ctrl, sendControl); err != nil {
					return err
				}
				continue
			}
			if onControl != nil {
				if err := onControl(ctrl); err != nil {
					return err
				}
			}
		default:
			return ErrUnexpectedMsgType
		}
	}
}

func writeAll(w io.Writer, b []byte) error {
	for len(b) > 0 {
		n, err := w.Write(b)
		if err != nil {
			return err
		}
		b = b[n:]
	}
	return nil
}
