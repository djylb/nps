package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/rate"
	"github.com/djylb/nps/server"
	webservice "github.com/djylb/nps/web/service"
)

// LiveStatus accepts only explicit runtime fields, never configuration or credentials.
// Each resource is checked against the current owner, not a caller-supplied client ID.
func (a *App) LiveStatus(c Context) {
	principal := PrincipalFromActor(c.Actor())
	kind := c.String("resource")
	permission := map[string]string{"client": webservice.PermissionClientsRead, "tunnel": webservice.PermissionTunnelsRead, "host": webservice.PermissionHostsRead}[kind]
	if permission == "" {
		c.RespondJSON(400, map[string]string{"error": "invalid resource"})
		return
	}
	if a.Services.Authz.RequirePermission(principal, permission) != nil {
		c.RespondJSON(http.StatusForbidden, map[string]string{"error": "forbidden"})
		return
	}
	allowed := map[string]bool{"Speed": true, "UploadRate": true, "DownloadRate": true, "Flow.InletFlow": true, "Flow.ExportFlow": true, "TotalFlow": true, "ActualFlow": true, "FlowRemain": true, "NowConn": true, "Client.IsConnect": kind != "client", "IsConnect": kind == "client", "Status": kind != "host", "RunStatus": kind == "tunnel", "IsClose": kind == "host", "ConnectionHistory": kind == "client", "LastOnlineTime": kind == "client"}
	type item struct {
		ID     int      `json:"id"`
		Fields []string `json:"fields"`
	}
	var items []item
	if len(c.String("items")) > 65536 || json.Unmarshal([]byte(c.String("items")), &items) != nil || len(items) > 500 {
		c.RespondJSON(400, map[string]string{"error": "invalid items"})
		return
	}
	seen := map[int]bool{}
	for _, it := range items {
		if it.ID == 0 || seen[it.ID] || len(it.Fields) == 0 || len(it.Fields) > 16 {
			c.RespondJSON(400, map[string]string{"error": "invalid item"})
			return
		}
		seen[it.ID] = true
		for _, f := range it.Fields {
			if !allowed[f] {
				c.RespondJSON(400, map[string]string{"error": "invalid field"})
				return
			}
		}
	}
	wantGlobal := c.String("global") != ""
	globalFields := strings.Split(c.String("global"), ",")
	if wantGlobal {
		if len(globalFields) > 4 {
			c.RespondJSON(400, map[string]string{"error": "too many global fields"})
			return
		}
		if kind != "client" || a.Services.Authz.RequireAdmin(principal) != nil {
			c.RespondJSON(403, map[string]string{"error": "forbidden"})
			return
		}
		for _, f := range globalFields {
			if f != "In" && f != "Out" && f != "InBps" && f != "OutBps" {
				c.RespondJSON(400, map[string]string{"error": "invalid global field"})
				return
			}
		}
	}
	rows := make([]map[string]interface{}, 0, len(items))
	admin := a.Services.Authz.RequireAdmin(principal) == nil
	for _, it := range items {
		id := it.ID
		var client *file.Client
		var flow *file.Flow
		var rates func() (int64, int64)
		var connections func() int32
		var enabled, closed bool
		var history *file.ConnectionHistory
		switch kind {
		case "client":
			v, err := a.Services.Clients.Get(id)
			if err != nil || v == nil {
				continue
			}
			client = v
			flow = v.Flow
			rates = v.CurrentRates
			connections = v.CurrentConnections
			enabled = v.Status
			history = v.ConnectionHistory
		case "tunnel":
			v, err := a.Services.Index.GetTunnel(id)
			if err != nil || v == nil {
				continue
			}
			client = v.Client
			flow = v.Flow
			rates = v.CurrentRates
			connections = v.CurrentConnections
			enabled = v.Status
		case "host":
			v, err := a.Services.Index.GetHost(id)
			if err != nil || v == nil {
				continue
			}
			client = v.Client
			flow = v.Flow
			rates = v.CurrentRates
			connections = v.CurrentConnections
			closed = v.IsClose
		}
		if client == nil || (!admin && a.Services.Authz.RequireClient(principal, client.Id) != nil) {
			continue
		}
		row := map[string]interface{}{"Id": id}
		var up, down int64
		ratesRead := false
		var values file.FlowValues
		flowRead := false
		for _, f := range it.Fields {
			switch f {
			case "Speed", "UploadRate", "DownloadRate":
				if !ratesRead {
					up, down = rates()
					ratesRead = true
				}
				if f == "Speed" {
					row[f] = up + down
				} else if f == "UploadRate" {
					row[f] = up
				} else {
					row[f] = down
				}
			case "Flow.InletFlow", "Flow.ExportFlow", "TotalFlow", "ActualFlow", "FlowRemain":
				if !flowRead {
					values = flow.Values()
					flowRead = true
				}
				switch f {
				case "Flow.InletFlow":
					row[f] = values.InletFlow
				case "Flow.ExportFlow":
					row[f] = values.ExportFlow
				case "TotalFlow":
					row[f] = values.InletFlow + values.ExportFlow
				case "ActualFlow":
					row[f] = (values.InletFlow + values.ExportFlow) * 2
				case "FlowRemain":
					row[f] = values.FlowLimit*1024*1024 - values.Used
				}
			case "NowConn":
				row[f] = connections()
			case "Status":
				row[f] = enabled
			case "IsClose":
				row[f] = closed
			case "RunStatus":
				_, running := server.RunList.Load(id)
				row[f] = running
			case "IsConnect", "Client.IsConnect":
				row[f] = server.ClientConnected(client.Id)
			case "LastOnlineTime":
				row[f] = client.LastSeenTime()
			case "ConnectionHistory":
				row[f] = history
			}
		}
		rows = append(rows, row)
	}
	result := map[string]interface{}{"rows": rows}
	if wantGlobal {
		in, out := rate.GlobalTraffic()
		up, down := rate.GlobalThroughput()
		values := map[string]int64{"In": in, "Out": out, "InBps": up, "OutBps": down}
		traffic := map[string]int64{}
		for _, f := range globalFields {
			traffic[f] = values[f]
		}
		result["traffic"] = traffic
	}
	c.SetResponseHeader("Cache-Control", "no-store")
	c.RespondJSON(200, result)
}
