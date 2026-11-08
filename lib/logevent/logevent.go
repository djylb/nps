// Package logevent is the table of nps and npc events: their names, levels,
// and whether they also go to the security log or the event bus (webhook).
// Log lines and bus events take their names from here, so one thing that
// happens has one name everywhere.
//
// # Fields
//
// Every line, event or not, uses these keys for these meanings and types;
// a key is never reused for something else. Lines add other keys only for
// things this list does not name.
//
//	client      int     client id (never a name); peer is the other side's id in p2p
//	tunnel      int     tunnel id
//	role        string  which side or kind of party a line is about (see events)
//	host        int     domain entry id
//	domain      string  a domain name (host entry, certificate, SNI)
//	route       string  what the line is about: "<mode>:<port>" of a tunnel
//	                    (Tunnel.LogRoute), the domain of a host entry, or the
//	                    name of a service ("web", "bridge", "ip_register", "p2p")
//	src_ip      string  real peer IP, no port, never a prefix
//	src_port    int     peer port
//	addr        string  a peer address host:port that is not src_ip/src_port
//	listen      string  a listening address ip:port
//	port        int     a port alone
//	target      string  the host:port dialed for a visitor
//	server      string  npc: the nps address it connects to
//	carrier     string  tcp, tls, kcp, quic, ws, wss
//	network     string  tcp or udp of a link or local server
//	node        string  an npc instance (node uuid) of a client
//	session     string  a p2p or link session id
//	prefix      string  an address prefix (an IPv6 /64 ban)
//	mode        string  tunnel mode (tcp, udp, socks5, httpProxy, ...)
//	via         string  p2p path: direct or relay
//	user        string  a web manager user name
//	proxy_user  string  a user name a visitor gave a proxy tunnel
//	file        string  a file path
//	name        string  the name of a thing that has no id (geoip, a header rule)
//	path        string  an URL path only
//	method, status, bytes, in_bytes, out_bytes  HTTP and traffic, numbers as numbers
//	dur_ms      int     a duration in milliseconds; other units are not used
//	until       time    when something ends (RFC 3339)
//	version     string
//	reason      string  a fixed short word, listed with each event; never a sentence
//	err         string  the error text
//	detail      string  further text for people, when msg cannot carry it
//	count       int     how many
//	keys        string  config keys, comma-separated
//
// msg is a short lowercase English phrase without variables; values go in
// fields.
//
// # Levels
//
// error needs a person; warn is a security event or a degradation; info is a
// state change or a policy denial; debug is per connection or per request.
package logevent

import (
	"errors"
	"io"
	"log/slog"
	"net"
	"strings"

	"github.com/djylb/slogx"
)

const (
	debug = slog.LevelDebug
	info  = slog.LevelInfo
	warn  = slog.LevelWarn
	errl  = slog.LevelError
)

// Security: the IP is always src_ip, the first field after the fixed ones,
// so a filter anchors on it; fail2ban reads these lines.
var (
	// AuthFailed: an npc, visitor or API caller failed to authenticate.
	// src_ip, src_port, role (client, visitor, secret, api, http_auth),
	// client (when known), proxy_user (visitor), carrier, route, reason
	// (bad_key, bad_password, disabled, malformed, timeout, version,
	// forbidden), flag (forbidden: the refused workflow).
	AuthFailed = slogx.Def{Name: "auth_failed", Level: warn, Security: true, Bus: true, Limit: "src_ip"}
	// LoginFailed / LoginOK: web manager login. src_ip, user, reason
	// (bad_password, bad_captcha, bad_pow, bad_nonce, expired, malformed,
	// banned, acl, session_failed, error). LoginFailed is limited per src_ip.
	LoginFailed = slogx.Def{Name: "login_failed", Level: warn, Security: true, Bus: true, Limit: "src_ip"}
	LoginOK     = slogx.Def{Name: "login_ok", Level: info, Bus: true}
	// BanAdded / BanRemoved: src_ip (or user), scope (login), until.
	BanAdded   = slogx.Def{Name: "ban_added", Level: warn, Security: true, Bus: true}
	BanRemoved = slogx.Def{Name: "ban_removed", Level: info, Bus: true}
	// AdminAction: a change through the web manager or its API. src_ip,
	// user, action (such as client_update, tunnel_delete, config_export),
	// client, tunnel, host (the object, when it has one), result (ok,
	// failed), reason.
	AdminAction = slogx.Def{Name: "admin_action", Level: info, Bus: true}
)

// Clients and sessions.
var (
	// ClientOnline / ClientOffline: client, src_ip, carrier, version;
	// offline adds dur_ms and reason (replaced: a newer session took over; closed: its connections closed and it
	// did not come back within three health checks; deleted: the manager
	// kicked, disabled or deleted the client). The store publishes these to the bus with the client
	// itself, so the log lines do not go there too.
	ClientOnline  = slogx.Def{Name: "client_online", Level: info}
	ClientOffline = slogx.Def{Name: "client_offline", Level: info}
	// SessionTakeover: the same npc connected again; client, src_ip, addr
	// (the old connection).
	SessionTakeover = slogx.Def{Name: "session_takeover", Level: warn}
	// LimitExceeded: a connection refused by a limit; client, route, kind
	// (flow, conn, rate, expire).
	LimitExceeded = slogx.Def{Name: "limit_exceeded", Level: warn, Bus: true, Limit: "client"}
	// QuotaReached: a client, tunnel or host used up its flow or time
	// quota; client, tunnel, host, kind (flow, expire).
	QuotaReached = slogx.Def{Name: "quota_reached", Level: warn, Bus: true}
)

// Traffic and policy.
var (
	// TunnelDenied: a visitor was refused; src_ip, route, reason (acl,
	// dest_acl, not_registered, http_disabled, socks5_disabled, auth,
	// client_offline).
	TunnelDenied = slogx.Def{Name: "tunnel_denied", Level: info, Limit: "src_ip"}
	// RelayFailed: dialing or relaying a target failed; route, client,
	// target, reason (NetReason, no_target, client_offline).
	RelayFailed = slogx.Def{Name: "relay_failed", Level: info, Limit: "target"}
	// HTTPAccess: a request to the domain proxy, written when it ends, for
	// every request including refused ones: route, host, src_ip, method,
	// domain (the Host header), path, proto, status, bytes, dur_ms,
	// target. Debug in the main log; access_log writes every request to a
	// file of its own in combined format.
	HTTPAccess = slogx.Def{Name: "http_access", Level: debug}
	// ConnClosed: a tunnel connection ended; route, tunnel, client,
	// src_ip, target, in_bytes, out_bytes, dur_ms.
	ConnClosed = slogx.Def{Name: "conn_closed", Level: debug}
	// TargetDown / TargetUp: a backend target failed or passed its health
	// check; route, target, reason (check_failed, check_passed).
	TargetDown = slogx.Def{Name: "target_down", Level: warn, Bus: true}
	TargetUp   = slogx.Def{Name: "target_up", Level: info, Bus: true}
)

// Entries: the store publishes their changes to the bus with the entry
// itself. TunnelStarted, TunnelStopped, HostStarted and HostStopped are also
// written as lines: route, tunnel or host, client, reason.
var (
	ClientCreated = slogx.Def{Name: "client_created", Level: info}
	ClientUpdated = slogx.Def{Name: "client_updated", Level: info}
	ClientDeleted = slogx.Def{Name: "client_deleted", Level: info}
	TunnelCreated = slogx.Def{Name: "tunnel_created", Level: info}
	TunnelUpdated = slogx.Def{Name: "tunnel_updated", Level: info}
	TunnelStarted = slogx.Def{Name: "tunnel_started", Level: info}
	TunnelStopped = slogx.Def{Name: "tunnel_stopped", Level: info}
	TunnelDeleted = slogx.Def{Name: "tunnel_deleted", Level: info}
	HostCreated   = slogx.Def{Name: "host_created", Level: info}
	HostUpdated   = slogx.Def{Name: "host_updated", Level: info}
	HostStarted   = slogx.Def{Name: "host_started", Level: info}
	HostStopped   = slogx.Def{Name: "host_stopped", Level: info}
	HostDeleted   = slogx.Def{Name: "host_deleted", Level: info}
	// TunnelsReport is the periodic full report, bus only.
	TunnelsReport = slogx.Def{Name: "tunnels_report", Level: debug}
)

// Process, listeners and configuration.
var (
	// Started / Stopping: version, role (nps, npc), pid, file (config).
	Started  = slogx.Def{Name: "started", Level: info}
	Stopping = slogx.Def{Name: "stopping", Level: info}
	// Listening: route (web, bridge, http, https, http3, ip_register,
	// tunnel route), listen, carrier, tls (bool).
	Listening = slogx.Def{Name: "listening", Level: info}
	// ListenFailed: a port could not be opened; route, listen or port,
	// reason (NetReason), err.
	ListenFailed = slogx.Def{Name: "listen_failed", Level: errl, Bus: true}
	// ConfigApplied / ConfigFailed: file, keys, restart (keys needing a
	// restart), trigger (signal, web); failed adds reason, err.
	ConfigApplied = slogx.Def{Name: "config_applied", Level: info, Bus: true}
	ConfigFailed  = slogx.Def{Name: "config_failed", Level: warn, Bus: true}
	// UpdateAvailable / UpdateApplied / UpdateFailed: from, to, reason
	// (update_failed, rolled_back, restart_failed), err.
	UpdateAvailable = slogx.Def{Name: "update_available", Level: info, Bus: true}
	UpdateApplied   = slogx.Def{Name: "update_applied", Level: info, Bus: true}
	UpdateFailed    = slogx.Def{Name: "update_failed", Level: warn, Bus: true}
	// SourceLoaded / SourceUpdated / SourceFailed: external data such as
	// geo files; name, file, reason, err.
	SourceLoaded  = slogx.Def{Name: "source_loaded", Level: info}
	SourceUpdated = slogx.Def{Name: "source_updated", Level: info}
	SourceFailed  = slogx.Def{Name: "source_failed", Level: warn}
)

// Certificates.
var (
	// CertLoaded: a certificate in use; route, domain, file, until
	// (expiry), issuer.
	CertLoaded = slogx.Def{Name: "cert_loaded", Level: info}
	// CertIssued: domain, issuer, renewal (bool).
	CertIssued = slogx.Def{Name: "cert_issued", Level: info, Bus: true}
	// CertFailed: domain, renewal, remaining_s, reason (issue_failed,
	// challenge_failed, rate_limited, dns_failed, account_failed,
	// load_failed), err. Use CertFailedUrgent when fewer than 7 days remain.
	CertFailed       = slogx.Def{Name: "cert_failed", Level: warn, Bus: true, Limit: "domain"}
	CertFailedUrgent = slogx.Def{Name: "cert_failed", Level: errl, Bus: true, Limit: "domain"}
	// CertRevoked: domain, reason (ocsp).
	CertRevoked = slogx.Def{Name: "cert_revoked", Level: warn, Bus: true}
)

// npc.
var (
	// Connected / ConnectFailed / Disconnected: server, carrier, attempt
	// (failed attempts in a row) or attempts, reason (connect_failed: dial,
	// send_type, signal_stream, no_tunnel, establish; disconnected: lost, closed), err;
	// disconnected adds dur_ms.
	Connected     = slogx.Def{Name: "connected", Level: info}
	Disconnected  = slogx.Def{Name: "disconnected", Level: warn}
	ConnectFailed = slogx.Def{Name: "connect_failed", Level: warn, Limit: "server"}
	// P2PConnected / P2PFailed / P2PPathChanged: client, peer, tunnel,
	// session, via (direct, relay), addr, reason, err.
	P2PConnected   = slogx.Def{Name: "p2p_connected", Level: info, Bus: true}
	P2PFailed      = slogx.Def{Name: "p2p_failed", Level: info, Bus: true}
	P2PPathChanged = slogx.Def{Name: "p2p_path_changed", Level: debug}
)

// All is every event of the table.
var All = []*slogx.Def{
	&AuthFailed, &LoginFailed, &LoginOK, &BanAdded, &BanRemoved, &AdminAction,
	&ClientOnline, &ClientOffline, &SessionTakeover, &LimitExceeded, &QuotaReached,
	&TunnelDenied, &RelayFailed, &HTTPAccess, &ConnClosed, &TargetDown, &TargetUp,
	&ClientCreated, &ClientUpdated, &ClientDeleted,
	&TunnelCreated, &TunnelUpdated, &TunnelStarted, &TunnelStopped, &TunnelDeleted,
	&HostCreated, &HostUpdated, &HostStarted, &HostStopped, &HostDeleted,
	&TunnelsReport,
	&Started, &Stopping, &Listening, &ListenFailed, &ConfigApplied, &ConfigFailed,
	&UpdateAvailable, &UpdateApplied, &UpdateFailed, &SourceLoaded, &SourceUpdated, &SourceFailed,
	&CertLoaded, &CertIssued, &CertFailed, &CertRevoked,
	&Connected, &ConnectFailed, &Disconnected, &P2PConnected, &P2PFailed, &P2PPathChanged,
}

func init() { slogx.Register(All...) }

// NetReason maps a dial, listen or relay error to a short reason word: eof,
// timeout, refused, reset, closed, dns, tls, limit, in_use, permission,
// client_offline, other.
func NetReason(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return "eof"
	}
	if errors.Is(err, net.ErrClosed) || errors.Is(err, io.ErrClosedPipe) {
		return "closed"
	}
	if ne, ok := errors.AsType[net.Error](err); ok && ne.Timeout() {
		return "timeout"
	}
	if _, ok := errors.AsType[*net.DNSError](err); ok {
		return "dns"
	}
	s := strings.ToLower(err.Error())
	switch {
	case strings.Contains(s, "not connect") || strings.Contains(s, "offline"):
		return "client_offline"
	case strings.Contains(s, "in use"):
		return "in_use"
	case strings.Contains(s, "permission") || strings.Contains(s, "access is denied"):
		return "permission"
	case strings.Contains(s, "refused"):
		return "refused"
	case strings.Contains(s, "reset"):
		return "reset"
	case strings.Contains(s, "tls") || strings.Contains(s, "x509") || strings.Contains(s, "certificate"):
		return "tls"
	case strings.Contains(s, "too many") || strings.Contains(s, "limit") || strings.Contains(s, "exceed"):
		return "limit"
	case strings.Contains(s, "closed"):
		return "closed"
	case strings.Contains(s, "eof"):
		return "eof"
	}
	return "other"
}
