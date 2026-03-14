package core

import (
	"errors"
)

var (
	ErrNoKeys = errors.New("session keys not set")
)

// Session holds encryption keys and sequence counters.
type Session struct {
	KeyC2S []byte
	KeyS2C []byte
	SeqC2S uint64
	SeqS2C uint64
	ReplayC2S *ReplayWindow
	ReplayS2C *ReplayWindow
	AEADAlg  string
}

func NewSession(aeadAlg string, keyC2S, keyS2C []byte) *Session {
	return &Session{
		KeyC2S:   append([]byte{}, keyC2S...),
		KeyS2C:   append([]byte{}, keyS2C...),
		SeqC2S:   0,
		SeqS2C:   0,
		ReplayC2S: NewReplayWindow(4096),
		ReplayS2C: NewReplayWindow(4096),
		AEADAlg:  aeadAlg,
	}
}

// EncryptFrame encrypts a payload into a Frame for the given direction.
// direction: 0x00 for c2s, 0x01 for s2c.
func (s *Session) EncryptFrame(direction byte, msgType uint8, payload []byte) ([]byte, error) {
	if len(s.KeyC2S) == 0 || len(s.KeyS2C) == 0 {
		return nil, ErrNoKeys
	}
	var seq *uint64
	var key []byte
	if direction == 0x00 {
		seq = &s.SeqC2S
		key = s.KeyC2S
	} else {
		seq = &s.SeqS2C
		key = s.KeyS2C
	}
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
	if len(s.KeyC2S) == 0 || len(s.KeyS2C) == 0 {
		return nil, ErrNoKeys
	}
	var f Frame
	if err := f.UnmarshalBinary(frameBytes); err != nil {
		return nil, err
	}
	var replay *ReplayWindow
	var key []byte
	if direction == 0x00 {
		replay = s.ReplayC2S
		key = s.KeyC2S
	} else {
		replay = s.ReplayS2C
		key = s.KeyS2C
	}
	if !replay.Accept(f.Seq) {
		return nil, ErrReplayDetected
	}
	nonce := NonceFromSeq(direction, f.Seq)
	pt, err := Decrypt(s.AEADAlg, key, nonce, nil, f.Payload)
	if err != nil {
		return nil, err
	}
	return pt, nil
}
