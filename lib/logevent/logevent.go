// Package logevent is the table of nps and npc events: their names, levels,
// and whether they also go to the security log or the event bus (webhook).
// Log lines and bus events take their names from here, so one thing that
// happens has one name everywhere. Names and fields only grow; a published
// one is never renamed.
//
// Level rule: error needs a person; warn is a security event or a
// degradation; info is a state change or a policy denial; debug is per
// connection detail.
package logevent

import (
	"log/slog"

	"github.com/djylb/nps/lib/obs"
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
	AuthFailed = obs.Def{Name: "auth_failed", Level: warn, Security: true, Bus: true, Limit: "src_ip"}
	// LoginFailed / LoginOK: web manager login. Fields: src_ip, user,
	// reason (bad_password, bad_captcha, bad_totp, banned, disabled).
	LoginFailed = obs.Def{Name: "login_failed", Level: warn, Security: true, Bus: true}
	LoginOK     = obs.Def{Name: "login_ok", Level: info, Bus: true}
	// BanAdded / BanRemoved: src_ip, scope (login, auth), until.
	BanAdded   = obs.Def{Name: "ban_added", Level: warn, Security: true, Bus: true}
	BanRemoved = obs.Def{Name: "ban_removed", Level: info, Bus: true}
)

// Clients and sessions.
var (
	// ClientOnline / ClientOffline: identity (client id), src_ip, carrier
	// (tcp, tls, kcp, quic, ws, wss), version; offline adds duration_ms.
	ClientOnline  = obs.Def{Name: "client_online", Level: info, Bus: true}
	ClientOffline = obs.Def{Name: "client_offline", Level: info, Bus: true}
	// SessionTakeover: the same client connected again; identity, src_ip.
	SessionTakeover = obs.Def{Name: "session_takeover", Level: warn}
	// LimitExceeded: identity, route, kind (flow, conn, rate, expire).
	LimitExceeded = obs.Def{Name: "limit_exceeded", Level: warn, Bus: true, Limit: "identity"}
)

// Traffic and policy.
var (
	// TunnelDenied: a visitor was refused; src_ip, route, reason (acl,
	// ip_limit, black_ip, conn_limit, client_offline, auth).
	TunnelDenied = obs.Def{Name: "tunnel_denied", Level: info, Limit: "src_ip"}
	// RelayFailed: dialing or relaying a target failed; route, target,
	// reason.
	RelayFailed = obs.Def{Name: "relay_failed", Level: info, Limit: "target"}
	// HTTPAccess: a request through the domain proxy; route, src_ip,
	// method, host, path, status, bytes, dur_ms.
	HTTPAccess = obs.Def{Name: "http_access", Level: debug}
)

// Entries: tunnels and domain entries, as the store reports them. The bus
// carries the entry itself.
var (
	TunnelCreated = obs.Def{Name: "tunnel_created", Level: info}
	TunnelUpdated = obs.Def{Name: "tunnel_updated", Level: info}
	TunnelStarted = obs.Def{Name: "tunnel_started", Level: info}
	TunnelStopped = obs.Def{Name: "tunnel_stopped", Level: info}
	TunnelDeleted = obs.Def{Name: "tunnel_deleted", Level: info}
	HostCreated   = obs.Def{Name: "host_created", Level: info}
	HostUpdated   = obs.Def{Name: "host_updated", Level: info}
	HostStarted   = obs.Def{Name: "host_started", Level: info}
	HostStopped   = obs.Def{Name: "host_stopped", Level: info}
	HostDeleted   = obs.Def{Name: "host_deleted", Level: info}
	// TunnelsReport is the periodic full report, bus only.
	TunnelsReport = obs.Def{Name: "tunnels_report", Level: debug}
	// ListenFailed: a tunnel or service port could not be opened; route,
	// addr, reason.
	ListenFailed = obs.Def{Name: "listen_failed", Level: errl, Bus: true}
)

// Process and configuration.
var (
	// Started / Stopping: version, role (nps, npc).
	Started  = obs.Def{Name: "started", Level: info}
	Stopping = obs.Def{Name: "stopping", Level: info}
	// ConfigApplied / ConfigFailed: keys (changed), reason.
	ConfigApplied = obs.Def{Name: "config_applied", Level: info, Bus: true}
	ConfigFailed  = obs.Def{Name: "config_failed", Level: warn, Bus: true}
	// UpdateAvailable / UpdateApplied / UpdateFailed: from, to, reason.
	UpdateAvailable = obs.Def{Name: "update_available", Level: info, Bus: true}
	UpdateApplied   = obs.Def{Name: "update_applied", Level: info, Bus: true}
	UpdateFailed    = obs.Def{Name: "update_failed", Level: warn, Bus: true}
	// SourceUpdated / SourceFailed: external data such as geo files; source,
	// reason.
	SourceUpdated = obs.Def{Name: "source_updated", Level: info}
	SourceFailed  = obs.Def{Name: "source_failed", Level: warn}
)

// Certificates.
var (
	// CertIssued: domain, issuer, renewal (0 or 1), expires, remaining_s.
	CertIssued = obs.Def{Name: "cert_issued", Level: info, Bus: true}
	// CertFailed: domain, renewal, remaining_s, reason (issue_failed,
	// challenge_failed, rate_limited, dns_failed, account_failed,
	// load_failed, expired, file_removed), msg. Use CertFailedUrgent when
	// fewer than 7 days remain.
	CertFailed       = obs.Def{Name: "cert_failed", Level: warn, Bus: true, Limit: "domain"}
	CertFailedUrgent = obs.Def{Name: "cert_failed", Level: errl, Bus: true, Limit: "domain"}
	// CertRevoked: domain, reason, revoked_at.
	CertRevoked = obs.Def{Name: "cert_revoked", Level: warn, Bus: true}
)

// npc.
var (
	// Connected / ConnectFailed: server, carrier, reason, attempt.
	Connected     = obs.Def{Name: "connected", Level: info}
	ConnectFailed = obs.Def{Name: "connect_failed", Level: warn, Limit: "server"}
	// P2PConnected / P2PFailed / P2PPathChanged: peer, path (direct, relay),
	// reason, rtt_ms.
	P2PConnected   = obs.Def{Name: "p2p_connected", Level: info, Bus: true}
	P2PFailed      = obs.Def{Name: "p2p_failed", Level: info, Bus: true}
	P2PPathChanged = obs.Def{Name: "p2p_path_changed", Level: debug}
)

// All is every event of the table.
var All = []*obs.Def{
	&AuthFailed, &LoginFailed, &LoginOK, &BanAdded, &BanRemoved,
	&ClientOnline, &ClientOffline, &SessionTakeover, &LimitExceeded,
	&TunnelDenied, &RelayFailed, &HTTPAccess,
	&TunnelCreated, &TunnelUpdated, &TunnelStarted, &TunnelStopped, &TunnelDeleted,
	&HostCreated, &HostUpdated, &HostStarted, &HostStopped, &HostDeleted,
	&TunnelsReport, &ListenFailed,
	&Started, &Stopping, &ConfigApplied, &ConfigFailed,
	&UpdateAvailable, &UpdateApplied, &UpdateFailed, &SourceUpdated, &SourceFailed,
	&CertIssued, &CertFailed, &CertRevoked,
	&Connected, &ConnectFailed, &P2PConnected, &P2PFailed, &P2PPathChanged,
	&obs.LogFallback, &obs.TLSHandshakeFailed,
}

func init() { obs.Register(All...) }
