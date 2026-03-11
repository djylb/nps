package controllers

import (
	"fmt"
	"strings"

	"github.com/beego/beego"
	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/server"
	"github.com/djylb/nps/server/tool"
)

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
	return nil
}
