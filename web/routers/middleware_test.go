package routers

import (
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/servercfg"
	"github.com/djylb/nps/web/controllers"
	webservice "github.com/djylb/nps/web/service"
	"github.com/gin-gonic/gin"
)

type requestParamFixture struct {
	handler http.Handler
	cookies []*http.Cookie
	clientA *file.Client
	clientB *file.Client
	clientC *file.Client
	hostOfB *file.Host
}

func newRequestParamFixture(t *testing.T) *requestParamFixture {
	t.Helper()
	oldConfPath := common.ConfPath
	common.ConfPath = testRepoRoot(t)
	t.Cleanup(func() { common.ConfPath = oldConfPath })

	resetTestDB(t)
	controllers.RemoveAllLoginBan()
	configPath := writeTestConfig(t, "nps.conf", strings.Join([]string{
		"web_username=admin",
		"web_password=secret",
		"web_base_url=",
		"allow_user_login=true",
		"open_captcha=false",
		"secure_mode=false",
	}, "\n")+"\n")
	if err := servercfg.Load(configPath); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	f := &requestParamFixture{}
	f.clientA = createTestClient(t, 101, "operator", "op-secret")
	f.clientB = createTestClient(t, 102, "operator-b", "op-secret-b")
	f.clientC = createTestClient(t, 103, "other", "other-secret")
	createTestTunnel(t, 201, f.clientA, 19081)
	createTestTunnel(t, 203, f.clientC, 19083)
	f.hostOfB = createTestHost(t, 301, f.clientB, "b.example.test")
	createTestHost(t, 302, f.clientC, "c.example.test")

	crypt.InitTls(tls.Certificate{})
	gin.SetMode(gin.TestMode)
	f.handler = Init()
	f.cookies = sessionCookiesFromIdentity(t, servercfg.Current(), (&webservice.SessionIdentity{
		Version:       webservice.SessionIdentityVersion,
		Authenticated: true,
		Kind:          "user",
		Provider:      "test",
		SubjectID:     "user:operator",
		Username:      "operator",
		ClientIDs:     []int{f.clientA.Id, f.clientB.Id},
		Roles:         []string{"user"},
	}).Normalize())
	return f
}

func performJSONRequest(handler http.Handler, path string, cookies []*http.Cookie, payload map[string]interface{}) *httptest.ResponseRecorder {
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	return resp
}

func TestRequestIntTrimsSpace(t *testing.T) {
	f := newRequestParamFixture(t)

	resp := performFormRequest(f.handler, http.MethodPost, "/api/v1/index/getonetunnel", f.cookies, map[string]string{"id": " 201 "})
	if resp.Code != http.StatusOK || !strings.Contains(resp.Body.String(), "\"code\":1") || !strings.Contains(resp.Body.String(), "\"Id\":201") {
		t.Fatalf("padded id: status=%d body=%s", resp.Code, resp.Body.String())
	}

	resp = performJSONRequest(f.handler, "/api/v1/index/getonetunnel", f.cookies, map[string]interface{}{"id": " 201"})
	if resp.Code != http.StatusOK || !strings.Contains(resp.Body.String(), "\"Id\":201") {
		t.Fatalf("padded json id: status=%d body=%s", resp.Code, resp.Body.String())
	}

	for _, id := range []string{" 203", "203 ", "\t203"} {
		resp = performFormRequest(f.handler, http.MethodPost, "/api/v1/index/getonetunnel", f.cookies, map[string]string{"id": id})
		if resp.Code != http.StatusForbidden {
			t.Fatalf("id %q status = %d, want 403", id, resp.Code)
		}
	}

	for _, path := range []string{"/api/v1/index/getonetunnel", "/api/v1/index/gethost", "/api/v1/client/getclient"} {
		resp = performFormRequest(f.handler, http.MethodPost, path, f.cookies, map[string]string{"id": "1x"})
		if resp.Code != http.StatusForbidden {
			t.Fatalf("%s with malformed id status = %d, want 403", path, resp.Code)
		}
	}

	resp = performFormRequest(f.handler, http.MethodGet, "/index/edit?id=%20203", f.cookies, nil)
	if resp.Code != http.StatusForbidden {
		t.Fatalf("edit page with padded id status = %d, want 403", resp.Code)
	}

	resp = performFormRequest(f.handler, http.MethodPost, "/api/v1/index/hostlist", f.cookies, map[string]string{"client_id": "1x"})
	if resp.Code != http.StatusForbidden {
		t.Fatalf("hostlist with malformed client_id status = %d, want 403", resp.Code)
	}
}

func TestEditHostClientSelection(t *testing.T) {
	f := newRequestParamFixture(t)
	base := map[string]interface{}{
		"id":     f.hostOfB.Id,
		"host":   f.hostOfB.Host,
		"target": "127.0.0.1:80",
	}
	with := func(extra map[string]interface{}) map[string]interface{} {
		payload := make(map[string]interface{}, len(base)+len(extra))
		for k, v := range base {
			payload[k] = v
		}
		for k, v := range extra {
			payload[k] = v
		}
		return payload
	}
	ownerOf := func() int {
		host, err := file.GetDb().GetHostById(f.hostOfB.Id)
		if err != nil {
			t.Fatalf("GetHostById() error = %v", err)
		}
		return host.Client.Id
	}

	resp := performJSONRequest(f.handler, "/api/v1/index/edithost", f.cookies, with(map[string]interface{}{"client_id": f.clientC.Id}))
	if resp.Code != http.StatusForbidden {
		t.Fatalf("json client_id outside scope status = %d, want 403", resp.Code)
	}
	resp = performFormRequest(f.handler, http.MethodPost, "/api/v1/index/edithost", f.cookies, map[string]string{
		"id": "301", "host": f.hostOfB.Host, "target": "127.0.0.1:80", "client_id": "103",
	})
	if resp.Code != http.StatusForbidden {
		t.Fatalf("form client_id outside scope status = %d, want 403", resp.Code)
	}
	if got := ownerOf(); got != f.clientB.Id {
		t.Fatalf("host client = %d, want %d", got, f.clientB.Id)
	}

	resp = performJSONRequest(f.handler, "/api/v1/index/edithost", f.cookies, with(nil))
	if resp.Code != http.StatusOK || !strings.Contains(resp.Body.String(), "\"status\":1") {
		t.Fatalf("edit without client_id: status=%d body=%s", resp.Code, resp.Body.String())
	}
	if got := ownerOf(); got != f.clientB.Id {
		t.Fatalf("host client after edit without client_id = %d, want %d", got, f.clientB.Id)
	}

	resp = performJSONRequest(f.handler, "/api/v1/index/edithost", f.cookies, with(map[string]interface{}{"client_id": f.clientA.Id}))
	if resp.Code != http.StatusOK || !strings.Contains(resp.Body.String(), "\"status\":1") {
		t.Fatalf("edit to own client: status=%d body=%s", resp.Code, resp.Body.String())
	}
	if got := ownerOf(); got != f.clientA.Id {
		t.Fatalf("host client after move = %d, want %d", got, f.clientA.Id)
	}

	f.clientB.MaxTunnelNum = 1
	createTestHost(t, 304, f.clientB, "b2.example.test")
	resp = performJSONRequest(f.handler, "/api/v1/index/edithost", f.cookies, with(map[string]interface{}{"client_id": f.clientB.Id}))
	if !strings.Contains(resp.Body.String(), "\"status\":0") {
		t.Fatalf("edit to client at limit should fail, status=%d body=%s", resp.Code, resp.Body.String())
	}
	if got := ownerOf(); got != f.clientA.Id {
		t.Fatalf("host client after limited move = %d, want %d", got, f.clientA.Id)
	}
}

// The service restarts tunnels after editing; keep this test independent of
// listeners so that it exercises the real request and persistence paths.
type virtualClientEditRuntime struct{ webservice.Runtime }

func (virtualClientEditRuntime) StopTunnel(int) error  { return nil }
func (virtualClientEditRuntime) StartTunnel(int) error { return nil }

func TestEditResourceVirtualClientSelection(t *testing.T) {
	for _, resource := range []string{"tunnel", "host"} {
		t.Run(resource, func(t *testing.T) {
			f := newRequestParamFixture(t)
			createTestClient(t, -1, "", "")
			createTestClient(t, -2, "", "")
			state := NewState(nil)
			backend := webservice.DefaultBackend()
			backend.Runtime = virtualClientEditRuntime{backend.Runtime}
			state.App.Services.Index = webservice.DefaultIndexService{Backend: backend}
			engine := gin.New()
			group := engine.Group("")
			group.Use(sessionMiddleware(), requestContextMiddleware(state), protectedRouteMiddleware(state))
			registerIndexRoutes(group, state)
			adminCookies := sessionCookiesFromIdentity(t, servercfg.Current(), (&webservice.SessionIdentity{
				Authenticated: true, IsAdmin: true, Username: "admin", Roles: []string{"admin"},
			}).Normalize())
			action, id := "edit", 201
			if resource == "host" {
				action, id = "edithost", 301
			}
			payload := map[string]interface{}{"id": id, "target": "127.0.0.1:80"}
			if resource == "host" {
				payload["host"] = f.hostOfB.Host
			}
			owner := func() int {
				if resource == "host" {
					h, err := file.GetDb().GetHostById(id)
					if err != nil {
						t.Fatal(err)
					}
					return h.Client.Id
				}
				task, err := file.GetDb().GetTask(id)
				if err != nil {
					t.Fatal(err)
				}
				return task.Client.Id
			}
			original := owner()
			payload["client_id"] = -1
			denied := performJSONRequest(engine, "/api/v1/index/"+action, f.cookies, payload)
			if denied.Code != http.StatusForbidden || owner() != original {
				t.Fatalf("non-admin virtual client move: status=%d owner=%d body=%s", denied.Code, owner(), denied.Body.String())
			}
			resp := performJSONRequest(engine, "/api/v1/index/"+action, adminCookies, payload)
			if resp.Code != http.StatusOK || !strings.Contains(resp.Body.String(), `"status":1`) || owner() != -1 {
				t.Fatalf("admin virtual client move: status=%d owner=%d body=%s", resp.Code, owner(), resp.Body.String())
			}
			for _, unset := range []bool{true, false} {
				delete(payload, "client_id")
				if !unset {
					payload["client_id"] = 0
				}
				resp = performJSONRequest(engine, "/api/v1/index/"+action, adminCookies, payload)
				if !strings.Contains(resp.Body.String(), `"status":1`) || owner() != -1 {
					t.Fatalf("unset client changed virtual owner: owner=%d body=%s", owner(), resp.Body.String())
				}
			}
			payload["client_id"] = -999
			resp = performJSONRequest(engine, "/api/v1/index/"+action, adminCookies, payload)
			if !strings.Contains(resp.Body.String(), `"status":0`) || owner() != -1 {
				t.Fatalf("missing virtual client accepted: owner=%d body=%s", owner(), resp.Body.String())
			}
			form := map[string]string{"id": strconv.Itoa(id), "target": "127.0.0.1:80", "client_id": "-2"}
			if resource == "host" {
				form["host"] = f.hostOfB.Host
			}
			resp = performFormRequest(engine, http.MethodPost, "/index/"+action, adminCookies, form)
			if !strings.Contains(resp.Body.String(), `"status":1`) || owner() != -2 {
				t.Fatalf("form virtual client move: owner=%d body=%s", owner(), resp.Body.String())
			}
			payload["client_id"] = f.clientA.Id
			resp = performJSONRequest(engine, "/api/v1/index/"+action, adminCookies, payload)
			if !strings.Contains(resp.Body.String(), `"status":1`) || owner() != f.clientA.Id {
				t.Fatalf("move back to regular client: owner=%d body=%s", owner(), resp.Body.String())
			}
		})
	}
}
