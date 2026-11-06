package routers

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/djylb/nps/lib/servercfg"
	webapi "github.com/djylb/nps/web/api"
	webservice "github.com/djylb/nps/web/service"
	"github.com/gin-gonic/gin"
)

func TestClientClearAllowsAdminVirtualIDOnly(t *testing.T) {
	gin.SetMode(gin.TestMode)
	state := NewStateWithApp(webapi.New(&servercfg.Snapshot{}))
	var specs []webapi.ActionSpec
	calls := 0
	for _, spec := range webapi.ProtectedActionCatalog(state.App) {
		if spec.Controller != "client" || spec.Name != "clear" {
			continue
		}
		if spec.Permission != webservice.PermissionManagementAdmin || spec.Ownership != webapi.ActionOwnershipNone {
			t.Fatal("clear must have its own administrator gate")
		}
		spec.Handler = func(c webapi.Context) { calls++; c.RespondJSON(200, map[string]int{"id": c.Int("id")}) }
		specs = append(specs, spec)
	}
	if len(specs) != 1 {
		t.Fatal("clear action not found")
	}
	r := gin.New()
	group := r.Group("")
	group.Use(func(c *gin.Context) {
		if c.GetHeader("X-Test-Admin") == "yes" {
			setActor(c, webapi.AdminActorWithFallback("admin", "admin"))
		} else {
			setActor(c, webapi.UserActor("user", []int{1}))
		}
	})
	registerProtectedControllerRoutes(group, state, "client", specs)
	for _, path := range []string{"/client/clear", "/api/v1/client/clear"} {
		for _, id := range []int{-1, 0, 1} {
			for _, admin := range []bool{true, false} {
				before := calls
				req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(fmt.Sprintf("id=%d&mode=flow", id)))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				if admin {
					req.Header.Set("X-Test-Admin", "yes")
				}
				res := httptest.NewRecorder()
				r.ServeHTTP(res, req)
				if admin {
					if res.Code != 200 || calls != before+1 || !strings.Contains(res.Body.String(), fmt.Sprintf(`"id":%d`, id)) {
						t.Fatalf("admin id=%d: %d %s", id, res.Code, res.Body.String())
					}
				} else if res.Code != 403 || calls != before {
					t.Fatalf("user bypassed clear gate: id=%d code=%d", id, res.Code)
				}
			}
		}
	}
	if state.Authorization().RequireClient(webapi.PrincipalFromActor(webapi.UserActor("user", []int{1})), -1) == nil {
		t.Fatal("ordinary user gained virtual client access")
	}
}
