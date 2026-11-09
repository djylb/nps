package bridge

import (
	"errors"
	"fmt"
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

// hasNodeBesides reports whether client id has a connected node other than uuid.
func (s *Bridge) hasNodeBesides(id int, uuid string) bool {
	v, ok := s.Client.Load(id)
	if !ok {
		return false
	}
	for _, n := range v.(*Client).nodeList() {
		if n.UUID != uuid {
			return true
		}
	}
	return false
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
			// Whether an npc serves the key is the bridge's to say:
			// client.IsConnect is refreshed only when the web lists it.
			// The npc asking does not count: on the new protocol its own
			// session is a node of uuid.
			if !isPub && s.hasNodeBesides(id, uuid) {
				s.store.RangeHosts(func(v *model.Host) bool {
					if v.Client.Id == id {
						remarks = append(remarks, v.View().Remark)
					}
					return true
				})

				s.store.RangeTunnels(func(v *model.Tunnel) bool {
					if _, ok := s.runList.Load(v.Id); ok && v.Client.Id == id {
						remarks = append(remarks, v.View().Remark)
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
			// What arrives is the peer's: relays read client.Cnf.
			if client.Cnf == nil {
				client.Cnf = new(model.Config)
			}

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
			//noinspection GoResourceLeak
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

			if t.Mode == model.ModeService {
				log.Info("tunnel from npc config refused", "client", client.Id, "remark", t.Remark, "reason", "virtual_backend_listens_nowhere")
				if !writeAddFail(c) {
					fail = true
					break loop
				}
				continue
			}
			if t.Mode == "file" {
				log.Info("tunnel from npc config refused", "client", client.Id, "remark", t.Remark, "reason", "file_mode_unsupported")
				if !writeAddFail(c) {
					fail = true
					break loop
				}
				continue
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
				if s.generatedPortTaken(tl, client) {
					// The backend stays listed; a tunnel to it can be made by hand.
					log.Debug("generated tunnel not opened", "client", client.Id, "port", tl.Port, "reason", "port_in_use")
					if legacy.WriteStatus(c, true) != nil {
						fail = true
						break loop
					}
					continue
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
	tl.RunStatus, tl.NowConn, tl.IsHttp, tl.Routes, tl.Generated = false, 0, false, nil, false
	tl.ServiceKind, tl.ServiceName = "", ""
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
		tl.Target = &model.Target{TargetStr: model.PortTaskTarget(t.TargetAddr, targets[i])}
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
// generatedPortTaken reports whether tl, a tunnel an npc sent for one of its
// virtual backends, is new and its port is in use: such a tunnel is skipped,
// which is no failure.
func (s *Bridge) generatedPortTaken(tl *model.Tunnel, client *model.Client) bool {
	if _, ok := model.ParseServiceTarget(tl.Target.TargetStr); !ok || tl.Port <= 0 {
		return false
	}
	s.routeMu.Lock()
	defer s.routeMu.Unlock()
	if _, exists := s.store.ClientTunnel(client, tl); exists {
		return false
	}
	return !s.ports.TestTunnelPort(tl)
}

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
	cur := tt.View()
	joining := tt.Routes.HasOther(uuid) && cur.Mode == tl.Mode && cur.ServerIp == tl.ServerIp
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
