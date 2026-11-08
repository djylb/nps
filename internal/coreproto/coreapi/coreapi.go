// Package coreapi is the boundary between the core protocol and the code
// that uses it: the bridge and the client see only these interfaces, with
// no zmux or wire type, as they see the old protocol only through
// bridge/legacy. internal/coreproto/session implements it; cmd/nps and
// cmd/npc connect the two.
package coreapi

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/netip"
	"time"

	"github.com/quic-go/quic-go"
)

// FirstBytes are the first bytes of a core protocol carrier on a port shared
// with the old protocol: Establish Profiles 1 and 3, and Join.
var FirstBytes = []string{"\x01", "\x02", "\x03"}

// ClientRecord is what the entry reads of a stored client.
type ClientRecord struct {
	ID      int
	VKey    string
	Enabled bool
	Crypt   bool
	// Compress is the client's default compression, algorithms in order of
	// preference (§7): the first the peer can decompress is used.
	Compress []string
	// SchedulerDown names the scheduler of sending to the client; "" is
	// affinity.
	SchedulerDown string
}

// Networks of a link.
const (
	NetworkTCP = "tcp"
	NetworkUDP = "udp"
)

// LinkRequest is one link to open on a session.
type LinkRequest struct {
	Network string // NetworkTCP or NetworkUDP
	// Target is the final target, host:port; empty only for addressed UDP,
	// where each packet names its own.
	Target string
	// Source is the visitor's address, host:port, or empty.
	Source string
	// WantReply waits for the exit's result before the link is returned.
	WantReply bool
	// Compress is the compression of a TCP link, algorithms in order of
	// preference; nil for none. UDP is not compressed.
	Compress []string
}

// LinkError is a link the exit refused or could not make.
type LinkError struct {
	Code   uint64 // the protocol's error code
	Reason string // a word of logevent.NetReason
}

func (e *LinkError) Error() string { return "link refused: " + e.Reason }

// NetReason gives the word to logevent.NetReason.
func (e *LinkError) NetReason() string { return e.Reason }

// Timeout marks a timed-out dial as net.Error does.
func (e *LinkError) Timeout() bool { return e.Reason == "timeout" }

// Temporary implements net.Error.
func (e *LinkError) Temporary() bool { return false }

// Session is one authenticated core protocol session on the server.
type Session interface {
	ClientID() int
	Instance() string // identifies the npc process; opaque
	Source() netip.AddrPort
	Carrier() string
	Agent() string
	Since() time.Time
	// OpenLink opens a link to the exit, within ctx: a connection to the
	// target, bytes for TCP, length-prefixed frames as the old protocol
	// shows them for UDP (SOCKS5 datagrams in the addressed mode).
	OpenLink(ctx context.Context, r LinkRequest) (net.Conn, error)
	// Ping measures the round trip to npc.
	Ping(ctx context.Context) (time.Duration, error)
	// Closed reports whether the session ended.
	Closed() bool
	// Close ends the session; npc may come back at once.
	Close() error
}

// AuthFailure is a failed authentication of an identified client.
type AuthFailure struct {
	Source  netip.AddrPort
	Carrier string
	Client  int
	Reason  string // bad_key, malformed, disabled, forbidden, pipes (client_max_pipes)
	Flag    string // forbidden: what was refused
}

// Hooks are what the entry calls in the bridge.
type Hooks struct {
	// Online runs once a session is established; it returns promptly.
	Online func(Session)
	// Takeover runs when s replaces old, the same npc instance.
	Takeover func(s, old Session)
	// Offline runs when the session ended; reason is a word of the
	// client_offline event.
	Offline func(s Session, reason string)
	// AuthFailed reports a failed authentication of an identified client.
	AuthFailed func(AuthFailure)
	// Legacy takes over a carrier of the old protocol, every byte replayed.
	// It owns c from then on.
	Legacy func(c net.Conn, carrier string)
	// LegacyQUIC takes over a QUIC connection of the old protocol: c is its
	// first stream, every byte replayed. It returns once the connection is
	// handed over; qc stays open until it ends.
	LegacyQUIC func(c net.Conn, qc *quic.Conn)
	// Fallback serves a carrier that is not ours (the domain proxy), every
	// byte replayed; state is its TLS when this process terminated it. It
	// returns false, leaving c to the caller, when there is no target.
	Fallback func(c net.Conn, state *tls.ConnectionState) bool
}

// Entry is the core protocol on the server's shared bridge ports.
type Entry interface {
	// Start starts accepting sessions. It must return before the listeners
	// open.
	Start(ctx context.Context, h Hooks) error
	// Serve runs one accepted carrier of a shared port, a net.Conn or a
	// *tls.Conn before its handshake. It blocks while the carrier is
	// admitted, so it runs on a goroutine the caller waits for.
	Serve(ctx context.Context, c net.Conn, carrier string)
	// ServeMixed is Serve for a port of several carriers: what is no carrier
	// of the core protocol goes to other, every byte replayed, instead of
	// Hooks.Legacy and Hooks.Fallback. state is c's TLS when this process
	// terminated it. other returns false, leaving c to the entry to stall.
	ServeMixed(ctx context.Context, c net.Conn, carrier string, other func(c net.Conn, state *tls.ConnectionState) bool)
	// QUICConfig returns base with what QUIC carriers of this entry need.
	QUICConfig(base *quic.Config) *quic.Config
	// ServeQUIC runs one QUIC connection until it ends. With legacy, a
	// connection whose first stream starts with the old protocol's magic
	// goes to Hooks.LegacyQUIC; ALPN plays no part (§12).
	ServeQUIC(ctx context.Context, qc *quic.Conn, legacy bool)
	// WebSocket returns the handler of the WebSocket bridge path: requests
	// of the core protocol go to it, every other request to legacy.
	WebSocket(legacy http.Handler) (http.Handler, error)
	// WebSocketAnyProtocol returns the handler of the paths that are the
	// core protocol's alone (new_protocol_ws_paths): every WebSocket
	// request goes to it, with the subprotocol it offered or none. Other
	// requests go to next.
	WebSocketAnyProtocol(next http.Handler) (http.Handler, error)
	// ClientChanged tells the entry the state of a client after a change.
	ClientChanged(ClientRecord)
	// ClientRemoved tells the entry that a client no longer exists.
	ClientRemoved(id int)
	// Kick closes the sessions of a client. With auth the credential no
	// longer holds (§3.7); otherwise the client may come back at once.
	Kick(id int, auth bool)
	Close() error
}

// Exit is what npc does for the links the server opens, its local policy
// included.
type Exit interface {
	// Dial connects to target, host:port, on network; a refusal by local
	// policy is a permission error.
	Dial(ctx context.Context, network, target string) (net.Conn, error)
	// ServeUDP5 relays the SOCKS5 datagrams of c, length-prefixed frames as
	// conn.HandleUdp5 reads them, until it ends.
	ServeUDP5(ctx context.Context, c net.Conn)
}

// ClientSession is an established session on npc.
type ClientSession interface {
	// Carrier names the carrier in use: tcp, tls, quic, ws or wss.
	Carrier() string
	// Done is closed when the session ended; Err then says why.
	Done() <-chan struct{}
	Err() error
	Close() error
}

// Dialer establishes sessions of one server profile of npc.
type Dialer interface {
	Dial(ctx context.Context) (ClientSession, error)
	// Server names the profile for log lines.
	Server() string
}
