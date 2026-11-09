package proxy

import (
	"context"
	"net"
	"strconv"
	"time"

	"github.com/djylb/nps/lib/model"

	"github.com/djylb/nps/lib/acl"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/ipreg"
	"github.com/djylb/nps/lib/logevent"
	"github.com/djylb/nps/lib/netaddr"
	"github.com/djylb/slogx"
)

var log = slogx.Module("proxy")

// trace writes a line at trace level.
func trace(msg string, args ...any) {
	log.Log(context.Background(), slogx.LevelTrace, msg, args...)
}

// srcIP returns the IP address of addr, without the port.
func srcIP(addr net.Addr) string {
	if addr == nil {
		return ""
	}
	return netaddr.Host(addr.String())
}

// destDenied writes the tunnel_denied event of a visitor whose destination
// the tunnel's destination list refuses.
func destDenied(addr net.Addr, t *model.Tunnel, dest string) {
	slogx.Emit(context.Background(), log, &logevent.TunnelDenied, "src_ip", srcIP(addr), "route", taskName(t), "reason", "dest_acl", "client", t.Client.Id, "target", netaddr.ExtractHost(dest))
}

// proxyAuthFailed writes the auth_failed event of a visitor whose proxy
// credentials the tunnel refuses.
func proxyAuthFailed(addr net.Addr, t *model.Tunnel, user string) {
	slogx.Emit(context.Background(), log, &logevent.AuthFailed, "src_ip", srcIP(addr), "role", "visitor", "client", t.Client.Id, "proxy_user", user, "reason", "bad_password", "route", taskName(t))
}

// limitExceeded writes the limit_exceeded event of a connection to t that
// flow f, the tunnel's or its client's, refused with err.
func limitExceeded(t *model.Tunnel, f conn.Flow, err error) {
	kind := "conn"
	if f != nil {
		if traffic, expired := f.Over(); expired {
			kind = "expire"
		} else if traffic {
			kind = "flow"
		}
	}
	slogx.Emit(context.Background(), log, &logevent.LimitExceeded, "client", t.Client.Id, "route", taskName(t), "kind", kind, "reason", logevent.NetReason(err), "err", err)
}

// relayFailed writes the relay_failed event of a link to target through t
// that could not be opened.
func relayFailed(t *model.Tunnel, target string, err error) {
	slogx.Emit(context.Background(), log, &logevent.RelayFailed, "route", taskName(t), "client", t.Client.Id, "target", target, "reason", logevent.NetReason(err), "err", err)
}

// connClosed writes the conn_closed line of a connection from visitor src
// through t to target, which began at start; in is the bytes from the
// visitor, out those to it. Nothing is built when the line is off.
func connClosed(t *model.Tunnel, src net.Addr, target string, in, out int64, start time.Time) {
	if t == nil || !log.Enabled(context.Background(), logevent.ConnClosed.Level) {
		return
	}
	slogx.Emit(context.Background(), log, &logevent.ConnClosed, "route", taskName(t), "tunnel", t.Id, "client", clientOf(t),
		"src_ip", srcIP(src), "target", target, "in_bytes", in, "out_bytes", out, "dur_ms", time.Since(start).Milliseconds())
}

func clientOf(t *model.Tunnel) int {
	if t.Client == nil {
		return 0
	}
	return t.Client.Id
}

// sourcePolicies returns the source access lists of the client and entry, a
// tunnel or host; missing levels allow everything.
func sourcePolicies(client *model.Client, entry model.SourceView) (cli, ent *acl.Source) {
	if client != nil {
		cli = client.SourceACL()
	}
	return cli, entry.ACL()
}

// SourceDenied reports whether the access lists of the global settings, the
// client and the entry deny the visitor addr, and logs a denial. g, the global lists, is
// nil where there are no global lists, as on npc. entryName names the entry
// in the log, for example "tunnel 3".
func SourceDenied(addr net.Addr, g *acl.Source, client *model.Client, entry model.SourceView, entryName string) bool {
	c, e := sourcePolicies(client, entry)
	if !acl.Allows(addr, g, c, e) {
		slogx.Emit(context.Background(), log, &logevent.TunnelDenied, "src_ip", srcIP(addr), "route", entryName, "reason", "acl", "rule", acl.Decide(addr, levelNames(client, entryName), g, c, e))
		return true
	}
	if client != nil && registrationRequired(client, entry) && !ipreg.RegisteredAddr(client.Id, addr) {
		slogx.Emit(context.Background(), log, &logevent.TunnelDenied, "src_ip", srcIP(addr), "route", entryName, "reason", "not_registered", "client", client.Id)
		return true
	}
	return false
}

// SourceDeniedString is SourceDenied for an address given as text.
func SourceDeniedString(addr string, g *acl.Source, client *model.Client, entry model.SourceView, entryName string) bool {
	c, e := sourcePolicies(client, entry)
	if !acl.AllowsString(addr, g, c, e) {
		slogx.Emit(context.Background(), log, &logevent.TunnelDenied, "src_ip", netaddr.Host(addr), "route", entryName, "reason", "acl", "rule", acl.DecideString(addr, levelNames(client, entryName), g, c, e))
		return true
	}
	if client != nil && registrationRequired(client, entry) && !ipreg.RegisteredString(client.Id, addr) {
		slogx.Emit(context.Background(), log, &logevent.TunnelDenied, "src_ip", netaddr.Host(addr), "route", entryName, "reason", "not_registered", "client", client.Id)
		return true
	}
	return false
}

// datagramDenied is SourceDenied for single datagrams, which are dropped
// with a trace entry only so that a flood of them does not flood the log.
func datagramDenied(addr net.Addr, g *acl.Source, client *model.Client, entry model.SourceView) bool {
	c, e := sourcePolicies(client, entry)
	if acl.Allows(addr, g, c, e) && (client == nil || !registrationRequired(client, entry) || ipreg.RegisteredAddr(client.Id, addr)) {
		return false
	}
	trace("datagram denied", "src_ip", srcIP(addr))
	return true
}

// registrationRequired reports whether the client or the entry only allows
// visitor addresses registered for the client.
func registrationRequired(client *model.Client, entry model.SourceView) bool {
	if client == nil {
		return false
	}
	return client.RegisteredOnly || entry.RegisteredOnly()
}

// levelNames names the levels of a decision for the log.
func levelNames(client *model.Client, entryName string) []string {
	clientName := "client"
	if client != nil {
		clientName = "client " + strconv.Itoa(client.Id)
	}
	return []string{"global", clientName, entryName}
}

func taskName(t *model.Tunnel) string { return t.LogRoute() }

func taskLists(t *model.Tunnel) model.SourceView {
	if t == nil {
		return model.SourceView{}
	}
	return t.View().Source
}
