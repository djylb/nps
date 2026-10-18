package api

import (
	"errors"

	"github.com/djylb/nps/lib/common"
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
	respondClientList(c, result.Rows, result.Total, result.BridgeIP, result.BridgeAddr, result.BridgeType, result.BridgePort)
}

func (a *App) AddClient(c Context) {
	id, err := a.Services.Clients.Add(webservice.AddClientInput{
		VKey:             requestEscapedString(c, "vkey"),
		Remark:           requestEscapedString(c, "remark"),
		User:             requestEscapedString(c, "u"),
		Password:         requestEscapedString(c, "p"),
		Compress:         common.GetBoolByStr(requestEscapedString(c, "compress")),
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
	a.Emit(c, Event{
		Name:     "client.created",
		Resource: "client",
		Action:   "create",
		Fields:   map[string]interface{}{"id": id},
	})
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
		respondCodeData(c, 1, client)
	}
}

func (a *App) EditClient(c Context) {
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
		Compress:                common.GetBoolByStr(requestEscapedString(c, "compress")),
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
		case errors.Is(err, webservice.ErrInvalidAccessList):
			ajax(c, err.Error(), 0)
		case errors.Is(err, webservice.ErrRegisterKeyDuplicate):
			ajax(c, "registration key duplicate, please reset", 0)
		default:
			ajax(c, "client ID not found", 0)
		}
		return
	}
	a.Emit(c, Event{
		Name:     "client.updated",
		Resource: "client",
		Action:   "update",
		Fields:   map[string]interface{}{"id": requestIntValue(c, "id")},
	})
	ajax(c, "save success", 1)
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

func (a *App) DeleteClient(c Context) {
	id := requestIntValue(c, "id")
	if err := a.Services.Clients.Delete(id); err != nil {
		ajax(c, "delete error", 0)
		return
	}
	a.Emit(c, Event{
		Name:     "client.deleted",
		Resource: "client",
		Action:   "delete",
		Fields:   map[string]interface{}{"id": id},
	})
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
		c.RespondString(500, "QR encode failed")
		return
	}
	c.RespondData(200, "image/png", png)
}
