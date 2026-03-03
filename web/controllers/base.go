package controllers

import (
	"crypto/subtle"
	"html"
	"html/template"
	"math"
	"strconv"
	"strings"

	"github.com/beego/beego"
	"github.com/djylb/nps/bridge"
	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/server"
	"github.com/djylb/nps/server/connection"
)

type BaseController struct {
	beego.Controller
	controllerName string
	actionName     string
}

func (s *BaseController) Prepare() {
	s.Data["web_base_url"] = beego.AppConfig.String("web_base_url")
	s.Data["head_custom_code"] = template.HTML(beego.AppConfig.String("head_custom_code"))
	controllerName, actionName := s.GetControllerAndAction()
	s.controllerName = strings.ToLower(controllerName[0 : len(controllerName)-10])
	s.actionName = strings.ToLower(actionName)

	// web api verify
	// param 1 is md5(authKey+Current timestamp)
	// param 2 is timestamp (It's limited to 20 seconds.)
	md5Key := s.getEscapeString("auth_key")
	timestamp := s.GetIntNoErr("timestamp")
	configKey := beego.AppConfig.String("auth_key")
	timeNowUnix := common.TimeNow().Unix()
	if !isValidAuthKey(configKey, md5Key, timestamp, timeNowUnix) {
		if s.GetSession("auth") != true {
			s.Redirect(beego.AppConfig.String("web_base_url")+"/login/index", 302)
		}
	} else {
		s.SetSession("isAdmin", true)
		s.Data["isAdmin"] = true
	}
	if s.GetSession("isAdmin") != nil && !s.GetSession("isAdmin").(bool) {
		s.Ctx.Input.SetData("client_id", s.GetSession("clientId").(int))
		s.Ctx.Input.SetParam("client_id", strconv.Itoa(s.GetSession("clientId").(int)))
		s.Data["isAdmin"] = false
		s.Data["username"] = s.GetSession("username")
		s.CheckUserAuth()
	} else {
		s.Data["isAdmin"] = true
	}

	//s.Data["https_just_proxy"], _ = beego.AppConfig.Bool("https_just_proxy")
	s.Data["allow_user_login"], _ = beego.AppConfig.Bool("allow_user_login")
	s.Data["allow_flow_limit"], _ = beego.AppConfig.Bool("allow_flow_limit")
	s.Data["allow_rate_limit"], _ = beego.AppConfig.Bool("allow_rate_limit")
	s.Data["allow_time_limit"], _ = beego.AppConfig.Bool("allow_time_limit")
	s.Data["allow_connection_num_limit"], _ = beego.AppConfig.Bool("allow_connection_num_limit")
	s.Data["allow_multi_ip"], _ = beego.AppConfig.Bool("allow_multi_ip")
	s.Data["system_info_display"], _ = beego.AppConfig.Bool("system_info_display")
	s.Data["allow_tunnel_num_limit"], _ = beego.AppConfig.Bool("allow_tunnel_num_limit")
	allowLocalProxy := beego.AppConfig.DefaultBool("allow_local_proxy", false)
	s.Data["allow_local_proxy"] = allowLocalProxy
	s.Data["allow_user_local"] = beego.AppConfig.DefaultBool("allow_user_local", allowLocalProxy)
	s.Data["allow_secret_link"], _ = beego.AppConfig.Bool("allow_secret_link")
	s.Data["allow_user_change_username"], _ = beego.AppConfig.Bool("allow_user_change_username")
}

func isValidAuthKey(configKey, md5Key string, timestamp int, nowUnix int64) bool {
	if configKey == "" || md5Key == "" {
		return false
	}
	if math.Abs(float64(nowUnix-int64(timestamp))) > 20 {
		return false
	}
	expected := crypt.Md5(configKey + strconv.Itoa(timestamp))
	if len(expected) != len(md5Key) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(expected), []byte(md5Key)) == 1
}

func (s *BaseController) display(tpl ...string) {
	s.Data["web_base_url"] = beego.AppConfig.String("web_base_url")
	s.Data["head_custom_code"] = template.HTML(beego.AppConfig.String("head_custom_code"))
	s.Data["version"] = server.GetVersion()
	s.Data["year"] = server.GetCurrentYear()
	var tplname string
	if s.Data["menu"] == nil {
		s.Data["menu"] = s.actionName
	}
	if len(tpl) > 0 {
		tplname = strings.Join([]string{tpl[0], "html"}, ".")
	} else {
		tplname = s.controllerName + "/" + s.actionName + ".html"
	}
	ip := s.Ctx.Request.Host
	s.Data["bridgeType"], s.Data["addr"], ip, s.Data["p"] = GetBestBridge(ip)
	s.Data["ip"] = ip
	if common.IsWindows() {
		s.Data["win"] = ".exe"
	}
	if beego.AppConfig.DefaultBool("bridge_tcp_show", bridge.ServerTcpEnable) {
		s.Data["tcp_ip"] = beego.AppConfig.DefaultString("bridge_tcp_show_ip", ip)
		s.Data["tcp_p"] = beego.AppConfig.DefaultString("bridge_tcp_show_port", strconv.Itoa(connection.BridgeTcpPort))
	}
	if beego.AppConfig.DefaultBool("bridge_kcp_show", bridge.ServerKcpEnable) {
		s.Data["kcp_ip"] = beego.AppConfig.DefaultString("bridge_kcp_show_ip", ip)
		s.Data["kcp_p"] = beego.AppConfig.DefaultString("bridge_kcp_show_port", strconv.Itoa(connection.BridgeKcpPort))
	}
	if beego.AppConfig.DefaultBool("bridge_tls_show", bridge.ServerTlsEnable) {
		s.Data["tls_ip"] = beego.AppConfig.DefaultString("bridge_tls_show_ip", ip)
		s.Data["tls_p"] = beego.AppConfig.DefaultString("bridge_tls_show_port", strconv.Itoa(connection.BridgeTlsPort))
	}
	if beego.AppConfig.DefaultBool("bridge_quic_show", bridge.ServerQuicEnable) {
		quicIp := beego.AppConfig.DefaultString("bridge_quic_show_ip", ip)
		quicPort := beego.AppConfig.DefaultString("bridge_quic_show_port", strconv.Itoa(connection.BridgeQuicPort))
		quicAlpn := beego.AppConfig.DefaultString("bridge_quic_show_alpn", connection.QuicAlpn[0])
		quicAddr := quicIp + ":" + quicPort
		if quicAlpn != "" && quicAlpn != "nps" {
			quicAddr = quicAddr + "/" + quicAlpn
		}
		s.Data["quic_ip"] = quicIp
		s.Data["quic_p"] = quicPort
		s.Data["quic_alpn"] = quicAlpn
		s.Data["quic_addr"] = quicAddr
	}
	if wsPath := beego.AppConfig.String("bridge_path"); wsPath != "" {
		s.Data["ws_path"] = beego.AppConfig.DefaultString("bridge_show_path", wsPath)
		if beego.AppConfig.DefaultBool("bridge_ws_show", bridge.ServerWsEnable) {
			s.Data["ws_ip"] = beego.AppConfig.DefaultString("bridge_ws_show_ip", ip)
			s.Data["ws_p"] = beego.AppConfig.DefaultString("bridge_ws_show_port", beego.AppConfig.String("bridge_ws_port"))
		}
		if beego.AppConfig.DefaultBool("bridge_wss_show", bridge.ServerWssEnable) {
			s.Data["wss_ip"] = beego.AppConfig.DefaultString("bridge_wss_show_ip", ip)
			s.Data["wss_p"] = beego.AppConfig.DefaultString("bridge_wss_show_port", beego.AppConfig.String("bridge_wss_port"))
		}
	}
	s.Data["proxyPort"] = beego.AppConfig.String("hostPort")

	s.Layout = "public/layout.html"
	s.TplName = tplname
}

func (s *BaseController) error() {
	s.Data["web_base_url"] = beego.AppConfig.String("web_base_url")
	s.Data["head_custom_code"] = template.HTML(beego.AppConfig.String("head_custom_code"))
	s.Data["version"] = server.GetVersion()
	s.Data["year"] = server.GetCurrentYear()
	s.Layout = "public/layout.html"
	s.TplName = "public/error.html"
}

func (s *BaseController) getEscapeString(key string) string {
	return html.EscapeString(s.GetString(key))
}

func (s *BaseController) GetIntNoErr(key string, def ...int) int {
	strv := s.Ctx.Input.Query(key)
	if len(strv) == 0 && len(def) > 0 {
		return def[0]
	}
	val, _ := strconv.Atoi(strv)
	return val
}

func (s *BaseController) GetBoolNoErr(key string, def ...bool) bool {
	strv := s.Ctx.Input.Query(key)
	if len(strv) == 0 && len(def) > 0 {
		return def[0]
	}
	val, _ := strconv.ParseBool(strv)
	return val
}

func (s *BaseController) AjaxOk(str string) {
	s.Data["json"] = ajax(str, 1)
	s.ServeJSON()
	s.StopRun()
}

func (s *BaseController) AjaxOkWithId(str string, id int) {
	s.Data["json"] = ajaxWithId(str, 1, id)
	s.ServeJSON()
	s.StopRun()
}

func (s *BaseController) AjaxErr(str string) {
	s.Data["json"] = ajax(str, 0)
	s.ServeJSON()
	s.StopRun()
}

func ajax(str string, status int) map[string]interface{} {
	json := make(map[string]interface{})
	json["status"] = status
	json["msg"] = str
	return json
}

func ajaxWithId(str string, status int, id int) map[string]interface{} {
	json := make(map[string]interface{})
	json["status"] = status
	json["msg"] = str
	json["id"] = id
	return json
}

func (s *BaseController) AjaxTable(list interface{}, cnt int, recordsTotal int, kwargs map[string]interface{}) {
	json := make(map[string]interface{})
	json["rows"] = list
	json["total"] = recordsTotal
	if kwargs != nil {
		for k, v := range kwargs {
			if v != nil {
				json[k] = v
			}
		}
	}
	s.Data["json"] = json
	s.ServeJSON()
	s.StopRun()
}

func (s *BaseController) GetAjaxParams() (start, limit int) {
	return s.GetIntNoErr("offset"), s.GetIntNoErr("limit")
}

func (s *BaseController) SetInfo(name string) {
	s.Data["name"] = name
}

func (s *BaseController) SetType(name string) {
	s.Data["type"] = name
}

func (s *BaseController) CheckUserAuth() {
	if s.controllerName == "client" {
		if s.actionName == "add" {
			s.StopRun()
			return
		}
		if id := s.GetIntNoErr("id"); id != 0 {
			if id != s.GetSession("clientId").(int) {
				s.StopRun()
				return
			}
		}
	}
	if s.controllerName == "index" {
		if id := s.GetIntNoErr("id"); id != 0 {
			belong := false
			if strings.Contains(s.actionName, "h") {
				if v, ok := file.GetDb().JsonDb.Hosts.Load(id); ok {
					if v.(*file.Host).Client.Id == s.GetSession("clientId").(int) {
						belong = true
					}
				}
			} else {
				if v, ok := file.GetDb().JsonDb.Tasks.Load(id); ok {
					if v.(*file.Tunnel).Client.Id == s.GetSession("clientId").(int) {
						belong = true
					}
				}
			}
			if !belong {
				s.StopRun()
			}
		}
	}
}

func GetBestBridge(ip string) (bridgeType, bridgeAddr, bridgeIp, bridgePort string) {
	bridgeIp = common.GetIpByAddr(beego.AppConfig.DefaultString("bridge_addr", ip))
	if strings.IndexByte(bridgeIp, ':') >= 0 && !(strings.HasPrefix(bridgeIp, "[") && strings.HasSuffix(bridgeIp, "]")) {
		bridgeIp = "[" + bridgeIp + "]"
	}
	bridgeType = beego.AppConfig.String("bridge_type")
	bridgePort = strconv.Itoa(server.Bridge.TunnelPort)
	bridgeAddr = bridgeIp + ":" + bridgePort
	if bridgeType == "both" {
		bridgeType = "tcp"
	}
	if beego.AppConfig.DefaultBool("bridge_tls_show", bridge.ServerTlsEnable) {
		bridgeType = "tls"
		bridgePort = beego.AppConfig.DefaultString("bridge_tls_show_port", strconv.Itoa(connection.BridgeTlsPort))
		bridgeTlsIp := beego.AppConfig.DefaultString("bridge_tls_show_ip", bridgeIp)
		bridgeAddr = bridgeTlsIp + ":" + bridgePort
	} else if beego.AppConfig.DefaultBool("bridge_quic_show", bridge.ServerQuicEnable) {
		bridgeType = "quic"
		bridgePort = beego.AppConfig.DefaultString("bridge_quic_show_port", strconv.Itoa(connection.BridgeQuicPort))
		bridgeQuicIp := beego.AppConfig.DefaultString("bridge_quic_show_ip", bridgeIp)
		bridgeAddr = bridgeQuicIp + ":" + bridgePort
		quicAlpn := beego.AppConfig.DefaultString("bridge_quic_show_alpn", connection.QuicAlpn[0])
		if quicAlpn != "" && quicAlpn != "nps" {
			bridgeAddr = bridgeAddr + "/" + quicAlpn
		}
	} else if beego.AppConfig.DefaultBool("bridge_wss_show", bridge.ServerWssEnable) {
		bridgeType = "wss"
		bridgePort = beego.AppConfig.DefaultString("bridge_wss_show_port", strconv.Itoa(connection.BridgeWssPort))
		bridgeWssIp := beego.AppConfig.DefaultString("bridge_wss_show_ip", bridgeIp)
		bridgeAddr = bridgeWssIp + ":" + bridgePort + beego.AppConfig.DefaultString("bridge_show_path", connection.BridgePath)
	} else if beego.AppConfig.DefaultBool("bridge_tcp_show", bridge.ServerTcpEnable) {
		bridgeType = "tcp"
		bridgePort = beego.AppConfig.DefaultString("bridge_tcp_show_port", strconv.Itoa(connection.BridgeTcpPort))
		bridgeTcpIp := beego.AppConfig.DefaultString("bridge_tcp_show_ip", bridgeIp)
		bridgeAddr = bridgeTcpIp + ":" + bridgePort
	} else if beego.AppConfig.DefaultBool("bridge_kcp_show", bridge.ServerKcpEnable) {
		bridgeType = "kcp"
		bridgePort = beego.AppConfig.DefaultString("bridge_kcp_show_port", strconv.Itoa(connection.BridgeKcpPort))
		bridgeKcpIp := beego.AppConfig.DefaultString("bridge_kcp_show_ip", bridgeIp)
		bridgeAddr = bridgeKcpIp + ":" + bridgePort
	} else if beego.AppConfig.DefaultBool("bridge_ws_show", bridge.ServerWsEnable) {
		bridgeType = "ws"
		bridgePort = beego.AppConfig.DefaultString("bridge_ws_show_port", strconv.Itoa(connection.BridgeWsPort))
		bridgeWsIp := beego.AppConfig.DefaultString("bridge_ws_show_ip", bridgeIp)
		bridgeAddr = bridgeWsIp + ":" + bridgePort + beego.AppConfig.DefaultString("bridge_show_path", connection.BridgePath)
	}
	return
}
