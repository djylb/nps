package api

import (
	"errors"

	"github.com/djylb/nps/lib/file"
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
	result, err := a.Services.Index.AddTunnel(addTunnelInput(c, a.currentConfig()))
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

	a.Emit(c, Event{
		Name:     "tunnel.created",
		Resource: "tunnel",
		Action:   "create",
		Fields:   map[string]interface{}{"id": result.ID, "client_id": result.ClientID, "mode": result.Mode},
	})
	ajaxWithID(c, "add success", 1, result.ID)
}

func (a *App) GetOneTunnel(c Context) {
	id := requestIntValue(c, "id")
	if tunnel, err := a.Services.Index.GetTunnel(id); err != nil {
		respondCode(c, 0)
	} else {
		respondCodeData(c, 1, tunnelRateRows([]*file.Tunnel{tunnel})[0])
	}
}

func (a *App) EditTunnel(c Context) {
	result, err := a.Services.Index.EditTunnel(editTunnelInput(c, a.currentConfig()))
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

	a.Emit(c, Event{
		Name:     "tunnel.updated",
		Resource: "tunnel",
		Action:   "update",
		Fields:   map[string]interface{}{"id": result.ID, "client_id": result.ClientID},
	})
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
	a.Emit(c, Event{
		Name:     "tunnel.deleted",
		Resource: "tunnel",
		Action:   "delete",
		Fields:   map[string]interface{}{"id": id},
	})
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
		respondCodeData(c, 1, hostRateRows([]*file.Host{host})[0])
	}
}

func (a *App) DeleteHost(c Context) {
	id := requestIntValue(c, "id")
	if err := a.Services.Index.DeleteHost(id); err != nil {
		ajax(c, "delete error", 0)
		return
	}
	a.Emit(c, Event{
		Name:     "host.deleted",
		Resource: "host",
		Action:   "delete",
		Fields:   map[string]interface{}{"id": id},
	})
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
	result, err := a.Services.Index.AddHost(addHostInput(c, a.currentConfig()))
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

	a.Emit(c, Event{
		Name:     "host.created",
		Resource: "host",
		Action:   "create",
		Fields:   map[string]interface{}{"id": result.ID, "client_id": result.ClientID},
	})
	ajaxWithID(c, "add success", 1, result.ID)
}

func (a *App) EditHost(c Context) {
	result, err := a.Services.Index.EditHost(editHostInput(c, a.currentConfig()))
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

	a.Emit(c, Event{
		Name:     "host.updated",
		Resource: "host",
		Action:   "update",
		Fields:   map[string]interface{}{"id": result.ID, "client_id": result.ClientID},
	})
	ajax(c, "modified success", 1)
}

func addTunnelInput(c Context, cfg *servercfg.Snapshot) webservice.AddTunnelInput {
	isAdmin := sessionBool(c, "isAdmin")
	return webservice.AddTunnelInput{
		IsAdmin:        isAdmin,
		AllowUserLocal: indexAllowLocal(cfg, isAdmin),
		ClientID:       requestIntValue(c, "client_id"),
		Port:           requestIntValue(c, "port"),
		ServerIP:       requestEscapedString(c, "server_ip"),
		Mode:           requestEscapedString(c, "type"),
		TargetType:     requestEscapedString(c, "target_type"),
		Target:         requestEscapedString(c, "target"),
		ProxyProtocol:  requestIntValue(c, "proxy_protocol"),
		LocalProxy:     requestBoolValue(c, "local_proxy"),
		Auth:           requestEscapedString(c, "auth"),
		Remark:         requestEscapedString(c, "remark"),
		Password:       requestEscapedString(c, "password"),
		LocalPath:      requestEscapedString(c, "local_path"),
		StripPre:       requestEscapedString(c, "strip_pre"),
		EnableHTTP:     requestBoolValue(c, "enable_http"),
		EnableSocks5:   requestBoolValue(c, "enable_socks5"),
		DestACLMode:    requestIntValue(c, "dest_acl_mode"),
		DestACLRules:   requestString(c, "dest_acl_rules"),
		Access:         requestAccessLists(c, ""),
		MaxConn:        requestIntValue(c, "max_conn"),
		UploadLimit:    requestIntValue(c, "upload_limit"),
		DownloadLimit:  requestIntValue(c, "download_limit"),
		FlowLimit:      int64(requestIntValue(c, "flow_limit")),
		TimeLimit:      requestEscapedString(c, "time_limit"),
	}
}

func editTunnelInput(c Context, cfg *servercfg.Snapshot) webservice.EditTunnelInput {
	isAdmin := sessionBool(c, "isAdmin")
	return webservice.EditTunnelInput{
		ID:             requestIntValue(c, "id"),
		IsAdmin:        isAdmin,
		AllowUserLocal: indexAllowLocal(cfg, isAdmin),
		ClientID:       requestIntValue(c, "client_id"),
		Port:           requestIntValue(c, "port"),
		ServerIP:       requestEscapedString(c, "server_ip"),
		Mode:           requestEscapedString(c, "type"),
		TargetType:     requestEscapedString(c, "target_type"),
		Target:         requestEscapedString(c, "target"),
		ProxyProtocol:  requestIntValue(c, "proxy_protocol"),
		LocalProxy:     requestBoolValue(c, "local_proxy"),
		Auth:           requestEscapedString(c, "auth"),
		Remark:         requestEscapedString(c, "remark"),
		Password:       requestEscapedString(c, "password"),
		LocalPath:      requestEscapedString(c, "local_path"),
		StripPre:       requestEscapedString(c, "strip_pre"),
		EnableHTTP:     requestBoolValue(c, "enable_http"),
		EnableSocks5:   requestBoolValue(c, "enable_socks5"),
		DestACLMode:    requestIntValue(c, "dest_acl_mode"),
		DestACLRules:   requestString(c, "dest_acl_rules"),
		Access:         requestAccessLists(c, ""),
		MaxConn:        requestIntValue(c, "max_conn"),
		UploadLimit:    requestIntValue(c, "upload_limit"),
		DownloadLimit:  requestIntValue(c, "download_limit"),
		FlowLimit:      int64(requestIntValue(c, "flow_limit")),
		TimeLimit:      requestEscapedString(c, "time_limit"),
		ResetFlow:      requestBoolValue(c, "flow_reset"),
	}
}

func addHostInput(c Context, cfg *servercfg.Snapshot) webservice.AddHostInput {
	isAdmin := sessionBool(c, "isAdmin")
	return webservice.AddHostInput{
		IsAdmin:        isAdmin,
		AllowUserLocal: indexAllowLocal(cfg, isAdmin),
		ClientID:       requestIntValue(c, "client_id"),
		Access:         requestAccessLists(c, ""),
		Host:           requestEscapedString(c, "host"),
		Target:         requestEscapedString(c, "target"),
		ProxyProtocol:  requestIntValue(c, "proxy_protocol"),
		LocalProxy:     requestBoolValue(c, "local_proxy"),
		Auth:           requestEscapedString(c, "auth"),
		Header:         requestEscapedString(c, "header"),
		RespHeader:     requestEscapedString(c, "resp_header"),
		HostChange:     requestEscapedString(c, "hostchange"),
		Remark:         requestEscapedString(c, "remark"),
		Location:       requestEscapedString(c, "location"),
		PathRewrite:    requestEscapedString(c, "path_rewrite"),
		RedirectURL:    requestEscapedString(c, "redirect_url"),
		UploadLimit:    requestIntValue(c, "upload_limit"),
		DownloadLimit:  requestIntValue(c, "download_limit"),
		FlowLimit:      int64(requestIntValue(c, "flow_limit")),
		TimeLimit:      requestEscapedString(c, "time_limit"),
		Scheme:         requestEscapedString(c, "scheme"),
		HTTPSJustProxy: requestBoolValue(c, "https_just_proxy"),
		TLSOffload:     requestBoolValue(c, "tls_offload"),
		AutoSSL:        requestBoolValue(c, "auto_ssl"),
		KeyFile:        requestEscapedString(c, "key_file"),
		CertFile:       requestEscapedString(c, "cert_file"),
		AutoHTTPS:      requestBoolValue(c, "auto_https"),
		AutoCORS:       requestBoolValue(c, "auto_cors"),
		CompatMode:     requestBoolValue(c, "compat_mode"),
		TargetIsHTTPS:  requestBoolValue(c, "target_is_https"),
	}
}

func editHostInput(c Context, cfg *servercfg.Snapshot) webservice.EditHostInput {
	isAdmin := sessionBool(c, "isAdmin")
	return webservice.EditHostInput{
		ID:             requestIntValue(c, "id"),
		IsAdmin:        isAdmin,
		AllowUserLocal: indexAllowLocal(cfg, isAdmin),
		ClientID:       requestIntValue(c, "client_id"),
		Access:         requestAccessLists(c, ""),
		Host:           requestEscapedString(c, "host"),
		Target:         requestEscapedString(c, "target"),
		ProxyProtocol:  requestIntValue(c, "proxy_protocol"),
		LocalProxy:     requestBoolValue(c, "local_proxy"),
		Auth:           requestEscapedString(c, "auth"),
		Header:         requestEscapedString(c, "header"),
		RespHeader:     requestEscapedString(c, "resp_header"),
		HostChange:     requestEscapedString(c, "hostchange"),
		Remark:         requestEscapedString(c, "remark"),
		Location:       requestEscapedString(c, "location"),
		PathRewrite:    requestEscapedString(c, "path_rewrite"),
		RedirectURL:    requestEscapedString(c, "redirect_url"),
		UploadLimit:    requestIntValue(c, "upload_limit"),
		DownloadLimit:  requestIntValue(c, "download_limit"),
		FlowLimit:      int64(requestIntValue(c, "flow_limit")),
		TimeLimit:      requestEscapedString(c, "time_limit"),
		ResetFlow:      requestBoolValue(c, "flow_reset"),
		Scheme:         requestEscapedString(c, "scheme"),
		HTTPSJustProxy: requestBoolValue(c, "https_just_proxy"),
		TLSOffload:     requestBoolValue(c, "tls_offload"),
		AutoSSL:        requestBoolValue(c, "auto_ssl"),
		KeyFile:        requestEscapedString(c, "key_file"),
		CertFile:       requestEscapedString(c, "cert_file"),
		AutoHTTPS:      requestBoolValue(c, "auto_https"),
		AutoCORS:       requestBoolValue(c, "auto_cors"),
		CompatMode:     requestBoolValue(c, "compat_mode"),
		TargetIsHTTPS:  requestBoolValue(c, "target_is_https"),
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
	*file.Tunnel
	NowConn      int32
	UploadRate   int64
	DownloadRate int64
}

type hostRateRow struct {
	*file.Host
	NowConn      int32
	UploadRate   int64
	DownloadRate int64
}

func tunnelRateRows(rows []*file.Tunnel) []*tunnelRateRow {
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

func hostRateRows(rows []*file.Host) []*hostRateRow {
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
