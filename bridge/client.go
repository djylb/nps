package bridge

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/logs"
	"github.com/djylb/nps/lib/mux"
	"github.com/djylb/nps/lib/pool"
	"github.com/quic-go/quic-go"
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

var ClientSelectMode = Primary

func SetClientSelectMode(v any) error {
	const (
		minMode = int32(Primary)
		maxMode = int32(Random)
	)

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

	if int32(mode) < minMode || int32(mode) > maxMode {
		bad = true
	}

	if bad {
		ClientSelectMode = Primary
		logs.Warn("Invalid client select mode %v; fallback to %s(%d)", v, nameOf(Primary), Primary)
		return fmt.Errorf("invalid select mode %v: fallback to Primary", v)
	}
	ClientSelectMode = mode
	logs.Info("Client select mode set to %s(%d)", nameOf(mode), mode)
	return nil
}

const retryTimeMax = 3
const connectGraceProtectWindow = 3 * time.Second
const nodeJoinGraceProtectWindow = 3 * time.Second

type Node struct {
	mu        sync.RWMutex
	sendMu    sync.Mutex // serializes bridge-initiated writes on the signal connection
	Client    *Client
	UUID      string
	Version   string
	BaseVer   int
	signal    *conn.Conn
	tunnel    any //*mux.Mux or *quic.Conn
	retryTime int
	joinNano  int64
}

func NewNode(uuid, vs string, bv int) *Node {
	return &Node{
		UUID:     uuid,
		Version:  vs,
		BaseVer:  bv,
		joinNano: time.Now().UnixNano(),
	}
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
	n.sendMu.Lock()
	defer n.sendMu.Unlock()
	_ = signal.SetWriteDeadline(time.Now().Add(timeout))
	_, err := signal.SendInfo(v, flag)
	_ = signal.SetWriteDeadline(time.Time{})
	if err != nil {
		_ = signal.Close()
	}
	return err
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

func (n *Node) AddNode(node *Node) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if node.Version != "" {
		n.Version = node.Version
	}
	if node.BaseVer != 0 {
		n.BaseVer = node.BaseVer
	}
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
		_ = n.signal.Close()
	}
	n.signal = signal
	n.touchJoinTime()
}

func (n *Node) AddTunnel(tunnel any) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.addTunnel(tunnel)
}

func (n *Node) addTunnel(tunnel any) {
	if n.tunnel != tunnel {
		_ = n.closeTunnel("override")
		n.tunnel = tunnel
		n.touchJoinTime()
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

func (n *Node) GetTunnel() any {
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
	if n.tunnel == nil {
		return true
	}
	switch t := n.tunnel.(type) {
	case *mux.Mux:
		return t.IsClosed()
	case *quic.Conn:
		return t.Context().Err() != nil
	default:
		return true
	}
}

func (n *Node) IsOffline() bool {
	n.mu.RLock()
	defer n.mu.RUnlock()
	if n.BaseVer < 5 {
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
func (n *Node) CloseIfTunnelCurrent(tunnel any) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.tunnel != tunnel {
		return false
	}
	n.closeLocked()
	return true
}

func (n *Node) closeLocked() {
	_ = n.closeTunnel("node close")
	if n.signal != nil {
		_ = n.signal.Close()
		n.signal = nil
	}
	n.retryTime = retryTimeMax
}

func (n *Node) closeTunnel(err string) error {
	if n.tunnel != nil {
		switch t := n.tunnel.(type) {
		case *mux.Mux:
			_ = t.Close()
		case *quic.Conn:
			_ = t.CloseWithError(0, err)
		default:
		}
		n.tunnel = nil
	}
	return nil
}

type Client struct {
	mu              sync.RWMutex
	Id              int
	LastUUID        string
	nodeList        *pool.Pool[string] // nodeUUID
	nodes           sync.Map           // map[nodeUUID]*Node
	files           sync.Map           // map[fileUUID]nodeUUID
	retryTime       int                // it will add 1 when ping not ok until to 3 will close the client
	closed          uint32
	lastConnectNano atomic.Int64
}

func NewClient(id int, n *Node) *Client {
	c := &Client{
		Id:       id,
		LastUUID: n.UUID,
		nodeList: pool.New[string](),
	}
	c.MarkConnectedNow()
	n.Client = c
	c.nodes.Store(n.UUID, n)
	c.nodeList.Add(n.UUID)
	return c
}

func (c *Client) AddNode(n *Node) {
	if n == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if v, ok := c.nodes.Load(n.UUID); ok {
		existing := v.(*Node)
		if existing.IsOnline() && n.BaseVer < 6 {
			_ = n.Close()
			return
		}
		existing.AddNode(n)
		c.LastUUID = n.UUID
		return
	}
	n.Client = c
	c.nodes.Store(n.UUID, n)
	c.nodeList.Add(n.UUID)
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
	if _, ok := c.nodes.Load(uuid); !ok {
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
	c.mu.RLock()
	n, ok := c.nodes.Load(uuid)
	c.mu.RUnlock()
	if !ok {
		return nil, false
	}
	node, ok := n.(*Node)
	if ok {
		if node.IsOnline() {
			return node, true
		}
		if c.InConnectGraceWindow(connectGraceProtectWindow) {
			return nil, false
		}
		c.closeAndRemoveNodeIfCurrent(uuid, node, nodeNotOnline)
	}
	return nil, false
}

func (c *Client) CheckNode() *Node {
	c.mu.RLock()
	size := c.nodeList.Size()
	c.mu.RUnlock()
	if size == 0 {
		logs.Warn("Client %d has no nodes to switch to", c.Id)
		return nil
	}
	first := true
	graceChecksLeft := size
	for {
		var lastUUID string
		c.mu.RLock()
		lastUUID = c.LastUUID
		c.mu.RUnlock()
		if lastUUID == "" {
			var nextUUID string
			switch ClientSelectMode {
			case Primary, RoundRobin:
				nextUUID, _ = c.nodeList.Next()
			case Random:
				nextUUID, _ = c.nodeList.Random()
			default:
				nextUUID, _ = c.nodeList.Next()
			}
			if nextUUID == "" {
				logs.Warn("Client %d has no nodes to switch to", c.Id)
				return nil
			}
			c.mu.Lock()
			c.LastUUID = nextUUID
			c.mu.Unlock()
			lastUUID = nextUUID
		}
		c.mu.RLock()
		raw, _ := c.nodes.Load(lastUUID)
		c.mu.RUnlock()
		node, _ := raw.(*Node)
		if node != nil {
			if !node.IsOffline() {
				if !first {
					logs.Info("Client %d switched to backup node %s", c.Id, lastUUID)
				}
				return node
			}
			if c.InConnectGraceWindow(connectGraceProtectWindow) || node.InJoinGraceWindow(nodeJoinGraceProtectWindow) {
				first = false
				graceChecksLeft--
				if graceChecksLeft <= 0 {
					return nil
				}
				c.mu.Lock()
				c.LastUUID = ""
				c.mu.Unlock()
				continue
			}
		}
		first = false
		// If the entry changed or came back online meanwhile, the next
		// iteration re-evaluates it.
		if c.closeAndRemoveNodeIfCurrent(lastUUID, node, (*Node).IsOffline) {
			logs.Info("Client %d removed node %s", c.Id, lastUUID)
		}
	}
}

func (c *Client) GetNode() *Node {
	switch ClientSelectMode {
	case Primary:
		return c.CheckNode()
	case RoundRobin:
		c.mu.Lock()
		c.LastUUID, _ = c.nodeList.Next()
		c.mu.Unlock()
		return c.CheckNode()
	case Random:
		c.mu.Lock()
		c.LastUUID, _ = c.nodeList.Random()
		c.mu.Unlock()
		return c.CheckNode()
	default:
	}
	return c.CheckNode()
}

func (c *Client) GetNodeByUUID(uuid string) (*Node, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	raw, ok := c.nodes.Load(uuid)
	if !ok {
		return nil, false
	}
	node, ok := raw.(*Node)
	return node, ok
}

func (c *Client) NodeCount() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.nodeList.Size()
}

func (c *Client) RemoveOfflineNodes(ignoreClientGrace bool) (removed int) {
	return c.removeOfflineNodes("", false, ignoreClientGrace)
}

func (c *Client) RemoveOfflineNodesExcept(keepUUID string, ignoreClientGrace bool) (removed int) {
	return c.removeOfflineNodes(keepUUID, false, ignoreClientGrace)
}

func (c *Client) removeOfflineNodes(keepUUID string, force bool, ignoreClientGrace bool) (removed int) {
	if c.nodeList.Size() == 0 {
		return 0
	}
	if !force && !ignoreClientGrace && c.InConnectGraceWindow(connectGraceProtectWindow) {
		return 0
	}
	type pair struct {
		uuid string
		node *Node
	}
	var toRemove []pair
	c.nodes.Range(func(key, value any) bool {
		uuid, ok1 := key.(string)
		node, ok2 := value.(*Node)
		if !ok1 || !ok2 || uuid == keepUUID {
			return true
		}
		if node.InJoinGraceWindow(nodeJoinGraceProtectWindow) {
			return true
		}
		if node.IsOffline() {
			if force || !node.Retry() {
				toRemove = append(toRemove, pair{uuid: uuid, node: node})
			}
		}
		return true
	})
	for _, it := range toRemove {
		if c.closeAndRemoveNodeIfCurrent(it.uuid, it.node, (*Node).IsOffline) {
			removed++
			logs.Info("Client %d removed offline node %s", c.Id, it.uuid)
		}
	}
	if removed > 0 {
		logs.Info("Client %d pruned %d offline node(s)", c.Id, removed)
	}
	return removed
}

func nodeNotOnline(n *Node) bool { return !n.IsOnline() }

// closeAndRemoveNodeIfCurrent removes uuid only while it still maps to node
// and dead(node) still holds, both checked under the client lock, and closes
// node after the lock is released. A nil node removes a uuid whose entry is
// missing or invalid, which keeps the node list in sync with the node map.
func (c *Client) closeAndRemoveNodeIfCurrent(uuid string, node *Node, dead func(*Node) bool) bool {
	c.mu.Lock()
	raw, _ := c.nodes.Load(uuid)
	current, _ := raw.(*Node)
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

func (c *Client) removeNode(uuid string) {
	c.nodes.Delete(uuid)
	c.nodeList.Remove(uuid)
	if c.LastUUID == uuid {
		if next, ok := c.nodeList.Next(); ok {
			c.LastUUID = next
		} else {
			c.LastUUID = ""
		}
	}
	c.files.Range(func(key, value interface{}) bool {
		if v, ok := value.(string); ok && v == uuid {
			c.files.Delete(key)
		}
		return true
	})
}

func (c *Client) IsClosed() bool {
	return atomic.LoadUint32(&c.closed) == 1
}

func (c *Client) Close() error {
	if !atomic.CompareAndSwapUint32(&c.closed, 0, 1) {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nodes.Range(func(key, value interface{}) bool {
		if n, ok := value.(*Node); ok {
			_ = n.Close()
		}
		return true
	})
	c.nodeList.Clear(nil)
	c.nodes = sync.Map{}
	c.files = sync.Map{}
	return nil
}
