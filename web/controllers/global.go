package controllers

import (
	"strings"

	"github.com/djylb/nps/lib/file"
)

type GlobalController struct {
	BaseController
}

func (s *GlobalController) Index() {
	//if s.Ctx.Request.Method == "GET" {
	//
	//	return
	//}
	if !s.GetSession("isAdmin").(bool) {
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
	//global, err := file.GetDb().GetGlobal()
	//if err != nil {
	//	return
	//}
	if !s.GetSession("isAdmin").(bool) {
		return
	}
	if s.Ctx.Request.Method == "GET" {
		s.Data["menu"] = "global"
		s.SetInfo("save global")
		s.display()
	} else {

		t := &file.Glob{BlackIpList: RemoveRepeatedElement(strings.Split(s.getEscapeString("globalBlackIpList"), "\r\n"))}

		if err := file.GetDb().SaveGlobal(t); err != nil {
			s.AjaxErr(err.Error())
		}
		s.AjaxOk("save success")
	}
}
