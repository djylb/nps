package routers

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/djylb/nps/lib/acl"
	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/index"
	"github.com/djylb/nps/lib/ipreg"
	"github.com/djylb/nps/lib/rate"
	"github.com/djylb/nps/lib/servercfg"
	"github.com/djylb/nps/web/controllers"
	"github.com/djylb/nps/web/framework"
	webservice "github.com/djylb/nps/web/service"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/sessions"
)

func TestDirectPageRenderAndPageAPI(t *testing.T) {
	repoRoot := testRepoRoot(t)
	oldConfPath := common.ConfPath
	common.ConfPath = repoRoot
	defer func() {
		common.ConfPath = oldConfPath
	}()

	configPath := writeTestConfig(t, "nps.conf", strings.Join([]string{
		"web_username=admin",
		"web_password=secret",
		"web_base_url=",
		"allow_user_register=true",
		"open_captcha=false",
	}, "\n")+"\n")
	if err := servercfg.Load(configPath); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	crypt.InitTls(tls.Certificate{})
	gin.SetMode(gin.TestMode)
	handler := Init()

	pageResp := httptest.NewRecorder()
	pageReq := httptest.NewRequest(http.MethodGet, "/login/index", nil)
	handler.ServeHTTP(pageResp, pageReq)
	if pageResp.Code != http.StatusOK {
		t.Fatalf("GET /login/index status = %d, want 200", pageResp.Code)
	}
	if got := pageResp.Body.String(); !strings.Contains(got, "loginNonce") {
		t.Fatalf("GET /login/index should return rendered login html, got %q", got)
	}

	renderResp := httptest.NewRecorder()
	renderReq := httptest.NewRequest(http.MethodGet, "/auth/render/login/index", nil)
	handler.ServeHTTP(renderResp, renderReq)
	if renderResp.Code != http.StatusOK {
		t.Fatalf("GET /auth/render/login/index status = %d, want 200", renderResp.Code)
	}
	if got := renderResp.Body.String(); !strings.Contains(got, "loginNonce") {
		t.Fatalf("GET /auth/render/login/index should return rendered login html")
	}

	modelResp := httptest.NewRecorder()
	modelReq := httptest.NewRequest(http.MethodGet, "/auth/page/login/index", nil)
	handler.ServeHTTP(modelResp, modelReq)
	if modelResp.Code != http.StatusOK {
		t.Fatalf("GET /auth/page/login/index status = %d, want 200", modelResp.Code)
	}
	body := modelResp.Body.String()
	if !strings.Contains(body, "\"tpl_name\":\"login/index.html\"") {
		t.Fatalf("page model missing tpl_name: %s", body)
	}
	if !strings.Contains(body, "\"action\":\"Index\"") {
		t.Fatalf("page model missing action: %s", body)
	}
	if !strings.Contains(body, "\"page\":{\"controller\":\"login\"") {
		t.Fatalf("page model missing typed page metadata: %s", body)
	}
	if !strings.Contains(body, "\"direct_path\":\"/login/index\"") || !strings.Contains(body, "\"protected\":false") {
		t.Fatalf("page model missing page contract paths or protection flag: %s", body)
	}

	bootstrapResp := httptest.NewRecorder()
	bootstrapReq := httptest.NewRequest(http.MethodGet, "/auth/bootstrap", nil)
	handler.ServeHTTP(bootstrapResp, bootstrapReq)
	if bootstrapResp.Code != http.StatusOK {
		t.Fatalf("GET /auth/bootstrap status = %d, want 200", bootstrapResp.Code)
	}
	bootstrapBody := bootstrapResp.Body.String()
	if !strings.Contains(bootstrapBody, "\"page\":\"/auth/page\"") {
		t.Fatalf("bootstrap response missing auth page route: %s", bootstrapBody)
	}
	if !strings.Contains(bootstrapBody, "\"api_base\":\"/api/v1\"") {
		t.Fatalf("bootstrap response missing api base route: %s", bootstrapBody)
	}
	if !strings.Contains(bootstrapBody, "\"static_base\":\"/static\"") {
		t.Fatalf("bootstrap response missing static base route: %s", bootstrapBody)
	}
	if !strings.Contains(bootstrapBody, "\"management_api_base\":\"/api/v1\"") {
		t.Fatalf("bootstrap response missing management api route: %s", bootstrapBody)
	}
	if !strings.Contains(bootstrapBody, "\"app_shell\":\"/app\"") {
		t.Fatalf("bootstrap response missing app shell route: %s", bootstrapBody)
	}
	if !strings.Contains(bootstrapBody, "\"actor\"") {
		t.Fatalf("bootstrap response missing actor payload: %s", bootstrapBody)
	}
	if !strings.Contains(bootstrapBody, "\"cluster\"") {
		t.Fatalf("bootstrap response missing cluster extensions: %s", bootstrapBody)
	}
	if !strings.Contains(bootstrapBody, "\"allow_user_register\":true") {
		t.Fatalf("bootstrap response missing feature flags: %s", bootstrapBody)
	}
	if !strings.Contains(bootstrapBody, "\"api_path\":\"/api/v1/login/verify\"") {
		t.Fatalf("bootstrap response missing action catalog entries: %s", bootstrapBody)
	}
	if !strings.Contains(bootstrapBody, "\"shell_assets\"") {
		t.Fatalf("bootstrap response missing shell asset contract: %s", bootstrapBody)
	}

	apiBootstrapResp := httptest.NewRecorder()
	apiBootstrapReq := httptest.NewRequest(http.MethodGet, "/api/v1/auth/bootstrap", nil)
	handler.ServeHTTP(apiBootstrapResp, apiBootstrapReq)
	if apiBootstrapResp.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/auth/bootstrap status = %d, want 200", apiBootstrapResp.Code)
	}
	if got := apiBootstrapResp.Body.String(); !strings.Contains(got, "\"request\"") {
		t.Fatalf("GET /api/v1/auth/bootstrap should include request metadata: %s", got)
	}

	verifyGetResp := httptest.NewRecorder()
	verifyGetReq := httptest.NewRequest(http.MethodGet, "/login/verify", nil)
	handler.ServeHTTP(verifyGetResp, verifyGetReq)
	if verifyGetResp.Code != http.StatusNotFound {
		t.Fatalf("GET /login/verify status = %d, want 404", verifyGetResp.Code)
	}

	verifyV1GetResp := httptest.NewRecorder()
	verifyV1GetReq := httptest.NewRequest(http.MethodGet, "/api/v1/login/verify", nil)
	handler.ServeHTTP(verifyV1GetResp, verifyV1GetReq)
	if verifyV1GetResp.Code != http.StatusNotFound {
		t.Fatalf("GET /api/v1/login/verify status = %d, want 404", verifyV1GetResp.Code)
	}

	logoutV1Resp := httptest.NewRecorder()
	logoutV1Req := httptest.NewRequest(http.MethodPost, "/api/v1/login/logout", nil)
	handler.ServeHTTP(logoutV1Resp, logoutV1Req)
	if logoutV1Resp.Code != http.StatusOK {
		t.Fatalf("POST /api/v1/login/logout status = %d, want 200", logoutV1Resp.Code)
	}
	if got := logoutV1Resp.Body.String(); !strings.Contains(got, "\"msg\":\"logout success\"") {
		t.Fatalf("POST /api/v1/login/logout should return json, got %s", got)
	}

	getTimePostResp := httptest.NewRecorder()
	getTimePostReq := httptest.NewRequest(http.MethodPost, "/auth/gettime", nil)
	handler.ServeHTTP(getTimePostResp, getTimePostReq)
	if getTimePostResp.Code != http.StatusNotFound {
		t.Fatalf("POST /auth/gettime status = %d, want 404", getTimePostResp.Code)
	}

	getTimeResp := httptest.NewRecorder()
	getTimeReq := httptest.NewRequest(http.MethodGet, "/api/v1/auth/gettime", nil)
	handler.ServeHTTP(getTimeResp, getTimeReq)
	if getTimeResp.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/auth/gettime status = %d, want 200", getTimeResp.Code)
	}
	if body := getTimeResp.Body.String(); !strings.Contains(body, "\"time\":") {
		t.Fatalf("GET /api/v1/auth/gettime should return typed time payload, got %s", body)
	}

	getTunnelGetResp := httptest.NewRecorder()
	getTunnelGetReq := httptest.NewRequest(http.MethodGet, "/index/gettunnel", nil)
	handler.ServeHTTP(getTunnelGetResp, getTunnelGetReq)
	if getTunnelGetResp.Code != http.StatusNotFound {
		t.Fatalf("GET /index/gettunnel status = %d, want 404", getTunnelGetResp.Code)
	}

	getTunnelPostResp := httptest.NewRecorder()
	getTunnelPostReq := httptest.NewRequest(http.MethodPost, "/index/gettunnel", nil)
	handler.ServeHTTP(getTunnelPostResp, getTunnelPostReq)
	if getTunnelPostResp.Code != http.StatusFound {
		t.Fatalf("POST /index/gettunnel status = %d, want 302", getTunnelPostResp.Code)
	}

	getTunnelV1GetResp := httptest.NewRecorder()
	getTunnelV1GetReq := httptest.NewRequest(http.MethodGet, "/api/v1/index/gettunnel", nil)
	handler.ServeHTTP(getTunnelV1GetResp, getTunnelV1GetReq)
	if getTunnelV1GetResp.Code != http.StatusNotFound {
		t.Fatalf("GET /api/v1/index/gettunnel status = %d, want 404", getTunnelV1GetResp.Code)
	}

	getTunnelV1PostResp := httptest.NewRecorder()
	getTunnelV1PostReq := httptest.NewRequest(http.MethodPost, "/api/v1/index/gettunnel", nil)
	handler.ServeHTTP(getTunnelV1PostResp, getTunnelV1PostReq)
	if getTunnelV1PostResp.Code != http.StatusUnauthorized {
		t.Fatalf("POST /api/v1/index/gettunnel status = %d, want 401", getTunnelV1PostResp.Code)
	}
	if !strings.Contains(getTunnelV1PostResp.Body.String(), "\"msg\":\"unauthorized\"") {
		t.Fatalf("POST /api/v1/index/gettunnel should return unauthorized json, got %s", getTunnelV1PostResp.Body.String())
	}

	protectedRenderResp := httptest.NewRecorder()
	protectedRenderReq := httptest.NewRequest(http.MethodGet, "/auth/render/index/index", nil)
	handler.ServeHTTP(protectedRenderResp, protectedRenderReq)
	if protectedRenderResp.Code != http.StatusFound {
		t.Fatalf("GET /auth/render/index/index status = %d, want 302", protectedRenderResp.Code)
	}

	appShellResp := httptest.NewRecorder()
	appShellReq := httptest.NewRequest(http.MethodGet, "/app", nil)
	handler.ServeHTTP(appShellResp, appShellReq)
	if appShellResp.Code != http.StatusFound {
		t.Fatalf("GET /app status = %d, want 302", appShellResp.Code)
	}

	rootResp := httptest.NewRecorder()
	rootReq := httptest.NewRequest(http.MethodGet, "/", nil)
	handler.ServeHTTP(rootResp, rootReq)
	if rootResp.Code != http.StatusFound {
		t.Fatalf("GET / status = %d, want 302", rootResp.Code)
	}

	getClientGetResp := httptest.NewRecorder()
	getClientGetReq := httptest.NewRequest(http.MethodGet, "/client/getclient", nil)
	handler.ServeHTTP(getClientGetResp, getClientGetReq)
	if getClientGetResp.Code != http.StatusNotFound {
		t.Fatalf("GET /client/getclient status = %d, want 404", getClientGetResp.Code)
	}

	getClientPostResp := httptest.NewRecorder()
	getClientPostReq := httptest.NewRequest(http.MethodPost, "/client/getclient", nil)
	handler.ServeHTTP(getClientPostResp, getClientPostReq)
	if getClientPostResp.Code != http.StatusFound {
		t.Fatalf("POST /client/getclient status = %d, want 302", getClientPostResp.Code)
	}

	unbanAllGetResp := httptest.NewRecorder()
	unbanAllGetReq := httptest.NewRequest(http.MethodGet, "/global/unbanall", nil)
	handler.ServeHTTP(unbanAllGetResp, unbanAllGetReq)
	if unbanAllGetResp.Code != http.StatusNotFound {
		t.Fatalf("GET /global/unbanall status = %d, want 404", unbanAllGetResp.Code)
	}

	unbanAllV1PostResp := httptest.NewRecorder()
	unbanAllV1PostReq := httptest.NewRequest(http.MethodPost, "/api/v1/global/unbanall", nil)
	handler.ServeHTTP(unbanAllV1PostResp, unbanAllV1PostReq)
	if unbanAllV1PostResp.Code != http.StatusUnauthorized {
		t.Fatalf("POST /api/v1/global/unbanall status = %d, want 401", unbanAllV1PostResp.Code)
	}

	unknownResp := httptest.NewRecorder()
	unknownReq := httptest.NewRequest(http.MethodGet, "/unknown/action", nil)
	handler.ServeHTTP(unknownResp, unknownReq)
	if unknownResp.Code != http.StatusNotFound {
		t.Fatalf("GET /unknown/action status = %d, want 404", unknownResp.Code)
	}
	if got := unknownResp.Body.String(); got != "" {
		t.Fatalf("GET /unknown/action body = %q, want empty body", got)
	}

	missingPageResp := httptest.NewRecorder()
	missingPageReq := httptest.NewRequest(http.MethodGet, "/auth/render/login/missing", nil)
	handler.ServeHTTP(missingPageResp, missingPageReq)
	if missingPageResp.Code != http.StatusNotFound {
		t.Fatalf("GET /auth/render/login/missing status = %d, want 404", missingPageResp.Code)
	}
	if got := missingPageResp.Body.String(); got != "" {
		t.Fatalf("GET /auth/render/login/missing body = %q, want empty body", got)
	}

	trailingSlashResp := httptest.NewRecorder()
	trailingSlashReq := httptest.NewRequest(http.MethodGet, "/login/index/", nil)
	handler.ServeHTTP(trailingSlashResp, trailingSlashReq)
	if trailingSlashResp.Code != http.StatusNotFound {
		t.Fatalf("GET /login/index/ status = %d, want 404", trailingSlashResp.Code)
	}
	if got := trailingSlashResp.Body.String(); got != "" {
		t.Fatalf("GET /login/index/ body = %q, want empty body", got)
	}
}

func TestAPIInputMiddlewareSupportsJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	api := engine.Group("/api/v1")
	api.Use(apiInputMiddleware())
	api.POST("/echo", func(c *gin.Context) {
		id, ok, err := requestInt(c, "id")
		if err != nil || !ok || id != 42 {
			t.Fatalf("requestInt(id) = %d, %v, want 42, true", id, ok)
		}
		name, ok := framework.RequestParam(c, "name")
		if !ok || name != "demo" {
			t.Fatalf("RequestParam(name) = %q, %v, want demo, true", name, ok)
		}
		enabled, ok := framework.RequestParam(c, "enabled")
		if !ok || enabled != "true" {
			t.Fatalf("RequestParam(enabled) = %q, %v, want true, true", enabled, ok)
		}
		c.Status(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/echo", strings.NewReader(`{"id":42,"name":"demo","enabled":true}`))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	engine.ServeHTTP(resp, req)
	if resp.Code != http.StatusNoContent {
		t.Fatalf("POST /api/v1/echo status = %d, want 204", resp.Code)
	}

	badReq := httptest.NewRequest(http.MethodPost, "/api/v1/echo", strings.NewReader(`{"id":`))
	badReq.Header.Set("Content-Type", "application/json")
	badResp := httptest.NewRecorder()
	engine.ServeHTTP(badResp, badReq)
	if badResp.Code != http.StatusBadRequest {
		t.Fatalf("POST /api/v1/echo with invalid json status = %d, want 400", badResp.Code)
	}
	if body := badResp.Body.String(); !strings.Contains(body, "\"status\":0") || !strings.Contains(body, "\"msg\":\"invalid json body\"") {
		t.Fatalf("POST /api/v1/echo with invalid json should return typed error payload, got %s", body)
	}
}

func TestWebBaseURLPrefix(t *testing.T) {
	repoRoot := testRepoRoot(t)
	oldConfPath := common.ConfPath
	common.ConfPath = repoRoot
	defer func() {
		common.ConfPath = oldConfPath
	}()

	configPath := writeTestConfig(t, "nps.conf", strings.Join([]string{
		"web_username=admin",
		"web_password=secret",
		"web_base_url=/nps",
		"allow_user_register=true",
		"open_captcha=false",
	}, "\n")+"\n")
	if err := servercfg.Load(configPath); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	crypt.InitTls(tls.Certificate{})
	gin.SetMode(gin.TestMode)
	handler := Init()

	prefixedResp := httptest.NewRecorder()
	prefixedReq := httptest.NewRequest(http.MethodGet, "/nps/login/index", nil)
	handler.ServeHTTP(prefixedResp, prefixedReq)
	if prefixedResp.Code != http.StatusOK {
		t.Fatalf("GET /nps/login/index status = %d, want 200", prefixedResp.Code)
	}

	unprefixedResp := httptest.NewRecorder()
	unprefixedReq := httptest.NewRequest(http.MethodGet, "/login/index", nil)
	handler.ServeHTTP(unprefixedResp, unprefixedReq)
	if unprefixedResp.Code != http.StatusNotFound {
		t.Fatalf("GET /login/index status = %d, want 404 when web_base_url is /nps", unprefixedResp.Code)
	}
	if got := unprefixedResp.Body.String(); got != "" {
		t.Fatalf("GET /login/index body = %q, want empty body", got)
	}

	bootstrapResp := httptest.NewRecorder()
	bootstrapReq := httptest.NewRequest(http.MethodGet, "/nps/auth/bootstrap", nil)
	handler.ServeHTTP(bootstrapResp, bootstrapReq)
	if bootstrapResp.Code != http.StatusOK {
		t.Fatalf("GET /nps/auth/bootstrap status = %d, want 200", bootstrapResp.Code)
	}
	body := bootstrapResp.Body.String()
	if !strings.Contains(body, "\"page\":\"/nps/auth/page\"") {
		t.Fatalf("bootstrap response missing prefixed page route: %s", body)
	}
	if !strings.Contains(body, "\"static_base\":\"/nps/static\"") {
		t.Fatalf("bootstrap response missing prefixed static base route: %s", body)
	}
	if !strings.Contains(body, "\"api_base\":\"/nps/api/v1\"") {
		t.Fatalf("bootstrap response missing prefixed api base route: %s", body)
	}

	modelResp := httptest.NewRecorder()
	modelReq := httptest.NewRequest(http.MethodGet, "/nps/auth/page/login/index", nil)
	handler.ServeHTTP(modelResp, modelReq)
	if modelResp.Code != http.StatusOK {
		t.Fatalf("GET /nps/auth/page/login/index status = %d, want 200", modelResp.Code)
	}
	if got := modelResp.Body.String(); !strings.Contains(got, "\"direct_path\":\"/nps/login/index\"") {
		t.Fatalf("prefixed page model missing direct_path prefix: %s", got)
	}

	baseResp := httptest.NewRecorder()
	baseReq := httptest.NewRequest(http.MethodGet, "/nps", nil)
	handler.ServeHTTP(baseResp, baseReq)
	if baseResp.Code != http.StatusFound {
		t.Fatalf("GET /nps status = %d, want 302", baseResp.Code)
	}
	if location := baseResp.Header().Get("Location"); location != "/nps/login/index" {
		t.Fatalf("GET /nps redirect = %q, want /nps/login/index", location)
	}

	adminCookies := sessionCookiesFromIdentity(t, servercfg.Current(), (&webservice.SessionIdentity{
		Version:       webservice.SessionIdentityVersion,
		Authenticated: true,
		Kind:          "admin",
		Provider:      "test",
		SubjectID:     "admin:admin",
		Username:      "admin",
		IsAdmin:       true,
		Roles:         []string{webservice.RoleAdmin},
	}).Normalize())
	adminResp := httptest.NewRecorder()
	adminReq := httptest.NewRequest(http.MethodGet, "/nps", nil)
	for _, cookie := range adminCookies {
		adminReq.AddCookie(cookie)
	}
	handler.ServeHTTP(adminResp, adminReq)
	if adminResp.Code != http.StatusFound {
		t.Fatalf("GET /nps as admin status = %d, want 302", adminResp.Code)
	}
	if location := adminResp.Header().Get("Location"); location != "/nps/index/index" {
		t.Fatalf("GET /nps as admin redirect = %q, want /nps/index/index", location)
	}
}

func TestInvalidSessionCookieFallsBackToFreshSession(t *testing.T) {
	repoRoot := testRepoRoot(t)
	oldConfPath := common.ConfPath
	common.ConfPath = repoRoot
	defer func() {
		common.ConfPath = oldConfPath
	}()

	configPath := writeTestConfig(t, "nps.conf", strings.Join([]string{
		"web_username=admin",
		"web_password=secret",
		"web_base_url=",
		"open_captcha=false",
	}, "\n")+"\n")
	if err := servercfg.Load(configPath); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	crypt.InitTls(tls.Certificate{})
	gin.SetMode(gin.TestMode)
	handler := Init()

	resp := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/login/index", nil)
	req.AddCookie(&http.Cookie{Name: "nps_session", Value: "invalid-session-cookie"})
	handler.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("GET /login/index with invalid session cookie status = %d, want 200", resp.Code)
	}
	if got := resp.Body.String(); !strings.Contains(got, "loginNonce") {
		t.Fatalf("GET /login/index with invalid session cookie should render login page, got %q", got)
	}
	if cookieByName(resp.Result().Cookies(), "nps_session") == nil {
		t.Fatal("GET /login/index with invalid session cookie should issue a fresh session cookie")
	}
}

func TestBlankAdminCredentialsAutoLogin(t *testing.T) {
	repoRoot := testRepoRoot(t)
	oldConfPath := common.ConfPath
	common.ConfPath = repoRoot
	defer func() {
		common.ConfPath = oldConfPath
	}()

	configPath := writeTestConfig(t, "nps.conf", strings.Join([]string{
		"web_username=",
		"web_password=",
		"web_base_url=",
		"open_captcha=false",
	}, "\n")+"\n")
	if err := servercfg.Load(configPath); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	crypt.InitTls(tls.Certificate{})
	gin.SetMode(gin.TestMode)
	handler := Init()

	loginResp := httptest.NewRecorder()
	loginReq := httptest.NewRequest(http.MethodGet, "/login/index", nil)
	handler.ServeHTTP(loginResp, loginReq)
	if loginResp.Code != http.StatusFound {
		t.Fatalf("GET /login/index with blank admin credentials status = %d, want 302", loginResp.Code)
	}
	if location := loginResp.Header().Get("Location"); location != "/index/index" {
		t.Fatalf("GET /login/index redirect = %q, want /index/index", location)
	}

	bootstrapResp := httptest.NewRecorder()
	bootstrapReq := httptest.NewRequest(http.MethodGet, "/api/v1/auth/bootstrap", nil)
	handler.ServeHTTP(bootstrapResp, bootstrapReq)
	if bootstrapResp.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/auth/bootstrap with blank admin credentials status = %d, want 200", bootstrapResp.Code)
	}
	body := bootstrapResp.Body.String()
	if !strings.Contains(body, "\"authenticated\":true") || !strings.Contains(body, "\"is_admin\":true") {
		t.Fatalf("GET /api/v1/auth/bootstrap should expose authenticated admin session, got %s", body)
	}
}

func TestLoginVerifyRejectsOversizedChunkedBody(t *testing.T) {
	repoRoot := testRepoRoot(t)
	oldConfPath := common.ConfPath
	common.ConfPath = repoRoot
	defer func() {
		common.ConfPath = oldConfPath
	}()

	configPath := writeTestConfig(t, "nps.conf", strings.Join([]string{
		"web_username=admin",
		"web_password=secret",
		"web_base_url=",
		"open_captcha=false",
		"login_max_body=32",
	}, "\n")+"\n")
	if err := servercfg.Load(configPath); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	crypt.InitTls(tls.Certificate{})
	gin.SetMode(gin.TestMode)
	handler := Init()

	body := "username=admin&password=" + strings.Repeat("x", 128)
	req := httptest.NewRequest(http.MethodPost, "/login/verify", io.NopCloser(strings.NewReader(body)))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.ContentLength = -1
	req.TransferEncoding = []string{"chunked"}
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)

	if resp.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("POST /login/verify with oversized chunked body status = %d, want 413", resp.Code)
	}
}

func TestCloseUnknownPathWithoutResponseWhenEnabled(t *testing.T) {
	repoRoot := testRepoRoot(t)
	oldConfPath := common.ConfPath
	common.ConfPath = repoRoot
	defer func() {
		common.ConfPath = oldConfPath
	}()

	configPath := writeTestConfig(t, "nps.conf", strings.Join([]string{
		"web_username=admin",
		"web_password=secret",
		"web_base_url=",
		"web_close_on_not_found=true",
		"open_captcha=false",
	}, "\n")+"\n")
	if err := servercfg.Load(configPath); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	crypt.InitTls(tls.Certificate{})
	gin.SetMode(gin.TestMode)
	ts := httptest.NewServer(Init())
	defer ts.Close()

	addr := strings.TrimPrefix(ts.URL, "http://")
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer func() { _ = conn.Close() }()
	if err := conn.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("SetDeadline() error = %v", err)
	}
	if _, err := fmt.Fprintf(conn, "GET /definitely-missing HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", addr); err != nil {
		t.Fatalf("write raw request error = %v", err)
	}
	raw, err := io.ReadAll(conn)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	if len(raw) != 0 {
		t.Fatalf("unknown path should close without HTTP response, got %q", string(raw))
	}

	client := ts.Client()
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}
	resp, err := client.Get(ts.URL + "/")
	if err != nil {
		t.Fatalf("GET / error = %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("GET / status = %d, want 302", resp.StatusCode)
	}
	if location := resp.Header.Get("Location"); location != "/login/index" {
		t.Fatalf("GET / redirect = %q, want /login/index", location)
	}
}

func TestBootstrapPagesReflectPermissions(t *testing.T) {
	repoRoot := testRepoRoot(t)
	oldConfPath := common.ConfPath
	common.ConfPath = repoRoot
	defer func() {
		common.ConfPath = oldConfPath
	}()

	resetTestDB(t)
	configPath := writeTestConfig(t, "nps.conf", strings.Join([]string{
		"web_username=admin",
		"web_password=secret",
		"web_base_url=",
		"head_custom_code=<meta name=\"shell-probe\" content=\"present\">",
		"allow_user_login=true",
		"allow_user_register=true",
		"open_captcha=false",
	}, "\n")+"\n")
	if err := servercfg.Load(configPath); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	crypt.InitTls(tls.Certificate{})
	gin.SetMode(gin.TestMode)
	handler := Init()

	anonymousResp := httptest.NewRecorder()
	anonymousReq := httptest.NewRequest(http.MethodGet, "/api/v1/auth/bootstrap", nil)
	handler.ServeHTTP(anonymousResp, anonymousReq)
	if anonymousResp.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/auth/bootstrap status = %d, want 200", anonymousResp.Code)
	}
	anonymousPages := bootstrapDirectPaths(t, anonymousResp.Body.Bytes())
	if !anonymousPages["/login/index"] {
		t.Fatalf("anonymous bootstrap should expose login page, got %v", sortedKeys(anonymousPages))
	}
	if !anonymousPages["/login/register"] {
		t.Fatalf("anonymous bootstrap should expose register page, got %v", sortedKeys(anonymousPages))
	}
	if anonymousPages["/index/index"] || anonymousPages["/client/list"] || anonymousPages["/global/index"] {
		t.Fatalf("anonymous bootstrap should not expose protected pages, got %v", sortedKeys(anonymousPages))
	}
	anonymousLogin := bootstrapPageByDirectPath(t, anonymousResp.Body.Bytes(), "/login/index")
	if anonymousLogin == nil || anonymousLogin.Section != "auth" || anonymousLogin.Template != "login/index.html" || !anonymousLogin.Navigation {
		t.Fatalf("anonymous bootstrap should include login page metadata, got %+v", anonymousLogin)
	}
	anonymousRegister := bootstrapPageByDirectPath(t, anonymousResp.Body.Bytes(), "/login/register")
	if anonymousRegister == nil || len(anonymousRegister.RequiredFeatures) != 1 || anonymousRegister.RequiredFeatures[0] != "allow_user_register" {
		t.Fatalf("anonymous bootstrap should include register page feature metadata, got %+v", anonymousRegister)
	}
	anonymousActions := bootstrapActionAPIPaths(t, anonymousResp.Body.Bytes())
	if !anonymousActions["/api/v1/login/verify"] || !anonymousActions["/api/v1/auth/bootstrap"] {
		t.Fatalf("anonymous bootstrap should expose public actions, got %v", sortedKeys(anonymousActions))
	}
	if anonymousActions["/api/v1/index/gettunnel"] || anonymousActions["/api/v1/global/banlist"] {
		t.Fatalf("anonymous bootstrap should not expose protected actions, got %v", sortedKeys(anonymousActions))
	}

	userCookies := sessionCookiesFromIdentity(t, servercfg.Current(), (&webservice.SessionIdentity{
		Version:       webservice.SessionIdentityVersion,
		Authenticated: true,
		Kind:          "user",
		Provider:      "test",
		SubjectID:     "user:operator",
		Username:      "operator",
		ClientIDs:     []int{101},
		Roles:         []string{webservice.RoleUser},
	}).Normalize())
	userResp := httptest.NewRecorder()
	userReq := httptest.NewRequest(http.MethodGet, "/api/v1/auth/bootstrap", nil)
	for _, cookie := range userCookies {
		userReq.AddCookie(cookie)
	}
	handler.ServeHTTP(userResp, userReq)
	if userResp.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/auth/bootstrap as user status = %d, want 200", userResp.Code)
	}
	userPages := bootstrapDirectPaths(t, userResp.Body.Bytes())
	if !userPages["/index/index"] || !userPages["/client/list"] {
		t.Fatalf("user bootstrap should expose management pages, got %v", sortedKeys(userPages))
	}
	if userPages["/client/add"] || userPages["/global/index"] {
		t.Fatalf("user bootstrap should not expose admin/global pages, got %v", sortedKeys(userPages))
	}
	userEditPage := bootstrapPageByDirectPath(t, userResp.Body.Bytes(), "/index/edit")
	if userEditPage == nil || userEditPage.Navigation || len(userEditPage.Params) != 1 || userEditPage.Params[0].Name != "id" || !userEditPage.Params[0].Required {
		t.Fatalf("user bootstrap should include edit page parameter metadata, got %+v", userEditPage)
	}
	userActions := bootstrapActionAPIPaths(t, userResp.Body.Bytes())
	if !userActions["/api/v1/index/gettunnel"] || !userActions["/api/v1/client/list"] {
		t.Fatalf("user bootstrap should expose permitted protected actions, got %v", sortedKeys(userActions))
	}
	if userActions["/api/v1/global/banlist"] || userActions["/api/v1/client/add"] || userActions["/api/v1/client/update"] || userActions["/api/v1/index/updateserver"] {
		t.Fatalf("user bootstrap should not expose disallowed actions, got %v", sortedKeys(userActions))
	}

	globalManagerCookies := sessionCookiesFromIdentity(t, servercfg.Current(), (&webservice.SessionIdentity{
		Version:       webservice.SessionIdentityVersion,
		Authenticated: true,
		Kind:          "service",
		Provider:      "test",
		SubjectID:     "service:global-manager",
		Username:      "global-manager",
		Roles:         []string{"service"},
		Permissions:   []string{webservice.PermissionGlobalManage},
	}).Normalize())
	globalResp := httptest.NewRecorder()
	globalReq := httptest.NewRequest(http.MethodGet, "/api/v1/auth/bootstrap", nil)
	for _, cookie := range globalManagerCookies {
		globalReq.AddCookie(cookie)
	}
	handler.ServeHTTP(globalResp, globalReq)
	if globalResp.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/auth/bootstrap as global manager status = %d, want 200", globalResp.Code)
	}
	globalPages := bootstrapDirectPaths(t, globalResp.Body.Bytes())
	if !globalPages["/global/index"] {
		t.Fatalf("global manager bootstrap should expose global page, got %v", sortedKeys(globalPages))
	}
	globalIndex := bootstrapPageByDirectPath(t, globalResp.Body.Bytes(), "/global/index")
	if globalIndex == nil || globalIndex.Section != "global" || globalIndex.Template != "global/index.html" {
		t.Fatalf("global manager bootstrap should include global page metadata, got %+v", globalIndex)
	}
	globalActions := bootstrapActionAPIPaths(t, globalResp.Body.Bytes())
	if !globalActions["/api/v1/global/banlist"] {
		t.Fatalf("global manager bootstrap should expose global actions, got %v", sortedKeys(globalActions))
	}

	shellResp := httptest.NewRecorder()
	shellReq := httptest.NewRequest(http.MethodGet, "/app/overview", nil)
	for _, cookie := range userCookies {
		shellReq.AddCookie(cookie)
	}
	handler.ServeHTTP(shellResp, shellReq)
	if shellResp.Code != http.StatusOK {
		t.Fatalf("GET /app/overview as user status = %d, want 200", shellResp.Code)
	}
	shellBody := shellResp.Body.String()
	if !strings.Contains(shellBody, "nps-bootstrap") || !strings.Contains(shellBody, "window.__NPS_BOOTSTRAP__") {
		t.Fatalf("management shell should embed bootstrap payload, got %s", shellBody)
	}
	if !strings.Contains(shellBody, "/api/v1") || !strings.Contains(shellBody, "/client/list") {
		t.Fatalf("management shell should render bootstrap contract, got %s", shellBody)
	}
	if !strings.Contains(shellBody, "shell-probe") {
		t.Fatalf("management shell should include head_custom_code, got %s", shellBody)
	}

	rootResp := httptest.NewRecorder()
	rootReq := httptest.NewRequest(http.MethodGet, "/", nil)
	for _, cookie := range userCookies {
		rootReq.AddCookie(cookie)
	}
	handler.ServeHTTP(rootResp, rootReq)
	if rootResp.Code != http.StatusFound {
		t.Fatalf("GET / as user status = %d, want 302", rootResp.Code)
	}
	if location := rootResp.Header().Get("Location"); location != "/index/index" {
		t.Fatalf("GET / as user redirect = %q, want /index/index", location)
	}
}

func TestAuthPageControllerAliases(t *testing.T) {
	repoRoot := testRepoRoot(t)
	oldConfPath := common.ConfPath
	common.ConfPath = repoRoot
	defer func() {
		common.ConfPath = oldConfPath
	}()

	configPath := writeTestConfig(t, "nps.conf", strings.Join([]string{
		"web_username=admin",
		"web_password=secret",
		"web_base_url=",
		"allow_user_login=true",
		"allow_user_register=true",
		"open_captcha=false",
	}, "\n")+"\n")
	if err := servercfg.Load(configPath); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	crypt.InitTls(tls.Certificate{})
	gin.SetMode(gin.TestMode)
	handler := Init()

	loginRenderResp := httptest.NewRecorder()
	loginRenderReq := httptest.NewRequest(http.MethodGet, "/auth/render/login", nil)
	handler.ServeHTTP(loginRenderResp, loginRenderReq)
	if loginRenderResp.Code != http.StatusOK {
		t.Fatalf("GET /auth/render/login status = %d, want 200", loginRenderResp.Code)
	}
	if got := loginRenderResp.Body.String(); !strings.Contains(got, "loginNonce") {
		t.Fatalf("GET /auth/render/login should render login page, got %q", got)
	}

	loginModelResp := httptest.NewRecorder()
	loginModelReq := httptest.NewRequest(http.MethodGet, "/auth/page/login", nil)
	handler.ServeHTTP(loginModelResp, loginModelReq)
	if loginModelResp.Code != http.StatusOK {
		t.Fatalf("GET /auth/page/login status = %d, want 200", loginModelResp.Code)
	}
	if got := loginModelResp.Body.String(); !strings.Contains(got, "\"tpl_name\":\"login/index.html\"") {
		t.Fatalf("GET /auth/page/login should return login page model, got %s", got)
	}

	userCookies := sessionCookiesFromIdentity(t, servercfg.Current(), (&webservice.SessionIdentity{
		Version:       webservice.SessionIdentityVersion,
		Authenticated: true,
		Kind:          "user",
		Provider:      "test",
		SubjectID:     "user:operator",
		Username:      "operator",
		ClientIDs:     []int{101},
		Roles:         []string{webservice.RoleUser},
	}).Normalize())

	clientModelResp := httptest.NewRecorder()
	clientModelReq := httptest.NewRequest(http.MethodGet, "/auth/page/client", nil)
	for _, cookie := range userCookies {
		clientModelReq.AddCookie(cookie)
	}
	handler.ServeHTTP(clientModelResp, clientModelReq)
	if clientModelResp.Code != http.StatusOK {
		t.Fatalf("GET /auth/page/client status = %d, want 200", clientModelResp.Code)
	}
	if got := clientModelResp.Body.String(); !strings.Contains(got, "\"tpl_name\":\"client/list.html\"") {
		t.Fatalf("GET /auth/page/client should return client list model, got %s", got)
	}

	clientPageResp := httptest.NewRecorder()
	clientPageReq := httptest.NewRequest(http.MethodGet, "/client/list", nil)
	for _, cookie := range userCookies {
		clientPageReq.AddCookie(cookie)
	}
	handler.ServeHTTP(clientPageResp, clientPageReq)
	if clientPageResp.Code != http.StatusOK {
		t.Fatalf("GET /client/list status = %d, want 200", clientPageResp.Code)
	}
	if got := clientPageResp.Body.String(); !strings.Contains(got, "client") {
		t.Fatalf("GET /client/list should render client page, got %q", got)
	}

	globalRenderResp := httptest.NewRecorder()
	globalRenderReq := httptest.NewRequest(http.MethodGet, "/auth/render/global", nil)
	for _, cookie := range userCookies {
		globalRenderReq.AddCookie(cookie)
	}
	handler.ServeHTTP(globalRenderResp, globalRenderReq)
	if globalRenderResp.Code != http.StatusForbidden {
		t.Fatalf("GET /auth/render/global as user status = %d, want 403", globalRenderResp.Code)
	}

	globalPageResp := httptest.NewRecorder()
	globalPageReq := httptest.NewRequest(http.MethodGet, "/global/index", nil)
	for _, cookie := range userCookies {
		globalPageReq.AddCookie(cookie)
	}
	handler.ServeHTTP(globalPageResp, globalPageReq)
	if globalPageResp.Code != http.StatusForbidden {
		t.Fatalf("GET /global/index as user status = %d, want 403", globalPageResp.Code)
	}
}

func TestPageCatalogRespectsFeatureFlags(t *testing.T) {
	repoRoot := testRepoRoot(t)
	oldConfPath := common.ConfPath
	common.ConfPath = repoRoot
	defer func() {
		common.ConfPath = oldConfPath
	}()

	configPath := writeTestConfig(t, "nps.conf", strings.Join([]string{
		"web_username=admin",
		"web_password=secret",
		"web_base_url=",
		"allow_user_login=true",
		"allow_user_register=false",
		"open_captcha=false",
	}, "\n")+"\n")
	if err := servercfg.Load(configPath); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	crypt.InitTls(tls.Certificate{})
	gin.SetMode(gin.TestMode)
	handler := Init()

	registerPageResp := httptest.NewRecorder()
	registerPageReq := httptest.NewRequest(http.MethodGet, "/login/register", nil)
	handler.ServeHTTP(registerPageResp, registerPageReq)
	if registerPageResp.Code != http.StatusNotFound {
		t.Fatalf("GET /login/register status = %d, want 404 when allow_user_register=false", registerPageResp.Code)
	}

	registerRenderResp := httptest.NewRecorder()
	registerRenderReq := httptest.NewRequest(http.MethodGet, "/auth/render/login/register", nil)
	handler.ServeHTTP(registerRenderResp, registerRenderReq)
	if registerRenderResp.Code != http.StatusNotFound {
		t.Fatalf("GET /auth/render/login/register status = %d, want 404 when allow_user_register=false", registerRenderResp.Code)
	}

	bootstrapResp := httptest.NewRecorder()
	bootstrapReq := httptest.NewRequest(http.MethodGet, "/api/v1/auth/bootstrap", nil)
	handler.ServeHTTP(bootstrapResp, bootstrapReq)
	if bootstrapResp.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/auth/bootstrap status = %d, want 200", bootstrapResp.Code)
	}
	bootstrapPages := bootstrapDirectPaths(t, bootstrapResp.Body.Bytes())
	if bootstrapPages["/login/register"] {
		t.Fatalf("bootstrap should hide register page when allow_user_register=false, got %v", sortedKeys(bootstrapPages))
	}
}

func TestAuthorizationBoundariesForMultiClientUser(t *testing.T) {
	repoRoot := testRepoRoot(t)
	oldConfPath := common.ConfPath
	common.ConfPath = repoRoot
	defer func() {
		common.ConfPath = oldConfPath
	}()

	resetTestDB(t)
	controllers.RemoveAllLoginBan()
	configPath := writeTestConfig(t, "nps.conf", strings.Join([]string{
		"web_username=admin",
		"web_password=secret",
		"web_base_url=",
		"allow_user_login=true",
		"allow_user_register=true",
		"open_captcha=false",
		"secure_mode=false",
	}, "\n")+"\n")
	if err := servercfg.Load(configPath); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	operatorA := createTestClient(t, 101, "operator", "op-secret")
	operatorB := createTestClient(t, 102, "operator-b", "op-secret-b")
	outsider := createTestClient(t, 103, "outsider", "other-secret")
	createTestTunnel(t, 201, operatorA, 19081)
	createTestTunnel(t, 202, operatorB, 19082)
	createTestTunnel(t, 203, outsider, 19083)
	createTestHost(t, 301, operatorA, "a.example.test")
	createTestHost(t, 303, operatorB, "c.example.test")
	createTestHost(t, 302, outsider, "b.example.test")

	crypt.InitTls(tls.Certificate{})
	gin.SetMode(gin.TestMode)
	handler := Init()
	cookies := sessionCookiesFromIdentity(t, servercfg.Current(), (&webservice.SessionIdentity{
		Version:       webservice.SessionIdentityVersion,
		Authenticated: true,
		Kind:          "user",
		Provider:      "test",
		SubjectID:     "user:operator",
		Username:      "operator",
		ClientIDs:     []int{operatorA.Id, operatorB.Id},
		Roles:         []string{"user"},
	}).Normalize())

	clientListResp := performFormRequest(handler, http.MethodPost, "/api/v1/client/list", cookies, map[string]string{
		"offset": "0",
		"limit":  "0",
	})
	if clientListResp.Code != http.StatusOK {
		t.Fatalf("POST /api/v1/client/list status = %d, want 200", clientListResp.Code)
	}
	if body := clientListResp.Body.String(); !strings.Contains(body, "\"total\":2") {
		t.Fatalf("POST /api/v1/client/list should expose both owned clients, got %s", body)
	} else if !strings.Contains(body, "\"bridgeType\"") || !strings.Contains(body, "\"bridgePort\"") {
		t.Fatalf("POST /api/v1/client/list should expose typed bridge metadata, got %s", body)
	}

	ownClientResp := performFormRequest(handler, http.MethodPost, "/api/v1/client/getclient", cookies, map[string]string{
		"id": fmt.Sprintf("%d", operatorA.Id),
	})
	if ownClientResp.Code != http.StatusOK || !strings.Contains(ownClientResp.Body.String(), "\"code\":1") {
		t.Fatalf("POST /api/v1/client/getclient for owned client should succeed, status=%d body=%s", ownClientResp.Code, ownClientResp.Body.String())
	}

	foreignClientResp := performFormRequest(handler, http.MethodPost, "/api/v1/client/getclient", cookies, map[string]string{
		"id": fmt.Sprintf("%d", outsider.Id),
	})
	if foreignClientResp.Code != http.StatusForbidden {
		t.Fatalf("POST /api/v1/client/getclient for foreign client status = %d, want 403", foreignClientResp.Code)
	}
	if body := foreignClientResp.Body.String(); !strings.Contains(body, "\"msg\":\"forbidden\"") {
		t.Fatalf("foreign client request should return forbidden json, got %s", body)
	}

	clientCreateResp := performFormRequest(handler, http.MethodPost, "/api/v1/client/add", cookies, nil)
	if clientCreateResp.Code != http.StatusForbidden {
		t.Fatalf("POST /api/v1/client/add as regular user status = %d, want 403", clientCreateResp.Code)
	}

	clientUpdateResp := performFormRequest(handler, http.MethodPost, "/api/v1/client/update", cookies, map[string]string{
		"id": fmt.Sprintf("%d", operatorB.Id),
	})
	if clientUpdateResp.Code != http.StatusForbidden {
		t.Fatalf("POST /api/v1/client/update for an owned client as regular user status = %d, want 403", clientUpdateResp.Code)
	}

	serverUpdateResp := performFormRequest(handler, http.MethodPost, "/api/v1/index/updateserver", cookies, nil)
	if serverUpdateResp.Code != http.StatusForbidden {
		t.Fatalf("POST /api/v1/index/updateserver as regular user status = %d, want 403", serverUpdateResp.Code)
	}

	secondClientHostListResp := performFormRequest(handler, http.MethodPost, "/api/v1/index/hostlist", cookies, map[string]string{
		"client_id": fmt.Sprintf("%d", operatorB.Id),
		"offset":    "0",
		"limit":     "0",
	})
	if secondClientHostListResp.Code != http.StatusOK {
		t.Fatalf("POST /api/v1/index/hostlist for secondary owned client status = %d, want 200", secondClientHostListResp.Code)
	}
	if body := secondClientHostListResp.Body.String(); !strings.Contains(body, "\"total\":1") {
		t.Fatalf("secondary owned client host list should be scoped correctly, got %s", body)
	}

	foreignHostListResp := performFormRequest(handler, http.MethodPost, "/api/v1/index/hostlist", cookies, map[string]string{
		"client_id": fmt.Sprintf("%d", outsider.Id),
		"offset":    "0",
		"limit":     "0",
	})
	if foreignHostListResp.Code != http.StatusForbidden {
		t.Fatalf("POST /api/v1/index/hostlist for foreign client status = %d, want 403", foreignHostListResp.Code)
	}

	ownTunnelResp := performFormRequest(handler, http.MethodPost, "/api/v1/index/getonetunnel", cookies, map[string]string{
		"id": "201",
	})
	if ownTunnelResp.Code != http.StatusOK || !strings.Contains(ownTunnelResp.Body.String(), "\"code\":1") {
		t.Fatalf("POST /api/v1/index/getonetunnel for owned tunnel should succeed, status=%d body=%s", ownTunnelResp.Code, ownTunnelResp.Body.String())
	}

	foreignTunnelResp := performFormRequest(handler, http.MethodPost, "/api/v1/index/getonetunnel", cookies, map[string]string{
		"id": "203",
	})
	if foreignTunnelResp.Code != http.StatusForbidden {
		t.Fatalf("POST /api/v1/index/getonetunnel for foreign tunnel status = %d, want 403", foreignTunnelResp.Code)
	}

	ownHostResp := performFormRequest(handler, http.MethodPost, "/api/v1/index/gethost", cookies, map[string]string{
		"id": "301",
	})
	if ownHostResp.Code != http.StatusOK || !strings.Contains(ownHostResp.Body.String(), "\"code\":1") {
		t.Fatalf("POST /api/v1/index/gethost for owned host should succeed, status=%d body=%s", ownHostResp.Code, ownHostResp.Body.String())
	}

	foreignHostResp := performFormRequest(handler, http.MethodPost, "/api/v1/index/gethost", cookies, map[string]string{
		"id": "302",
	})
	if foreignHostResp.Code != http.StatusForbidden {
		t.Fatalf("POST /api/v1/index/gethost for foreign host status = %d, want 403", foreignHostResp.Code)
	}

	globalResp := performFormRequest(handler, http.MethodPost, "/api/v1/global/unbanall", cookies, nil)
	if globalResp.Code != http.StatusForbidden {
		t.Fatalf("POST /api/v1/global/unbanall as non-admin status = %d, want 403", globalResp.Code)
	}

	globalManagerCookies := sessionCookiesFromIdentity(t, servercfg.Current(), (&webservice.SessionIdentity{
		Version:       webservice.SessionIdentityVersion,
		Authenticated: true,
		Kind:          "service",
		Provider:      "test",
		SubjectID:     "service:global-manager",
		Username:      "global-manager",
		Roles:         []string{"service"},
		Permissions:   []string{webservice.PermissionGlobalManage},
	}).Normalize())
	globalManagerResp := performFormRequest(handler, http.MethodPost, "/api/v1/global/banlist", globalManagerCookies, nil)
	if globalManagerResp.Code != http.StatusOK {
		t.Fatalf("POST /api/v1/global/banlist with global.manage permission status = %d, want 200", globalManagerResp.Code)
	}
}

func writeTestConfig(t *testing.T, name, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func testRepoRoot(t *testing.T) string {
	t.Helper()
	_, callerFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatalf("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(callerFile), "..", ".."))
}

func resetTestDB(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "conf"), 0o755); err != nil {
		t.Fatalf("create temp conf dir: %v", err)
	}
	// Run the lazy default initialization first so it cannot replace the
	// temporary database installed below.
	_ = file.GetDb()
	oldDb := file.Db
	oldHostIndex := file.HostIndex
	oldVkeyIndex := file.Blake2bVkeyIndex
	oldTaskPasswordIndex := file.TaskPasswordIndex
	file.Db = &file.DbUtils{JsonDb: file.NewJsonDb(root)}
	file.Db.JsonDb.Global = &file.Glob{}
	file.HostIndex = index.NewDomainIndex()
	file.Blake2bVkeyIndex = index.NewStringIDIndex()
	file.TaskPasswordIndex = index.NewStringIDIndex()
	t.Cleanup(func() {
		file.Db = oldDb
		file.HostIndex = oldHostIndex
		file.Blake2bVkeyIndex = oldVkeyIndex
		file.TaskPasswordIndex = oldTaskPasswordIndex
	})
	return root
}

func createTestClient(t *testing.T, id int, username, password string) *file.Client {
	t.Helper()
	client := &file.Client{
		Id:          id,
		Status:      true,
		VerifyKey:   fmt.Sprintf("vk-%d", id),
		Cnf:         &file.Config{},
		WebUserName: username,
		WebPassword: password,
		Flow:        &file.Flow{},
	}
	if err := file.GetDb().NewClient(client); err != nil {
		t.Fatalf("NewClient(%d) error = %v", id, err)
	}
	return client
}

func createTestTunnel(t *testing.T, id int, client *file.Client, port int) *file.Tunnel {
	t.Helper()
	tunnel := &file.Tunnel{
		Id:         id,
		Port:       port,
		Mode:       "tcp",
		Status:     true,
		Client:     client,
		TargetType: common.ConnTCP,
		Target: &file.Target{
			TargetStr: "127.0.0.1:80",
		},
	}
	if err := file.GetDb().NewTask(tunnel); err != nil {
		t.Fatalf("NewTask(%d) error = %v", id, err)
	}
	return tunnel
}

func createTestHost(t *testing.T, id int, client *file.Client, host string) *file.Host {
	t.Helper()
	record := &file.Host{
		Id:     id,
		Host:   host,
		Client: client,
		Target: &file.Target{
			TargetStr: "127.0.0.1:80",
		},
	}
	if err := file.GetDb().NewHost(record); err != nil {
		t.Fatalf("NewHost(%d) error = %v", id, err)
	}
	return record
}

func performFormRequest(handler http.Handler, method, path string, cookies []*http.Cookie, form map[string]string) *httptest.ResponseRecorder {
	values := url.Values{}
	if len(form) > 0 {
		for key, value := range form {
			values.Set(key, value)
		}
	}
	req := httptest.NewRequest(method, path, strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	return resp
}

func cookieByName(cookies []*http.Cookie, name string) *http.Cookie {
	for _, cookie := range cookies {
		if cookie.Name == name {
			return cookie
		}
	}
	return nil
}

func sessionCookiesFromIdentity(t *testing.T, cfg *servercfg.Snapshot, identity *webservice.SessionIdentity) []*http.Cookie {
	t.Helper()
	encoded, err := webservice.MarshalSessionIdentity(identity)
	if err != nil {
		t.Fatalf("MarshalSessionIdentity() error = %v", err)
	}
	authKey, encKey := deriveTestSessionKeys(cfg)
	store := sessions.NewCookieStore(authKey, encKey)
	store.Options = &sessions.Options{
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	resp := httptest.NewRecorder()
	session, err := store.Get(req, "nps_session")
	if err != nil {
		t.Fatalf("CookieStore.Get() error = %v", err)
	}
	session.Values["auth"] = true
	session.Values["isAdmin"] = identity.IsAdmin
	session.Values["username"] = identity.Username
	session.Values[webservice.SessionIdentityKey] = encoded
	if len(identity.ClientIDs) > 0 {
		session.Values["clientId"] = identity.ClientIDs[0]
		session.Values["clientIds"] = strings.Trim(strings.Replace(fmt.Sprint(identity.ClientIDs), " ", ",", -1), "[]")
	}
	if err := session.Save(req, resp); err != nil {
		t.Fatalf("session.Save() error = %v", err)
	}
	return resp.Result().Cookies()
}

func bootstrapDirectPaths(t *testing.T, body []byte) map[string]bool {
	t.Helper()
	pages := bootstrapPages(t, body)
	paths := make(map[string]bool, len(pages))
	for _, page := range pages {
		paths[page.DirectPath] = true
	}
	return paths
}

func bootstrapPageByDirectPath(t *testing.T, body []byte, directPath string) *bootstrapPage {
	t.Helper()
	for _, page := range bootstrapPages(t, body) {
		if page.DirectPath == directPath {
			pageCopy := page
			return &pageCopy
		}
	}
	return nil
}

func bootstrapPages(t *testing.T, body []byte) []bootstrapPage {
	t.Helper()
	var payload struct {
		Pages []bootstrapPage `json:"pages"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("json.Unmarshal(bootstrap) error = %v, body=%s", err, string(body))
	}
	return payload.Pages
}

func bootstrapActionAPIPaths(t *testing.T, body []byte) map[string]bool {
	t.Helper()
	actions := bootstrapActions(t, body)
	paths := make(map[string]bool, len(actions))
	for _, action := range actions {
		paths[action.APIPath] = true
	}
	return paths
}

func bootstrapActions(t *testing.T, body []byte) []bootstrapAction {
	t.Helper()
	var payload struct {
		Actions []bootstrapAction `json:"actions"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("json.Unmarshal(bootstrap actions) error = %v, body=%s", err, string(body))
	}
	return payload.Actions
}

type bootstrapPage struct {
	DirectPath       string   `json:"direct_path"`
	Section          string   `json:"section"`
	Template         string   `json:"template"`
	Navigation       bool     `json:"navigation"`
	RequiredFeatures []string `json:"required_features"`
	Params           []struct {
		Name     string `json:"name"`
		Required bool   `json:"required"`
	} `json:"params"`
}

type bootstrapAction struct {
	APIPath string `json:"api_path"`
}

func sortedKeys(values map[string]bool) []string {
	keys := make([]string, 0, len(values))
	for key, ok := range values {
		if ok {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

func deriveTestSessionKeys(cfg *servercfg.Snapshot) ([]byte, []byte) {
	seed := fmt.Sprintf("%s|%s|%s|%s", cfg.Web.Username, cfg.Web.Password, cfg.Auth.Key, cfg.App.Name)
	authSum := sha256.Sum256([]byte("auth:" + seed))
	encSum := sha256.Sum256([]byte("enc:" + seed))
	return authSum[:], encSum[:]
}

func TestIPRegisterPageAndGlobalFirewall(t *testing.T) {
	repoRoot := testRepoRoot(t)
	oldConfPath := common.ConfPath
	common.ConfPath = repoRoot
	defer func() { common.ConfPath = oldConfPath }()
	configPath := writeTestConfig(t, "nps.conf", strings.Join([]string{
		"web_username=admin",
		"web_password=secret",
		"web_base_url=",
		"open_captcha=false",
	}, "\n")+"\n")
	if err := servercfg.Load(configPath); err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	resetTestDB(t)
	ipreg.Reset()
	t.Cleanup(ipreg.Reset)
	client := createTestClient(t, 9, "", "")
	client.RegisterKey = "reg-key-9"
	crypt.InitTls(tls.Certificate{})
	gin.SetMode(gin.TestMode)
	handler := Init()

	pageResp := httptest.NewRecorder()
	handler.ServeHTTP(pageResp, httptest.NewRequest(http.MethodGet, "/ip/register?key=prefilled", nil))
	if pageResp.Code != http.StatusOK || !strings.Contains(pageResp.Body.String(), `value="prefilled"`) || !strings.Contains(pageResp.Body.String(), "192.0.2.1") {
		t.Fatalf("GET /ip/register status = %d, body %q", pageResp.Code, pageResp.Body.String())
	}

	wrong := performFormRequest(handler, http.MethodPost, "/ip/register", nil, map[string]string{"key": "wrong"})
	if !strings.Contains(wrong.Body.String(), `"status":0`) {
		t.Fatalf("POST /ip/register with a wrong key = %s", wrong.Body.String())
	}
	if again := performFormRequest(handler, http.MethodPost, "/ip/register", nil, map[string]string{"key": "reg-key-9"}); !strings.Contains(again.Body.String(), `"status":0`) {
		t.Fatalf("a retry right after a failure was not rate limited: %s", again.Body.String())
	}
	webservice.SharedLoginPolicy().RemoveBan("192.0.2.1")
	ok := performFormRequest(handler, http.MethodPost, "/ip/register", nil, map[string]string{"key": "reg-key-9", "hours": "1"})
	if !strings.Contains(ok.Body.String(), `"status":1`) || !ipreg.RegisteredString(9, "192.0.2.1") {
		t.Fatalf("POST /ip/register with the registration key = %s", ok.Body.String())
	}

	g := new(file.Glob)
	g.SetSourceLists(acl.DenyFirst, []string{"192.0.2.0/24"}, nil, acl.ScopeGlobal, "global")
	file.Db.JsonDb.Global = g
	for _, path := range []string{"/login/index", "/ip/register", "/static/css/style.css"} {
		resp := httptest.NewRecorder()
		handler.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, path, nil))
		if resp.Code != http.StatusForbidden {
			t.Errorf("GET %s from a globally denied address status = %d, want 403", path, resp.Code)
		}
	}
	allowed := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/login/index", nil)
	req.RemoteAddr = "198.51.100.7:1234"
	handler.ServeHTTP(allowed, req)
	if allowed.Code == http.StatusForbidden {
		t.Fatal("allowed address denied by the global lists")
	}
}

func TestIPRegisterPathsWithBaseURLAndStandalone(t *testing.T) {
	repoRoot := testRepoRoot(t)
	oldConfPath := common.ConfPath
	common.ConfPath = repoRoot
	defer func() { common.ConfPath = oldConfPath }()
	configPath := writeTestConfig(t, "nps.conf", strings.Join([]string{
		"web_username=admin",
		"web_password=secret",
		"web_base_url=/nps",
		"open_captcha=false",
		"public_vkey=pub-key",
	}, "\n")+"\n")
	if err := servercfg.Load(configPath); err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	resetTestDB(t)
	ipreg.Reset()
	t.Cleanup(ipreg.Reset)
	crypt.InitTls(tls.Certificate{})
	gin.SetMode(gin.TestMode)
	handler := Init()

	login := httptest.NewRecorder()
	handler.ServeHTTP(login, httptest.NewRequest(http.MethodGet, "/nps/login/index", nil))
	if !strings.Contains(login.Body.String(), `href="/nps/ip/register"`) {
		t.Fatal("login page lacks the registration entry under the base URL")
	}
	page := httptest.NewRecorder()
	handler.ServeHTTP(page, httptest.NewRequest(http.MethodGet, "/nps/ip/register", nil))
	body := page.Body.String()
	if page.Code != http.StatusOK || !strings.Contains(body, `"/nps/static/`) || !strings.Contains(body, `href="/nps/login/index"`) || !strings.Contains(body, `<option value="720">30d</option>`) {
		t.Fatalf("GET /nps/ip/register status = %d, body %q", page.Code, body)
	}
	reg := performFormRequest(handler, http.MethodPost, "/nps/ip/register", nil, map[string]string{"key": "pub-key", "hours": "720"})
	if !strings.Contains(reg.Body.String(), `"status":1`) || !ipreg.RegisteredString(77, "192.0.2.1") {
		t.Fatalf("POST /nps/ip/register with the public key = %s", reg.Body.String())
	}

	standalone := InitIPRegister()
	for _, path := range []string{"/", "/ip/register"} {
		resp := httptest.NewRecorder()
		standalone.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, path, nil))
		b := resp.Body.String()
		if resp.Code != http.StatusOK || !strings.Contains(b, `"/static/`) || strings.Contains(b, "/nps/") || strings.Contains(b, "login/index") {
			t.Fatalf("standalone GET %s status = %d, body %q", path, resp.Code, b)
		}
	}
	for _, path := range []string{"/login/index", "/index/index", "/client/list"} {
		resp := httptest.NewRecorder()
		standalone.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, path, nil))
		if resp.Code != http.StatusNotFound {
			t.Errorf("standalone GET %s status = %d, want 404", path, resp.Code)
		}
	}
	static := httptest.NewRecorder()
	standalone.ServeHTTP(static, httptest.NewRequest(http.MethodGet, "/static/css/style.css", nil))
	if static.Code != http.StatusOK {
		t.Fatalf("standalone static status = %d", static.Code)
	}
}

func TestGlobalTrafficAndRateLimit(t *testing.T) {
	repoRoot := testRepoRoot(t)
	oldConfPath := common.ConfPath
	common.ConfPath = repoRoot
	defer func() { common.ConfPath = oldConfPath }()
	configPath := writeTestConfig(t, "nps.conf", "web_username=admin\nweb_password=secret\nweb_base_url=\nopen_captcha=false\n")
	if err := servercfg.Load(configPath); err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	resetTestDB(t)
	t.Cleanup(func() { rate.SetGlobalLimit(0); rate.ResetGlobalTraffic() })
	crypt.InitTls(tls.Certificate{})
	gin.SetMode(gin.TestMode)
	handler := Init()
	admin := sessionCookiesFromIdentity(t, servercfg.Current(), (&webservice.SessionIdentity{
		Version:       webservice.SessionIdentityVersion,
		Authenticated: true,
		Kind:          "admin",
		Provider:      "test",
		SubjectID:     "admin:admin",
		Username:      "admin",
		IsAdmin:       true,
		Roles:         []string{webservice.RoleAdmin},
	}).Normalize())

	save := performFormRequest(handler, http.MethodPost, "/global/save", admin, map[string]string{"globalIpListPriority": "0", "globalRateLimit": "100"})
	if !strings.Contains(save.Body.String(), `"status":1`) || rate.GlobalLimit() != 100*1024 || file.Db.JsonDb.Global.RateLimit != 100 {
		t.Fatalf("POST /global/save = %s, limit %d", save.Body.String(), rate.GlobalLimit())
	}

	rate.SetGlobalTraffic(3000, 7000)
	list := performFormRequest(handler, http.MethodPost, "/client/list", admin, map[string]string{"offset": "0", "limit": "10"})
	if !strings.Contains(list.Body.String(), `"traffic":{"in":3000,"out":7000,`) || !strings.Contains(list.Body.String(), `"limit":102400`) {
		t.Fatalf("POST /client/list = %d %s %v", list.Code, list.Body.String(), list.Header())
	}

	cleared := performFormRequest(handler, http.MethodPost, "/client/clear", admin, map[string]string{"id": "0", "mode": "flow"})
	if in, out := rate.GlobalTraffic(); !strings.Contains(cleared.Body.String(), `"status":1`) || in != 0 || out != 0 {
		t.Fatalf("POST /client/clear = %d %s, traffic %d/%d", cleared.Code, cleared.Body.String(), in, out)
	}
	if g := file.Db.JsonDb.Global; g.InletFlow != 0 || g.ExportFlow != 0 {
		t.Fatalf("cleared traffic not stored: %d/%d", g.InletFlow, g.ExportFlow)
	}
}

// A login answers with the final session cookie alone: a cookie per changed
// session value made the response header outgrow the default header buffer
// of reverse proxies such as Nginx, which then failed the login.
func TestLoginSetsOneSessionCookie(t *testing.T) {
	repoRoot := testRepoRoot(t)
	oldConfPath := common.ConfPath
	common.ConfPath = repoRoot
	defer func() { common.ConfPath = oldConfPath }()
	configPath := writeTestConfig(t, "nps.conf", "web_username=admin\nweb_password=secret\nweb_base_url=\nopen_captcha=false\nsecure_mode=true\n")
	if err := servercfg.Load(configPath); err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	resetTestDB(t)
	crypt.InitTls(tls.Certificate{})
	gin.SetMode(gin.TestMode)
	handler := Init()

	page := httptest.NewRecorder()
	handler.ServeHTTP(page, httptest.NewRequest(http.MethodGet, "/login/index", nil))
	m := regexp.MustCompile(`loginNonce\s*:\s*"([^"]+)"`).FindStringSubmatch(page.Body.String())
	if m == nil {
		t.Fatal("no login nonce on the login page")
	}
	pubPEM, err := crypt.GetRSAPublicKeyPEM()
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode([]byte(pubPEM))
	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(crypt.LoginPayload{Nonce: m[1], Timestamp: time.Now().UnixMilli(), Password: "secret"})
	cipher, err := rsa.EncryptOAEP(sha256.New(), rand.Reader, pub.(*rsa.PublicKey), payload, nil)
	if err != nil {
		t.Fatal(err)
	}
	login := performFormRequest(handler, http.MethodPost, "/login/verify", page.Result().Cookies(), map[string]string{
		"username": "admin",
		"password": base64.StdEncoding.EncodeToString(cipher),
	})
	if !strings.Contains(login.Body.String(), `"status":1`) {
		t.Fatalf("POST /login/verify = %s", login.Body.String())
	}
	var sessionCookies, size int
	for _, c := range login.Header().Values("Set-Cookie") {
		if strings.HasPrefix(c, "nps_session=") {
			sessionCookies++
		}
	}
	for k, vs := range login.Header() {
		for _, v := range vs {
			size += len(k) + len(v) + 4
		}
	}
	if sessionCookies != 1 || size > 2048 {
		t.Fatalf("login response sets %d session cookies in %d header bytes, want 1 in at most 2 KiB", sessionCookies, size)
	}
	indexResp := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/index/index", nil)
	for _, c := range login.Result().Cookies() {
		req.AddCookie(c)
	}
	handler.ServeHTTP(indexResp, req)
	if indexResp.Code != http.StatusOK {
		t.Fatalf("GET /index/index with the login cookie status = %d, want 200", indexResp.Code)
	}
}
