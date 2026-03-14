package core

import "errors"

var (
	ErrReplayDetected  = errors.New("replay detected")
	ErrUnsupportedAlgo = errors.New("unsupported algorithm")
	ErrBadHello        = errors.New("bad hello")
)
