package core

import "errors"

var (
	ErrReplayDetected    = errors.New("replay detected")
	ErrHandshakeReplay   = errors.New("handshake replay detected")
	ErrServerIDMismatch  = errors.New("server id mismatch")
	ErrUnsupportedAlgo   = errors.New("unsupported algorithm")
	ErrBadHello          = errors.New("bad hello")
	ErrInvalidMsgType    = errors.New("invalid message type")
	ErrInvalidControlMsg = errors.New("invalid control message")
	ErrNonZeroReserved   = errors.New("reserved fields must be zero")
	ErrRekeyStaleEpoch   = errors.New("rekey epoch is stale")
	ErrRekeyNotPrepared  = errors.New("rekey keys are not prepared")
	ErrRekeyAckMismatch  = errors.New("rekey ack does not match prepared key")
	ErrRekeyNotAccepted  = errors.New("rekey ack is not accepted")
)
