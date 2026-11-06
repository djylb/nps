package bridge

import (
	"encoding/binary"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/logs"
	"github.com/djylb/nps/server/tool"
)

// writeAddFail reports one host or task that could not be added, after which
// the client goes on with its next one: npc takes a failure as the end of the
// statuses of that task, so nothing more is written for its remaining ports.
// It returns false if the client is gone.
func writeAddFail(c *conn.Conn) bool {
	return c.WriteAddFail() == nil
}

func (s *Bridge) getConfig(c *conn.Conn, isPub bool, client *file.Client, ver int, vs, uuid string) {
	var fail bool
loop:
	for {
		flag, err := c.ReadFlag()
		if err != nil {
			break
		}

		switch flag {
		case common.WorkStatus:
			b, err := c.GetShortContent(64)
			if err != nil {
				break loop
			}

			id, err := file.GetDb().GetClientIdByBlake2bVkey(string(b))
			if err != nil {
				break loop
			}

			var strBuilder strings.Builder
			if client.IsConnect && !isPub {
				file.GetDb().JsonDb.Hosts.Range(func(key, value interface{}) bool {
					v := value.(*file.Host)
					if v.Client.Id == id {
						strBuilder.WriteString(v.Remark + common.ConnDataSeq)
					}
					return true
				})

				file.GetDb().JsonDb.Tasks.Range(func(key, value interface{}) bool {
					v := value.(*file.Tunnel)
					if _, ok := s.runList.Load(v.Id); ok && v.Client.Id == id {
						strBuilder.WriteString(v.Remark + common.ConnDataSeq)
					}
					return true
				})
			}
			str := strBuilder.String()
			_ = binary.Write(c, binary.LittleEndian, int32(len([]byte(str))))
			_ = binary.Write(c, binary.LittleEndian, []byte(str))
			break loop

		case common.FlagNewConf:
			client, err = c.GetConfigInfo()
			if err != nil {
				fail = true
				_ = c.WriteAddFail()
				break loop
			}
			// The server assigns the client id and verify key.
			client.Id = 0
			client.VerifyKey = ""

			if err = file.GetDb().NewClient(client); err != nil {
				fail = true
				_ = c.WriteAddFail()
				break loop
			}

			_ = c.WriteAddOk()
			_, _ = c.Write([]byte(client.VerifyKey))
			// The bridge client map owns the new client from here on.
			//noinspection GoResourceLeak
			s.storeClient(client.Id, NewClient(client.Id, NewNode(uuid, vs, ver)))

		case common.FlagNewHost:
			h, err := c.GetHostInfo()
			if err != nil || h.Target == nil {
				fail = true
				_ = c.WriteAddFail()
				break loop
			}

			h.Client = client
			if h.Location == "" {
				h.Location = "/"
			}

			if ver > routedVersion {
				s.joinNode(client.Id, uuid, NewNode(uuid, vs, ver), func(*Node) {})
			}
			hh, err := s.publishHost(h, client, uuid, ver)
			if err != nil {
				logs.Warn("Add host %s of client %d error: %v", h.Host, client.Id, err)
				if !writeAddFail(c) {
					fail = true
					break loop
				}
				continue
			}
			if hh != nil && !dispatch(s.OpenHost, hh) {
				logs.Warn("Open host %s of client %d timed out", hh.Host, client.Id)
				if !writeAddFail(c) {
					fail = true
					break loop
				}
				continue
			}
			if c.WriteAddOk() != nil {
				fail = true
				break loop
			}

		case common.FlagNewTask:
			t, err := c.GetTaskInfo()
			if err != nil {
				fail = true
				_ = c.WriteAddFail()
				break loop
			}

			ports, targets, ok := file.ConfigTaskPorts(t)
			if !ok {
				logs.Warn("Task %s of client %d has invalid ports", t.Remark, client.Id)
				if !writeAddFail(c) {
					fail = true
					break loop
				}
				continue
			}

		ports:
			for i := 0; i < len(ports); i++ {
				tl := &file.Tunnel{
					MaxConn:      t.MaxConn,
					RateLimits:   file.RateLimits{UploadLimit: t.UploadLimit, DownloadLimit: t.DownloadLimit},
					Mode:         t.Mode,
					Port:         ports[i],
					ServerIp:     t.ServerIp,
					Client:       client,
					Password:     t.Password,
					LocalPath:    t.LocalPath,
					StripPre:     t.StripPre,
					ReadOnly:     t.ReadOnly,
					Socks5Proxy:  t.Socks5Proxy,
					HttpProxy:    t.HttpProxy,
					TargetType:   t.TargetType,
					DestAclMode:  t.DestAclMode,
					DestAclRules: t.DestAclRules,
					MultiAccount: t.MultiAccount,
					Id:           int(file.GetDb().JsonDb.GetTaskId()),
					Status:       true,
					Flow:         new(file.Flow),
					NoStore:      true,
				}

				if len(ports) == 1 {
					tl.Target = t.Target
					tl.Target.LocalProxy = false
					tl.Remark = t.Remark
				} else {
					tl.Remark = fmt.Sprintf("%s_%d", t.Remark, tl.Port)
					if t.TargetAddr != "" {
						tl.Target = &file.Target{
							TargetStr: fmt.Sprintf("%s:%d", t.TargetAddr, targets[i]),
						}
					} else {
						tl.Target = &file.Target{
							TargetStr: strconv.Itoa(targets[i]),
						}
					}
					if t.Target != nil {
						tl.Target.ProxyProtocol = t.Target.ProxyProtocol
					}
				}
				if tl.MultiAccount == nil {
					tl.MultiAccount = new(file.MultiAccount)
				}
				if ver > routedVersion {
					s.joinNode(client.Id, uuid, NewNode(uuid, vs, ver), func(*Node) {})
				}
				if tl.Mode == "file" {
					cli := NewClient(client.Id, NewNode(uuid, vs, ver))
					if clientValue, ok := s.loadOrStoreClient(client.Id, cli); ok {
						cli, ok = clientValue.(*Client)
						if !ok {
							logs.Error("Fail to load client %d", client.Id)
							fail = true
							_ = c.WriteAddFail()
							break loop
						}
					}
					key := crypt.GenerateUUID(client.VerifyKey, tl.Mode, tl.ServerIp, strconv.Itoa(tl.Port), tl.LocalPath, tl.StripPre, strconv.FormatBool(tl.ReadOnly), tl.MultiAccount.Content)
					err = cli.AddFile(key.String(), uuid)
					if err != nil {
						logs.Error("Add file failed, error %v", err)
					}
					tl.Target.TargetStr = fmt.Sprintf("file://%s", key.String())
				}

				tt, created, err := s.publishTask(tl, client, uuid, ver)
				if err != nil {
					logs.Warn("Add task %s of client %d error: %v", tl.Remark, client.Id, err)
					if !writeAddFail(c) {
						fail = true
						break loop
					}
					break ports
				}
				if tt != nil && !dispatch(s.OpenTask, tt) {
					logs.Warn("Open task %s of client %d timed out", tt.Remark, client.Id)
					if created {
						_ = file.GetDb().DelTask(tt.Id)
					}
					if !writeAddFail(c) {
						fail = true
						break loop
					}
					break ports
				}
				if c.WriteAddOk() != nil {
					fail = true
					break loop
				}
			}
		}
	}

	if fail && client != nil {
		s.dropConfigNode(client.Id, uuid, ver)
	}
	_ = c.Close()
}

// routedVersion is the last protocol index without a UUID that stays the same
// across the connections of an npc. Entries of such clients have no routes:
// any node serves them, and they go when the client does.
const routedVersion = 5

// dropConfigNode ends the node whose configuration failed. The other
// instances on the same key stay; the last one takes the client with it.
func (s *Bridge) dropConfigNode(id int, uuid string, ver int) {
	if v, ok := s.Client.Load(id); ok && ver > routedVersion {
		client := v.(*Client)
		if node, ok := client.GetNodeByUUID(uuid); ok && client.NodeCount() > 1 {
			client.closeAndRemoveNodeIfCurrent(uuid, node, func(*Node) bool { return true })
			return
		}
	}
	s.DelClient(id)
}

func (s *Bridge) newRoutes(clientId int) *file.RouteSet {
	return file.NewRouteSet(func(node string) bool {
		v, ok := s.Client.Load(clientId)
		return ok && v.(*Client).usableNode(node)
	})
}

// publishHost adds h as uuid's route of its domain, creating the entry if it
// is new. It returns the existing entry that was changed, if any.
func (s *Bridge) publishHost(h *file.Host, client *file.Client, uuid string, ver int) (*file.Host, error) {
	s.routeMu.Lock()
	defer s.routeMu.Unlock()
	hh, ok := client.HasHost(h)
	if !ok {
		if file.GetDb().IsHostExist(h) {
			return nil, errors.New("the host already exists")
		}
		h.Routes = s.newRoutes(client.Id)
		if ver > routedVersion {
			h.Routes.Publish(uuid, h.Target)
		}
		return nil, file.GetDb().NewHost(h)
	}
	if !hh.NoStore {
		return nil, nil
	}
	if ver > routedVersion {
		hh.Routes.Publish(uuid, h.Target)
	}
	hh.Update(h)
	return hh, nil
}

// publishTask adds tl as uuid's route of its task, creating the entry if it
// is new. It returns the entry whose service has to be restarted, if any, and
// whether it was created. Another instance joining a running entry does not
// restart it, which would cut the traffic of the instances already there.
func (s *Bridge) publishTask(tl *file.Tunnel, client *file.Client, uuid string, ver int) (*file.Tunnel, bool, error) {
	s.routeMu.Lock()
	defer s.routeMu.Unlock()
	tt, ok := client.HasTunnel(tl)
	if !ok {
		if b := tool.TestTunnelPort(tl); !b && tl.Mode != "secret" && tl.Mode != "p2p" && tl.Port > 0 {
			return nil, false, fmt.Errorf("port %d is not available", tl.Port)
		}
		tl.Routes = s.newRoutes(client.Id)
		if ver > routedVersion {
			tl.Routes.Publish(uuid, tl.Target)
		}
		if err := file.GetDb().NewTask(tl); err != nil {
			return nil, false, err
		}
		return tl, true, nil
	}
	if !tt.NoStore {
		return nil, false, nil
	}
	tl.Id = tt.Id
	if err := file.GetDb().ReserveTaskPassword(tl); err != nil {
		return nil, false, err
	}
	joining := tt.Routes.HasOther(uuid) && tt.Mode == tl.Mode && tt.ServerIp == tl.ServerIp
	if ver > routedVersion {
		tt.Routes.Publish(uuid, tl.Target)
	}
	tt.Update(tl)
	if joining {
		return nil, false, nil
	}
	return tt, false, nil
}

// PruneRoutes removes the routes of nodes that left their client, and with
// the last route of an entry the entry, through delTask or delHost.
func (s *Bridge) PruneRoutes(delTask, delHost func(id int)) {
	s.routeMu.Lock()
	defer s.routeMu.Unlock()
	var tasks, hosts []int
	file.GetDb().JsonDb.Tasks.Range(func(_, value any) bool {
		v := value.(*file.Tunnel)
		if v.NoStore && s.pruneRoutes(v.Client.Id, v.Routes) {
			if v.Routes.Len() == 0 {
				tasks = append(tasks, v.Id)
			} else {
				v.Lock()
				v.Target = v.Routes.Holding(v.Target)
				v.Unlock()
			}
		}
		return true
	})
	file.GetDb().JsonDb.Hosts.Range(func(_, value any) bool {
		v := value.(*file.Host)
		if v.NoStore && s.pruneRoutes(v.Client.Id, v.Routes) {
			if v.Routes.Len() == 0 {
				hosts = append(hosts, v.Id)
			} else {
				v.Lock()
				v.Target = v.Routes.Holding(v.Target)
				v.Unlock()
			}
		}
		return true
	})
	for _, id := range tasks {
		delTask(id)
	}
	for _, id := range hosts {
		delHost(id)
	}
}

func (s *Bridge) pruneRoutes(clientId int, routes *file.RouteSet) bool {
	if routes.Len() == 0 {
		return false
	}
	v, ok := s.Client.Load(clientId)
	return routes.Retain(func(node string) bool {
		if !ok {
			return false
		}
		_, found := v.(*Client).GetNodeByUUID(node)
		return found
	})
}
