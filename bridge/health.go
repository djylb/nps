package bridge

import (
	"time"

	"github.com/djylb/netx"
	"github.com/djylb/nps/bridge/legacy"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/logs"
)

func (s *Bridge) GetHealthFromClient(id int, c *conn.Conn, client *Client, node *Node) {
	if id <= 0 {
		return
	}

	const maxRetry = 3
	var retry int
	//firstSuccess := false

	for {
		info, status, err := legacy.ReadHealth(c)
		if err != nil {
			//logs.Trace("GetHealthInfo error, id=%d, retry=%d, err=%v", id, retry, err)
			if netx.IsTimeout(err) && retry < maxRetry {
				retry++
				continue
			}
			//if !firstSuccess {
			//	return
			//}
			logs.Trace("GetHealthInfo error, id=%d, retry=%d, detail=%s", id, retry, netx.NetErrorDetails(err, c.Conn))
			break
		}
		//logs.Trace("GetHealthInfo: %v, %v, %v", info, err, status)
		//firstSuccess = true
		retry = 0

		// take an unhealthy target out of the rotation, or put a healthy
		// one back
		file.GetDb().JsonDb.Tasks.Range(func(key, value interface{}) bool {
			v := value.(*file.Tunnel)
			if v.Client.Id == id && v.Mode == "tcp" {
				v.Routes.Target(node.UUID, v.Target).SetHealthy(info, status)
			}
			return true
		})
		file.GetDb().JsonDb.Hosts.Range(func(key, value interface{}) bool {
			v := value.(*file.Host)
			if v.Client.Id == id {
				v.Routes.Target(node.UUID, v.Target).SetHealthy(info, status)
			}
			return true
		})
	}
	if !node.CloseIfSignalCurrent(c) {
		// a newer signal connection has taken over the node
		_ = c.Close()
	}
	client.RemoveOfflineNodes(false)
}

func (s *Bridge) ping() {
	ticker := time.NewTicker(time.Second * 5)
	defer ticker.Stop()

	for range ticker.C {
		s.pingClients()
	}
}

// pingClients closes the clients that had no online node for three rounds.
func (s *Bridge) pingClients() {
	closedClients := make([]int, 0)
	s.Client.Range(func(key, value interface{}) bool {
		clientID := key.(int)
		if clientID <= 0 {
			return true
		}
		client, ok := value.(*Client)
		if !ok || client == nil {
			logs.Trace("Client %d is nil", clientID)
			closedClients = append(closedClients, clientID)
			return true
		}
		client.RemoveOfflineNodes(false)
		node := client.CheckNode()
		if (node == nil || node.IsOffline()) && !client.awaitingTunnel() {
			client.retryTime++
			if client.retryTime >= 3 {
				logs.Trace("Stop client %d", clientID)
				closedClients = append(closedClients, clientID)
			}
		} else {
			client.retryTime = 0 // Reset retry count when the state is normal
		}
		return true
	})

	for _, clientId := range closedClients {
		logs.Info("the client %d closed", clientId)
		s.DelClient(clientId)
	}
}
