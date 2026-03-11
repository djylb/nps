package bridge

import (
	"strings"
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
			logs.Trace("GetHealthInfo error, id=%d, retry=%d, err=%v", id, retry, err)
			break
		}
		//logs.Trace("GetHealthInfo: %v, %v, %v", info, err, status)
		//firstSuccess = true
		retry = 0

		if !status { //the status is true , return target to the targetArr
			file.GetDb().JsonDb.Tasks.Range(func(key, value interface{}) bool {
				v := value.(*file.Tunnel)
				if v.Client.Id == id && v.Mode == "tcp" && strings.Contains(v.Target.TargetStr, info) {
					v.Lock()
					if v.Target.TargetArr == nil || (len(v.Target.TargetArr) == 0 && len(v.HealthRemoveArr) == 0) {
						v.Target.TargetArr = common.TrimArr(strings.Split(strings.ReplaceAll(v.Target.TargetStr, "\r\n", "\n"), "\n"))
					}
					v.Target.TargetArr = common.RemoveArrVal(v.Target.TargetArr, info)
					if v.HealthRemoveArr == nil {
						v.HealthRemoveArr = make([]string, 0)
					}
					v.HealthRemoveArr = append(v.HealthRemoveArr, info)
					v.Unlock()
				}
				return true
			})
			file.GetDb().JsonDb.Hosts.Range(func(key, value interface{}) bool {
				v := value.(*file.Host)
				if v.Client.Id == id && strings.Contains(v.Target.TargetStr, info) {
					v.Lock()
					if v.Target.TargetArr == nil || (len(v.Target.TargetArr) == 0 && len(v.HealthRemoveArr) == 0) {
						v.Target.TargetArr = common.TrimArr(strings.Split(strings.ReplaceAll(v.Target.TargetStr, "\r\n", "\n"), "\n"))
					}
					v.Target.TargetArr = common.RemoveArrVal(v.Target.TargetArr, info)
					if v.HealthRemoveArr == nil {
						v.HealthRemoveArr = make([]string, 0)
					}
					v.HealthRemoveArr = append(v.HealthRemoveArr, info)
					v.Unlock()
				}
				return true
			})
		} else { //the status is false,remove target from the targetArr
			file.GetDb().JsonDb.Tasks.Range(func(key, value interface{}) bool {
				v := value.(*file.Tunnel)
				if v.Client.Id == id && v.Mode == "tcp" && common.IsArrContains(v.HealthRemoveArr, info) && !common.IsArrContains(v.Target.TargetArr, info) {
					v.Lock()
					v.Target.TargetArr = append(v.Target.TargetArr, info)
					v.HealthRemoveArr = common.RemoveArrVal(v.HealthRemoveArr, info)
					v.Unlock()
				}
				return true
			})

			file.GetDb().JsonDb.Hosts.Range(func(key, value interface{}) bool {
				v := value.(*file.Host)
				if v.Client.Id == id && common.IsArrContains(v.HealthRemoveArr, info) && !common.IsArrContains(v.Target.TargetArr, info) {
					v.Lock()
					v.Target.TargetArr = append(v.Target.TargetArr, info)
					v.HealthRemoveArr = common.RemoveArrVal(v.HealthRemoveArr, info)
					v.Unlock()
				}
				return true
			})
		}
	}
	//s.DelClient(id)
	//_ = c.Close()
	_ = node.Close()
	client.RemoveOfflineNodes(false)
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
