package framework

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/djylb/nps/lib/servercfg"
	"github.com/gin-gonic/gin"
)

func newSessionTestContext(t *testing.T) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	cfg := &servercfg.Snapshot{}
	cfg.Web.Username, cfg.Web.Password = "admin", "secret"
	if err := InitSessionStore(cfg); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	return c, rec
}

func sessionCookies(rec *httptest.ResponseRecorder) []string {
	var out []string
	for _, c := range rec.Header().Values("Set-Cookie") {
		if strings.HasPrefix(c, sessionName+"=") {
			out = append(out, c)
		}
	}
	return out
}

func TestSessionSavesOneCookie(t *testing.T) {
	c, rec := newSessionTestContext(t)
	c.Header("Set-Cookie", "other=1")
	for _, key := range []string{"a", "b", "c", "d"} {
		if err := SetSessionValue(c, key, strings.Repeat(key, 50)); err != nil {
			t.Fatal(err)
		}
	}
	if err := DeleteSessionValue(c, "b"); err != nil {
		t.Fatal(err)
	}
	if got := sessionCookies(rec); len(got) != 1 {
		t.Fatalf("%d session cookies, want 1", len(got))
	}
	if got := rec.Header().Values("Set-Cookie"); len(got) != 2 || got[0] != "other=1" {
		t.Fatalf("other cookies not kept: %q", got)
	}
}

func TestSessionSaveError(t *testing.T) {
	c, rec := newSessionTestContext(t)
	if err := SetSessionValue(c, "small", "x"); err != nil {
		t.Fatal(err)
	}
	// a session over the 4 KiB a cookie may hold cannot be saved
	if err := SetSessionValue(c, "big", strings.Repeat("x", 8192)); err == nil {
		t.Fatal("oversized session saved without an error")
	}
	if got := sessionCookies(rec); len(got) != 1 {
		t.Fatalf("%d session cookies after a failed save, want the last good one", len(got))
	}
}
