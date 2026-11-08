package bridge

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/djylb/nps/bridge/legacy"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/logevent"
	"github.com/djylb/nps/lib/model"
	"github.com/djylb/nps/lib/proxy"
	"github.com/djylb/nps/lib/servercfg"
	"github.com/djylb/slogx"
)

// secretNonceSize is the length of an attach challenge.
const secretNonceSize = 32

// secretGrant is one visitor's authorization to one secret task: what an
// attach resolved once — the task, its policy and the server relaying under
// it — so that the visitor's links and P2P requests name the grant instead
// of proving the password again. Relay and direct path share it: the links
// it relays are served by its SecretServer, the invitations of its direct
// paths carry its policy.
//
// A grant ends (revoke) when its task stops, changes or is deleted, the
// server's settings it was resolved under change, the visitor or the
// provider client goes offline, or its carrier — the attach stream — ends.
// Ending closes the attach stream and the links relayed under it and tells
// the providers that hold direct paths of it.
//
// What a grant is (id, task, visitor client and node, policy) does not
// depend on the connection that carries it; only carrier ties it to one.
// Resuming a grant on a new bridge connection, for a seamless reconnect, is
// binding it to the new attach stream (replacing carrier) when the old one
// ends, within a grace period, instead of revoking it then; links and
// direct paths need no change for that.
type secretGrant struct {
	id      string
	tunnel  *model.Tunnel
	visitor int    // the visitor's client id
	node    string // the visitor's npc (node uuid)
	policy  *proxy.SecretPolicy
	server  *proxy.SecretServer
	wire    []byte // policy as the provider receives it

	mu        sync.Mutex
	revoked   bool
	carrier   net.Conn              // the attach stream the grant lives on
	links     map[net.Conn]struct{} // relayed links in progress
	providers map[*Node]struct{}    // provider nodes invited to direct paths of it
}

// secretGrants are the live grants by id.
type secretGrants struct {
	mu      sync.Mutex
	byID    map[string]*secretGrant
	relayed atomic.Int64
}

func newSecretGrants() *secretGrants {
	return &secretGrants{byID: make(map[string]*secretGrant)}
}

func (r *secretGrants) add(g *secretGrant) {
	r.mu.Lock()
	r.byID[g.id] = g
	r.mu.Unlock()
}

// get returns the live grant id of the visitor node, nil if there is none.
func (r *secretGrants) get(id string, visitor int, node string) *secretGrant {
	r.mu.Lock()
	g := r.byID[id]
	r.mu.Unlock()
	if g == nil || g.visitor != visitor || g.node != node || !g.tunnel.Status {
		return nil
	}
	return g
}

// revokeIf ends every grant match selects.
func (r *secretGrants) revokeIf(reason string, match func(*secretGrant) bool) {
	r.mu.Lock()
	var gone []*secretGrant
	for id, g := range r.byID {
		if match(g) {
			delete(r.byID, id)
			gone = append(gone, g)
		}
	}
	r.mu.Unlock()
	for _, g := range gone {
		g.end(reason)
	}
}

// revoke ends g.
func (r *secretGrants) revoke(g *secretGrant, reason string) {
	r.mu.Lock()
	if r.byID[g.id] == g {
		delete(r.byID, g.id)
	}
	r.mu.Unlock()
	g.end(reason)
}

// end closes what g holds once.
func (g *secretGrant) end(reason string) {
	g.mu.Lock()
	if g.revoked {
		g.mu.Unlock()
		return
	}
	g.revoked = true
	carrier, links, providers := g.carrier, g.links, g.providers
	g.carrier, g.links, g.providers = nil, nil, nil
	g.mu.Unlock()
	if carrier != nil {
		_ = carrier.Close()
	}
	for c := range links {
		_ = c.Close()
	}
	for n := range providers {
		go func() {
			if err := n.sendSignal(legacy.P2PRevokeGrant, legacy.P2PRevoke{Grant: g.id}, p2pHandshakeTimeout); err != nil {
				p2pLog.Debug("grant revocation not sent", "tunnel", g.tunnel.Id, "client", g.visitor, "peer", clientIDOf(g.tunnel), "err", err)
			}
		}()
	}
	log.Debug("secret grant ended", "tunnel", g.tunnel.Id, "client", g.visitor, "node", g.node, "peer", clientIDOf(g.tunnel),
		"reason", reason, "count", len(links))
}

// track adds a relayed link; false when g has ended.
func (g *secretGrant) track(c net.Conn) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.revoked {
		return false
	}
	g.links[c] = struct{}{}
	return true
}

func (g *secretGrant) untrack(c net.Conn) {
	g.mu.Lock()
	delete(g.links, c)
	g.mu.Unlock()
}

// invited records a provider node that serves a direct path of g; false
// when g has ended.
func (g *secretGrant) invited(n *Node) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.revoked {
		return false
	}
	g.providers[n] = struct{}{}
	return true
}

func clientIDOf(t *model.Tunnel) int {
	if t.Client == nil {
		return 0
	}
	return t.Client.Id
}

// RevokeSecretTunnel ends the grants of task id: the server calls it when
// the task stops, restarts with changed settings or is deleted.
func (s *Bridge) RevokeSecretTunnel(id int) {
	s.secrets.revokeIf("tunnel_changed", func(g *secretGrant) bool { return g.tunnel.Id == id })
}

// RevokeSecretGrants ends every grant: the server calls it when settings
// the policies were resolved under change.
func (s *Bridge) RevokeSecretGrants() {
	s.secrets.revokeIf("config_changed", func(*secretGrant) bool { return true })
}

// revokeSecretClient ends the grants client id holds as visitor or serves
// as provider: its session ended.
func (s *Bridge) revokeSecretClient(id int) {
	s.secrets.revokeIf("client_offline", func(g *secretGrant) bool { return g.visitor == id || clientIDOf(g.tunnel) == id })
}

// RelayedSecretLinks is how many links of secret tasks the bridge has
// relayed.
func (s *Bridge) RelayedSecretLinks() int64 { return s.secrets.relayed.Load() }

// handleSecretAttach serves a WorkSecretAttach stream of visitor client id
// on node uuid: it checks the visitor's proof of the task password against
// a fresh nonce, grants it access to the task and holds the grant while the
// stream lasts.
func (s *Bridge) handleSecretAttach(c *conn.Conn, id int, uuid string) {
	ip, port := srcIPPort(c.RemoteAddr())
	refuse := func(code, msg string) {
		_ = legacy.WriteP2P(c, legacy.SecretGranted{Code: code, Message: msg})
		_ = c.Close()
	}
	nonce := make([]byte, secretNonceSize)
	_, _ = rand.Read(nonce)
	_ = c.SetDeadline(time.Now().Add(bridgeHandshakeReadTimeout))
	if err := legacy.WriteP2P(c, legacy.SecretChallenge{Nonce: nonce}); err != nil {
		_ = c.Close()
		return
	}
	var req legacy.SecretAttach
	if err := legacy.ReadP2P(c, &req); err != nil {
		slogx.Emit(context.Background(), log, &logevent.AuthFailed, "src_ip", ip, "src_port", port, "role", "secret",
			"client", id, "reason", secretReadReason(err), "err", err)
		_ = c.Close()
		return
	}
	t := s.store.GetTaskBySecretIndex(req.Index)
	if t == nil || t.Mode != "secret" || !hmac.Equal(req.Proof, crypt.SecretProof(t.Password, nonce, uuid)) {
		slogx.Emit(context.Background(), log, &logevent.AuthFailed, "src_ip", ip, "src_port", port, "role", "secret",
			"client", id, "reason", "bad_key")
		refuse(legacy.P2PCodeRefused, "no secret task has this password")
		return
	}
	if !t.Status || t.Client == nil {
		slogx.Emit(context.Background(), log, &logevent.AuthFailed, "src_ip", ip, "src_port", port, "role", "secret",
			"client", id, "tunnel", t.Id, "route", t.LogRoute(), "reason", "disabled")
		refuse(legacy.P2PCodeRefused, fmt.Sprintf("task %d is disabled", t.Id))
		return
	}
	g, err := s.newSecretGrant(t, id, uuid, c)
	if err != nil {
		refuse(legacy.P2PCodeRefused, err.Error())
		return
	}
	s.secrets.add(g)
	// Registered before the reply, so that a revocation from now on closes
	// the stream and the visitor learns of it.
	err = legacy.WriteP2P(c, legacy.SecretGranted{Grant: g.id, Tunnel: t.Id, Peer: t.Client.Id,
		NameDest: g.policy.NameDest, AllowLocal: g.policy.AllowLocal, Direct: g.policy.Direct()})
	if err != nil {
		s.secrets.revoke(g, "visitor_closed")
		return
	}
	log.Debug("secret grant", "tunnel", t.Id, "client", id, "node", uuid, "peer", t.Client.Id, "src_ip", ip, "src_port", port)
	// The stream carries nothing more: it ends when the visitor drops the
	// grant or its connection, or when the grant is revoked.
	_ = c.SetDeadline(time.Time{})
	_, _ = io.Copy(io.Discard, c)
	s.secrets.revoke(g, "visitor_closed")
}

// newSecretGrant resolves task t for a visitor once, under the server's
// current settings.
func (s *Bridge) newSecretGrant(t *model.Tunnel, visitor int, node string, carrier net.Conn) (*secretGrant, error) {
	var raw [18]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return nil, err
	}
	feature := servercfg.Current().Feature
	p := proxy.NewSecretPolicy(t, t.Route().Target, feature.AllowLocalProxy, feature.AllowSecretLocal, feature.LegacySecretLink)
	wire, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}
	return &secretGrant{
		id:        base64.RawURLEncoding.EncodeToString(raw[:]),
		tunnel:    t,
		visitor:   visitor,
		node:      node,
		policy:    p,
		server:    proxy.NewSecretServer(s, t, p, feature.AllowLocalProxy),
		wire:      wire,
		carrier:   carrier,
		links:     make(map[net.Conn]struct{}),
		providers: make(map[*Node]struct{}),
	}, nil
}

// handleSecretLink serves a WorkSecretLink stream: one link of a visitor
// under its grant, relayed on this goroutine.
func (s *Bridge) handleSecretLink(c *conn.Conn, id int, uuid string) {
	ip, port := srcIPPort(c.RemoteAddr())
	_ = c.SetReadDeadline(time.Now().Add(bridgeHandshakeReadTimeout))
	var open legacy.SecretOpen
	if err := legacy.ReadP2P(c, &open); err != nil {
		log.Debug("reading secret link failed", "client", id, "src_ip", ip, "src_port", port, "err", err)
		_ = c.Close()
		return
	}
	lk, err := legacy.ReadLink(c)
	if err != nil || lk == nil {
		log.Debug("reading secret link failed", "client", id, "src_ip", ip, "src_port", port, "err", err)
		_ = c.Close()
		return
	}
	_ = c.SetReadDeadline(time.Time{})
	g := s.secrets.get(open.Grant, id, uuid)
	if g == nil || !g.track(c.Conn) {
		log.Debug("secret link refused", "client", id, "node", uuid, "src_ip", ip, "src_port", port, "reason", "revoked")
		_ = c.Close()
		return
	}
	defer g.untrack(c.Conn)
	s.secrets.relayed.Add(1)
	_ = g.server.Serve(c.Conn, lk, legacy.WriteAck)
}
