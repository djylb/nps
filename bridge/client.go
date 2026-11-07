package bridge

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/djylb/nps/bridge/legacy"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/logevent"
	"github.com/djylb/nps/lib/obs"
	"github.com/djylb/upstream"
)

type SelectMode int32

const (
	Primary SelectMode = iota
	RoundRobin
	Random
)

var selectModeNames = [...]string{
	"Primary",
	"RoundRobin",
	"Random",
}

func nameOf(m SelectMode) string {
	if i := int(m); i >= 0 && i < len(selectModeNames) {
		return selectModeNames[i]
	}
	return fmt.Sprintf("%d", m)
}

// ParseSelectMode reads a select mode given by name or number. Anything else
// is an error, with Primary as the mode to fall back to.
func ParseSelectMode(v any) (SelectMode, error) {
	var mode SelectMode
	var bad bool

	switch x := v.(type) {
	case SelectMode:
		mode = x
	case int:
		mode = SelectMode(x)
	case int32:
		mode = SelectMode(x)
	case int64:
		mode = SelectMode(int(x))
	case string:
		s := strings.TrimSpace(strings.ToLower(x))
		switch s {
		case "", "primary", "p":
			mode = Primary
		case "roundrobin", "round", "rr":
			mode = RoundRobin
		case "random", "rand":
			mode = Random
		default:
			n, err := strconv.Atoi(s)
			if err != nil {
				bad = true
			} else {
				mode = SelectMode(n)
			}
		}
	default:
		bad = true
	}

	if mode < Primary || mode > Random {
		bad = true
	}
	if bad {
		return Primary, fmt.Errorf("invalid select mode %v: fallback to %s(%d)", v, nameOf(Primary), Primary)
	}
	return mode, nil
}

// String names the mode.
func (m SelectMode) String() string { return nameOf(m) }

const retryTimeMax = 3
const connectGraceProtectWindow = 3 * time.Second
const nodeJoinGraceProtectWindow = 3 * time.Second

// nodeTunnelGrace is how long a node that published configuration waits for
// its tunnel. npc dials the tunnel after the config exchange, up to ten seconds
// per server address when a KCP or QUIC address is dead; if the dial fails it
// publishes again, which starts the grace anew.
const nodeTunnelGrace = 2 * time.Minute

// A Node is one npc process of a client. It joins with its signal
// connection, or with its tunnel, and is online once it has both (or, for
// a protocol whose signal travels on the tunnel, once the tunnel is up).
// After joining, and while it waits for a new tunnel (awaitTunnel), it is
// protected: an offline node is then skipped but kept. Outside protection
// an offline node is removed from its client and closed.
type Node struct {
	mu        sync.RWMutex
	sendMu    sync.Mutex // serializes bridge-initiated writes on the signal connection
	Client    *Client
	UUID      string
	Version   string
	BaseVer   int
	caps      legacy.Caps
	signal    *conn.Conn
	tunnel    legacy.Carrier
	retryTime int
	joinNano  int64
	// tunnelDeadline is when the node stops waiting for a new tunnel.
	tunnelDeadline time.Time
	// healthKick wakes the sender of health groups of the node's signal
	// connection; nil while there is none.
	healthKick atomic.Pointer[chan struct{}]
}

func NewNode(uuid, vs string, bv int) *Node {
	return &Node{
		UUID:     uuid,
		Version:  vs,
		BaseVer:  bv,
		caps:     legacy.CapsOf(bv, vs),
		joinNano: time.Now().UnixNano(),
	}
}

func newNode(uuid string, caps legacy.Caps) *Node {
	return NewNode(uuid, caps.ClientVersion, caps.Index)
}

// sendSignal writes one message to the node's signal connection within
// timeout. Writers are serialized, so that messages never interleave; a
// failed or late write may have left part of a message behind, so it closes
// the signal connection and the client reconnects.
func (n *Node) sendSignal(flag string, v any, timeout time.Duration) error {
	signal := n.GetSignal()
	if signal == nil {
		return errors.New("no signal connection")
	}
	return n.sendOn(signal, flag, v, timeout)
}

// sendOn is sendSignal on one given signal connection, for messages that
// depend on what was sent on that connection before.
func (n *Node) sendOn(signal *conn.Conn, flag string, v any, timeout time.Duration) error {
	if n.GetSignal() != signal {
		return errors.New("signal connection replaced")
	}
	n.sendMu.Lock()
	defer n.sendMu.Unlock()
	_ = signal.SetWriteDeadline(time.Now().Add(timeout))
	err := legacy.WriteSignal(signal, v, flag)
	_ = signal.SetWriteDeadline(time.Time{})
	if err != nil {
		_ = signal.Close()
	}
	return err
}

// Caps returns what the node's npc speaks.
func (n *Node) Caps() legacy.Caps {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.caps
}

// versions returns the node's protocol index and npc version.
func (n *Node) versions() (int, string) {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.BaseVer, n.Version
}

func (n *Node) touchJoinTime() {
	n.joinNano = time.Now().UnixNano()
}

// awaitTunnel keeps the node from being judged offline until a new tunnel
// arrives or nodeTunnelGrace passes.
func (n *Node) awaitTunnel() {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.tunnelDeadline = time.Now().Add(nodeTunnelGrace)
}

func (n *Node) awaitingTunnel() bool {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return time.Now().Before(n.tunnelDeadline)
}

// protected reports whether the node is within its join grace window or
// awaits its tunnel, so that being offline does not remove it.
func (n *Node) protected() bool {
	return n.InJoinGraceWindow(nodeJoinGraceProtectWindow) || n.awaitingTunnel()
}

func (n *Node) AddNode(node *Node) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if node.Version != "" {
		n.Version = node.Version
	}
	if node.BaseVer != 0 {
		n.BaseVer = node.BaseVer
	}
	n.caps = legacy.CapsOf(n.BaseVer, n.Version)
	n.touchJoinTime()
	if node.signal != nil {
		n.addSignal(node.signal)
	}
	if node.tunnel != nil {
		n.addTunnel(node.tunnel)
	}
}

func (n *Node) AddSignal(signal *conn.Conn) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.addSignal(signal)
}

func (n *Node) addSignal(signal *conn.Conn) {
	if n.signal != nil && n.signal != signal {
		if n.Client != nil {
			ip, _ := srcIPPort(signal.RemoteAddr())
			obs.Emit(log, &logevent.SessionTakeover, "identity", n.Client.Id, "src_ip", ip,
				"node", n.UUID, "prev_addr", n.signal.RemoteAddr())
		}
		_ = n.signal.Close()
	}
	n.signal = signal
	n.touchJoinTime()
	n.Client.markOnline(n)
}

func (n *Node) AddTunnel(tunnel legacy.Carrier) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.addTunnel(tunnel)
}

func (n *Node) addTunnel(tunnel legacy.Carrier) {
	if n.tunnel != tunnel {
		n.closeTunnel()
		n.tunnel = tunnel
		n.tunnelDeadline = time.Time{}
		n.touchJoinTime()
		n.Client.markOnline(n)
	}
}

func (n *Node) InJoinGraceWindow(window time.Duration) bool {
	if window <= 0 {
		return false
	}
	n.mu.RLock()
	v := n.joinNano
	n.mu.RUnlock()
	if v <= 0 {
		return false
	}
	return time.Since(time.Unix(0, v)) < window
}

func (n *Node) GetSignal() *conn.Conn {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.signal
}

func (n *Node) GetTunnel() legacy.Carrier {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.tunnel
}

func (n *Node) IsOnline() bool {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.isOnline()
}

func (n *Node) isOnline() bool {
	return !n.isTunnelClosed() && (n.signal != nil && !n.signal.IsClosed()) || n.Client.Id < 0
}

func (n *Node) IsTunnelClosed() bool {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.isTunnelClosed()
}

func (n *Node) isTunnelClosed() bool {
	return n.tunnel == nil || n.tunnel.Closed()
}

func (n *Node) IsOffline() bool {
	n.mu.RLock()
	defer n.mu.RUnlock()
	if !n.caps.TunnelStreams {
		return n.isTunnelClosed() && (n.signal == nil || n.signal.IsClosed()) && n.Client.Id > 0
	}
	return !n.isOnline()
}

func (n *Node) Retry() bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.retryTime < retryTimeMax {
		n.retryTime = n.retryTime + 1
		return true
	}
	return false
}

func (n *Node) Close() error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.closeLocked()
	return nil
}

// CloseIfSignalCurrent closes the node only while signal is still its signal
// connection, so that an outdated connection exiting cannot tear down a node
// that has been taken over by a newer connection with the same UUID.
func (n *Node) CloseIfSignalCurrent(signal *conn.Conn) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.signal != signal {
		return false
	}
	n.closeLocked()
	return true
}

// CloseIfTunnelCurrent is CloseIfSignalCurrent for the tunnel.
func (n *Node) CloseIfTunnelCurrent(tunnel legacy.Carrier) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.tunnel != tunnel {
		return false
	}
	n.closeLocked()
	return true
}

func (n *Node) closeLocked() {
	n.closeTunnel()
	if n.signal != nil {
		_ = n.signal.Close()
		n.signal = nil
	}
	n.retryTime = retryTimeMax
}

func (n *Node) closeTunnel() {
	if n.tunnel != nil {
		_ = n.tunnel.Close()
		n.tunnel = nil
	}
}

type Client struct {
	// mu serializes changes of the node set (putNode, removeNode, Close)
	// and guards LastUUID.
	mu       sync.RWMutex
	Id       int
	LastUUID string
	// nodePool is the node set, keyed by node UUID; it is closed when the
	// client is. Members are read through node and nodeList.
	nodePool        *upstream.Pool[*Node]
	mode            SelectMode // how nodes are chosen
	files           sync.Map   // map[fileUUID]nodeUUID
	retryTime       int        // it will add 1 when ping not ok until to 3 will close the client
	lastConnectNano atomic.Int64
	started         time.Time // when the session began, for client_offline
}

// errNodeOffline marks a node that is offline within a grace window, so that
// it is skipped until it connects again.
var errNodeOffline = errors.New("node offline")

// nodePoolConfig returns the node pool configuration of mode. Primary
// fails over in round-robin order; a request may try every node.
func nodePoolConfig(mode SelectMode) upstream.Config {
	cfg := upstream.Config{Balancer: upstream.RoundRobin(), Retry: upstream.RetryConfig{Attempts: math.MaxInt}}
	if mode == Random {
		cfg.Balancer = upstream.Random()
	}
	return cfg
}

// NewClient returns client id with node n, choosing nodes by mode.
func NewClient(id int, n *Node, mode SelectMode) *Client {
	c := &Client{
		Id:       id,
		LastUUID: n.UUID,
		mode:     mode,
		started:  time.Now(),
		nodePool: upstream.New[*Node](nodePoolConfig(mode)),
	}
	c.MarkConnectedNow()
	c.putNode(n)
	return c
}

// putNode adds n to the node set; a closed client refuses it and n is
// closed. Callers hold c.mu, except NewClient.
func (c *Client) putNode(n *Node) {
	n.Client = c
	if c.nodePool.Put(upstream.Target[*Node]{ID: n.UUID, Weight: 1, Value: n}) != nil {
		_ = n.Close()
	}
}

// node returns the node of uuid.
func (c *Client) node(uuid string) (*Node, bool) {
	if c.IsClosed() {
		return nil, false
	}
	b, ok := c.nodePool.Backend(uuid)
	if !ok {
		return nil, false
	}
	return b.Value(), true
}

// nodeList returns the nodes in pool order.
func (c *Client) nodeList() []*Node {
	if c.IsClosed() {
		return nil
	}
	bs := c.nodePool.Backends()
	nodes := make([]*Node, len(bs))
	for i, b := range bs {
		nodes[i] = b.Value()
	}
	return nodes
}

// markOnline lets a node that connected again take connections.
func (c *Client) markOnline(n *Node) {
	if c == nil {
		return
	}
	if b, ok := c.nodePool.Backend(n.UUID); ok && b.Value() == n {
		c.nodePool.MarkOnline(b)
	}
}

func (c *Client) AddNode(n *Node) {
	if n == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if existing, ok := c.node(n.UUID); ok {
		if existing.IsOnline() && !n.caps.NodeID {
			_ = n.Close()
			return
		}
		existing.AddNode(n)
		c.LastUUID = n.UUID
		return
	}
	c.putNode(n)
	c.LastUUID = n.UUID
}

func (c *Client) MarkConnectedNow() {
	c.lastConnectNano.Store(time.Now().UnixNano())
}

func (c *Client) InConnectGraceWindow(window time.Duration) bool {
	if window <= 0 {
		return false
	}
	v := c.lastConnectNano.Load()
	if v <= 0 {
		return false
	}
	return time.Since(time.Unix(0, v)) < window
}

func (c *Client) AddFile(key, uuid string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.node(uuid); !ok {
		return fmt.Errorf("uuid %q not found", uuid)
	}
	c.files.Store(key, uuid)
	return nil
}

func (c *Client) RemoveFile(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.files.Delete(key)
}

func (c *Client) GetNodeByFile(key string) (*Node, bool) {
	c.mu.RLock()
	v, ok := c.files.Load(key)
	c.mu.RUnlock()
	if !ok {
		return nil, false
	}
	uuid, ok := v.(string)
	if !ok {
		return nil, false
	}
	node, ok := c.node(uuid)
	if !ok {
		return nil, false
	}
	if node.IsOnline() {
		return node, true
	}
	if c.InConnectGraceWindow(connectGraceProtectWindow) || node.awaitingTunnel() {
		return nil, false
	}
	c.closeAndRemoveNodeIfCurrent(uuid, node, nodeNotOnline)
	return nil, false
}

// CheckNode returns the current node: in Primary mode the last node used
// while it is online, otherwise, as in every mode, the next online node
// chosen by the mode, which becomes the current one. Offline nodes are
// skipped; outside the grace windows they are removed. CheckNode returns
// nil if no node is online.
func (c *Client) CheckNode() *Node { return c.selectNode(true) }

// GetNode is CheckNode, except that the RoundRobin and Random modes choose
// a node for every call.
func (c *Client) GetNode() *Node { return c.selectNode(c.mode == Primary) }

func (c *Client) selectNode(sticky bool) *Node {
	c.mu.RLock()
	last := c.LastUUID
	c.mu.RUnlock()
	if sticky && last != "" {
		if b, ok := c.nodePool.Backend(last); ok && c.usable(b) {
			return b.Value()
		}
	}
	req := c.nodePool.Request("")
	for {
		l, err := req.Next()
		if err != nil {
			if c.nodePool.Len() == 0 {
				log.Debug("client has no nodes", "client", c.Id)
			}
			return nil
		}
		b := l.Backend()
		l.Release(upstream.Neutral)
		if !c.usable(b) {
			continue
		}
		c.mu.Lock()
		c.LastUUID = b.ID()
		c.mu.Unlock()
		if sticky && last != "" && last != b.ID() {
			log.Info("client switched node", "client", c.Id, "node", b.ID())
		}
		return b.Value()
	}
}

// usable reports whether the node of b is online. An offline node is taken
// out of rotation until it connects again while the client or the node is
// within a grace window or the node awaits its tunnel, and removed otherwise.
func (c *Client) usable(b *upstream.Backend[*Node]) bool {
	node := b.Value()
	if !node.IsOffline() {
		return true
	}
	if c.InConnectGraceWindow(connectGraceProtectWindow) || node.protected() {
		c.nodePool.MarkOffline(b, errNodeOffline)
		// It may have connected again meanwhile.
		if !node.IsOffline() {
			c.nodePool.MarkOnline(b)
			return true
		}
		return false
	}
	// If the entry changed or came back online meanwhile, the next pick
	// re-evaluates it.
	if c.closeAndRemoveNodeIfCurrent(b.ID(), node, (*Node).IsOffline) {
		log.Debug("node removed", "client", c.Id, "node", b.ID())
	}
	return false
}

// usableNode reports whether the node of uuid can take connections.
func (c *Client) usableNode(uuid string) bool {
	b, ok := c.nodePool.Backend(uuid)
	return ok && c.usable(b)
}

// awaitingTunnel reports whether a node of the client awaits its tunnel.
func (c *Client) awaitingTunnel() bool {
	for _, n := range c.nodeList() {
		if n.awaitingTunnel() {
			return true
		}
	}
	return false
}

func (c *Client) GetNodeByUUID(uuid string) (*Node, bool) { return c.node(uuid) }

func (c *Client) NodeCount() int { return len(c.nodeList()) }

func (c *Client) RemoveOfflineNodes(ignoreClientGrace bool) (removed int) {
	return c.removeOfflineNodes("", false, ignoreClientGrace)
}

func (c *Client) RemoveOfflineNodesExcept(keepUUID string, ignoreClientGrace bool) (removed int) {
	return c.removeOfflineNodes(keepUUID, false, ignoreClientGrace)
}

func (c *Client) removeOfflineNodes(keepUUID string, force bool, ignoreClientGrace bool) (removed int) {
	nodes := c.nodeList()
	if len(nodes) == 0 {
		return 0
	}
	if !force && !ignoreClientGrace && c.InConnectGraceWindow(connectGraceProtectWindow) {
		return 0
	}
	for _, node := range nodes {
		if node.UUID == keepUUID || node.protected() || !node.IsOffline() {
			continue
		}
		if !force && node.Retry() {
			continue
		}
		if c.closeAndRemoveNodeIfCurrent(node.UUID, node, (*Node).IsOffline) {
			removed++
		}
	}
	if removed > 0 {
		log.Debug("offline nodes removed", "client", c.Id, "removed", removed)
	}
	return removed
}

func nodeNotOnline(n *Node) bool { return !n.IsOnline() }

// closeAndRemoveNodeIfCurrent removes uuid only while it still maps to node
// and dead(node) still holds, both checked under the client lock, and closes
// node after the lock is released. A nil node removes a uuid that has no
// node.
func (c *Client) closeAndRemoveNodeIfCurrent(uuid string, node *Node, dead func(*Node) bool) bool {
	c.mu.Lock()
	current, _ := c.node(uuid)
	if node != nil {
		if current != node || !dead(node) {
			c.mu.Unlock()
			return false
		}
	} else if current != nil {
		c.mu.Unlock()
		return false
	}
	c.removeNode(uuid)
	c.mu.Unlock()
	if node != nil {
		_ = node.Close()
	}
	return true
}

// removeNode takes uuid out of the node set; callers hold c.mu.
func (c *Client) removeNode(uuid string) {
	c.nodePool.Remove(uuid)
	if c.LastUUID == uuid {
		c.LastUUID = ""
		if bs := c.nodePool.Backends(); len(bs) > 0 {
			c.LastUUID = bs[0].ID()
		}
	}
	c.files.Range(func(key, value interface{}) bool {
		if v, ok := value.(string); ok && v == uuid {
			c.files.Delete(key)
		}
		return true
	})
}

// IsClosed reports whether Close was called; the node pool records it.
func (c *Client) IsClosed() bool {
	select {
	case <-c.nodePool.Done():
		return true
	default:
		return false
	}
}

// Close closes the client and every node of it.
func (c *Client) Close() error {
	c.mu.Lock()
	if c.IsClosed() {
		c.mu.Unlock()
		return nil
	}
	nodes := c.nodeList()
	c.nodePool.Close()
	c.files.Clear()
	c.mu.Unlock()
	for _, n := range nodes {
		_ = n.Close()
	}
	return nil
}
