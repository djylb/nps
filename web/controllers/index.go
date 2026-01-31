package controllers

import (
	"fmt"
	"html/template"
	"strings"

	"github.com/beego/beego"
	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/server"
	"github.com/djylb/nps/server/tool"
)

type IndexController struct {
	BaseController
}

func (s *IndexController) Index() {
	s.Data["web_base_url"] = beego.AppConfig.String("web_base_url")
	s.Data["head_custom_code"] = template.HTML(beego.AppConfig.String("head_custom_code"))
	s.Data["data"] = server.GetDashboardData(true)
	s.SetInfo("dashboard")
	s.display("index/index")
}

func (s *IndexController) Stats() {
	data := make(map[string]interface{})
	data["code"] = 0
	if isAdmin, ok := s.GetSession("isAdmin").(bool); ok && isAdmin {
		data["code"] = 1
		data["data"] = server.GetDashboardData(false)
	}
	s.Data["json"] = data
	s.ServeJSON()
}

func (s *IndexController) Help() {
	s.SetInfo("about")
	s.display("index/help")
}

func (s *IndexController) Tcp() {
	s.SetInfo("tcp")
	s.SetType("tcp")
	s.display("index/list")
}

func (s *IndexController) Udp() {
	s.SetInfo("udp")
	s.SetType("udp")
	s.display("index/list")
}

func (s *IndexController) Socks5() {
	s.SetInfo("socks5")
	s.SetType("socks5")
	s.display("index/list")
}

func (s *IndexController) Http() {
	s.SetInfo("http proxy")
	s.SetType("httpProxy")
	s.display("index/list")
}

func (s *IndexController) Mix() {
	s.SetInfo("mix proxy")
	s.SetType("mixProxy")
	s.display("index/list")
}

func (s *IndexController) File() {
	s.SetInfo("file server")
	s.SetType("file")
	s.display("index/list")
}

func (s *IndexController) Secret() {
	s.SetInfo("secret")
	s.SetType("secret")
	s.display("index/list")
}
func (s *IndexController) P2p() {
	s.SetInfo("p2p")
	s.SetType("p2p")
	s.display("index/list")
}

func (s *IndexController) Host() {
	s.SetInfo("host")
	s.SetType("hostServer")
	s.display("index/list")
}

func (s *IndexController) All() {
	s.Data["menu"] = "client"
	clientId := s.getEscapeString("client_id")
	s.Data["client_id"] = clientId
	s.SetInfo("client id:" + clientId)
	s.display("index/list")
}

func (s *IndexController) GetTunnel() {
	start, length := s.GetAjaxParams()
	taskType := s.getEscapeString("type")
	clientId := s.GetIntNoErr("client_id")
	list, cnt := server.GetTunnel(start, length, taskType, clientId, s.getEscapeString("search"), s.getEscapeString("sort"), s.getEscapeString("order"))
	s.AjaxTable(list, cnt, cnt, nil)
}

func (s *IndexController) Add() {
	if s.Ctx.Request.Method == "GET" {
		s.Data["type"] = s.getEscapeString("type")
		s.Data["client_id"] = s.getEscapeString("client_id")
		s.SetInfo("add tunnel")
		s.display()
		return
	}

	id := int(file.GetDb().JsonDb.GetTaskId())
	clientId := s.GetIntNoErr("client_id")
	isAdmin := s.GetSession("isAdmin").(bool)
	allowLocal := beego.AppConfig.DefaultBool("allow_user_local", beego.AppConfig.DefaultBool("allow_local_proxy", false)) || isAdmin

	targetStr := strings.ToLower(strings.TrimSpace(strings.ReplaceAll(s.getEscapeString("target"), "\r\n", "\n")))
	if !isAdmin && strings.Contains(targetStr, "bridge://") {
		targetStr = ""
	}

	destMode := s.GetIntNoErr("dest_acl_mode")
	if destMode != file.AclOff && destMode != file.AclWhitelist && destMode != file.AclBlacklist {
		destMode = file.AclOff
	}
	destRules := strings.ToLower(strings.TrimSpace(strings.ReplaceAll(s.getEscapeString("dest_acl_rules"), "\r\n", "\n")))

	t := &file.Tunnel{
		Port:       s.GetIntNoErr("port"),
		ServerIp:   s.getEscapeString("server_ip"),
		Mode:       s.getEscapeString("type"),
		TargetType: s.getEscapeString("target_type"),
		Target: &file.Target{
			TargetStr:     targetStr,
			ProxyProtocol: s.GetIntNoErr("proxy_protocol"),
			LocalProxy:    (clientId > 0 && s.GetBoolNoErr("local_proxy") && allowLocal) || clientId <= 0,
		},
		UserAuth: &file.MultiAccount{
			Content:    s.getEscapeString("auth"),
			AccountMap: common.DealMultiUser(s.getEscapeString("auth")),
		},
		Id:           id,
		Status:       true,
		Remark:       s.getEscapeString("remark"),
		Password:     s.getEscapeString("password"),
		LocalPath:    s.getEscapeString("local_path"),
		StripPre:     s.getEscapeString("strip_pre"),
		HttpProxy:    s.GetBoolNoErr("enable_http"),
		Socks5Proxy:  s.GetBoolNoErr("enable_socks5"),
		DestAclMode:  destMode,
		DestAclRules: destRules,
		Flow: &file.Flow{
			FlowLimit: int64(s.GetIntNoErr("flow_limit")),
			TimeLimit: common.GetTimeNoErrByStr(s.getEscapeString("time_limit")),
		},
	}

	if t.Port <= 0 {
		t.Port = tool.GenerateServerPort(t.Mode)
	}
	if !tool.TestServerPort(t.Port, t.Mode) {
		s.AjaxErr("The port cannot be opened because it may has been occupied or is no longer allowed.")
		return
	}

	var err error
	if t.Client, err = file.GetDb().GetClient(clientId); err != nil {
		s.AjaxErr(err.Error())
	}
	if t.Client.MaxTunnelNum != 0 && t.Client.GetTunnelNum() >= t.Client.MaxTunnelNum {
		s.AjaxErr("The number of tunnels exceeds the limit")
		return
	}

	if err := file.GetDb().NewTask(t); err != nil {
		s.AjaxErr(err.Error())
		return
	}
	if err := server.AddTask(t); err != nil {
		s.AjaxErr(err.Error())
		return
	}

	s.AjaxOkWithId("add success", id)
}

func (s *IndexController) GetOneTunnel() {
	id := s.GetIntNoErr("id")
	data := make(map[string]interface{})
	if t, err := file.GetDb().GetTask(id); err != nil {
		data["code"] = 0
	} else {
		data["code"] = 1
		data["data"] = t
	}
	s.Data["json"] = data
	s.ServeJSON()
}

func (s *IndexController) Edit() {
	id := s.GetIntNoErr("id")
	if s.Ctx.Request.Method == "GET" {
		if t, err := file.GetDb().GetTask(id); err != nil {
			s.error()
			return
		} else {
			s.Data["t"] = t
			if t.UserAuth == nil {
				s.Data["auth"] = ""
			} else {
				s.Data["auth"] = t.UserAuth.Content
			}
		}
		s.SetInfo("edit tunnel")
		s.display()
		return
	}

	t, err := file.GetDb().GetTask(id)
	if err != nil {
		s.error()
		return
	}

	clientId := s.GetIntNoErr("client_id")
	if client, err := file.GetDb().GetClient(clientId); err != nil {
		s.AjaxErr("modified error,the client is not exist")
		return
	} else {
		t.Client = client
	}

	if s.GetIntNoErr("port") != t.Port {
		t.Port = s.GetIntNoErr("port")
		if t.Port <= 0 {
			t.Port = tool.GenerateServerPort(t.Mode)
		}
		if !tool.TestServerPort(s.GetIntNoErr("port"), t.Mode) {
			s.AjaxErr("The port cannot be opened because it may has been occupied or is no longer allowed.")
			return
		}
	}

	isAdmin := s.GetSession("isAdmin").(bool)
	allowLocal := beego.AppConfig.DefaultBool("allow_user_local", beego.AppConfig.DefaultBool("allow_local_proxy", false)) || isAdmin

	targetStr := strings.ToLower(strings.TrimSpace(strings.ReplaceAll(s.getEscapeString("target"), "\r\n", "\n")))
	if !isAdmin && strings.Contains(targetStr, "bridge://") {
		if t.Target != nil {
			targetStr = t.Target.TargetStr
		} else {
			targetStr = ""
		}
	}

	destMode := s.GetIntNoErr("dest_acl_mode")
	if destMode != file.AclOff && destMode != file.AclWhitelist && destMode != file.AclBlacklist {
		destMode = file.AclOff
	}
	destRules := strings.ToLower(strings.TrimSpace(strings.ReplaceAll(s.getEscapeString("dest_acl_rules"), "\r\n", "\n")))

	t.ServerIp = s.getEscapeString("server_ip")
	t.Mode = s.getEscapeString("type")
	t.TargetType = s.getEscapeString("target_type")
	t.Target = &file.Target{TargetStr: targetStr}
	t.UserAuth = &file.MultiAccount{Content: s.getEscapeString("auth"), AccountMap: common.DealMultiUser(s.getEscapeString("auth"))}
	t.Id = id
	t.Password = s.getEscapeString("password")
	t.LocalPath = s.getEscapeString("local_path")
	t.StripPre = s.getEscapeString("strip_pre")
	t.HttpProxy = s.GetBoolNoErr("enable_http")
	t.Socks5Proxy = s.GetBoolNoErr("enable_socks5")
	t.DestAclMode = destMode
	t.DestAclRules = destRules
	t.Remark = s.getEscapeString("remark")
	t.Flow.FlowLimit = int64(s.GetIntNoErr("flow_limit"))
	t.Flow.TimeLimit = common.GetTimeNoErrByStr(s.getEscapeString("time_limit"))
	if s.GetBoolNoErr("flow_reset") {
		t.Flow.ExportFlow = 0
		t.Flow.InletFlow = 0
	}
	t.Target.ProxyProtocol = s.GetIntNoErr("proxy_protocol")
	t.Target.LocalProxy = (clientId > 0 && s.GetBoolNoErr("local_proxy") && allowLocal) || clientId <= 0
	_ = file.GetDb().UpdateTask(t)
	_ = server.StopServer(t.Id)
	_ = server.StartTask(t.Id)

	s.AjaxOk("modified success")
}

func (s *IndexController) Stop() {
	id := s.GetIntNoErr("id")
	mode := s.getEscapeString("mode")
	if mode != "" {
		if err := changeStatus(id, mode, "stop"); err != nil {
			s.AjaxErr("stop error")
		}
		s.AjaxOk("stop success")
	}
	if err := server.StopServer(id); err != nil && err.Error() != "task is not running" {
		s.AjaxErr("stop error")
	}
	s.AjaxOk("stop success")
}

func (s *IndexController) Del() {
	id := s.GetIntNoErr("id")
	if err := server.DelTask(id); err != nil {
		s.AjaxErr("delete error")
	}
	s.AjaxOk("delete success")
}

func (s *IndexController) Start() {
	id := s.GetIntNoErr("id")
	mode := s.getEscapeString("mode")
	if mode != "" {
		if err := changeStatus(id, mode, "start"); err != nil {
			s.AjaxErr("start error")
		}
		s.AjaxOk("start success")
	}
	if err := server.StartTask(id); err != nil {
		if err.Error() == "the port open error" {
			s.AjaxErr("The port cannot be opened because it may has been occupied or is no longer allowed.")
		}
		s.AjaxErr("start error")
	}
	s.AjaxOk("start success")
}

func (s *IndexController) Clear() {
	id := s.GetIntNoErr("id")
	mode := s.getEscapeString("mode")
	if mode != "" {
		if err := changeStatus(id, mode, "clear"); err != nil {
			s.AjaxErr("modified fail")
		}
		s.AjaxOk("modified success")
	}
	s.AjaxErr("modified fail")
}

func changeStatus(id int, name, action string) error {
	t, err := file.GetDb().GetTask(id)
	if err != nil {
		return err
	}
	a := strings.ToLower(strings.TrimSpace(action))
	switch name {
	case "http":
		if err := applyBoolAction(&t.HttpProxy, a); err != nil {
			return err
		}
	case "socks5":
		if err := applyBoolAction(&t.Socks5Proxy, a); err != nil {
			return err
		}
	case "flow":
		if a != "clear" {
			return fmt.Errorf("unsupported action %q for %s", a, name)
		}
		t.Flow.ExportFlow = 0
		t.Flow.InletFlow = 0
	case "flow_limit":
		if a != "clear" {
			return fmt.Errorf("unsupported action %q for %s", a, name)
		}
		t.Flow.FlowLimit = 0
	case "time_limit":
		if a != "clear" {
			return fmt.Errorf("unsupported action %q for %s", a, name)
		}
		t.Flow.TimeLimit = common.GetTimeNoErrByStr("")
	default:
		return fmt.Errorf("unknown name: %q", name)
	}
	_ = file.GetDb().UpdateTask(t)
	//server.StopServer(t.Id)
	//server.StartTask(t.Id)
	return nil
}

func (s *IndexController) HostList() {
	if s.Ctx.Request.Method == "GET" {
		s.Data["httpProxyPort"] = beego.AppConfig.String("http_proxy_port")
		s.Data["httpsProxyPort"] = beego.AppConfig.String("https_proxy_port")
		s.Data["client_id"] = s.getEscapeString("client_id")
		s.Data["menu"] = "host"
		s.SetInfo("host list")
		s.display("index/hlist")
	} else {
		start, length := s.GetAjaxParams()
		clientId := s.GetIntNoErr("client_id")
		//list, cnt := file.GetDb().GetHost(start, length, clientId, s.getEscapeString("search"))
		list, cnt := server.GetHostList(start, length, clientId, s.getEscapeString("search"), s.getEscapeString("sort"), s.getEscapeString("order"))
		s.AjaxTable(list, cnt, cnt, nil)
	}
}

func (s *IndexController) GetHost() {
	if s.Ctx.Request.Method == "POST" {
		data := make(map[string]interface{})
		if h, err := file.GetDb().GetHostById(s.GetIntNoErr("id")); err != nil {
			data["code"] = 0
		} else {
			data["data"] = h
			data["code"] = 1
		}
		s.Data["json"] = data
		s.ServeJSON()
	}
}

func (s *IndexController) DelHost() {
	id := s.GetIntNoErr("id")
	server.HttpProxyCache.Remove(id)
	if err := file.GetDb().DelHost(id); err != nil {
		s.AjaxErr("delete error")
	}
	s.AjaxOk("delete success")
}

func (s *IndexController) StartHost() {
	id := s.GetIntNoErr("id")
	server.HttpProxyCache.Remove(id)
	mode := s.getEscapeString("mode")
	if mode != "" {
		if err := changeHostStatus(id, mode, "start"); err != nil {
			s.AjaxErr("modified fail")
		}
		s.AjaxOk("modified success")
	}
	h, err := file.GetDb().GetHostById(id)
	if err != nil {
		s.error()
		return
	}
	h.IsClose = false
	file.GetDb().JsonDb.StoreHostToJsonFile()
	s.AjaxOk("start success")
}

func (s *IndexController) StopHost() {
	id := s.GetIntNoErr("id")
	server.HttpProxyCache.Remove(id)
	mode := s.getEscapeString("mode")
	if mode != "" {
		if err := changeHostStatus(id, mode, "stop"); err != nil {
			s.AjaxErr("modified fail")
		}
		s.AjaxOk("modified success")
	}
	h, err := file.GetDb().GetHostById(id)
	if err != nil {
		s.error()
		return
	}
	h.IsClose = true
	file.GetDb().JsonDb.StoreHostToJsonFile()
	s.AjaxOk("stop success")
}

func (s *IndexController) ClearHost() {
	id := s.GetIntNoErr("id")
	server.HttpProxyCache.Remove(id)
	mode := s.getEscapeString("mode")
	if mode != "" {
		if err := changeHostStatus(id, mode, "clear"); err != nil {
			s.AjaxErr("modified fail")
		}
		s.AjaxOk("modified success")
	}
	s.AjaxErr("modified fail")
}

func (s *IndexController) AddHost() {
	if s.Ctx.Request.Method == "GET" {
		s.Data["client_id"] = s.getEscapeString("client_id")
		s.Data["menu"] = "host"
		s.SetInfo("add host")
		s.display("index/hadd")
	} else {
		id := int(file.GetDb().JsonDb.GetHostId())
		isAdmin := s.GetSession("isAdmin").(bool)
		allowLocal := beego.AppConfig.DefaultBool("allow_user_local", beego.AppConfig.DefaultBool("allow_local_proxy", false)) || isAdmin
		clientId := s.GetIntNoErr("client_id")
		targetStr := strings.ToLower(strings.TrimSpace(strings.ReplaceAll(s.getEscapeString("target"), "\r\n", "\n")))
		if !isAdmin && strings.Contains(targetStr, "bridge://") {
			targetStr = ""
		}
		h := &file.Host{
			Id:   id,
			Host: s.getEscapeString("host"),
			Target: &file.Target{
				TargetStr:     targetStr,
				ProxyProtocol: s.GetIntNoErr("proxy_protocol"),
				LocalProxy:    (clientId > 0 && s.GetBoolNoErr("local_proxy") && allowLocal) || clientId <= 0,
			},
			UserAuth: &file.MultiAccount{
				Content:    s.getEscapeString("auth"),
				AccountMap: common.DealMultiUser(s.getEscapeString("auth")),
			},
			HeaderChange:     s.getEscapeString("header"),
			RespHeaderChange: s.getEscapeString("resp_header"),
			HostChange:       s.getEscapeString("hostchange"),
			Remark:           s.getEscapeString("remark"),
			Location:         s.getEscapeString("location"),
			PathRewrite:      s.getEscapeString("path_rewrite"),
			RedirectURL:      s.getEscapeString("redirect_url"),
			Flow: &file.Flow{
				FlowLimit: int64(s.GetIntNoErr("flow_limit")),
				TimeLimit: common.GetTimeNoErrByStr(s.getEscapeString("time_limit")),
			},
			Scheme:         s.getEscapeString("scheme"),
			HttpsJustProxy: s.GetBoolNoErr("https_just_proxy"),
			TlsOffload:     s.GetBoolNoErr("tls_offload"),
			AutoSSL:        s.GetBoolNoErr("auto_ssl"),
			KeyFile:        s.getEscapeString("key_file"),
			CertFile:       s.getEscapeString("cert_file"),
			AutoHttps:      s.GetBoolNoErr("auto_https"),
			AutoCORS:       s.GetBoolNoErr("auto_cors"),
			CompatMode:     s.GetBoolNoErr("compat_mode"),
			TargetIsHttps:  s.GetBoolNoErr("target_is_https"),
		}
		var err error
		if h.Client, err = file.GetDb().GetClient(s.GetIntNoErr("client_id")); err != nil {
			s.AjaxErr("add error the client can not be found")
		}
		if h.Client.MaxTunnelNum != 0 && h.Client.GetTunnelNum() >= h.Client.MaxTunnelNum {
			s.AjaxErr("The number of tunnels exceeds the limit")
		}

		if err := file.GetDb().NewHost(h); err != nil {
			s.AjaxErr("add fail" + err.Error())
		}
		s.AjaxOkWithId("add success", id)
	}
}

func (s *IndexController) EditHost() {
	id := s.GetIntNoErr("id")
	server.HttpProxyCache.Remove(id)
	if s.Ctx.Request.Method == "GET" {
		s.Data["menu"] = "host"
		if h, err := file.GetDb().GetHostById(id); err != nil {
			s.error()
		} else {
			s.Data["h"] = h
			if h.UserAuth == nil {
				s.Data["auth"] = ""
			} else {
				s.Data["auth"] = h.UserAuth.Content
			}
		}
		s.SetInfo("edit")
		s.display("index/hedit")
	} else {
		if h, err := file.GetDb().GetHostById(id); err != nil {
			s.error()
		} else {
			oleHost := h.Host
			scheme := s.getEscapeString("scheme")
			if scheme != "all" && scheme != "http" && scheme != "https" {
				scheme = "all"
			}
			if h.Host != s.getEscapeString("host") || h.Location != s.getEscapeString("location") || h.Scheme != scheme {
				tmpHost := new(file.Host)
				tmpHost.Id = h.Id
				tmpHost.Host = s.getEscapeString("host")
				tmpHost.Location = s.getEscapeString("location")
				tmpHost.Scheme = scheme
				if file.GetDb().IsHostExist(tmpHost) {
					s.AjaxErr("host has exist")
					return
				}
			}
			clientId := s.GetIntNoErr("client_id")
			if client, err := file.GetDb().GetClient(clientId); err != nil {
				s.AjaxErr("modified error, the client is not exist")
			} else {
				h.Client = client
			}
			isAdmin := s.GetSession("isAdmin").(bool)
			allowLocal := beego.AppConfig.DefaultBool("allow_user_local", beego.AppConfig.DefaultBool("allow_local_proxy", false)) || isAdmin
			targetStr := strings.ToLower(strings.TrimSpace(strings.ReplaceAll(s.getEscapeString("target"), "\r\n", "\n")))
			if !isAdmin && strings.Contains(targetStr, "bridge://") {
				if h.Target != nil {
					targetStr = h.Target.TargetStr
				} else {
					targetStr = ""
				}
			}
			h.Host = s.getEscapeString("host")
			h.Target = &file.Target{TargetStr: targetStr}
			h.UserAuth = &file.MultiAccount{Content: s.getEscapeString("auth"), AccountMap: common.DealMultiUser(s.getEscapeString("auth"))}
			h.HeaderChange = s.getEscapeString("header")
			h.RespHeaderChange = s.getEscapeString("resp_header")
			h.HostChange = s.getEscapeString("hostchange")
			h.Remark = s.getEscapeString("remark")
			h.Location = s.getEscapeString("location")
			h.PathRewrite = s.getEscapeString("path_rewrite")
			h.RedirectURL = s.getEscapeString("redirect_url")
			h.Scheme = scheme
			h.HttpsJustProxy = s.GetBoolNoErr("https_just_proxy")
			h.TlsOffload = s.GetBoolNoErr("tls_offload")
			h.AutoSSL = s.GetBoolNoErr("auto_ssl")
			h.KeyFile = s.getEscapeString("key_file")
			h.CertFile = s.getEscapeString("cert_file")
			h.Target.ProxyProtocol = s.GetIntNoErr("proxy_protocol")
			h.Target.LocalProxy = (clientId > 0 && s.GetBoolNoErr("local_proxy") && allowLocal) || clientId <= 0
			h.Flow.FlowLimit = int64(s.GetIntNoErr("flow_limit"))
			h.Flow.TimeLimit = common.GetTimeNoErrByStr(s.getEscapeString("time_limit"))
			if s.GetBoolNoErr("flow_reset") {
				h.Flow.ExportFlow = 0
				h.Flow.InletFlow = 0
			}
			h.AutoHttps = s.GetBoolNoErr("auto_https")
			h.AutoCORS = s.GetBoolNoErr("auto_cors")
			h.CompatMode = s.GetBoolNoErr("compat_mode")
			h.TargetIsHttps = s.GetBoolNoErr("target_is_https")
			if h.Host != oleHost {
				file.HostIndex.Remove(oleHost, h.Id)
				file.HostIndex.Add(h.Host, h.Id)
			}
			h.CertType = common.GetCertType(h.CertFile)
			h.CertHash = crypt.FNV1a64(h.CertType, h.CertFile, h.KeyFile)
			file.GetDb().JsonDb.StoreHostToJsonFile()
		}
		s.AjaxOk("modified success")
	}
}

func changeHostStatus(id int, name, action string) (err error) {
	h, err := file.GetDb().GetHostById(id)
	if err != nil {
		return err
	}
	a := strings.ToLower(strings.TrimSpace(action))
	switch name {
	case "flow":
		if a != "clear" {
			return fmt.Errorf("unsupported action %q for %s", a, name)
		}
		h.Flow.ExportFlow = 0
		h.Flow.InletFlow = 0
	case "flow_limit":
		if a != "clear" {
			return fmt.Errorf("unsupported action %q for %s", a, name)
		}
		h.Flow.FlowLimit = 0
	case "time_limit":
		if a != "clear" {
			return fmt.Errorf("unsupported action %q for %s", a, name)
		}
		h.Flow.TimeLimit = common.GetTimeNoErrByStr("")
	case "auto_ssl":
		if err := applyBoolAction(&h.AutoSSL, a); err != nil {
			return err
		}
	case "https_just_proxy":
		if err := applyBoolAction(&h.HttpsJustProxy, a); err != nil {
			return err
		}
	case "tls_offload":
		if err := applyBoolAction(&h.TlsOffload, a); err != nil {
			return err
		}
	case "auto_https":
		if err := applyBoolAction(&h.AutoHttps, a); err != nil {
			return err
		}
	case "auto_cors":
		if err := applyBoolAction(&h.AutoCORS, a); err != nil {
			return err
		}
	case "compat_mode":
		if err := applyBoolAction(&h.CompatMode, a); err != nil {
			return err
		}
	case "target_is_https":
		if err := applyBoolAction(&h.TargetIsHttps, a); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown name: %q", name)
	}
	file.GetDb().JsonDb.StoreHostToJsonFile()

	return nil
}

func applyBoolAction(dst *bool, action string) error {
	switch action {
	case "start", "true", "on":
		*dst = true
	case "stop", "false", "off":
		*dst = false
	case "clear", "turn", "switch":
		*dst = !*dst
	default:
		return fmt.Errorf("unknown action: %q", action)
	}
	return nil
}
