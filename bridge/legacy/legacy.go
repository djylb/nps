// Package legacy is nps's one door to the wire protocol that released npc
// speak: it alone turns protocol messages into nps's own types. The rest of
// nps knows the protocol only through what this package exports, so a future
// protocol is a second package beside it. It goes when the last client that
// speaks this protocol does.
package legacy

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"time"

	"github.com/djylb/nps/internal/tunnelwire"
	"github.com/djylb/nps/internal/tunnelwire/mux"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/slogx"
)

var log = slogx.Module("bridge")

// muxLogger carries the mux package's printf-style lines; their text goes
// into the detail field, since the mux has no structured fields to give.
type muxLogger struct{}

func (muxLogger) Println(v ...any) { log.Debug("mux", "detail", fmt.Sprint(v...)) }
func (muxLogger) Printf(format string, v ...any) {
	log.Debug("mux", "detail", fmt.Sprintf(format, v...))
}
func (muxLogger) Warn(format string, v ...any) { muxLine(slog.LevelWarn, fmt.Sprintf(format, v...)) }
func (muxLogger) Error(format string, v ...any) {
	muxLine(slog.LevelError, fmt.Sprintf(format, v...))
}

// muxLine writes a warning or error of the mux; one that only says the peer
// went away, as every disconnect does, is debug.
func muxLine(level slog.Level, detail string) {
	for _, s := range []string{"EOF", "use of closed", "connection reset", "broken pipe", "closed pipe"} {
		if strings.Contains(detail, s) {
			level = slog.LevelDebug
			break
		}
	}
	log.Log(context.Background(), level, "mux", "detail", detail)
}

func init() { mux.SetLogger(muxLogger{}) }

// Carrier is a connection to a peer carrying many streams.
type Carrier = tunnelwire.Carrier

// NewTunnel returns the carrier of a connection that a peer with caps gave
// work chan or vstr: the QUIC connection itself where it speaks that, otherwise
// a mux over c. The carrier owns c's connection.
func NewTunnel(c *conn.Conn, connType string, caps Caps, disconnectTime int, initiator bool) Carrier {
	if caps.NativeQUIC {
		//noinspection GoResourceLeak
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
