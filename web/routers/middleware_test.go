package routers

import (
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
