package bridge

import (
	"time"

	"github.com/djylb/nps/lib/common"
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
		info, status, err := c.GetHealthInfo()
		if err != nil {
			//logs.Trace("GetHealthInfo error, id=%d, retry=%d, err=%v", id, retry, err)
			if conn.IsTimeout(err) && retry < maxRetry {
				retry++
				continue
			}
			//if !firstSuccess {
			//	return
			//}
			logs.Trace("GetHealthInfo error, id=%d, retry=%d, detail=%s", id, retry, conn.DescribeNetError(err, c.Conn))
			break
		}
		//logs.Trace("GetHealthInfo: %v, %v, %v", info, err, status)
		//firstSuccess = true
		retry = 0

		if !status {
			// the target is unhealthy: take it out of the rotation
			file.GetDb().JsonDb.Tasks.Range(func(key, value interface{}) bool {
				v := value.(*file.Tunnel)
				if v.Client.Id == id && v.Mode == "tcp" && v.Target.HasTarget(info) {
					markTargetDown(v.Target, &v.Health, info)
				}
				return true
			})
			file.GetDb().JsonDb.Hosts.Range(func(key, value interface{}) bool {
				v := value.(*file.Host)
				if v.Client.Id == id && v.Target.HasTarget(info) {
					markTargetDown(v.Target, &v.Health, info)
				}
				return true
			})
		} else {
			// the target is healthy again: put it back into the rotation
			file.GetDb().JsonDb.Tasks.Range(func(key, value interface{}) bool {
				v := value.(*file.Tunnel)
				if v.Client.Id == id && v.Mode == "tcp" {
					markTargetUp(v.Target, &v.Health, info)
				}
				return true
			})
			file.GetDb().JsonDb.Hosts.Range(func(key, value interface{}) bool {
				v := value.(*file.Host)
				if v.Client.Id == id {
					markTargetUp(v.Target, &v.Health, info)
				}
				return true
			})
		}
	}
	if !node.CloseIfSignalCurrent(c) {
		// a newer signal connection has taken over the node
		_ = c.Close()
	}
	client.RemoveOfflineNodes(false)
}

// markTargetDown removes addr from the target rotation and records it in the
// health state. The target list is guarded by the Target itself and the
// removal record by the Health lock, so no lock is held across both.
func markTargetDown(t *file.Target, h *file.Health, addr string) {
	t.RemoveTarget(addr)
	h.Lock()
	if !common.IsArrContains(h.HealthRemoveArr, addr) {
		h.HealthRemoveArr = append(h.HealthRemoveArr, addr)
	}
	h.Unlock()
}

func markTargetUp(t *file.Target, h *file.Health, addr string) {
	t.RestoreTarget(addr)
	h.Lock()
	h.HealthRemoveArr = common.RemoveArrVal(h.HealthRemoveArr, addr)
	h.Unlock()
}

func (s *Bridge) ping() {
	ticker := time.NewTicker(time.Second * 5)
	defer ticker.Stop()

	for range ticker.C {
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
			if node == nil || node.IsOffline() {
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
}
