// Package coreapi is the boundary between the core protocol and the code
// that uses it: the bridge and the client see only these interfaces, with
// no zmux or wire type, as they see the old protocol only through
// bridge/legacy. internal/coreproto/session implements it; cmd/nps and
// cmd/npc connect the two.
package coreapi

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"time"

	"github.com/djylb/nps/internal/coreproto/wire"
	"github.com/djylb/nps/lib/healthcheck"
	"github.com/quic-go/quic-go"
)

// ErrPlainRefused is matched by a dial error of the plain tcp carrier that the
// server did not answer: its port may take only tls.
var ErrPlainRefused = errors.New("no reply on the plain tcp carrier")

// FirstBytes are the first bytes of a core protocol carrier on a port shared
// with the old protocol: Establish Profiles 1 and 3, and Join.
var FirstBytes = []string{"\x01", "\x02", "\x03"}

// ClientRecord is what the entry reads of a stored client.
type ClientRecord struct {
	ID      int
	Enabled bool
	Crypt   bool
	// ConfigOnly is the public key: its sessions only upload a configuration
	// and are never nodes of a client.
	ConfigOnly bool
	// VisitorOnly is the visitor key: its sessions only visit, as
	// wire.Hello.VisitorOnly sessions do, and upload or register nothing.
	VisitorOnly bool
	// Shaping is the client's shaping switch (§13).
	Shaping bool
	VKey    string
	// Compress is the client's default compression, algorithms in order of
	// preference (§7): the first the peer can decompress is used.
	Compress []string
	// SchedulerDown names the scheduler of sending to the client; "" is
	// affinity.
	SchedulerDown string
	// SchedulerUp names the scheduler npc is told to send with; "" tells
	// nothing and npc keeps its own.
	SchedulerUp string
}

// Networks of a link.
const (
	NetworkTCP = "tcp"
	NetworkUDP = "udp"
	// NetworkAll is a virtual service of both networks.
	NetworkAll = "all"
	// NetworkUDP5 is addressed UDP on a server-exit link.
	NetworkUDP5 = "udp5"
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
	// ProviderTarget leaves the choice of target to the provider, Target
	// empty, in a nested session (§9.3).
	ProviderTarget bool
	// Compress is the compression of a TCP link, algorithms in order of
	// preference; nil for none. UDP is not compressed.
	Compress []string
	// LinkID names a server-exit LINK (§4.6).
	LinkID uint64
	// Service names the virtual service to reach instead of a target (§8),
	// from ServiceID.
	Service []byte
}

// ServiceKindFile is the kind of a file service.
const ServiceKindFile = "file"

// ServiceIDLen is the length of a service id.
const ServiceIDLen = wire.ServiceIDLen

// ServiceID is the id of the virtual service of kind and name (§8).
func ServiceID(kind, name string) []byte { return wire.ServiceID(kind, name) }

// ServiceInfo is one virtual service a session reported (§8).
type ServiceInfo struct {
	Kind, Name string
	Meta       string
	Network    string // NetworkTCP, NetworkUDP or NetworkAll
}

// LocalService is a virtual service npc offers on a session (§8). Its
// settings stay on npc.
type LocalService struct {
	Kind, Name string
	Meta       string // for display only
	Network    string // NetworkTCP (or empty), NetworkUDP or NetworkAll
	// Serve takes over c, one connection to the service; it returns promptly
	// or when c ended.
	Serve func(ctx context.Context, c net.Conn)
}

// ServiceHost is implemented by an established session that can offer virtual
// services.
type ServiceHost interface {
	// PublishServices makes list what this session offers: the complete
	// snapshot, replacing any before it.
	PublishServices(ctx context.Context, list []LocalService) error
}

// LinkError is a link the exit refused or could not make.
type LinkError struct {
	Code   uint64 // the protocol's error code
	Reason string // a word of logevent.NetReason
}

func (e *LinkError) Error() string { return "link refused: " + e.Reason }

// TunnelOffline is the refusal of a link whose client has no session to
// carry it (NPS_TUNNEL_OFFLINE).
func TunnelOffline() *LinkError {
	return &LinkError{Code: wire.CodeTunnelOffline, Reason: "client_offline"}
}

// IsNotAllowed reports whether err is a refusal by the peer's local policy.
func IsNotAllowed(err error) bool {
	var e *LinkError
	return errors.As(err, &e) && e.Code == wire.CodeNotAllowed
}

// ServiceUnavailable is the refusal of a link to a service nobody reports
// (NPS_SERVICE_UNAVAILABLE).
func ServiceUnavailable() *LinkError {
	return &LinkError{Code: wire.CodeServiceUnavailable, Reason: "other"}
}

// LinkNotAllowed is the refusal of a link its visitor may not make.
func LinkNotAllowed() *LinkError {
	return &LinkError{Code: wire.CodeNotAllowed, Reason: "permission"}
}

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
	// Suspended reports whether the session lost all its pipes and waits
	// for one within its resume timeout (§3.8). A session that ended is
	// not suspended. A suspended session is still online.
	Suspended() bool
	// Close ends the session; npc may come back at once.
	Close() error
	// CanUpdate reports whether npc answers update requests (§2.3).
	CanUpdate() bool
	// RequestUpdate asks npc to update itself; a refusal by its local policy
	// is a LinkError with the code NOT_ALLOWED.
	RequestUpdate(ctx context.Context) error
	// ShapingStats reports what the shaping of the session did, in the
	// direction this side sends.
	ShapingStats() ShapingStats
	// CanHealth reports whether npc runs health checks (§2.3).
	CanHealth() bool
	// SetHealth makes groups, complete, what npc checks (§5.3).
	SetHealth(groups []healthcheck.Group)
}

// ShapingStats counts what the shaping of a session did.
type ShapingStats struct {
	Wire          uint64        // wire bytes added
	Stall         time.Duration // waits applied
	Injected      uint64        // records sent for shaping only
	Clamped       uint64        // decisions reduced by a limit
	InjectDropped uint64        // injections not sent
	Faults        uint64        // shaper calls that failed
}

// Add returns the sum of s and o.
func (s ShapingStats) Add(o ShapingStats) ShapingStats {
	return ShapingStats{s.Wire + o.Wire, s.Stall + o.Stall, s.Injected + o.Injected,
		s.Clamped + o.Clamped, s.InjectDropped + o.InjectDropped, s.Faults + o.Faults}
}

// HealthSink takes the reports of a session's checks. Its call returns within
// a bounded time; after an error it is not called again.
type HealthSink interface {
	ReportHealth(healthcheck.Report) error
}

// HealthRunner runs the checks of one session on npc.
type HealthRunner interface {
	// Set makes groups, complete, the checks that run.
	Set(groups []healthcheck.Group)
	// Stop ends the checks.
	Stop()
}

// DialObserver is a HealthRunner that wants the outcome of the exit's dials:
// real traffic says as much about a target as a check does.
type DialObserver interface {
	ObserveDial(network, addr string, err error)
}

// LinkResolve is the request of a LINK.
type LinkResolve struct {
	Tunnel int // 0 when AccessID names it
	// AccessID finds the task of an access (§9.1); nil when Tunnel names it.
	AccessID []byte
	// Verify reports whether the visitor proved it holds password on this
	// session; set with AccessID. The password itself is never on the wire.
	Verify func(password []byte) bool
	// Source is where the visitor's session comes from.
	Source netip.AddrPort
	// ServerExit is the visitor asking for the server as the exit of the
	// link; the tunnel decides whether it is (LinkGrant.ServerExit).
	ServerExit bool
}

// AccessID is what an access key is found by (§9.1).
func AccessID(password []byte) []byte { return wire.AccessID(password) }

// Limiter meters one direction of a relayed path. Its method set is what the
// protocol's splice takes.
type Limiter interface {
	// Ready waits until at least minBytes bytes (or the burst, if less) are
	// allowed and returns the allowance, at most maxBytes. An error ends
	// the direction.
	Ready(ctx context.Context, minBytes, maxBytes int) (int, error)
	// TryReady returns the allowance now, 0 when it would have to wait.
	TryReady(maxBytes int) int
	// Charge accounts n bytes forwarded, as they pass.
	Charge(n int)
}

// Forwarding resolves the source of an HTTP carrier behind trusted
// intermediaries. A nil Forwarding takes the peer of each request.
type Forwarding interface {
	// Source is the client of r, as the WebSocket routes count it.
	Source(r *http.Request) netip.AddrPort
}

// LinkGrant is what the bridge resolved of a LINK.
type LinkGrant struct {
	Tunnel   int
	Provider int // the provider's client; unused with ServerExit
	// ServerExit makes the server the exit of the link (§9.1): the visitor
	// asked for it and the tunnel allows it, or the tunnel's provider is the
	// server itself.
	ServerExit bool
	// Policy is the provider's local policy of the link.
	Policy LinkPolicy
	// Valid reports whether the tunnel and the access still hold (§4.6).
	Valid func() bool
	// ServeExit serves one link of a server-exit LINK until it ends. It calls
	// ack once the link is admitted and its target reached, before any byte
	// of c (§4.4); an error returned before that is what the stream is
	// refused with, a *LinkError naming its code.
	ServeExit func(ctx context.Context, c net.Conn, network, target string, ack func() error) error
	// Admit admits one relayed PIPE against the quota and the connection
	// limits; the PIPE holds what release gives back. stop ends the PIPE
	// when its quota expires.
	Admit func(stop func()) (release func(), err error)
	// RelayAllowed reports whether the server may relay the link: the
	// tunnel allows it and its traffic quota is not spent. It is asked at
	// LINK, at every PIPE and when a PIPE's limiter stops. nil allows
	// relaying. Valid, unlike it, ends the whole link.
	RelayAllowed func() bool
	// WatchRelay calls changed, which returns at once, whenever what
	// RelayAllowed reports may have changed, until stop is called. nil
	// watches nothing.
	WatchRelay func(changed func()) (stop func())
	// Limiters limit and meter a relayed PIPE: from the visitor and to it.
	Limiters func() (up, down Limiter)
	// AddDirect counts the direct traffic its provider reported (§9.6): the
	// bytes it received and sent since its last report. A statistic only,
	// outside quota and rates. It returns promptly; nil counts nothing.
	AddDirect func(in, out uint64)
}

// LinkPolicy is a link's policy at its provider (§9.1).
type LinkPolicy struct {
	Targets    []string // host:port
	TargetType string   // tcp, udp, or "" for all
	NameDest   bool
	Scope      string // rule text (§9.5), "" for everything
}

// PunchPair are the control streams of one punch.
type PunchPair struct {
	Visitor, Provider net.Conn
	VisitorID         int
	ProviderID        int
	Tunnel            int
	Background        bool
	ServerAddr        string
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
	// SuspendChanged runs after Session.Suspended of s changed, between
	// Online and Offline of s; it returns promptly.
	SuspendChanged func(s Session)
	// AuthFailed reports a failed authentication of an identified client.
	AuthFailed func(AuthFailure)
	// Legacy takes over a carrier of the old protocol, every byte replayed.
	// It owns c from then on.
	Legacy func(c net.Conn, carrier string)
	// ResolveLink authorizes a LINK of visitor client (§9.1); nil refuses
	// them all.
	ResolveLink func(client int, r LinkResolve) (*LinkGrant, error)
	// Coordinate runs the punch of a link over the control streams of its
	// visitor and provider until it ends; nil refuses PUNCH.
	Coordinate func(ctx context.Context, p PunchPair)
	// ConfigUpload admits the config upload a session of its client opens
	// (0.35 transition, removed in the next major version): it returns what
	// serves the stream, or nil to refuse. nil here refuses all.
	ConfigUpload func(Session) func(net.Conn)
	// Register admits an npc register a session opens, like ConfigUpload
	// (0.35 transition, removed in the next major version).
	Register func(Session) func(net.Conn)
	// Health takes one report of a session's checks; it returns promptly.
	Health func(s Session, r healthcheck.Report)
	// Services takes a session's complete snapshot of its virtual services
	// (§8); calls for one session do not overlap. It returns promptly.
	Services func(s Session, list []ServiceInfo)
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
	// ServeTerminated runs a carrier whose TLS a domain route of this process
	// ended; head are the bytes already read of it. The TLS is end to end, so
	// the carrier can be Bound. It blocks like Serve.
	ServeTerminated(ctx context.Context, tc *tls.Conn, head []byte, carrier string)
	// QUICConfig returns base with what QUIC carriers of this entry need.
	QUICConfig(base *quic.Config) *quic.Config
	// ServeQUIC runs one QUIC connection of the core protocol until it ends;
	// the caller chose it by ALPN (§12).
	ServeQUIC(ctx context.Context, qc *quic.Conn)
	// WebSocket returns the handler of the WebSocket bridge path: requests
	// of the core protocol go to it, every other request to legacy.
	WebSocket(legacy http.Handler, f Forwarding) (http.Handler, error)
	// WebSocketAnyProtocol returns the handler of the paths that are the
	// core protocol's alone (new_protocol_ws_paths): every WebSocket
	// request goes to it, with the subprotocol it offered or none. Other
	// requests go to next.
	WebSocketAnyProtocol(next http.Handler, f Forwarding) (http.Handler, error)
	// HTTPCarriers returns the handler of the HTTP carriers (streaming,
	// split and, with an admission key, exchange) of a path of the core protocol; other requests go to next.
	HTTPCarriers(next http.Handler, f Forwarding) (http.Handler, error)
	// Forwarding builds what the WebSocket routes take: the prefixes are the
	// trusted intermediaries, header the field they report the client in.
	// Nil for no prefixes.
	Forwarding(trusted []netip.Prefix, header string) Forwarding
	// Stall holds c without an answer for a while and closes it, so that
	// bytes of neither protocol tell a prober nothing. Past the limits of
	// stalled carriers it closes c at once. It blocks.
	Stall(ctx context.Context, c net.Conn)
	// ClientChanged tells the entry the state of a client after a change.
	ClientChanged(ClientRecord)
	// ClientRemoved tells the entry that a client no longer exists.
	ClientRemoved(id int)
	// RevokeLinks ends the links match selects (§9.4a).
	RevokeLinks(match func(tunnel, visitor, provider int) bool)
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

// DirectPath is a punched path on which QUIC is up.
type DirectPath struct {
	QUIC  *quic.Conn
	Close func() // ends the path and its socket
}

// Puncher makes direct paths of a link (§9.4).
type Puncher interface {
	// Punch runs holepunch over the control stream ctrl and brings QUIC up:
	// as its client with accept false, as its server with accept true.
	Punch(ctx context.Context, ctrl net.Conn, background, accept bool) (*DirectPath, error)
}

// Visitor is the visitor's access to one link's provider (§9).
type Visitor interface {
	// OpenLink opens a link to the provider as Session.OpenLink does.
	OpenLink(ctx context.Context, r LinkRequest) (net.Conn, error)
	Close() error
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

// ConfigUploader is implemented by an established session that can open the
// config upload stream (0.35 transition, removed in the next major version).
type ConfigUploader interface {
	// OpenConfigUpload opens the stream once the server has admitted it.
	OpenConfigUpload(ctx context.Context) (net.Conn, error)
	// OpenRegister opens the stream of npc register in the same way.
	OpenRegister(ctx context.Context) (net.Conn, error)
}

// Dialer establishes sessions of one server profile of npc.
type Dialer interface {
	Dial(ctx context.Context) (ClientSession, error)
	// Server names the profile for log lines.
	Server() string
}

// Outer is the one session of a profile to the server, which what hosts on it
// and what visits through it share (§9.4a). A host keeps it through Dial; the
// visitors dial it on demand and give it up when idle, unless it is held.
type Outer interface {
	Dialer
	// Visitor makes the access of the secret task Access in connection mode
	// mode (auto, relay or direct). relayIdle is how long a relay stands by
	// once the direct path is up; zero is the default. serverExit asks for
	// the server as the exit of the link (local_proxy): the tunnel may not
	// allow it, then the link goes to the provider.
	Visitor(access []byte, mode string, relayIdle time.Duration, serverExit bool) Visitor
	// Hold sets what keeps the session whatever the visitors need: keep
	// always, hosting while it returns true.
	Hold(keep bool, hosting func() bool)
	// Close ends the session.
	Close()
}
