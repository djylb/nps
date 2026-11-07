package api

import (
	"errors"
	"fmt"
	"time"

	"github.com/djylb/nps/lib/servercfg"
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
	log.Info("global settings saved", "user", actorName(c))
	ajax(c, "save success", 1)
}

// ExportConfig sends the stored configuration, secrets included, as a file.
func (a *App) ExportConfig(c Context) {
	data, err := a.Services.Config.Export()
	if err != nil {
		c.RespondString(500, "export failed")
		return
	}
	c.SetResponseHeader("Content-Disposition", `attachment; filename="nps-config-`+time.Now().Format("20060102-150405")+`.json"`)
	c.SetResponseHeader("Cache-Control", "no-store")
	c.RespondData(200, "application/json", data)
}

// ImportConfig adds the entries of an exported file, taken from the form field
// "data" verbatim.
func (a *App) ImportConfig(c Context) {
	result, err := a.Services.Config.Import(webservice.ImportConfigInput{
		Data:                  []byte(c.String("data")),
		ReservedAdminUsername: a.currentConfig().Web.Username,
	})
	if err != nil {
		ajax(c, err.Error(), 0)
		return
	}
	ajax(c, fmt.Sprintf("imported %d clients, %d tunnels, %d hosts", result.Clients, result.Tunnels, result.Hosts), 1)
}

func (a *App) UpdateGeoData(c Context) {
	if _, err := a.system().UpdateGeoData(requestString(c, "name")); err != nil {
		if errors.Is(err, webservice.ErrGeoDataUpdateRunning) {
			ajax(c, "geodata update is already running", 0)
			return
		}
		ajax(c, "geodata update failed: "+err.Error(), 0)
		return
	}
	ajax(c, "geodata updated", 1)
}

// ReloadConfigResponse is the answer of ReloadConfig: status 1 when the new
// settings are current, msg sums the report up.
type ReloadConfigResponse struct {
	Status int              `json:"status"`
	Msg    string           `json:"msg"`
	Report servercfg.Report `json:"report"`
}

// ReloadConfig reloads the config file into the running server.
func (a *App) ReloadConfig(c Context) {
	r := a.system().ReloadConfig()
	status := 0
	if r.OK() {
		status = 1
	}
	c.RespondJSON(200, ReloadConfigResponse{Status: status, Msg: r.String(), Report: r})
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
		ajax(c, "unban success", 1)
		return
	}
	ajax(c, "record not found", 0)
}

func (a *App) UnbanAll(c Context) {
	a.Services.Globals.UnbanAll()
	ajax(c, "all records cleared", 1)
}

func (a *App) BanClean(c Context) {
	a.Services.Globals.CleanBans()
	ajax(c, "operation success", 1)
}

// actorName names the user of a request for the log.
func actorName(c Context) string {
	if actor := c.Actor(); actor != nil {
		return actor.Username
	}
	return ""
}
