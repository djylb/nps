package bridge

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

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

type Node struct {
	mu        sync.RWMutex
	Client    *Client
	UUID      string
	Version   string
	BaseVer   int
	signal    *conn.Conn
	tunnel    any //*mux.Mux or *quic.Conn
	retryTime int
}

func NewNode(uuid, vs string, bv int) *Node {
	return &Node{
		UUID:    uuid,
		Version: vs,
		BaseVer: bv,
	}
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
	}
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
	_ = n.closeTunnel("node close")
	if n.signal != nil {
		_ = n.signal.Close()
		n.signal = nil
	}
	n.retryTime = retryTimeMax
	return nil
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
	mu        sync.RWMutex
	Id        int
	LastUUID  string
	nodeList  *pool.Pool[string] // nodeUUID
	nodes     sync.Map           // map[nodeUUID]*Node
	files     sync.Map           // map[fileUUID]nodeUUID
	retryTime int                // it will add 1 when ping not ok until to 3 will close the client
	closed    uint32
}

func NewClient(id int, n *Node) *Client {
	c := &Client{
		Id:       id,
		LastUUID: n.UUID,
		nodeList: pool.New[string](),
	}
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
		_ = node.Close()
		c.mu.Lock()
		c.removeNode(uuid)
		c.mu.Unlock()
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
		raw, ok := c.nodes.Load(lastUUID)
		c.mu.RUnlock()
		if ok {
			node, ok := raw.(*Node)
			if ok {
				if !node.IsOffline() {
					if !first {
						logs.Info("Client %d switched to backup node %s", c.Id, lastUUID)
					}
					return node
				}
				_ = node.Close()
			}
		}
		first = false
		c.mu.Lock()
		removed := c.LastUUID
		c.removeNode(removed)
		c.mu.Unlock()
		logs.Info("Client %d removed node %s", c.Id, removed)
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

func (c *Client) RemoveOfflineNodes() (removed int) {
	if c.nodeList.Size() == 0 {
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
		if ok1 && ok2 && node.IsOffline() {
			if !node.Retry() {
				toRemove = append(toRemove, pair{uuid: uuid, node: node})
			}
		}
		return true
	})
	if len(toRemove) == 0 {
		return 0
	}
	for _, it := range toRemove {
		_ = it.node.Close()
	}
	c.mu.Lock()
	for _, it := range toRemove {
		if v, ok := c.nodes.Load(it.uuid); ok && v == it.node && !it.node.IsOnline() {
			c.removeNode(it.uuid)
			removed++
			logs.Info("Client %d removed offline node %s", c.Id, it.uuid)
		}
	}
	c.mu.Unlock()
	if removed > 0 {
		logs.Info("Client %d pruned %d offline node(s)", c.Id, removed)
	}
	return removed
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
