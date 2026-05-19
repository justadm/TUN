package engine

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"tun/internal/core"
)

func TestRunPumpsDataBothWays(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	keyC2S := bytes(0x11, 32)
	keyS2C := bytes(0x22, 32)
	clientSess := core.NewSession(core.AEADChaCha20Poly1305, keyC2S, keyS2C)
	serverSess := core.NewSession(core.AEADChaCha20Poly1305, keyC2S, keyS2C)

	dev := newFakeTun("test0", 1400)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- Run(ctx, dev, clientConn, clientSess, Options{})
	}()

	// TUN -> stream
	packetUp := []byte{0x45, 0x00, 0x00, 0x1c, 0xaa}
	dev.injectRead(packetUp)

	wire, err := core.ReadMsg(serverConn)
	if err != nil {
		t.Fatalf("server read msg: %v", err)
	}
	msgType, pt, err := serverSess.DecryptFrameWithType(0x00, wire)
	if err != nil {
		t.Fatalf("server decrypt: %v", err)
	}
	if msgType != core.MsgTypeData {
		t.Fatalf("expected data msg type, got %d", msgType)
	}
	if !bytesEqual(pt, packetUp) {
		t.Fatalf("upstream packet mismatch")
	}

	// stream -> TUN
	packetDown := []byte{0x60, 0x00, 0x00, 0x00, 0xbb}
	respWire, err := serverSess.EncryptFrame(0x01, core.MsgTypeData, packetDown)
	if err != nil {
		t.Fatalf("server encrypt: %v", err)
	}
	if err := core.WriteMsg(serverConn, respWire); err != nil {
		t.Fatalf("server write msg: %v", err)
	}
	got, ok := dev.waitWrite(2 * time.Second)
	if !ok {
		t.Fatalf("timeout waiting for packet write to tun")
	}
	if !bytesEqual(got, packetDown) {
		t.Fatalf("downstream packet mismatch")
	}

	cancel()
	err = <-errCh
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestRunRejectsUnexpectedMessageType(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	keyC2S := bytes(0x33, 32)
	keyS2C := bytes(0x44, 32)
	clientSess := core.NewSession(core.AEADChaCha20Poly1305, keyC2S, keyS2C)
	serverSess := core.NewSession(core.AEADChaCha20Poly1305, keyC2S, keyS2C)

	dev := newFakeTun("test1", 1400)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- Run(ctx, dev, clientConn, clientSess, Options{})
	}()

	wire, err := serverSess.EncryptFrame(0x01, core.MsgTypeHandshake, []byte{0x01})
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if err := core.WriteMsg(serverConn, wire); err != nil {
		t.Fatalf("write: %v", err)
	}

	err = <-errCh
	if !errors.Is(err, ErrUnexpectedMsgType) {
		t.Fatalf("expected ErrUnexpectedMsgType, got %v", err)
	}
}

func TestRunValidatesControlPayload(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	keyC2S := bytes(0x55, 32)
	keyS2C := bytes(0x66, 32)
	clientSess := core.NewSession(core.AEADChaCha20Poly1305, keyC2S, keyS2C)
	serverSess := core.NewSession(core.AEADChaCha20Poly1305, keyC2S, keyS2C)

	dev := newFakeTun("test2", 1400)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- Run(ctx, dev, clientConn, clientSess, Options{})
	}()

	// Too short to be a valid ControlMessage payload.
	wire, err := serverSess.EncryptFrame(0x01, core.MsgTypeControl, []byte{0x01, 0x00, 0x00})
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if err := core.WriteMsg(serverConn, wire); err != nil {
		t.Fatalf("write: %v", err)
	}

	err = <-errCh
	if !errors.Is(err, core.ErrInvalidControlMsg) {
		t.Fatalf("expected ErrInvalidControlMsg, got %v", err)
	}
}

func TestRunOnControlWithSendCanReply(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	keyC2S := bytes(0x67, 32)
	keyS2C := bytes(0x68, 32)
	clientSess := core.NewSession(core.AEADChaCha20Poly1305, keyC2S, keyS2C)
	serverSess := core.NewSession(core.AEADChaCha20Poly1305, keyC2S, keyS2C)

	dev := newFakeTun("ctrl0", 1400)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- Run(ctx, dev, clientConn, clientSess, Options{
			OnControlWithSend: func(msg core.ControlMessage, send func(core.ControlMessage) error) error {
				if msg.ControlType != core.ControlTypeRekeyInit {
					return nil
				}
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
				return send(core.ControlMessage{
					ControlType: core.ControlTypeRekeyAck,
					Body:        ackBody,
				})
			},
		})
	}()

	var keyID [16]byte
	var nonce [12]byte
	keyID[0] = 1
	nonce[0] = 2
	init := core.RekeyInitV1{
		Version:       core.RekeyVersionV1,
		Epoch:         1,
		OverlapMillis: 1500,
		NewKeyID:      keyID,
		RekeyNonce:    nonce,
	}
	initBody, err := init.MarshalBinary()
	if err != nil {
		t.Fatalf("marshal init: %v", err)
	}
	ctrlWire, err := (&core.ControlMessage{
		ControlType: core.ControlTypeRekeyInit,
		Body:        initBody,
	}).MarshalBinary()
	if err != nil {
		t.Fatalf("marshal ctrl: %v", err)
	}
	encWire, err := serverSess.EncryptFrame(0x01, core.MsgTypeControl, ctrlWire)
	if err != nil {
		t.Fatalf("encrypt ctrl: %v", err)
	}
	if err := core.WriteMsg(serverConn, encWire); err != nil {
		t.Fatalf("write init: %v", err)
	}

	replyWire, err := core.ReadMsg(serverConn)
	if err != nil {
		t.Fatalf("read reply: %v", err)
	}
	msgType, pt, err := serverSess.DecryptFrameWithType(0x00, replyWire)
	if err != nil {
		t.Fatalf("decrypt reply: %v", err)
	}
	if msgType != core.MsgTypeControl {
		t.Fatalf("expected control msg type, got %d", msgType)
	}
	var replyCtrl core.ControlMessage
	if err := replyCtrl.UnmarshalBinary(pt); err != nil {
		t.Fatalf("unmarshal reply ctrl: %v", err)
	}
	if replyCtrl.ControlType != core.ControlTypeRekeyAck {
		t.Fatalf("expected rekey ack, got %d", replyCtrl.ControlType)
	}

	cancel()
	runErr := <-errCh
	if !errors.Is(runErr, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", runErr)
	}
}

func TestRunSupportsServerDirections(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	keyC2S := bytes(0x77, 32)
	keyS2C := bytes(0x88, 32)
	serverSess := core.NewSession(core.AEADChaCha20Poly1305, keyC2S, keyS2C)
	clientSess := core.NewSession(core.AEADChaCha20Poly1305, keyC2S, keyS2C)

	dev := newFakeTun("srv0", 1400)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- Run(ctx, dev, serverConn, serverSess, Options{
			OutDirection: 0x01,
			InDirection:  0x00,
		})
	}()

	// TUN -> stream from server should be s2c direction (0x01).
	serverPacket := []byte{0xde, 0xad, 0xbe, 0xef}
	dev.injectRead(serverPacket)
	wire, err := core.ReadMsg(clientConn)
	if err != nil {
		t.Fatalf("client read: %v", err)
	}
	msgType, pt, err := clientSess.DecryptFrameWithType(0x01, wire)
	if err != nil {
		t.Fatalf("client decrypt: %v", err)
	}
	if msgType != core.MsgTypeData || !bytesEqual(pt, serverPacket) {
		t.Fatalf("unexpected server outbound packet")
	}

	// stream -> TUN on server should decrypt with c2s direction (0x00).
	clientPacket := []byte{0xca, 0xfe, 0xba, 0xbe}
	respWire, err := clientSess.EncryptFrame(0x00, core.MsgTypeData, clientPacket)
	if err != nil {
		t.Fatalf("client encrypt: %v", err)
	}
	if err := core.WriteMsg(clientConn, respWire); err != nil {
		t.Fatalf("client write: %v", err)
	}
	got, ok := dev.waitWrite(2 * time.Second)
	if !ok || !bytesEqual(got, clientPacket) {
		t.Fatalf("server inbound packet mismatch")
	}

	cancel()
	err = <-errCh
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestRunSessionDurationLimitExceeded(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	keyC2S := bytes(0x91, 32)
	keyS2C := bytes(0x92, 32)
	sess := core.NewSession(core.AEADChaCha20Poly1305, keyC2S, keyS2C)

	dev := newFakeTun("limit0", 1400)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- Run(ctx, dev, clientConn, sess, Options{
			MaxSessionDuration: 200 * time.Millisecond,
		})
	}()

	select {
	case err := <-errCh:
		if !errors.Is(err, ErrSessionDurationLimitExceeded) {
			t.Fatalf("expected ErrSessionDurationLimitExceeded, got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("timeout waiting for duration-limited run to stop")
	}
}

func TestRunSessionBytesLimitExceeded(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	keyC2S := bytes(0x93, 32)
	keyS2C := bytes(0x94, 32)
	clientSess := core.NewSession(core.AEADChaCha20Poly1305, keyC2S, keyS2C)
	serverSess := core.NewSession(core.AEADChaCha20Poly1305, keyC2S, keyS2C)

	dev := newFakeTun("limit1", 1400)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- Run(ctx, dev, clientConn, clientSess, Options{
			MaxSessionBytes: 4,
		})
	}()

	// Inject 5 bytes from TUN->stream so byte budget is exceeded quickly.
	dev.injectRead([]byte{0x45, 0x00, 0x00, 0x01, 0xaa})
	wire, err := core.ReadMsg(serverConn)
	if err != nil {
		t.Fatalf("server read msg: %v", err)
	}
	if _, _, err := serverSess.DecryptFrameWithType(0x00, wire); err != nil {
		t.Fatalf("server decrypt: %v", err)
	}

	select {
	case err := <-errCh:
		if !errors.Is(err, ErrSessionBytesLimitExceeded) {
			t.Fatalf("expected ErrSessionBytesLimitExceeded, got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("timeout waiting for bytes-limited run to stop")
	}
}

type fakeTun struct {
	name string
	mtu  int

	readCh chan []byte
	mu     sync.Mutex
	writes [][]byte

	closeOnce sync.Once
	closed    chan struct{}
}

func newFakeTun(name string, mtu int) *fakeTun {
	return &fakeTun{
		name:   name,
		mtu:    mtu,
		readCh: make(chan []byte, 16),
		closed: make(chan struct{}),
	}
}

func (d *fakeTun) Read(p []byte) (int, error) {
	select {
	case <-d.closed:
		return 0, io.EOF
	case b := <-d.readCh:
		n := copy(p, b)
		return n, nil
	}
}

func (d *fakeTun) Write(p []byte) (int, error) {
	select {
	case <-d.closed:
		return 0, io.EOF
	default:
	}
	cp := append([]byte{}, p...)
	d.mu.Lock()
	d.writes = append(d.writes, cp)
	d.mu.Unlock()
	return len(p), nil
}

func (d *fakeTun) Close() error {
	d.closeOnce.Do(func() {
		close(d.closed)
	})
	return nil
}

func (d *fakeTun) Name() string {
	return d.name
}

func (d *fakeTun) MTU() (int, error) {
	return d.mtu, nil
}

func (d *fakeTun) injectRead(b []byte) {
	d.readCh <- append([]byte{}, b...)
}

func (d *fakeTun) waitWrite(timeout time.Duration) ([]byte, bool) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		d.mu.Lock()
		if len(d.writes) > 0 {
			b := d.writes[0]
			d.writes = d.writes[1:]
			d.mu.Unlock()
			return b, true
		}
		d.mu.Unlock()
		time.Sleep(5 * time.Millisecond)
	}
	return nil, false
}

func bytes(seed byte, n int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = seed
	}
	return out
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
