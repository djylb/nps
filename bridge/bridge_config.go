package bridge

import (
	"encoding/binary"
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

func (s *Bridge) getConfig(c *conn.Conn, isPub bool, client *file.Client, ver int, vs, uuid string) {
	var fail bool
loop:
	for {
		flag, err := c.ReadFlag()
		if err != nil {
			break
		}

		switch flag {
		case common.WORK_STATUS:
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
						strBuilder.WriteString(v.Remark + common.CONN_DATA_SEQ)
					}
					return true
				})

				file.GetDb().JsonDb.Tasks.Range(func(key, value interface{}) bool {
					v := value.(*file.Tunnel)
					if _, ok := s.runList.Load(v.Id); ok && v.Client.Id == id {
						strBuilder.WriteString(v.Remark + common.CONN_DATA_SEQ)
					}
					return true
				})
			}
			str := strBuilder.String()
			_ = binary.Write(c, binary.LittleEndian, int32(len([]byte(str))))
			_ = binary.Write(c, binary.LittleEndian, []byte(str))
			break loop

		case common.NEW_CONF:
			client, err = c.GetConfigInfo()
			if err != nil {
				fail = true
				_ = c.WriteAddFail()
				break loop
			}

			if err = file.GetDb().NewClient(client); err != nil {
				fail = true
				_ = c.WriteAddFail()
				break loop
			}

			_ = c.WriteAddOk()
			_, _ = c.Write([]byte(client.VerifyKey))
			s.Client.Store(client.Id, NewClient(client.Id, NewNode(uuid, vs, ver)))

		case common.NEW_HOST:
			h, err := c.GetHostInfo()
			if err != nil {
				fail = true
				_ = c.WriteAddFail()
				break loop
			}

			h.Client = client
			if h.Location == "" {
				h.Location = "/"
			}

			hh, ok := client.HasHost(h)
			if !ok {
				if file.GetDb().IsHostExist(h) {
					fail = true
					_ = c.WriteAddFail()
					break loop
				}
				_ = file.GetDb().NewHost(h)
			} else {
				if hh.NoStore {
					hh.Update(h)
					s.OpenHost <- hh
				}
			}
			_ = c.WriteAddOk()

		case common.NEW_TASK:
			t, err := c.GetTaskInfo()
			if err != nil {
				fail = true
				_ = c.WriteAddFail()
				break loop
			}

			ports := common.GetPorts(t.Ports)
			targets := common.GetPorts(t.Target.TargetStr)
			if len(ports) > 1 && (t.Mode == "tcp" || t.Mode == "udp") && (len(ports) != len(targets)) {
				fail = true
				_ = c.WriteAddFail()
				break loop
			} else if t.Mode == "secret" || t.Mode == "p2p" {
				ports = append(ports, 0)
			}
			if t.Mode == "file" && len(ports) == 0 {
				ports = append(ports, 0)
			}

			if len(ports) == 0 {
				fail = true
				_ = c.WriteAddFail()
				break loop
			}

			for i := 0; i < len(ports); i++ {
				tl := &file.Tunnel{
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
				if tl.Mode == "file" {
					cli := NewClient(client.Id, NewNode(uuid, vs, ver))
					if clientValue, ok := s.Client.LoadOrStore(client.Id, cli); ok {
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

				tt, ok := client.HasTunnel(tl)
				if !ok {
					if err := file.GetDb().NewTask(tl); err != nil {
						logs.Warn("Add task error: %v", err)
						fail = true
						_ = c.WriteAddFail()
						break loop
					}

					if b := tool.TestServerPort(tl.Port, tl.Mode); !b && t.Mode != "secret" && t.Mode != "p2p" && tl.Port > 0 {
						fail = true
						_ = c.WriteAddFail()
						break loop
					}

					s.OpenTask <- tl
				} else {
					if tt.NoStore {
						tt.Update(tl)
						s.OpenTask <- tt
					}
				}
				_ = c.WriteAddOk()
			}
		}
	}

	if fail && client != nil {
		s.DelClient(client.Id)
	}
	_ = c.Close()
}

func (s *Bridge) IsServer() bool {
	return true
}
