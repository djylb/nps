package routers

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/djylb/nps/lib/servercfg"
	webapi "github.com/djylb/nps/web/api"
	"github.com/gin-gonic/gin"
)

func TestLiveStatusRoutesRequireActor(t *testing.T) {
	gin.SetMode(gin.TestMode)
	state := NewStateWithApp(webapi.New(&servercfg.Snapshot{}))
	r := gin.New()
	group := r.Group("")
	group.Use(func(c *gin.Context) {
		if c.GetHeader("X-Test-Admin") == "yes" {
			setActor(c, webapi.AdminActorWithFallback("admin", "admin"))
		} else {
			setActor(c, webapi.AnonymousActor())
		}
	})
	registerProtectedRoutes(group, state)
	for _, path := range []string{"/live/status", "/api/v1/live/status"} {
		for _, admin := range []bool{false, true} {
			body := url.Values{"resource": {"client"}, "items": {"[]"}, "global": {"In"}}
			req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			if admin {
				req.Header.Set("X-Test-Admin", "yes")
			}
			res := httptest.NewRecorder()
			r.ServeHTTP(res, req)
			want := http.StatusForbidden
			if admin {
				want = http.StatusOK
			}
			if res.Code != want {
				t.Fatalf("%s admin=%v: %d %s", path, admin, res.Code, res.Body.String())
			}
		}
	}
}
