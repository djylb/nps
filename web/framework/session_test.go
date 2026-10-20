package framework

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/djylb/nps/lib/servercfg"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/sessions"
)

func newSessionTestContext(t *testing.T) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	cfg := &servercfg.Snapshot{}
	cfg.Web.InstanceKeyFile = filepath.Join(t.TempDir(), "instance.key")
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

func TestPersistentSessionKeysIgnoreCredentials(t *testing.T) {
	cfg := &servercfg.Snapshot{}
	cfg.Web.InstanceKeyFile = filepath.Join(t.TempDir(), "instance.key")
	auth, enc, err := deriveSessionKeys(cfg)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Web.Username, cfg.Web.Password, cfg.Web.TOTPSecret = "new-admin", "new-password", "new-totp"
	cfg.Auth.Key, cfg.App.Name = "new-api-key", "another-app"
	auth2, enc2, err := deriveSessionKeys(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(auth, auth2) || !bytes.Equal(enc, enc2) {
		t.Fatal("credentials changed session keys")
	}
	if bytes.Equal(auth, enc) {
		t.Fatal("signing and encryption keys are identical")
	}
	first, err := buildSessionStore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	session := newBlankSession(first)
	session.Values["auth"] = true
	if err := session.Save(req, rec); err != nil {
		t.Fatal(err)
	}
	second, err := buildSessionStore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("GET", "/", nil)
	request.AddCookie(rec.Result().Cookies()[0])
	restored, err := second.Get(request, sessionName)
	if err != nil || restored.Values["auth"] != true {
		t.Fatalf("restart did not preserve session: %v", err)
	}
}

func TestRejectOldForgedAndTamperedCookies(t *testing.T) {
	cfg := &servercfg.Snapshot{}
	cfg.Web.Username, cfg.Web.Password, cfg.App.Name = "admin", "password", "nps"
	cfg.Web.InstanceKeyFile = filepath.Join(t.TempDir(), "instance.key")
	if err := InitSessionStore(cfg); err != nil {
		t.Fatal(err)
	}
	seed := fmt.Sprintf("%s|%s|%s|%s", cfg.Web.Username, cfg.Web.Password, cfg.Auth.Key, cfg.App.Name)
	oldAuth := sha256.Sum256([]byte("auth:" + seed))
	oldEnc := sha256.Sum256([]byte("enc:" + seed))
	oldStore := sessions.NewCookieStore(oldAuth[:], oldEnc[:])
	other := *cfg
	other.Web.InstanceKeyFile = filepath.Join(t.TempDir(), "instance.key")
	forgedStore, err := buildSessionStore(&other)
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []*sessions.CookieStore{oldStore, forgedStore, sessionStore} {
		req := httptest.NewRequest("GET", "/", nil)
		rec := httptest.NewRecorder()
		session := newBlankSession(source)
		session.Values["auth"], session.Values["isAdmin"] = true, true
		if err := session.Save(req, rec); err != nil {
			t.Fatal(err)
		}
		cookie := rec.Result().Cookies()[0]
		if source == sessionStore {
			cookie.Value = "x" + cookie.Value[1:]
		}
		request := httptest.NewRequest("GET", "/", nil)
		request.AddCookie(cookie)
		state, err := newSessionState(httptest.NewRecorder(), request, cfg)
		if err != nil {
			t.Fatal(err)
		}
		if state.Get("auth") == true || state.Get("isAdmin") == true {
			t.Fatal("forged cookie authenticated")
		}
	}
}

func TestSessionStoreFailsOnInvalidInstanceKey(t *testing.T) {
	cfg := &servercfg.Snapshot{}
	cfg.Web.InstanceKeyFile = filepath.Join(t.TempDir(), "instance.key")
	if err := os.WriteFile(cfg.Web.InstanceKeyFile, []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := InitSessionStore(cfg); err == nil {
		t.Fatal("initialized session store with broken key")
	}
	data, err := os.ReadFile(cfg.Web.InstanceKeyFile)
	if err != nil || string(data) != "broken" {
		t.Fatal("broken key was replaced")
	}
}
