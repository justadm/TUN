package core

import (
	"errors"
)

var (
	ErrNoKeys = errors.New("session keys not set")
)

// Session holds encryption keys and sequence counters.
type Session struct {
	KeyC2S    []byte
	KeyS2C    []byte
	SeqC2S    uint64
	SeqS2C    uint64
	ReplayC2S *ReplayWindow
	ReplayS2C *ReplayWindow
	AEADAlg   string
	Plain     bool
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
	if s.Plain {
		var f Frame
		if err := f.UnmarshalBinary(frameBytes); err != nil {
			return 0, nil, err
		}
		if !s.selectReplay(direction).Accept(f.Seq) {
			return 0, nil, ErrReplayDetected
		}
		return f.MsgType, f.Payload, nil
	}
	if len(s.KeyC2S) == 0 || len(s.KeyS2C) == 0 {
		return 0, nil, ErrNoKeys
	}
	var f Frame
	if err := f.UnmarshalBinary(frameBytes); err != nil {
		return 0, nil, err
	}
	replay := s.selectReplay(direction)
	_, key := s.selectSeqKey(direction)
	if !replay.Accept(f.Seq) {
		return 0, nil, ErrReplayDetected
	}
	nonce := NonceFromSeq(direction, f.Seq)
	pt, err := Decrypt(s.AEADAlg, key, nonce, nil, f.Payload)
	if err != nil {
		return 0, nil, err
	}
	return f.MsgType, pt, nil
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
