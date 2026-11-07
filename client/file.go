package client

import (
	"context"
	"encoding/base64"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/djylb/netx"
	"github.com/djylb/nps/bridge/legacy"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/model"
	"github.com/djylb/nps/lib/netaddr"
	"golang.org/x/net/webdav"
)

// ------------------------------
// FileServerManager
// ------------------------------

type FileServerManager struct {
	ctx     context.Context
	cancel  context.CancelFunc
	mu      sync.Mutex
	unwatch func() bool // stops closing when the parent context ends; under mu
	wg      sync.WaitGroup
	servers map[string]*fileServer
}

type fileServer struct {
	srv      *http.Server
	listener *netx.ChanListener
}

func NewFileServerManager(parentCtx context.Context) *FileServerManager {
	ctx, cancel := context.WithCancel(parentCtx)
	fsm := &FileServerManager{
		ctx:     ctx,
		cancel:  cancel,
		servers: make(map[string]*fileServer),
	}
	// mu holds back a CloseAll of a parent that has ended already until
	// unwatch is set.
	fsm.mu.Lock()
	fsm.unwatch = context.AfterFunc(parentCtx, fsm.CloseAll)
	fsm.mu.Unlock()
	return fsm
}

func (fsm *FileServerManager) StartFileServer(t *model.Tunnel, vkey string) {
	if fsm.ctx.Err() != nil {
		log.Debug("file server manager closed, not starting file server")
		return
	}
	vl := conn.NewVirtualListener(netx.HostAddr(t.ServerIp, uint16(t.Port)))
	if t.MultiAccount == nil {
		t.MultiAccount = new(model.MultiAccount)
	}
	ports := netaddr.GetPorts(t.Ports)
	if len(ports) == 0 {
		ports = append(ports, 0)
	}
	t.Port = ports[0]
	key := legacy.FileKey(vkey, t)
	registered := false
	defer func() {
		if !registered {
			_ = vl.Close()
		}
	}()
	fs := http.FileServer(http.Dir(t.LocalPath))
	davHandler := &webdav.Handler{
		Prefix:     t.StripPre,
		FileSystem: webdav.Dir(t.LocalPath),
		LockSystem: webdav.NewMemLS(),
	}
	var handler http.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "GET", "HEAD":
			http.StripPrefix(t.StripPre, fs).ServeHTTP(w, r)
		default:
			davHandler.ServeHTTP(w, r)
		}
	})
	accounts := make(map[string]string)
	if t.Client != nil && t.Client.Cnf != nil && t.Client.Cnf.U != "" && t.Client.Cnf.P != "" {
		accounts[t.Client.Cnf.U] = t.Client.Cnf.P
	}
	if t.MultiAccount != nil {
		for user, pass := range t.MultiAccount.AccountMap {
			accounts[user] = pass
		}
	}
	if t.UserAuth != nil {
		for user, pass := range t.UserAuth.AccountMap {
			accounts[user] = pass
		}
	}
	if len(accounts) > 0 {
		handler = basicAuth(accounts, "WebDAV", handler)
	}
	if t.ReadOnly {
		handler = readOnly(handler)
	}
	srv := &http.Server{
		BaseContext: func(_ net.Listener) context.Context { return fsm.ctx },
		Handler:     handler,
	}
	log.Info("webdav server started", "path", t.LocalPath, "strip_prefix", t.StripPre, "ports", t.Ports)
	fsm.mu.Lock()
	if fsm.servers == nil {
		fsm.mu.Unlock()
		log.Debug("file server manager closed, not starting file server")
		return
	}
	fsm.servers[key] = &fileServer{
		srv:      srv,
		listener: vl,
	}
	fsm.mu.Unlock()
	registered = true

	fsm.wg.Add(1)
	go func() {
		defer fsm.wg.Done()
		if err := srv.Serve(vl); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("webdav server failed", "err", err)
		}
	}()
}

func (fsm *FileServerManager) GetListenerByKey(key string) (*netx.ChanListener, bool) {
	fsm.mu.Lock()
	defer fsm.mu.Unlock()
	entry, ok := fsm.servers[key]
	if !ok {
		return nil, false
	}
	return entry.listener, true
}

func (fsm *FileServerManager) CloseAll() {
	fsm.mu.Lock()
	fsm.unwatch()
	fsm.mu.Unlock()
	fsm.cancel()
	fsm.mu.Lock()
	entries := fsm.servers
	fsm.servers = nil
	fsm.mu.Unlock()
	for key, e := range entries {
		ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
		if err := e.srv.Shutdown(ctx2); err != nil {
			log.Warn("file server shutdown failed", "key", key, "err", err)
		}
		cancel2()
		_ = e.listener.Close()
	}
	fsm.wg.Wait()
}

func basicAuth(users map[string]string, realm string, next http.Handler) http.Handler {
	if len(users) == 0 {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Basic ") {
			w.Header().Set("WWW-Authenticate", `Basic realm="`+realm+`"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		payload, err := base64.StdEncoding.DecodeString(auth[len("Basic "):])
		if err != nil {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		parts := strings.SplitN(string(payload), ":", 2)
		if len(parts) != 2 || users[parts[0]] != parts[1] {
			w.Header().Set("WWW-Authenticate", `Basic realm="`+realm+`"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func readOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, "PROPFIND":
			next.ServeHTTP(w, r)
		default:
			w.Header().Set("Allow", "GET, HEAD, PROPFIND")
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		}
	})
}
