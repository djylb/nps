package controllers

import (
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/beego/beego"
	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/rate"
	"github.com/djylb/nps/server"
	"github.com/skip2/go-qrcode"
)

type ClientController struct {
	BaseController
}

func (s *ClientController) List() {
	if s.Ctx.Request.Method == "GET" {
		s.Data["menu"] = "client"
		s.SetInfo("client")
		s.display("client/list")
		return
	}
	start, length := s.GetAjaxParams()
	clientIdSession := s.GetSession("clientId")
	var clientId int
	if clientIdSession == nil {
		clientId = s.GetIntNoErr("clientId")
	} else {
		clientId = clientIdSession.(int)
	}
	list, cnt := server.GetClientList(start, length, s.getEscapeString("search"), s.getEscapeString("sort"), s.getEscapeString("order"), clientId)
	cmd := make(map[string]interface{})
	ip := s.Ctx.Request.Host
	bridgeType, bridgeAddr, bridgeIp, bridgePort := GetBestBridge(ip)
	cmd["ip"] = bridgeIp
	cmd["addr"] = bridgeAddr
	cmd["bridgeType"] = bridgeType
	cmd["bridgePort"], _ = strconv.Atoi(bridgePort)
	s.AjaxTable(list, cnt, cnt, cmd)
}

func (s *ClientController) Add() {
	if s.Ctx.Request.Method == "GET" {
		s.Data["menu"] = "client"
		s.SetInfo("add client")
		s.display()
	} else {
		id := int(file.GetDb().JsonDb.GetClientId())
		t := &file.Client{
			VerifyKey: s.getEscapeString("vkey"),
			Id:        id,
			Status:    true,
			Remark:    s.getEscapeString("remark"),
			Cnf: &file.Config{
				U:        s.getEscapeString("u"),
				P:        s.getEscapeString("p"),
				Compress: common.GetBoolByStr(s.getEscapeString("compress")),
				Crypt:    s.GetBoolNoErr("crypt"),
			},
			ConfigConnAllow: s.GetBoolNoErr("config_conn_allow"),
			RateLimit:       s.GetIntNoErr("rate_limit"),
			MaxConn:         s.GetIntNoErr("max_conn"),
			WebUserName:     s.getEscapeString("web_username"),
			WebPassword:     s.getEscapeString("web_password"),
			WebTotpSecret:   s.getEscapeString("web_totp_secret"),
			MaxTunnelNum:    s.GetIntNoErr("max_tunnel"),
			Flow: &file.Flow{
				ExportFlow: 0,
				InletFlow:  0,
				FlowLimit:  int64(s.GetIntNoErr("flow_limit")),
				TimeLimit:  common.GetTimeNoErrByStr(s.getEscapeString("time_limit")),
			},
			BlackIpList: RemoveRepeatedElement(strings.Split(s.getEscapeString("blackiplist"), "\r\n")),
			CreateTime:  time.Now().Format("2006-01-02 15:04:05"),
		}
		if err := file.GetDb().NewClient(t); err != nil {
			s.AjaxErr(err.Error())
		}
		s.AjaxOkWithId("add success", id)
	}
}

func (s *ClientController) PingClient() {
	id := s.GetIntNoErr("id")
	data := make(map[string]interface{})
	if _, err := file.GetDb().GetClient(id); err != nil {
		data["code"] = 0
	} else {
		data["code"] = 1
		data["rtt"] = server.PingClient(id, s.Ctx.Request.RemoteAddr)
	}
	s.Data["json"] = data
	s.ServeJSON()
}

func (s *ClientController) GetClient() {
	if s.Ctx.Request.Method == "POST" {
		id := s.GetIntNoErr("id")
		data := make(map[string]interface{})
		if c, err := file.GetDb().GetClient(id); err != nil {
			data["code"] = 0
		} else {
			data["code"] = 1
			data["data"] = c
		}
		s.Data["json"] = data
		s.ServeJSON()
	}
}

func (s *ClientController) Edit() {
	id := s.GetIntNoErr("id")
	if s.Ctx.Request.Method == "GET" {
		s.Data["menu"] = "client"
		if c, err := file.GetDb().GetClient(id); err != nil {
			s.error()
		} else {
			s.Data["c"] = c
			s.Data["BlackIpList"] = strings.Join(c.BlackIpList, "\r\n")
		}
		s.SetInfo("edit client")
		s.display()
	} else {
		if c, err := file.GetDb().GetClient(id); err != nil {
			s.error()
			s.AjaxErr("client ID not found")
			return
		} else {
			if s.getEscapeString("web_username") != "" {
				if s.getEscapeString("web_username") == beego.AppConfig.String("web_username") || !file.GetDb().VerifyUserName(s.getEscapeString("web_username"), c.Id) {
					s.AjaxErr("web login username duplicate, please reset")
					return
				}
			}
			if s.GetSession("isAdmin").(bool) {
				if !file.GetDb().VerifyVkey(s.getEscapeString("vkey"), c.Id) {
					s.AjaxErr("Vkey duplicate, please reset")
					return
				}
				file.Blake2bVkeyIndex.Remove(crypt.Blake2b(c.VerifyKey))
				c.VerifyKey = s.getEscapeString("vkey")
				file.Blake2bVkeyIndex.Add(crypt.Blake2b(c.VerifyKey), c.Id)
				c.Flow.FlowLimit = int64(s.GetIntNoErr("flow_limit"))
				c.Flow.TimeLimit = common.GetTimeNoErrByStr(s.getEscapeString("time_limit"))
				c.RateLimit = s.GetIntNoErr("rate_limit")
				c.MaxConn = s.GetIntNoErr("max_conn")
				c.MaxTunnelNum = s.GetIntNoErr("max_tunnel")
				if s.GetBoolNoErr("flow_reset") {
					c.Flow.ExportFlow = 0
					c.Flow.InletFlow = 0
				}
			}
			c.Remark = s.getEscapeString("remark")
			c.Cnf.U = s.getEscapeString("u")
			c.Cnf.P = s.getEscapeString("p")
			c.Cnf.Compress = common.GetBoolByStr(s.getEscapeString("compress"))
			c.Cnf.Crypt = s.GetBoolNoErr("crypt")
			b, err := beego.AppConfig.Bool("allow_user_change_username")
			if s.GetSession("isAdmin").(bool) || (err == nil && b) {
				c.WebUserName = s.getEscapeString("web_username")
			}
			c.WebPassword = s.getEscapeString("web_password")
			c.WebTotpSecret = s.getEscapeString("web_totp_secret")
			c.EnsureWebPassword()
			c.ConfigConnAllow = s.GetBoolNoErr("config_conn_allow")
			var limit int64
			if c.RateLimit > 0 {
				limit = int64(c.RateLimit) * 1024
			} else {
				limit = 0
			}
			if c.Rate == nil {
				c.Rate = rate.NewRate(limit)
				c.Rate.Start()
			} else {
				if c.Rate.Limit() != limit {
					c.Rate.SetLimit(limit)
				}
				c.Rate.Start()
			}

			c.BlackIpList = RemoveRepeatedElement(strings.Split(s.getEscapeString("blackiplist"), "\r\n"))
			file.GetDb().JsonDb.StoreClientsToJsonFile()
		}
		s.AjaxOk("save success")
	}
}

func RemoveRepeatedElement(arr []string) (newArr []string) {
	newArr = make([]string, 0)
	for i := 0; i < len(arr); i++ {
		repeat := false
		for j := i + 1; j < len(arr); j++ {
			if arr[i] == arr[j] {
				repeat = true
				break
			}
		}
		if !repeat {
			newArr = append(newArr, arr[i])
		}
	}
	return
}

func clearClientStatus(c *file.Client, name string) {
	switch name {
	case "flow":
		c.Flow.ExportFlow = 0
		c.Flow.InletFlow = 0
		c.ExportFlow = 0
		c.InletFlow = 0
		file.GetDb().JsonDb.Hosts.Range(func(key, value interface{}) bool {
			h := value.(*file.Host)
			if h.Client.Id == c.Id {
				h.Flow.InletFlow = 0
				h.Flow.ExportFlow = 0
			}
			return true
		})
		file.GetDb().JsonDb.Tasks.Range(func(key, value interface{}) bool {
			t := value.(*file.Tunnel)
			if t.Client.Id == c.Id {
				t.Flow.InletFlow = 0
				t.Flow.ExportFlow = 0
			}
			return true
		})
	case "flow_limit":
		c.Flow.FlowLimit = 0
	case "time_limit":
		c.Flow.TimeLimit = common.GetTimeNoErrByStr("")
	case "rate_limit":
		c.RateLimit = 0
	case "conn_limit":
		c.MaxConn = 0
	case "tunnel_limit":
		c.MaxTunnelNum = 0
	}
	var limit int64
	if c.RateLimit > 0 {
		limit = int64(c.RateLimit) * 1024
	} else {
		limit = 0
	}
	if c.Rate == nil {
		c.Rate = rate.NewRate(limit)
		c.Rate.Start()
	} else {
		if c.Rate.Limit() != limit {
			c.Rate.ResetLimit(limit)
		} else {
			c.Rate.Start()
		}
	}
	return
}

func clearStatus(id int, name string) (err error) {
	if id == 0 {
		file.GetDb().JsonDb.Clients.Range(func(key, value interface{}) bool {
			v := value.(*file.Client)
			clearClientStatus(v, name)
			return true
		})
		file.GetDb().JsonDb.StoreClientsToJsonFile()
		return
	}
	if c, err := file.GetDb().GetClient(id); err != nil {
		return err
	} else {
		clearClientStatus(c, name)
		file.GetDb().JsonDb.StoreClientsToJsonFile()
	}
	return
}

func (s *ClientController) Clear() {
	id := s.GetIntNoErr("id")
	if s.GetSession("isAdmin").(bool) {
		mode := s.getEscapeString("mode")
		if mode != "" {
			if err := clearStatus(id, mode); err != nil {
				s.AjaxErr("modified fail")
			}
			s.AjaxOk("modified success")
		}
	}
	s.AjaxErr("modified fail")
}

func (s *ClientController) ChangeStatus() {
	id := s.GetIntNoErr("id")
	if client, err := file.GetDb().GetClient(id); err == nil {
		client.Status = s.GetBoolNoErr("status")
		if client.Status == false {
			server.DelClientConnect(client.Id)
		}
		s.AjaxOk("modified success")
	}
	s.AjaxErr("modified fail")
}

func (s *ClientController) Del() {
	id := s.GetIntNoErr("id")
	if err := file.GetDb().DelClient(id); err != nil {
		s.AjaxErr("delete error")
	}
	server.DelTunnelAndHostByClientId(id, false)
	server.DelClientConnect(id)
	s.AjaxOk("delete success")
}

func (s *ClientController) Qr() {
	text := s.GetString("text")
	account := s.GetString("account")
	secret := s.GetString("secret")
	if text == "" && (account == "" || secret == "") {
		s.CustomAbort(400, "missing text")
		return
	}
	if text != "" {
		if decoded, err := url.QueryUnescape(text); err == nil {
			text = decoded
		}
	} else {
		issuer := beego.AppConfig.String("appname")
		text = crypt.BuildTotpUri(issuer, account, secret)
	}
	png, err := qrcode.Encode(text, qrcode.Medium, 256)
	if err != nil {
		s.CustomAbort(500, "QR encode failed")
		return
	}
	s.Ctx.Output.Header("Content-Type", "image/png")
	s.Ctx.Output.Body(png)
}
