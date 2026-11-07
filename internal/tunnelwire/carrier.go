package tunnelwire

import (
	"context"
	"net"
)

// Carrier is a connection to a peer carrying many streams. It is safe for
// concurrent use, and comparable: a Carrier is identified by its value.
type Carrier interface {
	// OpenStream opens a stream to the peer, waiting no longer than ctx allows.
	OpenStream(ctx context.Context) (net.Conn, error)
	// AcceptStream returns the next stream the peer opened.
	AcceptStream(ctx context.Context) (net.Conn, error)
	// Close closes the carrier and its streams.
	Close() error
	// Closed reports whether the carrier has closed.
	Closed() bool
	// Reason says why the carrier closed; empty if it is open or unknown.
	Reason() string
}
