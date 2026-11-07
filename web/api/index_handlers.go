package api

import (
	"errors"
	"html"

	"github.com/djylb/nps/lib/model"

	"github.com/djylb/nps/lib/servercfg"
	webservice "github.com/djylb/nps/web/service"
)

func (a *App) DashboardStats(c Context) {
	if sessionBool(c, "isAdmin") {
		respondCodeData(c, 1, a.Services.Index.DashboardData(false))
		return
	}
	respondCode(c, 0)
}

// Messages of UpdateServer; the dashboard reloads after serverUpdatedMsg.
const (
	serverUpdatedMsg  = "server updated, restarting"
	serverLatestMsg   = "server is already the latest version"
	serverUpdateError = "server update failed"
)

func (a *App) UpdateServer(c Context) {
	v, err := a.system().UpdateServer()
	switch {
	case v != "":
		// An error after a version means only the web assets failed; the
		// new version runs either way.
		ajax(c, serverUpdatedMsg, 1)
	case err == nil:
		ajax(c, serverLatestMsg, 1)
	case errors.Is(err, webservice.ErrServerUpdateRunning):
		ajax(c, "server update is already running", 0)
	case errors.Is(err, webservice.ErrServerUpdateUnavailable):
		ajax(c, "server update is unavailable", 0)
	default:
		ajax(c, serverUpdateError+": "+err.Error(), 0)
	}
}

func (a *App) GetTunnel(c Context) {
	rows, count := a.Services.Index.ListTunnels(webservice.TunnelListInput{
		Offset:   requestIntValue(c, "offset"),
		Limit:    requestIntValue(c, "limit"),
		Type:     requestEscapedString(c, "type"),
		ClientID: requestIntValue(c, "client_id"),
		Search:   requestEscapedString(c, "search"),
		Sort:     requestEscapedString(c, "sort"),
		Order:    requestEscapedString(c, "order"),
	})
	respondTable(c, tunnelRateRows(rows), count)
}

func (a *App) AddTunnel(c Context) {
	result, err := a.Services.Index.AddTunnel(tunnelInput(c, a.currentConfig()))
	if err != nil {
		switch {
		case errors.Is(err, webservice.ErrPortUnavailable):
			ajax(c, "The port cannot be opened because it may has been occupied or is no longer allowed.", 0)
		case errors.Is(err, webservice.ErrTunnelLimitExceeded):
			ajax(c, "The number of tunnels exceeds the limit", 0)
		default:
			ajax(c, err.Error(), 0)
		}
		return
	}

	ajaxWithID(c, "add success", 1, result.ID)
}

func (a *App) GetOneTunnel(c Context) {
	id := requestIntValue(c, "id")
	if tunnel, err := a.Services.Index.GetTunnel(id); err != nil {
		respondCode(c, 0)
	} else {
		respondCodeData(c, 1, tunnelRateRows([]*model.Tunnel{tunnel})[0])
	}
}

func (a *App) EditTunnel(c Context) {
	_, err := a.Services.Index.EditTunnel(tunnelInput(c, a.currentConfig()))
	if err != nil {
		switch {
		case errors.Is(err, webservice.ErrTunnelNotFound):
			ajax(c, "modified error,the task is not exist", 0)
		case errors.Is(err, webservice.ErrClientNotFound):
			ajax(c, "modified error,the client is not exist", 0)
		case errors.Is(err, webservice.ErrPortUnavailable):
			ajax(c, "The port cannot be opened because it may has been occupied or is no longer allowed.", 0)
		case errors.Is(err, webservice.ErrTunnelLimitExceeded):
			ajax(c, "The number of tunnels exceeds the limit", 0)
		default:
			ajax(c, err.Error(), 0)
		}
		return
	}

	ajax(c, "modified success", 1)
}

func (a *App) StopTunnel(c Context) {
	if err := a.Services.Index.StopTunnel(requestIntValue(c, "id"), requestEscapedString(c, "mode")); err != nil {
		ajax(c, "stop error", 0)
		return
	}
	ajax(c, "stop success", 1)
}

func (a *App) DeleteTunnel(c Context) {
	id := requestIntValue(c, "id")
	if err := a.Services.Index.DeleteTunnel(id); err != nil {
		ajax(c, "delete error", 0)
		return
	}
	ajax(c, "delete success", 1)
}

func (a *App) StartTunnel(c Context) {
	if err := a.Services.Index.StartTunnel(requestIntValue(c, "id"), requestEscapedString(c, "mode")); err != nil {
		if errors.Is(err, webservice.ErrPortUnavailable) {
			ajax(c, "The port cannot be opened because it may has been occupied or is no longer allowed.", 0)
			return
		}
		ajax(c, "start error", 0)
		return
	}
	ajax(c, "start success", 1)
}

func (a *App) ClearTunnel(c Context) {
	if err := a.Services.Index.ClearTunnel(requestIntValue(c, "id"), requestEscapedString(c, "mode")); err != nil {
		ajax(c, "modified fail", 0)
		return
	}
	ajax(c, "modified success", 1)
}

func (a *App) HostList(c Context) {
	rows, count := a.Services.Index.ListHosts(webservice.HostListInput{
		Offset:   requestIntValue(c, "offset"),
		Limit:    requestIntValue(c, "limit"),
		ClientID: requestIntValue(c, "client_id"),
		Search:   requestEscapedString(c, "search"),
		Sort:     requestEscapedString(c, "sort"),
		Order:    requestEscapedString(c, "order"),
	})
	respondTable(c, hostRateRows(rows), count)
}

func (a *App) GetHost(c Context) {
	id := requestIntValue(c, "id")
	if host, err := a.Services.Index.GetHost(id); err != nil {
		respondCode(c, 0)
	} else {
		respondCodeData(c, 1, hostRateRows([]*model.Host{host})[0])
	}
}

// CertSuggestion offers a certificate of another entry for a name. Only an
// administrator is offered those of other clients.
func (a *App) CertSuggestion(c Context) {
	clientID := 0
	if !sessionBool(c, "isAdmin") {
		if clientID = requestIntValue(c, "client_id"); clientID == 0 {
			respondCode(c, 0)
			return
		}
	}
	suggestion := a.Services.Index.SuggestCert(requestString(c, "host"), requestIntValue(c, "exclude_id"), clientID)
	if suggestion == nil {
		respondCode(c, 0)
		return
	}
	respondCodeData(c, 1, suggestion)
}

func (a *App) DeleteHost(c Context) {
	id := requestIntValue(c, "id")
	if err := a.Services.Index.DeleteHost(id); err != nil {
		ajax(c, "delete error", 0)
		return
	}
	ajax(c, "delete success", 1)
}

func (a *App) StartHost(c Context) {
	mode := requestEscapedString(c, "mode")
	if err := a.Services.Index.StartHost(requestIntValue(c, "id"), mode); err != nil {
		if mode != "" {
			ajax(c, "modified fail", 0)
			return
		}
		ajax(c, "start error", 0)
		return
	}
	ajax(c, "start success", 1)
}

func (a *App) StopHost(c Context) {
	mode := requestEscapedString(c, "mode")
	if err := a.Services.Index.StopHost(requestIntValue(c, "id"), mode); err != nil {
		if mode != "" {
			ajax(c, "modified fail", 0)
			return
		}
		ajax(c, "stop error", 0)
		return
	}
	ajax(c, "stop success", 1)
}

func (a *App) ClearHost(c Context) {
	if err := a.Services.Index.ClearHost(requestIntValue(c, "id"), requestEscapedString(c, "mode")); err != nil {
		ajax(c, "modified fail", 0)
		return
	}
	ajax(c, "modified success", 1)
}

func (a *App) AddHost(c Context) {
	result, err := a.Services.Index.AddHost(hostInput(c, a.currentConfig()))
	if err != nil {
		switch {
		case errors.Is(err, webservice.ErrClientNotFound):
			ajax(c, "add error the client can not be found", 0)
		case errors.Is(err, webservice.ErrTunnelLimitExceeded):
			ajax(c, "The number of tunnels exceeds the limit", 0)
		default:
			ajax(c, "add fail"+err.Error(), 0)
		}
		return
	}

	ajaxWithID(c, "add success", 1, result.ID)
}

func (a *App) EditHost(c Context) {
	_, err := a.Services.Index.EditHost(hostInput(c, a.currentConfig()))
	if err != nil {
		switch {
		case errors.Is(err, webservice.ErrHostNotFound):
			ajax(c, "modified error, the host is not exist", 0)
		case errors.Is(err, webservice.ErrHostExists):
			ajax(c, "host has exist", 0)
		case errors.Is(err, webservice.ErrClientNotFound):
			ajax(c, "modified error, the client is not exist", 0)
		case errors.Is(err, webservice.ErrTunnelLimitExceeded):
			ajax(c, "The number of tunnels exceeds the limit", 0)
		default:
			ajax(c, err.Error(), 0)
		}
		return
	}

	ajax(c, "modified success", 1)
}

func tunnelInput(c Context, cfg *servercfg.Snapshot) webservice.TunnelInput {
	isAdmin := sessionBool(c, "isAdmin")
	tunnel := new(model.Tunnel)
	requestSettings(c, model.TunnelSettings, tunnel)
	return webservice.TunnelInput{
		ID:             requestIntValue(c, "id"),
		ClientID:       requestIntValue(c, "client_id"),
		IsAdmin:        isAdmin,
		AllowUserLocal: indexAllowLocal(cfg, isAdmin),
		ResetFlow:      requestBoolValue(c, "flow_reset"),
		Access:         requestAccessLists(c, ""),
		Tunnel:         tunnel,
	}
}

func hostInput(c Context, cfg *servercfg.Snapshot) webservice.HostInput {
	isAdmin := sessionBool(c, "isAdmin")
	host := new(model.Host)
	requestSettings(c, model.HostSettings, host)
	return webservice.HostInput{
		ID:             requestIntValue(c, "id"),
		ClientID:       requestIntValue(c, "client_id"),
		IsAdmin:        isAdmin,
		AllowUserLocal: indexAllowLocal(cfg, isAdmin),
		ResetFlow:      requestBoolValue(c, "flow_reset"),
		Access:         requestAccessLists(c, ""),
		Host:           host,
	}
}

// requestSettings reads the form settings of entry, a *model.Tunnel or
// *model.Host, from the request.
func requestSettings(c Context, settings []model.EntrySetting, entry any) {
	for _, s := range settings {
		if s.Form == "" {
			continue
		}
		v := requestString(c, s.Form)
		if !s.FormRaw {
			v = html.EscapeString(v)
		}
		_ = s.Set(entry, v)
	}
}

func indexAllowLocal(cfg *servercfg.Snapshot, isAdmin bool) bool {
	if cfg == nil {
		return isAdmin
	}
	return cfg.Feature.AllowUserLocal || isAdmin
}

// These response fields are snapshots in bytes/s and are never persisted.
type tunnelRateRow struct {
	*model.Tunnel
	NowConn      int32
	UploadRate   int64
	DownloadRate int64
}

type hostRateRow struct {
	*model.Host
	NowConn      int32
	UploadRate   int64
	DownloadRate int64
}

func tunnelRateRows(rows []*model.Tunnel) []*tunnelRateRow {
	if rows == nil {
		return nil
	}
	result := make([]*tunnelRateRow, len(rows))
	for i, row := range rows {
		if row == nil {
			continue
		}
		upload, download := row.CurrentRates()
		result[i] = &tunnelRateRow{Tunnel: row, NowConn: row.CurrentConnections(), UploadRate: upload, DownloadRate: download}
	}
	return result
}

func hostRateRows(rows []*model.Host) []*hostRateRow {
	if rows == nil {
		return nil
	}
	result := make([]*hostRateRow, len(rows))
	for i, row := range rows {
		if row == nil {
			continue
		}
		upload, download := row.CurrentRates()
		result[i] = &hostRateRow{Host: row, NowConn: row.CurrentConnections(), UploadRate: upload, DownloadRate: download}
	}
	return result
}
