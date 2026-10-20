package api

import (
	"errors"

	webservice "github.com/djylb/nps/web/service"
)

func (a *App) SaveGlobal(c Context) {
	if err := a.Services.Globals.Save(webservice.SaveGlobalInput{
		Access: webservice.AccessListInput{
			Priority: requestIntValue(c, "globalIpListPriority"),
			Black:    requestString(c, "globalBlackIpList"),
			White:    requestString(c, "globalWhiteIpList"),
		},
		RateLimit: requestIntValue(c, "globalRateLimit"),
		MaxConn:   requestIntValue(c, "globalMaxConn"),
	}); err != nil {
		ajax(c, err.Error(), 0)
		return
	}
	a.Emit(c, Event{
		Name:     "global.updated",
		Resource: "global",
		Action:   "save",
	})
	ajax(c, "save success", 1)
}

func (a *App) UpdateGeoData(c Context) {
	if _, err := a.system().UpdateGeoData(); err != nil {
		if errors.Is(err, webservice.ErrGeoDataUpdateRunning) {
			ajax(c, "geodata update is already running", 0)
			return
		}
		ajax(c, "geodata update failed: "+err.Error(), 0)
		return
	}
	ajax(c, "geodata updated", 1)
}

func (a *App) BanList(c Context) {
	list := a.Services.Globals.BanList()
	c.RespondJSON(200, TableResponse{
		Rows:  list,
		Total: len(list),
	})
}

func (a *App) Unban(c Context) {
	key := requestString(c, "key")
	if key == "" {
		ajax(c, "key is required", 0)
		return
	}
	if a.Services.Globals.Unban(key) {
		a.Emit(c, Event{
			Name:     "login_ban.removed",
			Resource: "login_ban",
			Action:   "remove",
			Fields:   map[string]interface{}{"key": key},
		})
		ajax(c, "unban success", 1)
		return
	}
	ajax(c, "record not found", 0)
}

func (a *App) UnbanAll(c Context) {
	a.Services.Globals.UnbanAll()
	a.Emit(c, Event{
		Name:     "login_ban.cleared",
		Resource: "login_ban",
		Action:   "clear_all",
	})
	ajax(c, "all records cleared", 1)
}

func (a *App) BanClean(c Context) {
	a.Services.Globals.CleanBans()
	a.Emit(c, Event{
		Name:     "login_ban.cleaned",
		Resource: "login_ban",
		Action:   "clean",
	})
	ajax(c, "operation success", 1)
}
