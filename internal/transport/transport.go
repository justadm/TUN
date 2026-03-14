package transport

import "context"

// Stream is a reliable, ordered byte stream.
type Stream interface {
	Read(p []byte) (int, error)
	Write(p []byte) (int, error)
	Close() error
}

// Dialer creates outbound streams.
type Dialer interface {
	Dial(ctx context.Context, addr string) (Stream, error)
}

// Listener accepts inbound streams.
type Listener interface {
	Accept(ctx context.Context) (Stream, error)
	Close() error
}
