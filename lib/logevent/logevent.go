// Package logevent is the table of nps and npc events: their names, levels,
// and whether they also go to the security log or the event bus (webhook).
// Log lines and bus events take their names from here, so one thing that
// happens has one name everywhere. Names and fields only grow; a published
// one is never renamed.
//
// Common fields: route is "<mode>:<port>" for a tunnel (Tunnel.LogRoute)
// and the domain for a host entry; client and tunnel are ids; src_ip is the
// real peer address without port; err is the error text; reason is a fixed
// short word.
//
// Level rule: error needs a person; warn is a security event or a
// degradation; info is a state change or a policy denial; debug is per
// connection detail.
package logevent

import (
	"errors"
	"io"
	"log/slog"
	"net"
	"strings"

	"github.com/djylb/nps/lib/slogx"
)

const (
	debug = slog.LevelDebug
	info  = slog.LevelInfo
	warn  = slog.LevelWarn
	errl  = slog.LevelError
)

// Security: the IP is always src_ip; fail2ban reads these lines.
var (
	// AuthFailed: an npc or visitor failed to authenticate. Fields: src_ip,
	// src_port, role (client, visitor, p2p), identity (when known), reason
	// (bad_key, unknown_key, disabled, malformed, timeout, garbage,
	// version).
	AuthFailed = slogx.Def{Name: "auth_failed", Level: warn, Security: true, Bus: true, Limit: "src_ip"}
	// LoginFailed / LoginOK: web manager login. Fields: src_ip, user,
	// reason (bad_password, bad_captcha, bad_totp, banned, disabled).
	LoginFailed = slogx.Def{Name: "login_failed", Level: warn, Security: true, Bus: true}
	LoginOK     = slogx.Def{Name: "login_ok", Level: info, Bus: true}
	// BanAdded / BanRemoved: src_ip, scope (login, auth), until.
	BanAdded   = slogx.Def{Name: "ban_added", Level: warn, Security: true, Bus: true}
	BanRemoved = slogx.Def{Name: "ban_removed", Level: info, Bus: true}
)

// Clients and sessions.
var (
	// ClientOnline / ClientOffline: identity (client id), src_ip, carrier
	// (tcp, tls, kcp, quic, ws, wss), version; offline adds duration_ms.
	// The store publishes these to the bus with the client itself, so the
	// log lines do not go there too.
	ClientOnline  = slogx.Def{Name: "client_online", Level: info}
	ClientOffline = slogx.Def{Name: "client_offline", Level: info}
	// SessionTakeover: the same client connected again; identity, src_ip.
	SessionTakeover = slogx.Def{Name: "session_takeover", Level: warn}
	// LimitExceeded: identity, route, kind (flow, conn, rate, expire).
	LimitExceeded = slogx.Def{Name: "limit_exceeded", Level: warn, Bus: true, Limit: "identity"}
)

// Traffic and policy.
var (
	// TunnelDenied: a visitor was refused; src_ip, route, reason (acl,
	// ip_limit, black_ip, conn_limit, client_offline, auth).
	TunnelDenied = slogx.Def{Name: "tunnel_denied", Level: info, Limit: "src_ip"}
	// RelayFailed: dialing or relaying a target failed; route, target,
	// reason.
	RelayFailed = slogx.Def{Name: "relay_failed", Level: info, Limit: "target"}
	// HTTPAccess: a request through the domain proxy; route, src_ip,
	// method, host, path, status, bytes, dur_ms.
	HTTPAccess = slogx.Def{Name: "http_access", Level: debug}
)

// Entries: tunnels and domain entries, as the store reports them. The bus
// carries the entry itself.
var (
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
	// ListenFailed: a tunnel or service port could not be opened; route,
	// addr, reason.
	ListenFailed = slogx.Def{Name: "listen_failed", Level: errl, Bus: true}
)

// Process and configuration.
var (
	// Started / Stopping: version, role (nps, npc).
	Started  = slogx.Def{Name: "started", Level: info}
	Stopping = slogx.Def{Name: "stopping", Level: info}
	// ConfigApplied / ConfigFailed: keys (changed), reason.
	ConfigApplied = slogx.Def{Name: "config_applied", Level: info, Bus: true}
	ConfigFailed  = slogx.Def{Name: "config_failed", Level: warn, Bus: true}
	// UpdateAvailable / UpdateApplied / UpdateFailed: from, to, reason.
	UpdateAvailable = slogx.Def{Name: "update_available", Level: info, Bus: true}
	UpdateApplied   = slogx.Def{Name: "update_applied", Level: info, Bus: true}
	UpdateFailed    = slogx.Def{Name: "update_failed", Level: warn, Bus: true}
	// SourceUpdated / SourceFailed: external data such as geo files; source,
	// reason.
	SourceUpdated = slogx.Def{Name: "source_updated", Level: info}
	SourceFailed  = slogx.Def{Name: "source_failed", Level: warn}
)

// Certificates.
var (
	// CertIssued: domain, issuer, renewal (0 or 1), expires, remaining_s.
	CertIssued = slogx.Def{Name: "cert_issued", Level: info, Bus: true}
	// CertFailed: domain, renewal, remaining_s, reason (issue_failed,
	// challenge_failed, rate_limited, dns_failed, account_failed,
	// load_failed, expired, file_removed), msg. Use CertFailedUrgent when
	// fewer than 7 days remain.
	CertFailed       = slogx.Def{Name: "cert_failed", Level: warn, Bus: true, Limit: "domain"}
	CertFailedUrgent = slogx.Def{Name: "cert_failed", Level: errl, Bus: true, Limit: "domain"}
	// CertRevoked: domain, reason, revoked_at.
	CertRevoked = slogx.Def{Name: "cert_revoked", Level: warn, Bus: true}
)

// npc.
var (
	// Connected / ConnectFailed / Disconnected: server, carrier, reason;
	// disconnected adds dur_ms.
	Connected     = slogx.Def{Name: "connected", Level: info}
	Disconnected  = slogx.Def{Name: "disconnected", Level: warn}
	ConnectFailed = slogx.Def{Name: "connect_failed", Level: warn, Limit: "server"}
	// P2PConnected / P2PFailed / P2PPathChanged: peer, path (direct, relay),
	// reason, rtt_ms.
	P2PConnected   = slogx.Def{Name: "p2p_connected", Level: info, Bus: true}
	P2PFailed      = slogx.Def{Name: "p2p_failed", Level: info, Bus: true}
	P2PPathChanged = slogx.Def{Name: "p2p_path_changed", Level: debug}
)

// All is every event of the table.
var All = []*slogx.Def{
	&AuthFailed, &LoginFailed, &LoginOK, &BanAdded, &BanRemoved,
	&ClientOnline, &ClientOffline, &SessionTakeover, &LimitExceeded,
	&TunnelDenied, &RelayFailed, &HTTPAccess,
	&TunnelCreated, &TunnelUpdated, &TunnelStarted, &TunnelStopped, &TunnelDeleted,
	&HostCreated, &HostUpdated, &HostStarted, &HostStopped, &HostDeleted,
	&TunnelsReport, &ListenFailed,
	&Started, &Stopping, &ConfigApplied, &ConfigFailed,
	&UpdateAvailable, &UpdateApplied, &UpdateFailed, &SourceUpdated, &SourceFailed,
	&CertIssued, &CertFailed, &CertRevoked,
	&Connected, &ConnectFailed, &Disconnected, &P2PConnected, &P2PFailed, &P2PPathChanged,
	&slogx.LogFallback, &slogx.TLSHandshakeFailed,
}

func init() { slogx.Register(All...) }

// NetReason maps a dial or relay error to the short reason words of
// relay_failed and limit_exceeded: eof, timeout, refused, reset, closed,
// dns, tls, limit, in_use, permission, other.
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
