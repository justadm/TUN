package core

import (
	"encoding/binary"
	"errors"
	"sync"
	"time"
)

var (
	ErrNoKeys = errors.New("session keys not set")
)

// Session holds encryption keys and sequence counters.
type Session struct {
	mu sync.Mutex

	KeyC2S    []byte
	KeyS2C    []byte
	SeqC2S    uint64
	SeqS2C    uint64
	ReplayC2S *ReplayWindow
	ReplayS2C *ReplayWindow
	AEADAlg   string
	Plain     bool

	nextKeyC2S  []byte
	nextKeyS2C  []byte
	activeEpoch uint64
	nextEpoch   uint64
	nextKeyID   [16]byte
	overlapTo   time.Time
}

func NewSession(aeadAlg string, keyC2S, keyS2C []byte) *Session {
	return &Session{
		KeyC2S:    append([]byte{}, keyC2S...),
		KeyS2C:    append([]byte{}, keyS2C...),
		SeqC2S:    0,
		SeqS2C:    0,
		ReplayC2S: NewReplayWindow(4096),
		ReplayS2C: NewReplayWindow(4096),
		AEADAlg:   aeadAlg,
		Plain:     false,
	}
}

// EncryptFrame encrypts a payload into a Frame for the given direction.
// direction: 0x00 for c2s, 0x01 for s2c.
func (s *Session) EncryptFrame(direction byte, msgType uint8, payload []byte) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.Plain {
		f := &Frame{
			Version:  VersionV1,
			MsgType:  msgType,
			Flags:    0,
			Reserved: 0,
			Seq:      s.nextSeq(direction),
			Payload:  payload,
		}
		return f.MarshalBinary()
	}
	if len(s.KeyC2S) == 0 || len(s.KeyS2C) == 0 {
		return nil, ErrNoKeys
	}
	seq, key := s.selectSeqKey(direction)
	*seq++
	nonce := NonceFromSeq(direction, *seq)
	ct, err := Encrypt(s.AEADAlg, key, nonce, nil, payload)
	if err != nil {
		return nil, err
	}
	f := &Frame{
		Version:  VersionV1,
		MsgType:  msgType,
		Flags:    0,
		Reserved: 0,
		Seq:      *seq,
		Payload:  ct,
	}
	return f.MarshalBinary()
}

// DecryptFrame parses and decrypts a frame for the given direction.
func (s *Session) DecryptFrame(direction byte, frameBytes []byte) ([]byte, error) {
	_, pt, err := s.DecryptFrameWithType(direction, frameBytes)
	return pt, err
}

// DecryptFrameWithType parses and decrypts a frame, returning message type and plaintext.
func (s *Session) DecryptFrameWithType(direction byte, frameBytes []byte) (uint8, []byte, error) {
	var f Frame
	if err := f.UnmarshalBinary(frameBytes); err != nil {
		return 0, nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.Plain {
		replay := s.selectReplay(direction)
		if !replay.CanAccept(f.Seq) {
			return 0, nil, ErrReplayDetected
		}
		replay.Mark(f.Seq)
		return f.MsgType, f.Payload, nil
	}
	if len(s.KeyC2S) == 0 || len(s.KeyS2C) == 0 {
		return 0, nil, ErrNoKeys
	}
	replay := s.selectReplay(direction)
	if !replay.CanAccept(f.Seq) {
		return 0, nil, ErrReplayDetected
	}

	key := s.selectActiveKey(direction)
	nonce := NonceFromSeq(direction, f.Seq)
	pt, errActive := Decrypt(s.AEADAlg, key, nonce, nil, f.Payload)
	if errActive == nil {
		replay.Mark(f.Seq)
		return f.MsgType, pt, nil
	}

	if s.overlapActiveLocked(time.Now()) {
		nextKey := s.selectNextKey(direction)
		if len(nextKey) > 0 {
			ptNext, errNext := Decrypt(s.AEADAlg, nextKey, nonce, nil, f.Payload)
			if errNext == nil {
				replay.Mark(f.Seq)
				return f.MsgType, ptNext, nil
			}
		}
	}
	return 0, nil, errActive
}

func (s *Session) nextSeq(direction byte) uint64 {
	seq, _ := s.selectSeqKey(direction)
	*seq++
	return *seq
}

func (s *Session) selectReplay(direction byte) *ReplayWindow {
	if direction == 0x00 {
		return s.ReplayC2S
	}
	return s.ReplayS2C
}

func (s *Session) selectSeqKey(direction byte) (*uint64, []byte) {
	if direction == 0x00 {
		return &s.SeqC2S, s.KeyC2S
	}
	return &s.SeqS2C, s.KeyS2C
}

func (s *Session) selectActiveKey(direction byte) []byte {
	if direction == 0x00 {
		return s.KeyC2S
	}
	return s.KeyS2C
}

func (s *Session) selectNextKey(direction byte) []byte {
	if direction == 0x00 {
		return s.nextKeyC2S
	}
	return s.nextKeyS2C
}

func (s *Session) overlapActiveLocked(now time.Time) bool {
	if len(s.nextKeyC2S) == 0 || len(s.nextKeyS2C) == 0 {
		return false
	}
	if s.overlapTo.IsZero() {
		return false
	}
	return !now.After(s.overlapTo)
}

// InstallRekeyV1 prepares next traffic keys from RekeyInitV1 and enables overlap window.
func (s *Session) InstallRekeyV1(init RekeyInitV1, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if init.Epoch <= s.activeEpoch || init.Epoch <= s.nextEpoch {
		return ErrRekeyStaleEpoch
	}
	if len(s.KeyC2S) == 0 || len(s.KeyS2C) == 0 {
		return ErrNoKeys
	}

	nextC2S, nextS2C := deriveRekeyKeysV1(s.KeyC2S, s.KeyS2C, init)
	s.nextKeyC2S = nextC2S
	s.nextKeyS2C = nextS2C
	s.nextEpoch = init.Epoch
	s.nextKeyID = init.NewKeyID
	s.overlapTo = now.Add(time.Duration(init.OverlapMillis) * time.Millisecond)
	return nil
}

// CutoverRekeyV1 promotes prepared keys to active after accepted ack.
func (s *Session) CutoverRekeyV1(ack RekeyAckV1, _ time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if ack.Status != RekeyAckStatusAccepted {
		return ErrRekeyNotAccepted
	}
	if len(s.nextKeyC2S) == 0 || len(s.nextKeyS2C) == 0 || s.nextEpoch == 0 {
		return ErrRekeyNotPrepared
	}
	if ack.Epoch != s.nextEpoch || ack.ActiveKeyID != s.nextKeyID {
		return ErrRekeyAckMismatch
	}

	s.KeyC2S = append([]byte{}, s.nextKeyC2S...)
	s.KeyS2C = append([]byte{}, s.nextKeyS2C...)

	// Reset per-key sequence and replay windows at key boundary.
	s.SeqC2S = 0
	s.SeqS2C = 0
	s.ReplayC2S = NewReplayWindow(4096)
	s.ReplayS2C = NewReplayWindow(4096)

	s.nextKeyC2S = nil
	s.nextKeyS2C = nil
	s.activeEpoch = ack.Epoch
	s.nextEpoch = 0
	s.nextKeyID = [16]byte{}
	s.overlapTo = time.Time{}
	return nil
}

func deriveRekeyKeysV1(activeC2S, activeS2C []byte, init RekeyInitV1) ([]byte, []byte) {
	info := make([]byte, 8+16+12)
	binary.BigEndian.PutUint64(info[0:8], init.Epoch)
	copy(info[8:24], init.NewKeyID[:])
	copy(info[24:36], init.RekeyNonce[:])

	saltC2S := append([]byte("tun-rnd/rekey-v1/c2s/"), info...)
	saltS2C := append([]byte("tun-rnd/rekey-v1/s2c/"), info...)

	prkC2S := hkdfExtract(saltC2S, activeC2S)
	prkS2C := hkdfExtract(saltS2C, activeS2C)
	nextC2S := hkdfExpand(prkC2S, []byte("traffic"), 32)
	nextS2C := hkdfExpand(prkS2C, []byte("traffic"), 32)
	return nextC2S, nextS2C
}
