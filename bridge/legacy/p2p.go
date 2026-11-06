package legacy

import (
	"context"
	"io"
	"log/slog"
	"slices"
	"strings"

	"github.com/djylb/nps/lib/logs"
	"github.com/djylb/tunnelwire/control"
)

// The P2P messages npc and the bridge exchange on a control stream before it
// turns into a TLS stream for the hole punching library, and the names both
// sides agree on.
//
// A visitor opens a stream with WorkP2P and sends a P2PRequest; the bridge
// sends the provider a P2PInvite (P2PPunchStart) on its signal connection, the
// provider opens a stream with WorkP2PSession carrying a P2PJoin, and the
// bridge answers both with a P2PReply once the session exists. After an
// accepting reply, npc runs a TLS client handshake that pins the bridge
// certificate and the bridge a TLS server handshake with it; the TLS stream
// then carries the punching session.
type (
	P2PRequest      = control.P2PRequest
	P2PInvite       = control.P2PInvite
	P2PJoin         = control.P2PJoin
	P2PReply        = control.P2PReply
	P2PRefusedError = control.P2PRefusedError
)

const (
	P2PTransportMode   = control.P2PTransportMode
	P2PCodeRefused     = control.P2PCodeRefused
	P2PCodeUnsupported = control.P2PCodeUnsupported
	P2PCodeOffline     = control.P2PCodeOffline
	P2PCodeBusy        = control.P2PCodeBusy
)

// ErrP2PMessageSize is a message whose length prefix is out of range.
var ErrP2PMessageSize = control.ErrP2PMessageSize

// WriteP2P sends v as one message.
func WriteP2P(w io.Writer, v any) error { return control.WriteP2P(w, v) }

// ReadP2P reads one message into v.
func ReadP2P(r io.Reader, v any) error { return control.ReadP2P(r, v) }

// NewP2PLogger returns a logger that writes the hole punching library's log
// records to the nps log, each line starting with prefix and followed by
// its attributes as key=value.
func NewP2PLogger(prefix string) *slog.Logger { return slog.New(logHandler{prefix: prefix}) }

type logHandler struct {
	prefix string
	attrs  []slog.Attr
}

func (logHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h logHandler) Handle(_ context.Context, r slog.Record) error {
	var b strings.Builder
	b.WriteString(h.prefix)
	b.WriteString(r.Message)
	write := func(a slog.Attr) bool {
		b.WriteString(" " + a.Key + "=" + a.Value.String())
		return true
	}
	for _, a := range h.attrs {
		write(a)
	}
	r.Attrs(write)
	switch {
	case r.Level >= slog.LevelError:
		logs.Error("%s", b.String())
	case r.Level >= slog.LevelWarn:
		logs.Warn("%s", b.String())
	case r.Level >= slog.LevelInfo:
		logs.Info("%s", b.String())
	case r.Level >= slog.LevelDebug:
		logs.Debug("%s", b.String())
	default:
		logs.Trace("%s", b.String())
	}
	return nil
}

func (h logHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return logHandler{prefix: h.prefix, attrs: append(slices.Clone(h.attrs), attrs...)}
}

func (h logHandler) WithGroup(string) slog.Handler { return h }
