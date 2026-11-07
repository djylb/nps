package routers

import (
	"encoding/json"
	"net/http"
	"os"
	"slices"
	"testing"

	"github.com/djylb/nps/lib/servercfg"
	webapi "github.com/djylb/nps/web/api"
	webservice "github.com/djylb/nps/web/service"
	"github.com/gin-gonic/gin"
)

type reloadRuntime struct{ webservice.Runtime }

func (reloadRuntime) ReloadConfig() servercfg.Report {
	return servercfg.Reload(
		servercfg.Applier{Name: "log level", Settings: []string{"log_level"}},
		servercfg.Applier{Name: "web listener", Settings: []string{"web_port"}, Apply: servercfg.RestartRequired},
	)
}

func TestReloadConfigHTTP(t *testing.T) {
	f := newRequestParamFixture(t)
	state := NewState(webapi.NewWithOptions(nil, webapi.Options{Store: f.db}))
	backend := webservice.NewBackend(f.db, nil)
	backend.Runtime = reloadRuntime{backend.Runtime}
	state.App.Services.System = webservice.DefaultSystemService{Runtime: backend.Runtime}
	engine := gin.New()
	group := engine.Group("")
	group.Use(sessionMiddleware(), requestContextMiddleware(state), protectedRouteMiddleware(state))
	registerProtectedRoutes(group, state)
	admin := sessionCookiesFromIdentity(t, servercfg.Current(), (&webservice.SessionIdentity{Authenticated: true, IsAdmin: true, Username: "admin", Roles: []string{"admin"}}).Normalize())

	path := servercfg.Path()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(content, "log_level=debug\nweb_port=9999\n"...), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, cookies := range [][]*http.Cookie{nil, f.cookies} {
		if denied := performFormRequest(engine, http.MethodPost, "/api/v1/global/reload", cookies, nil); denied.Code == 200 {
			t.Fatalf("reload by a non-admin: %d %s", denied.Code, denied.Body.String())
		}
	}
	if servercfg.Current().Log.Level == "debug" {
		t.Fatal("a denied reload took effect")
	}
	response := performFormRequest(engine, http.MethodPost, "/api/v1/global/reload", admin, nil)
	var body webapi.ReloadConfigResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); response.Code != 200 || err != nil {
		t.Fatalf("reload: %d %s", response.Code, response.Body.String())
	}
	if body.Status != 1 || !slices.Equal(body.Report.Applied, []string{"log_level"}) || !slices.Equal(body.Report.Restart, []string{"web_port"}) {
		t.Fatalf("reload answer: %s", response.Body.String())
	}
}
