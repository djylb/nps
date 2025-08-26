package client

import (
	"context"
	"encoding/base64"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/logs"
	"golang.org/x/net/webdav"
)

// ------------------------------
// FileServerManager
// ------------------------------

type FileServerManager struct {
	ctx     context.Context
	cancel  context.CancelFunc
	mu      sync.Mutex
	wg      sync.WaitGroup
	servers map[string]*fileServer
}

type fileServer struct {
	srv      *http.Server
	listener *conn.VirtualListener
}

func NewFileServerManager(parentCtx context.Context) *FileServerManager {
	ctx, cancel := context.WithCancel(parentCtx)
	fsm := &FileServerManager{
		ctx:     ctx,
		cancel:  cancel,
		servers: make(map[string]*fileServer),
	}
	go func() {
		<-parentCtx.Done()
		fsm.CloseAll()
	}()
	return fsm
}

func (fsm *FileServerManager) StartFileServer(t *file.Tunnel, vkey string) {
	if fsm.ctx.Err() != nil {
		logs.Warn("file server manager already closed, skip StartFileServer")
		return
	}
	addr := net.JoinHostPort(t.ServerIp, strconv.Itoa(t.Port))
	vl := conn.NewVirtualListener(conn.ParseAddr(addr))
	if t.MultiAccount == nil {
		t.MultiAccount = new(file.MultiAccount)
	}
	ports := common.GetPorts(t.Ports)
	if len(ports) == 0 {
		ports = append(ports, 0)
	}
	t.Port = ports[0]
	key := crypt.GenerateUUID(vkey, t.Mode, t.ServerIp, strconv.Itoa(t.Port), t.LocalPath, t.StripPre, strconv.FormatBool(t.ReadOnly), t.MultiAccount.Content)
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
	//logs.Error("%v", accounts)
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
	logs.Info("start WebDAV server, local path %s, strip prefix %s, remote port %s", t.LocalPath, t.StripPre, t.Ports)
	fsm.mu.Lock()
	if fsm.servers == nil {
		fsm.mu.Unlock()
		logs.Warn("file server manager already closed, skip StartFileServer")
		return
	}
	fsm.servers[key.String()] = &fileServer{
		srv:      srv,
		listener: vl,
	}
	fsm.mu.Unlock()
	registered = true

	fsm.wg.Add(1)
	go func() {
		defer fsm.wg.Done()
		if err := srv.Serve(vl); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logs.Error("WebDAV Serve error: %v", err)
		}
	}()
}

func (fsm *FileServerManager) GetListenerByKey(key string) (*conn.VirtualListener, bool) {
	fsm.mu.Lock()
	defer fsm.mu.Unlock()
	entry, ok := fsm.servers[key]
	if !ok {
		return nil, false
	}
	return entry.listener, true
}

func (fsm *FileServerManager) CloseAll() {
	fsm.cancel()
	fsm.mu.Lock()
	entries := fsm.servers
	fsm.servers = nil
	fsm.mu.Unlock()
	for key, e := range entries {
		ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
		if err := e.srv.Shutdown(ctx2); err != nil {
			logs.Error("FileServer Shutdown error [%s]: %v", key, err)
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
