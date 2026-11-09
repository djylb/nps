package api

import (
	"errors"

	"github.com/djylb/nps/lib/model"
	"github.com/djylb/nps/lib/rate"
	webservice "github.com/djylb/nps/web/service"
)

func (a *App) ListClients(c Context) {
	scope := a.Services.Authz.ClientScope(PrincipalFromActor(c.Actor()))
	result := a.Services.Clients.List(webservice.ListClientsInput{
		Offset: requestIntValue(c, "offset"),
		Limit:  requestIntValue(c, "limit"),
		Search: requestEscapedString(c, "search"),
		Sort:   requestEscapedString(c, "sort"),
		Order:  requestEscapedString(c, "order"),
		Host:   c.Host(),
		Visibility: webservice.ClientVisibility{
			IsAdmin:         scope.All,
			PrimaryClientID: scope.PrimaryClientID,
			ClientIDs:       append([]int(nil), scope.ClientIDs...),
		},
	})
	var traffic *GlobalTrafficPayload
	if scope.All {
		in, out := rate.GlobalTraffic()
		inBps, outBps := rate.GlobalThroughput()
		traffic = &GlobalTrafficPayload{In: in, Out: out, InBps: inBps, OutBps: outBps, Limit: rate.GlobalLimit()}
	}
	respondClientList(c, clientRateRows(result.Rows), result.Total, result.BridgeIP, result.BridgeAddr, result.BridgeType, result.BridgePort, traffic)
}

// requestClientCompress is the compression of a client from the single
// compress choice (0/1 or an algorithm) and the older compress_alg.
func requestClientCompress(c Context) (bool, string) {
	return model.ParseClientCompress(requestEscapedString(c, "compress"), requestEscapedString(c, "compress_alg"))
}

func (a *App) AddClient(c Context) {
	compress, compressAlg := requestClientCompress(c)
	id, err := a.Services.Clients.Add(webservice.AddClientInput{
		VKey:             requestEscapedString(c, "vkey"),
		Remark:           requestEscapedString(c, "remark"),
		User:             requestEscapedString(c, "u"),
		Password:         requestEscapedString(c, "p"),
		Compress:         compress,
		CompressAlg:      compressAlg,
		SchedulerDown:    requestEscapedString(c, "scheduler_down"),
		SchedulerUp:      requestEscapedString(c, "scheduler_up"),
		Shaping:          requestBoolValue(c, "shaping"),
		Crypt:            requestBoolValue(c, "crypt"),
		ConfigConnAllow:  requestBoolValue(c, "config_conn_allow"),
		RateLimit:        requestIntValue(c, "rate_limit"),
		MaxConn:          requestIntValue(c, "max_conn"),
		WebUserName:      requestEscapedString(c, "web_username"),
		WebPassword:      requestEscapedString(c, "web_password"),
		WebTotpSecret:    requestEscapedString(c, "web_totp_secret"),
		MaxTunnelNum:     requestIntValue(c, "max_tunnel"),
		FlowLimit:        int64(requestIntValue(c, "flow_limit")),
		TimeLimit:        requestEscapedString(c, "time_limit"),
		Access:           requestAccessLists(c, ""),
		RegisterKey:      requestString(c, "register_key"),
		RegisterMaxHours: requestIntValue(c, "register_max_hours"),
	})
	if err != nil {
		ajax(c, err.Error(), 0)
		return
	}
	ajaxWithID(c, "add success", 1, id)
}

func (a *App) PingClient(c Context) {
	id := requestIntValue(c, "id")
	rtt, err := a.Services.Clients.Ping(id, c.RemoteAddr())
	if err != nil {
		respondCode(c, 0)
		return
	}
	respondCodeRTT(c, 1, rtt)
}

func (a *App) GetClient(c Context) {
	id := requestIntValue(c, "id")
	if client, err := a.Services.Clients.Get(id); err != nil {
		respondCode(c, 0)
	} else {
		respondCodeData(c, 1, clientRateRows([]*model.Client{client})[0])
	}
}

func (a *App) EditClient(c Context) {
	compress, compressAlg := requestClientCompress(c)
	cfg := a.currentConfig()
	if err := a.Services.Clients.Edit(webservice.EditClientInput{
		ID:                      requestIntValue(c, "id"),
		IsAdmin:                 sessionBool(c, "isAdmin"),
		AllowUserChangeUsername: cfg.Feature.AllowUserChangeUsername,
		ReservedAdminUsername:   cfg.Web.Username,
		VKey:                    requestEscapedString(c, "vkey"),
		Remark:                  requestEscapedString(c, "remark"),
		User:                    requestEscapedString(c, "u"),
		Password:                requestEscapedString(c, "p"),
		Compress:                compress,
		CompressAlg:             compressAlg,
		SchedulerDown:           requestEscapedString(c, "scheduler_down"),
		SchedulerUp:             requestEscapedString(c, "scheduler_up"),
		Shaping:                 requestBoolValue(c, "shaping"),
		Crypt:                   requestBoolValue(c, "crypt"),
		ConfigConnAllow:         requestBoolValue(c, "config_conn_allow"),
		RateLimit:               requestIntValue(c, "rate_limit"),
		MaxConn:                 requestIntValue(c, "max_conn"),
		WebUserName:             requestEscapedString(c, "web_username"),
		WebPassword:             requestEscapedString(c, "web_password"),
		WebTotpSecret:           requestEscapedString(c, "web_totp_secret"),
		MaxTunnelNum:            requestIntValue(c, "max_tunnel"),
		FlowLimit:               int64(requestIntValue(c, "flow_limit")),
		TimeLimit:               requestEscapedString(c, "time_limit"),
		ResetFlow:               requestBoolValue(c, "flow_reset"),
		Access:                  requestAccessLists(c, ""),
		RegisterKey:             requestString(c, "register_key"),
		RegisterMaxHours:        requestIntValue(c, "register_max_hours"),
	}); err != nil {
		switch {
		case errors.Is(err, webservice.ErrWebUsernameDuplicate):
			ajax(c, "web login username duplicate, please reset", 0)
		case errors.Is(err, webservice.ErrClientVKeyDuplicate):
			ajax(c, "Vkey duplicate, please reset", 0)
		case errors.Is(err, webservice.ErrInvalidAccessList), errors.Is(err, webservice.ErrInvalidScheduler):
			ajax(c, err.Error(), 0)
		case errors.Is(err, webservice.ErrRegisterKeyDuplicate):
			ajax(c, "registration key duplicate, please reset", 0)
		default:
			ajax(c, "client ID not found", 0)
		}
		return
	}
	ajax(c, "save success", 1)
}

// GetClientLaunch returns the saved start command form.
func (a *App) GetClientLaunch(c Context) {
	client, err := a.Services.Clients.Get(requestIntValue(c, "id"))
	if err != nil {
		respondCode(c, 0)
		return
	}
	respondCodeData(c, 1, client.LaunchForm)
}

// SaveClientLaunch stores the start command form; an empty form restores the default.
func (a *App) SaveClientLaunch(c Context) {
	respondLaunchSave(c, a.Services.Clients.SaveLaunchForm(requestIntValue(c, "id"), requestString(c, "form")))
}

func respondLaunchSave(c Context, err error) {
	switch {
	case errors.Is(err, webservice.ErrLaunchFormTooLarge):
		ajax(c, err.Error(), 0)
	case err != nil:
		ajax(c, "save error", 0)
	default:
		ajax(c, "save success", 1)
	}
}

func (a *App) ClearClient(c Context) {
	if err := a.Services.Clients.Clear(requestIntValue(c, "id"), requestEscapedString(c, "mode"), sessionBool(c, "isAdmin")); err != nil {
		ajax(c, "modified fail", 0)
		return
	}
	ajax(c, "modified success", 1)
}

func (a *App) ChangeClientStatus(c Context) {
	if err := a.Services.Clients.ChangeStatus(requestIntValue(c, "id"), requestBoolValue(c, "status")); err != nil {
		ajax(c, "modified fail", 0)
		return
	}
	ajax(c, "modified success", 1)
}

func (a *App) UpdateClient(c Context) {
	if _, err := a.Services.Clients.RequestUpdate(requestIntValue(c, "id")); err != nil {
		switch {
		case errors.Is(err, webservice.ErrClientOffline):
			ajax(c, "client is offline", 0)
		case errors.Is(err, webservice.ErrClientUpdateUnsupported):
			ajax(c, "client version does not support remote update", 0)
		case errors.Is(err, webservice.ErrClientNotFound):
			ajax(c, "client ID not found", 0)
		default:
			ajax(c, "update request failed", 0)
		}
		return
	}
	ajax(c, "update requested", 1)
}

func (a *App) KickClient(c Context) {
	if err := a.Services.Clients.Kick(requestIntValue(c, "id")); err != nil {
		switch {
		case errors.Is(err, webservice.ErrClientOffline):
			ajax(c, "client is offline", 0)
		case errors.Is(err, webservice.ErrClientNotFound):
			ajax(c, "client ID not found", 0)
		default:
			ajax(c, "modified fail", 0)
		}
		return
	}
	ajax(c, "client kicked", 1)
}

func (a *App) DeleteClient(c Context) {
	id := requestIntValue(c, "id")
	if err := a.Services.Clients.Delete(id); err != nil {
		ajax(c, "delete error", 0)
		return
	}
	ajax(c, "delete success", 1)
}

func (a *App) ClientQRCode(c Context) {
	cfg := a.currentConfig()
	png, err := a.Services.Clients.BuildQRCode(webservice.ClientQRCodeInput{
		Text:    requestString(c, "text"),
		Account: requestString(c, "account"),
		Secret:  requestString(c, "secret"),
		AppName: cfg.App.Name,
	})
	if err != nil {
		if errors.Is(err, webservice.ErrClientQRCodeTextRequired) {
			c.RespondString(400, "missing text")
			return
		}
		if errors.Is(err, webservice.ErrClientQRCodeSecretInvalid) {
			c.RespondString(400, "invalid secret")
			return
		}
		c.RespondString(500, "QR encode failed")
		return
	}
	c.RespondData(200, "image/png", png)
}

// Directional speeds are response-only; the client's combined RateLimit remains unchanged.
type clientRateRow struct {
	*model.Client
	// LaunchForm is never set: it hides the client's large field from lists,
	// which only need HasLaunch to fetch it when a row is opened.
	LaunchForm   string `json:",omitempty"`
	HasLaunch    bool   `json:",omitempty"`
	NowConn      int32
	UploadRate   int64
	DownloadRate int64
}

func clientRateRows(rows []*model.Client) []*clientRateRow {
	if rows == nil {
		return nil
	}
	result := make([]*clientRateRow, len(rows))
	for i, row := range rows {
		if row == nil {
			continue
		}
		upload, download := row.CurrentRates()
		result[i] = &clientRateRow{Client: row, HasLaunch: row.LaunchForm != "", NowConn: row.CurrentConnections(), UploadRate: upload, DownloadRate: download}
	}
	return result
}
