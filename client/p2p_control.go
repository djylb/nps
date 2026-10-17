package client

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"time"

	"github.com/djylb/holepunch"
	"github.com/djylb/holepunch/portmapper"
	"github.com/djylb/holepunch/protocol"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/logs"
	"github.com/djylb/nps/lib/p2pproto"
	"github.com/djylb/nps/lib/version"
)

// p2pControlTimeout bounds sending the request and the TLS handshake of a
// P2P control stream; p2pReplyTimeout the wait for the bridge's reply, which
// a visitor gets only once the provider joined: the invitation write and the
// join wait take up to 10 s each on the bridge.
const (
	p2pControlTimeout = 10 * time.Second
	p2pReplyTimeout   = 25 * time.Second
)

// errP2PServerIncompatible is a P2P request the server closed or answered
// without a reply of this protocol: an nps server older than
// version.P2PMinVersion.
var errP2PServerIncompatible = errors.New("the nps server does not speak this P2P protocol (it needs nps " + version.P2PMinVersion + " or newer)")

// openP2PControl turns a fresh bridge stream into a P2P control stream: it
// sends the work type and msg, reads the bridge's reply and runs a TLS
// handshake that accepts only the bridge certificate with fingerprint, the
// one the bridge sent, encrypted with the vkey, when the tunnel was set up.
// It returns the TLS stream and the session id; on error c is closed.
// Canceling ctx closes c.
func openP2PControl(ctx context.Context, c *conn.Conn, workType, uuid string, fingerprint []byte, msg any) (*tls.Conn, string, error) {
	if len(fingerprint) == 0 {
		_ = c.Close()
		return nil, "", errors.New("no bridge certificate fingerprint: P2P needs protocol 0.28.0 or newer")
	}
	stop := context.AfterFunc(ctx, func() { _ = c.Close() })
	defer stop()
	if err := SendType(c, workType, uuid); err != nil {
		return nil, "", fmt.Errorf("send work type: %w", err)
	}
	// After SendType, which clears the read deadline.
	_ = c.SetDeadline(time.Now().Add(p2pControlTimeout))
	if err := p2pproto.Write(c, msg); err != nil {
		_ = c.Close()
		return nil, "", fmt.Errorf("send request: %w", err)
	}
	_ = c.SetReadDeadline(time.Now().Add(p2pReplyTimeout))
	var reply p2pproto.Reply
	if err := p2pproto.Read(c, &reply); err != nil {
		_ = c.Close()
		// An answer that is not a reply comes from a server of another
		// protocol; an ended stream may be a restart or a network change.
		var syntax *json.SyntaxError
		if errors.As(err, &syntax) || errors.Is(err, p2pproto.ErrMessageSize) {
			return nil, "", fmt.Errorf("%w: %v", errP2PServerIncompatible, err)
		}
		return nil, "", fmt.Errorf("read reply: %w", err)
	}
	_ = c.SetDeadline(time.Now().Add(p2pControlTimeout))
	if err := reply.Err(); err != nil {
		_ = c.Close()
		return nil, "", err
	}
	tc := tls.Client(c, crypt.NewPinnedTlsClientConfig(fingerprint))
	if err := tc.HandshakeContext(ctx); err != nil {
		_ = tc.Close()
		return nil, "", fmt.Errorf("control tls: %w", err)
	}
	_ = c.SetDeadline(time.Time{})
	return tc, reply.SessionID, nil
}

// p2pOptions are the holepunch options of both sides: this side's pin as
// transport data, the local address of local_ip, the port mapper and the
// mode this side accepts.
func p2pOptions(localIP string, mapper *portmapper.Mapper, mode protocol.SessionMode) holepunch.Options {
	opts := holepunch.Options{
		LocalAddr:     p2pLocalAddr(localIP),
		TransportMode: p2pproto.TransportMode,
		TransportData: p2pLocalPin(),
		Mode:          mode,
		OnProgress: func(p protocol.Progress) {
			logs.Trace("P2P session %s %s: %s %s %s", p.SessionID, p.Role, p.Stage, p.Status, p.Detail)
		},
	}
	if mapper != nil {
		opts.PortMapper = mapper
	}
	return opts
}

// p2pLocalAddr parses local_ip; anything that is not a unicast IP binds every
// address.
func p2pLocalAddr(localIP string) netip.Addr {
	a, err := netip.ParseAddr(strings.Trim(strings.TrimSpace(localIP), "[]"))
	if err != nil || a.IsUnspecified() || a.IsMulticast() {
		return netip.Addr{}
	}
	return a.Unmap()
}

// p2pResultSummary describes a punched path for the log.
func p2pResultSummary(sess *holepunch.Session) string {
	res := sess.Result()
	p := res.Path
	return fmt.Sprintf("%s path %s -> %s over %s, rtt %s, keepalive %s (%s), %d rounds",
		p.Kind, p.LocalAddr, p.RemoteAddr, p.Family, p.RTT, p.Keepalive.Interval, p.Keepalive.Reason, res.Rounds)
}

// p2pNATSummary describes this side's NAT as the session measured it.
func p2pNATSummary(d *holepunch.Diagnostics) string {
	if d == nil {
		return "unknown"
	}
	var parts []string
	for _, t := range d.Self {
		parts = append(parts, fmt.Sprintf("%s mapping=%s filtering=%s ports=%s", t.Family, t.Mapping, t.Filtering, t.PortAlloc))
	}
	if len(parts) == 0 {
		return "unknown"
	}
	return strings.Join(parts, "; ")
}
