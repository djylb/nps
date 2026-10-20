package framework

import (
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/djylb/nps/lib/instancekey"
	"github.com/djylb/nps/lib/logs"
	"github.com/djylb/nps/lib/servercfg"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/sessions"
)

const sessionName = "nps_session"

type sessionState struct {
	store   *sessions.CookieStore
	session *sessions.Session
	writer  http.ResponseWriter
	request *http.Request
}

var (
	sessionMu             sync.Mutex
	sessionStore          *sessions.CookieStore
	sessionConfigProvider = servercfg.Current
)

func InitSessionStore(cfg *servercfg.Snapshot) error {
	sessionMu.Lock()
	defer sessionMu.Unlock()
	if cfg == nil {
		cfg = currentSessionConfigLocked()
	}
	store, err := buildSessionStore(cfg)
	if err != nil {
		return err
	}
	sessionStore = store
	return nil
}

func SetSessionConfigProvider(provider func() *servercfg.Snapshot) {
	sessionMu.Lock()
	defer sessionMu.Unlock()
	if provider == nil {
		sessionConfigProvider = servercfg.Current
		return
	}
	sessionConfigProvider = provider
}

func newSessionState(w http.ResponseWriter, r *http.Request, cfg *servercfg.Snapshot) (*sessionState, error) {
	sessionMu.Lock()
	if sessionStore == nil {
		store, err := buildSessionStore(cfg)
		if err != nil {
			sessionMu.Unlock()
			return nil, err
		}
		sessionStore = store
	}
	store := sessionStore
	sessionMu.Unlock()

	session, err := store.Get(r, sessionName)
	if err != nil {
		logs.Warn("Invalid web session cookie, resetting session: %v", err)
		_ = clearSessionCookie(store, w, r)
		session = newBlankSession(store)
	}
	return &sessionState{
		store:   store,
		session: session,
		writer:  w,
		request: r,
	}, nil
}

func EnsureSession(g *gin.Context) error {
	if sessionStateFromContext(g) != nil {
		return nil
	}
	session, err := newSessionState(g.Writer, g.Request, currentSessionConfig())
	if err != nil {
		return err
	}
	g.Set(sessionContextKey, session)
	return nil
}

func SessionValue(g *gin.Context, key string) interface{} {
	session := sessionStateFromContext(g)
	if session == nil {
		return nil
	}
	return session.Get(key)
}

func SetSessionValue(g *gin.Context, key string, value interface{}) error {
	if err := EnsureSession(g); err != nil {
		return err
	}
	return sessionStateFromContext(g).Set(key, value)
}

func DeleteSessionValue(g *gin.Context, key string) error {
	if err := EnsureSession(g); err != nil {
		return err
	}
	return sessionStateFromContext(g).Delete(key)
}

func sessionStateFromContext(g *gin.Context) *sessionState {
	if v, ok := g.Get(sessionContextKey); ok {
		if session, ok := v.(*sessionState); ok {
			return session
		}
	}
	return nil
}

func (s *sessionState) Get(key string) interface{} {
	if s == nil || s.session == nil {
		return nil
	}
	return s.session.Values[key]
}

func (s *sessionState) Set(key string, value interface{}) error {
	if s == nil || s.session == nil {
		return nil
	}
	s.session.Values[key] = value
	return s.save()
}

func (s *sessionState) Delete(key string) error {
	if s == nil || s.session == nil {
		return nil
	}
	delete(s.session.Values, key)
	return s.save()
}

// save writes the session cookie in place of the one an earlier save of
// the same response wrote, so that a response carries the final session
// once however many values a request changes; a header of a cookie per
// change can outgrow the header buffer of a reverse proxy.
func (s *sessionState) save() error {
	return saveSessionCookie(s.session, s.request, s.writer)
}

// saveSessionCookie saves session into the Set-Cookie headers of w,
// replacing any cookie of the session set there before.
func saveSessionCookie(session *sessions.Session, r *http.Request, w http.ResponseWriter) error {
	rec := cookieRecorder{header: http.Header{}}
	if err := session.Save(r, rec); err != nil {
		logs.Warn("Save web session: %v", err)
		return err
	}
	h := w.Header()
	prefix := session.Name() + "="
	kept := h.Values("Set-Cookie")[:0:0]
	for _, c := range h.Values("Set-Cookie") {
		if !strings.HasPrefix(c, prefix) {
			kept = append(kept, c)
		}
	}
	h["Set-Cookie"] = append(kept, rec.header.Values("Set-Cookie")...)
	return nil
}

// cookieRecorder collects the headers a session save sets.
type cookieRecorder struct{ header http.Header }

func (c cookieRecorder) Header() http.Header         { return c.header }
func (c cookieRecorder) Write(b []byte) (int, error) { return len(b), nil }
func (c cookieRecorder) WriteHeader(int)             {}

func buildSessionStore(cfg *servercfg.Snapshot) (*sessions.CookieStore, error) {
	authKey, encKey, err := deriveSessionKeys(cfg)
	if err != nil {
		return nil, err
	}
	store := sessions.NewCookieStore(authKey, encKey)
	store.Options = &sessions.Options{
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	}
	return store, nil
}

func clearSessionCookie(store *sessions.CookieStore, w http.ResponseWriter, r *http.Request) error {
	session := newBlankSession(store)
	session.Options.MaxAge = -1
	return saveSessionCookie(session, r, w)
}

func newBlankSession(store *sessions.CookieStore) *sessions.Session {
	session := sessions.NewSession(store, sessionName)
	if store != nil && store.Options != nil {
		options := *store.Options
		session.Options = &options
	}
	session.IsNew = true
	return session
}

func deriveSessionKeys(cfg *servercfg.Snapshot) ([]byte, []byte, error) {
	if cfg == nil || cfg.Web.InstanceKeyFile == "" {
		return nil, nil, fmt.Errorf("instance key file is not configured")
	}
	key, err := instancekey.LoadOrCreate(cfg.Web.InstanceKeyFile)
	if err != nil {
		return nil, nil, err
	}
	auth, err := key.DeriveKey("nps/session/auth/v1", 32)
	if err != nil {
		return nil, nil, err
	}
	enc, err := key.DeriveKey("nps/session/encryption/v1", 32)
	return auth, enc, err
}

func currentSessionConfig() *servercfg.Snapshot {
	sessionMu.Lock()
	defer sessionMu.Unlock()
	return currentSessionConfigLocked()
}

func currentSessionConfigLocked() *servercfg.Snapshot {
	if sessionConfigProvider != nil {
		if cfg := sessionConfigProvider(); cfg != nil {
			return cfg
		}
	}
	return servercfg.Current()
}
