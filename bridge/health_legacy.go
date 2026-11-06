package bridge

import (
	"time"

	"github.com/djylb/netx"
	"github.com/djylb/nps/bridge/legacy"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/healthcheck"
	"github.com/djylb/nps/lib/logs"
)

// healthSendTimeout bounds writing one health group message.
const healthSendTimeout = 10 * time.Second

// legacyHealthPeer carries health groups on a signal connection of the legacy
// protocol. Node.sendOn serializes them with the node's other messages.
type legacyHealthPeer struct {
	node   *Node
	signal *conn.Conn
}

func (p legacyHealthPeer) SetHealthGroup(g healthcheck.Group) error {
	for _, body := range legacy.HealthSetBodies(g) {
		if err := p.node.sendOn(p.signal, legacy.HealthSet, body, healthSendTimeout); err != nil {
			return err
		}
	}
	return nil
}

func (p legacyHealthPeer) DelHealthGroup(name string) error {
	return p.node.sendOn(p.signal, legacy.HealthDel, legacy.HealthDelBody(name), healthSendTimeout)
}

// GetHealthFromClient reads the health reports of a signal connection until it
// ends. While it runs, a node that supports it also gets its health groups; a
// released npc gets none, and its reports of its own checks have no group.
func (s *Bridge) GetHealthFromClient(id int, c *conn.Conn, client *Client, node *Node) {
	if id <= 0 {
		return
	}
	var push *healthPush
	if node.Caps().ServerHealth {
		push = s.startHealthPush(id, legacyHealthPeer{node: node, signal: c}, client, node)
	}

	const maxRetry = 3
	var retry int
	for {
		rep, err := legacy.ReadHealth(c)
		if err != nil {
			if netx.IsTimeout(err) && retry < maxRetry {
				retry++
				continue
			}
			logs.Trace("GetHealthInfo error, id=%d, retry=%d, detail=%s", id, retry, netx.NetErrorDetails(err, c.Conn))
			break
		}
		retry = 0
		switch {
		case rep.Group == "":
			legacy.ApplyOwnHealthReport(id, node.UUID, rep)
		case push != nil:
			push.apply(rep)
		}
	}
	if push != nil {
		push.stop()
	}
	if !node.CloseIfSignalCurrent(c) {
		// a newer signal connection has taken over the node
		_ = c.Close()
	}
	client.RemoveOfflineNodes(false)
	kickClientHealth(client)
}
