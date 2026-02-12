package controllers

import (
	"strings"

	"github.com/djylb/nps/lib/file"
)

type GlobalController struct {
	BaseController
}

func (s *GlobalController) Index() {
	isAdmin, ok := s.GetSession("isAdmin").(bool)
	if !ok || !isAdmin {
		return
	}

	s.Data["menu"] = "global"
	s.SetInfo("global")
	s.display("global/index")

	global := file.GetDb().GetGlobal()
	if global == nil {
		return
	}
	s.Data["globalBlackIpList"] = strings.Join(global.BlackIpList, "\r\n")
}

func (s *GlobalController) Save() {
	isAdmin, ok := s.GetSession("isAdmin").(bool)
	if !ok || !isAdmin {
		return
	}

	if s.Ctx.Request.Method == "GET" {
		s.Data["menu"] = "global"
		s.SetInfo("save global")
		s.display()
		return
	}

	t := &file.Glob{BlackIpList: RemoveRepeatedElement(strings.Split(s.getEscapeString("globalBlackIpList"), "\r\n"))}
	if err := file.GetDb().SaveGlobal(t); err != nil {
		s.AjaxErr(err.Error())
		return
	}
	s.AjaxOk("save success")
}

// BanList 封禁列表管理页面
func (s *GlobalController) BanList() {
	isAdmin, ok := s.GetSession("isAdmin").(bool)
	if !ok || !isAdmin {
		return
	}

	if s.Ctx.Request.Method == "GET" {
		s.Data["menu"] = "banlist"
		s.SetInfo("banlist")
		s.display("global/banlist")
		return
	}

	list := GetLoginBanList()
	s.Data["json"] = map[string]interface{}{
		"rows":  list,
		"total": len(list),
	}
	s.ServeJSON()
}

// Unban 解除指定key的封禁
func (s *GlobalController) Unban() {
	isAdmin, ok := s.GetSession("isAdmin").(bool)
	if !ok || !isAdmin {
		return
	}
	key := s.GetString("key")
	if key == "" {
		s.AjaxErr("key is required")
		return
	}
	if RemoveLoginBan(key) {
		s.AjaxOk("unban success")
	} else {
		s.AjaxErr("record not found")
	}
}

// UnbanAll 清除所有封禁记录
func (s *GlobalController) UnbanAll() {
	isAdmin, ok := s.GetSession("isAdmin").(bool)
	if !ok || !isAdmin {
		return
	}

	RemoveAllLoginBan()
	s.AjaxOk("all records cleared")
}
