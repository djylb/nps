// Package legacy is nps's one door to the wire protocol that released npc
// speak: it alone turns protocol messages into nps's own types. The rest of
// nps knows the protocol only through what this package exports, so a future
// protocol is a second package beside it. It goes when the last client that
// speaks this protocol does.
package legacy

import (
	"net"
	"time"

	"github.com/djylb/nps/internal/tunnelwire/carrier"
	"github.com/djylb/nps/internal/tunnelwire/mux"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/logs"
)

type muxLogger struct{}

func (muxLogger) Println(v ...any)               { logs.Println(v...) }
func (muxLogger) Printf(format string, v ...any) { logs.Printf(format, v...) }
func (muxLogger) Warn(format string, v ...any)   { logs.Warn(format, v...) }
func (muxLogger) Error(format string, v ...any)  { logs.Error(format, v...) }

func init() { mux.SetLogger(muxLogger{}) }

// Carrier is a connection to a peer carrying many streams.
type Carrier = carrier.Carrier

// NewTunnel returns the carrier of a connection that a peer with caps gave
// work chan or vstr: the QUIC connection itself where it speaks that, otherwise
// a mux over c. The carrier owns c's connection.
func NewTunnel(c *conn.Conn, connType string, caps Caps, disconnectTime int, initiator bool) Carrier {
	if caps.NativeQUIC {
		if qc, ok := conn.CarrierOf(c.Conn); ok {
			return qc
		}
	}
	return NewMuxCarrier(c.Conn, connType, disconnectTime, initiator)
}

// NewMuxCarrier returns a mux over c.
func NewMuxCarrier(c net.Conn, connType string, disconnectTime int, initiator bool) Carrier {
	return mux.NewMux(c, connType, disconnectTime, initiator)
}

// SetPingInterval sets how often muxes check their peer.
func SetPingInterval(d time.Duration) { mux.PingInterval = d }

// PingInterval returns how often muxes check their peer.
func PingInterval() time.Duration { return mux.PingInterval }
