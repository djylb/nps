package routers

import (
	"net/http"
	"strings"
	"testing"

	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/servercfg"
	webservice "github.com/djylb/nps/web/service"
	"github.com/gin-gonic/gin"
)

type virtualManagementRuntime struct{ webservice.Runtime }

func (virtualManagementRuntime) DisconnectClient(int) {}

func TestVirtualClientManagementHTTP(t *testing.T) {
	f := newRequestParamFixture(t)
	virtual := createTestClient(t, -1, "", "")
	state := NewState(nil)
	backend := webservice.DefaultBackend()
	backend.Runtime = virtualManagementRuntime{backend.Runtime}
	state.App.Services.Clients = webservice.DefaultClientService{Backend: backend}
	engine := gin.New()
	group := engine.Group("")
	group.Use(sessionMiddleware(), requestContextMiddleware(state), protectedRouteMiddleware(state))
	registerProtectedRoutes(group, state)
	admin := sessionCookiesFromIdentity(t, servercfg.Current(), (&webservice.SessionIdentity{Authenticated: true, IsAdmin: true, Username: "admin", Roles: []string{"admin"}}).Normalize())
	page := performFormRequest(engine, http.MethodGet, "/client/edit?id=-1", admin, nil)
	if page.Code != 200 || !strings.Contains(page.Body.String(), `value="-1"`) {
		t.Fatalf("virtual edit page: %d %s", page.Code, page.Body.String())
	}
	for _, prefix := range []string{"/client/", "/api/v1/client/"} {
		response := performFormRequest(engine, http.MethodPost, prefix+"getclient", admin, map[string]string{"id": "-1"})
		if response.Code != 200 || !strings.Contains(response.Body.String(), `"Id":-1`) {
			t.Fatalf("virtual read: %d %s", response.Code, response.Body.String())
		}
		denied := performFormRequest(engine, http.MethodPost, prefix+"getclient", f.cookies, map[string]string{"id": "-1"})
		if denied.Code != 403 {
			t.Fatalf("ordinary user read virtual: %d", denied.Code)
		}
		edited := performFormRequest(engine, http.MethodPost, prefix+"edit", admin, map[string]string{"id": "-1", "vkey": virtual.VerifyKey, "remark": "virtual edited", "rate_limit": "128", "max_conn": "5"})
		if edited.Code != 200 || !strings.Contains(edited.Body.String(), `"status":1`) || virtual.Remark != "virtual edited" || virtual.MaxConn != 5 {
			t.Fatalf("virtual save: %d %s", edited.Code, edited.Body.String())
		}
		for _, enabled := range []string{"0", "1"} {
			response = performFormRequest(engine, http.MethodPost, prefix+"changestatus", admin, map[string]string{"id": "-1", "status": enabled})
			if response.Code != 200 || !strings.Contains(response.Body.String(), `"status":1`) || virtual.Status != (enabled == "1") {
				t.Fatalf("virtual status: %d %s", response.Code, response.Body.String())
			}
		}
		for _, action := range []string{"del", "update"} {
			response = performFormRequest(engine, http.MethodPost, prefix+action, admin, map[string]string{"id": "-1"})
			if response.Code != 200 || !strings.Contains(response.Body.String(), `"status":0`) {
				t.Fatalf("virtual %s not blocked: %d %s", action, response.Code, response.Body.String())
			}
		}
		if _, err := file.GetDb().GetClient(-1); err != nil {
			t.Fatal("virtual client was deleted")
		}
	}
	if _, err := file.GetDb().GetClientIdByBlake2bVkey(crypt.Blake2b(virtual.VerifyKey)); err == nil {
		t.Fatal("virtual edit enabled npc authentication")
	}
}
