package bridge

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/djylb/nps/lib/model"

	"github.com/djylb/nps/bridge/legacy"
	"github.com/djylb/nps/lib/conn"
)

// writeAddFail reports one host or task that could not be added, after which
// the client goes on with its next one: npc takes a failure as the end of the
// statuses of that task, so nothing more is written for its remaining ports.
// It returns false if the client is gone.
func writeAddFail(c *conn.Conn) bool {
	return legacy.WriteStatus(c, false) == nil
}

func (s *Bridge) getConfig(c *conn.Conn, isPub bool, client *model.Client, caps legacy.Caps, uuid string) {
	var fail bool
	var published *Node   // the node that awaits its tunnel after this exchange
	var accepted []string // routes of the tunnels and hosts taken from the npc
	join := func(id int) {
		_, published = s.joinNode(id, uuid, newNode(uuid, caps), func(*Node) {})
		published.awaitTunnel()
	}
loop:
	for {
		flag, err := legacy.ReadFlag(c)
		if err != nil {
			break
		}

		switch flag {
		case legacy.WorkStatus:
			b, err := legacy.ReadExact(c, 64)
			if err != nil {
				break loop
			}

			id, err := s.store.GetClientIdByBlake2bVkey(string(b))
			if err != nil {
				break loop
			}

			var remarks []string
			if client.IsConnect && !isPub {
				s.store.RangeHosts(func(v *model.Host) bool {
					if v.Client.Id == id {
						remarks = append(remarks, v.Remark)
					}
					return true
				})

				s.store.RangeTunnels(func(v *model.Tunnel) bool {
					if _, ok := s.runList.Load(v.Id); ok && v.Client.Id == id {
						remarks = append(remarks, v.Remark)
					}
					return true
				})
			}
			_ = legacy.WriteStatusList(c, remarks)
			break loop

		case legacy.FlagNewConf:
			client, err = legacy.ReadClient(c)
			if err != nil {
				fail = true
				_ = legacy.WriteStatus(c, false)
				break loop
			}
			// The server assigns the client id and verify key.
			client.Id = 0
			client.VerifyKey = ""

			if err = s.store.NewClient(client); err != nil {
				fail = true
				_ = legacy.WriteStatus(c, false)
				break loop
			}

			_ = legacy.WriteStatus(c, true)
			_, _ = c.Write([]byte(client.VerifyKey))
			// The bridge client map owns the new client from here on.
			//noinspection GoResourceLeak
			node := newNode(uuid, caps)
			s.storeClient(client.Id, NewClient(client.Id, node, s.selectMode))
			if caps.NodeID {
				published = node
				published.awaitTunnel()
			}

		case legacy.FlagNewHost:
			h, err := legacy.ReadHost(c)
			if err == nil {
				h.Id = s.store.NextHostID()
			}
			if err != nil || h.Target == nil {
				fail = true
				_ = legacy.WriteStatus(c, false)
				break loop
			}

			h.Client = client
			if h.Location == "" {
				h.Location = "/"
			}

			if caps.NodeID {
				join(client.Id)
			}
			hh, err := s.publishHost(h, client, uuid, caps)
			if err != nil {
				log.Info("host from npc config refused", "client", client.Id, "domain", h.Host, "err", err)
				if !writeAddFail(c) {
					fail = true
					break loop
				}
				continue
			}
			if hh != nil && !dispatch(s.OpenHost, hh) {
				log.Warn("opening host timed out", "client", client.Id, "host", hh.Id, "domain", hh.Host)
				if !writeAddFail(c) {
					fail = true
					break loop
				}
				continue
			}
			if legacy.WriteStatus(c, true) != nil {
				fail = true
				break loop
			}
			accepted = append(accepted, h.Host)

		case legacy.FlagNewTask:
			t, err := legacy.ReadTask(c)
			if err == nil {
				t.Id = s.store.NextTunnelID()
			}
			if err != nil {
				fail = true
				_ = legacy.WriteStatus(c, false)
				break loop
			}

			ports, targets, ok := model.ConfigTaskPorts(t)
			if !ok {
				log.Info("tunnel from npc config refused", "client", client.Id, "remark", t.Remark, "reason", "invalid_ports")
				if !writeAddFail(c) {
					fail = true
					break loop
				}
				continue
			}

		ports:
			for i := range ports {
				tl := configPortTask(t, client, s.store.NextTunnelID(), ports, targets, i)
				if caps.NodeID {
					join(client.Id)
				}
				if tl.Mode == "file" {
					cli, _ := s.joinNode(client.Id, uuid, newNode(uuid, caps), func(*Node) {})
					key := legacy.FileKey(client.VerifyKey, tl)
					err = cli.AddFile(key, uuid)
					if err != nil {
						log.Warn("adding file tunnel failed", "client", client.Id, "err", err)
					}
					tl.Target.TargetStr = fmt.Sprintf("file://%s", key)
				}

				tt, created, err := s.publishTask(tl, client, uuid, caps)
				if err != nil {
					log.Info("tunnel from npc config refused", "client", client.Id, "remark", tl.Remark, "err", err)
					if !writeAddFail(c) {
						fail = true
						break loop
					}
					break ports
				}
				if tt != nil && !dispatch(s.OpenTask, tt) {
					log.Warn("opening tunnel timed out", "client", client.Id, "tunnel", tt.Id)
					if created {
						_ = s.store.DelTask(tt.Id)
					}
					if !writeAddFail(c) {
						fail = true
						break loop
					}
					break ports
				}
				if legacy.WriteStatus(c, true) != nil {
					fail = true
					break loop
				}
				if tt != nil {
					accepted = append(accepted, tt.LogRoute())
				} else {
					accepted = append(accepted, tl.LogRoute())
				}
			}
		}
	}
	if client != nil && len(accepted) > 0 {
		log.Info("npc config accepted", "client", client.Id, "count", len(accepted), "routes", strings.Join(accepted, ","))
	}

	if fail && client != nil {
		s.dropConfigNode(client.Id, uuid, caps)
	} else if published != nil {
		published.awaitTunnel() // the npc dials from here on
	}
	_ = c.Close()
}

// configPortTask returns the entry of the i-th port of t, a task a client
// sent, as the server stores it under id.
// The client's settings are carried over as they are, except for what the
// server decides: the state of the entry and the settings only the server's
// users give (access lists, user accounts, health checks).
func configPortTask(t *model.Tunnel, client *model.Client, id int, ports, targets []int, i int) *model.Tunnel {
	tl := t.Clone()
	tl.Id, tl.Port, tl.Client, tl.Status, tl.NoStore = id, ports[i], client, true, true
	tl.Flow = new(model.Flow)
	tl.RunStatus, tl.NowConn, tl.IsHttp, tl.Routes = false, 0, false, nil
	tl.Ports, tl.TargetAddr = "", "" // what the ports and targets of the entry were made of
	tl.CopySourceLists(new(model.SourceLists))
	tl.UserAuth, tl.HealthConfig = nil, nil
	tl.Compress = model.CompressDefault // the server decides compression (§7)

	if len(ports) == 1 {
		tl.Target = t.Target
		tl.Target.LocalProxy = false
		tl.Remark = t.Remark
	} else {
		tl.Remark = fmt.Sprintf("%s_%d", t.Remark, tl.Port)
		if t.TargetAddr != "" {
			tl.Target = &model.Target{
				TargetStr: fmt.Sprintf("%s:%d", t.TargetAddr, targets[i]),
			}
		} else {
			tl.Target = &model.Target{
				TargetStr: strconv.Itoa(targets[i]),
			}
		}
		if t.Target != nil {
			tl.Target.ProxyProtocol = t.Target.ProxyProtocol
		}
	}
	if tl.MultiAccount == nil {
		tl.MultiAccount = new(model.MultiAccount)
	}
	return tl
}

// dropConfigNode ends the node whose configuration failed. The other
// instances on the same key stay; the last one takes the client with it.
func (s *Bridge) dropConfigNode(id int, uuid string, caps legacy.Caps) {
	if v, ok := s.Client.Load(id); ok && caps.NodeID {
		client := v.(*Client)
		if node, ok := client.GetNodeByUUID(uuid); ok && client.NodeCount() > 1 {
			client.closeAndRemoveNodeIfCurrent(uuid, node, func(*Node) bool { return true })
			return
		}
	}
	s.delClient(id, "closed")
}

func (s *Bridge) newRoutes(clientId int) *model.RouteSet {
	return model.NewRouteSet(func(node string) bool {
		v, ok := s.Client.Load(clientId)
		return ok && v.(*Client).usableNode(node)
	})
}

// publishHost adds h as uuid's route of its domain, creating the entry if it
// is new. It returns the existing entry that was changed, if any.
// Entries of clients without a stable node UUID have no routes: any node
// serves them, and they go when the client does.
func (s *Bridge) publishHost(h *model.Host, client *model.Client, uuid string, caps legacy.Caps) (*model.Host, error) {
	s.routeMu.Lock()
	defer s.routeMu.Unlock()
	s.cancelClose(client.Id)
	hh, ok := s.store.ClientHost(client, h)
	if !ok {
		if s.store.IsHostExist(h) {
			return nil, errors.New("the host already exists")
		}
		h.Routes = s.newRoutes(client.Id)
		if caps.NodeID {
			h.Routes.Publish(uuid, h.Target)
		}
		return nil, s.store.NewHost(h)
	}
	if !hh.NoStore {
		return nil, nil
	}
	if caps.NodeID {
		hh.Routes.Publish(uuid, h.Target)
	}
	hh.Update(h)
	s.store.HostChanged(hh)
	return hh, nil
}

// publishTask adds tl as uuid's route of its task, creating the entry if it
// is new. It returns the entry whose service has to be restarted, if any, and
// whether it was created. Another instance joining a running entry does not
// restart it, which would cut the traffic of the instances already there.
func (s *Bridge) publishTask(tl *model.Tunnel, client *model.Client, uuid string, caps legacy.Caps) (*model.Tunnel, bool, error) {
	s.routeMu.Lock()
	defer s.routeMu.Unlock()
	s.cancelClose(client.Id)
	tt, ok := s.store.ClientTunnel(client, tl)
	if !ok {
		if b := s.ports.TestTunnelPort(tl); !b && tl.Mode != "secret" && tl.Port > 0 {
			return nil, false, fmt.Errorf("port %d is not available", tl.Port)
		}
		tl.Routes = s.newRoutes(client.Id)
		if caps.NodeID {
			tl.Routes.Publish(uuid, tl.Target)
		}
		if err := s.store.NewTask(tl); err != nil {
			return nil, false, err
		}
		return tl, true, nil
	}
	if !tt.NoStore {
		return nil, false, nil
	}
	tl.Id = tt.Id
	if err := s.store.ReserveTaskPassword(tl); err != nil {
		return nil, false, err
	}
	joining := tt.Routes.HasOther(uuid) && tt.Mode == tl.Mode && tt.ServerIp == tl.ServerIp
	if caps.NodeID {
		tt.Routes.Publish(uuid, tl.Target)
	}
	tt.Update(tl)
	s.store.TunnelChanged(tt)
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
	s.store.RangeTunnels(func(v *model.Tunnel) bool {
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
	s.store.RangeHosts(func(v *model.Host) bool {
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

func (s *Bridge) pruneRoutes(clientId int, routes *model.RouteSet) bool {
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
