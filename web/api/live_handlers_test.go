package api

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/djylb/nps/lib/model"

	webservice "github.com/djylb/nps/web/service"
)

type liveContext struct {
	rateResponseContext
	actor  *Actor
	status int
}

func (c *liveContext) Actor() *Actor                    { return c.actor }
func (c *liveContext) SetResponseHeader(string, string) {}
func (c *liveContext) RespondJSON(status int, v any)    { c.status = status; c.response = v }

type liveClients struct {
	webservice.ClientService
	clients map[int]*model.Client
}

func (s liveClients) Get(id int) (*model.Client, error) {
	c, ok := s.clients[id]
	if !ok {
		return nil, errors.New("missing")
	}
	return c, nil
}

type liveIndex struct {
	webservice.IndexService
	tunnels map[int]*model.Tunnel
	hosts   map[int]*model.Host
}

func (s liveIndex) GetTunnel(id int) (*model.Tunnel, error) {
	v, ok := s.tunnels[id]
	if !ok {
		return nil, errors.New("missing")
	}
	return v, nil
}
func (s liveIndex) GetHost(id int) (*model.Host, error) {
	v, ok := s.hosts[id]
	if !ok {
		return nil, errors.New("missing")
	}
	return v, nil
}
func TestLiveStatusFieldsAndOwnership(t *testing.T) {
	owner := &model.Client{Id: 1, VerifyKey: "secret", Flow: &model.Flow{InletFlow: 123, ExportFlow: 456}, Status: true}
	other := &model.Client{Id: 2, Flow: &model.Flow{}}
	virtual := &model.Client{Id: -1, Flow: &model.Flow{}}
	app := &App{Services: webservice.Services{Authz: webservice.DefaultAuthorizationService{}, Clients: liveClients{clients: map[int]*model.Client{1: owner, 2: other, -1: virtual}}, Index: liveIndex{tunnels: map[int]*model.Tunnel{10: {Id: 10, Client: owner, Flow: owner.Flow}, 20: {Id: 20, Client: other}}, hosts: map[int]*model.Host{10: {Id: 10, Client: owner, Flow: owner.Flow}, 20: {Id: 20, Client: other}}}}}
	for _, kind := range []string{"client", "tunnel", "host"} {
		ids := []int{10, 20, 99}
		if kind == "client" {
			ids = []int{1, 2, -1, 99}
		}
		var items []map[string]any
		for _, id := range ids {
			items = append(items, map[string]any{"id": id, "fields": []string{"Flow.InletFlow"}})
		}
		raw, _ := json.Marshal(items)
		for _, admin := range []bool{false, true} {
			actor := UserActor("user", []int{1})
			want := 1
			if admin {
				actor = AdminActorWithFallback("admin", "admin")
				want = 2
				if kind == "client" {
					want = 3
				}
			}
			c := &liveContext{actor: actor, values: map[string]string{"resource": kind, "items": string(raw)}}
			app.LiveStatus(c)
			if c.status != 200 {
				t.Fatalf("%s admin=%v: %d %+v", kind, admin, c.status, c.response)
			}
			rows := c.response.(map[string]any)["rows"].([]map[string]any)
			if len(rows) != want {
				t.Fatalf("%s admin=%v: %+v", kind, admin, rows)
			}
			for _, row := range rows {
				if len(row) != 2 {
					t.Fatalf("unrequested fields: %+v", row)
				}
			}
			encoded, _ := json.Marshal(c.response)
			if strings.Contains(string(encoded), "secret") {
				t.Fatal("credential leaked")
			}
		}
	}

	c := &liveContext{actor: AdminActorWithFallback("admin", "admin"), values: map[string]string{"resource": "client", "items": `[{"id":1,"fields":["TotalFlow"]},{"id":2,"fields":["NowConn"]}]`, "global": "In"}}
	app.LiveStatus(c)
	if c.status != 200 {
		t.Fatalf("derived request: %+v", c.response)
	}
	payload := c.response.(map[string]any)
	rows := payload["rows"].([]map[string]any)
	if len(rows[0]) != 2 || rows[0]["TotalFlow"] != int64(579) || len(rows[1]) != 2 {
		t.Fatalf("per-row fields not respected: %+v", rows)
	}
	if len(payload["traffic"].(map[string]int64)) != 1 {
		t.Fatal("unrequested global fields returned")
	}
	for _, tc := range []struct {
		resource, items, global string
		actor                   *Actor
		want                    int
	}{
		{"client", `[{"id":1,"fields":["VerifyKey"]}]`, "", UserActor("u", []int{1}), 400},
		{"client", `[{"id":1,"fields":["Status"]}]`, "In", UserActor("u", []int{1}), 403},
		{"client", `[{"id":1,"fields":["Status"]}]`, "", AnonymousActor(), 403},
		{"client", `[{"id":1,"fields":["Status"]},{"id":1,"fields":["Status"]}]`, "", UserActor("u", []int{1}), 400},
		{"client", `[]`, "VerifyKey", AdminActorWithFallback("admin", "admin"), 400},
	} {
		c := &liveContext{actor: tc.actor, values: map[string]string{"resource": tc.resource, "items": tc.items, "global": tc.global}}
		app.LiveStatus(c)
		if c.status != tc.want {
			t.Fatalf("got %d want %d: %+v", c.status, tc.want, tc)
		}
	}
}
