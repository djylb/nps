package controllers

import (
	"html/template"

	"github.com/beego/beego"
	"github.com/djylb/nps/server"
)

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
