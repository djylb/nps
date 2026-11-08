package client

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/djylb/nps/internal/coreproto/coreapi"
	"github.com/djylb/nps/lib/config"
)

// CoreOuter makes the outer session of cfg's profile to server on the new
// protocol. The command wires it (internal/coreproto/connect), so that the
// client sees the protocol only through coreapi.
var CoreOuter func(cfg *config.CommonConfig, server string) (coreapi.Outer, error)

var errNoCore = errors.New("the new protocol is not available in this build")

// coreKey is what makes two profiles of one process the same session to a
// server: the same dial and the same session settings.
type coreKey struct {
	server, vkey, carrier, localIP, proxy, pipes, scheduler string
	maxLanes, resume                                        int
	keep                                                    bool
}

// coreEntry is the outer session of a coreKey, shared by the host and the
// visitors of the process that use it (§9.4a).
type coreEntry struct {
	key   coreKey
	outer coreapi.Outer
	hosts atomic.Int32 // hosts running on it
	users int          // guarded by coreOuters.mu
	stop  context.CancelFunc
	done  chan struct{}
}

var coreOuters struct {
	mu sync.Mutex
	m  map[coreKey]*coreEntry
}

// acquireCore returns the entry for cfg's session to server, made on first
// use; release gives it back.
func acquireCore(cfg *config.CommonConfig, server string) (*coreEntry, error) {
	if CoreOuter == nil {
		return nil, errNoCore
	}
	key := coreKey{server, cfg.VKey, strings.ToLower(cfg.Tp), cfg.LocalIP, cfg.ProxyUrl, cfg.Pipes, cfg.SchedulerUp,
		cfg.MaxLanes, cfg.ResumeTimeout, cfg.KeepServer}
	coreOuters.mu.Lock()
	defer coreOuters.mu.Unlock()
	if e := coreOuters.m[key]; e != nil {
		e.users++
		return e, nil
	}
	outer, err := CoreOuter(cfg, server)
	if err != nil {
		return nil, err
	}
	e := &coreEntry{key: key, outer: outer, users: 1}
	outer.Hold(cfg.KeepServer, func() bool { return e.hosts.Load() > 0 })
	if cfg.KeepServer {
		var ctx context.Context
		ctx, e.stop = context.WithCancel(context.Background())
		e.done = make(chan struct{})
		go func() {
			defer close(e.done)
			keepOuter(ctx, outer)
		}()
	}
	if coreOuters.m == nil {
		coreOuters.m = make(map[coreKey]*coreEntry)
	}
	coreOuters.m[key] = e
	return e, nil
}

// release ends the entry's session once nobody uses it.
func (e *coreEntry) release() {
	coreOuters.mu.Lock()
	e.users--
	last := e.users == 0
	if last {
		delete(coreOuters.m, e.key)
	}
	coreOuters.mu.Unlock()
	if !last {
		return
	}
	if e.stop != nil {
		e.stop()
		<-e.done
	}
	e.outer.Close()
}

// keepOuter holds the session open for p2p_visitor_keep_server.
func keepOuter(ctx context.Context, outer coreapi.Dialer) {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		if _, err := outer.Dial(ctx); err != nil && ctx.Err() == nil {
			log.Debug("server session not made", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// RunCoreProfile hosts cfg's profile on the new protocol: it keeps the
// session, which serves what the server asks of this client, until ctx ends
// or, without autoReconnect, the session ends. The visitors of the process
// to the same server share the session and it is not closed while this runs.
// The error is a profile that cannot be made.
func RunCoreProfile(ctx context.Context, cfg *config.CommonConfig, autoReconnect bool) error {
	e, err := acquireCore(cfg, cfg.Server)
	if err != nil {
		return err
	}
	defer e.release()
	e.hosts.Add(1)
	defer e.hosts.Add(-1)
	RunCore(ctx, e.outer, autoReconnect)
	return nil
}

// UseCore makes the local sections of the manager reach their tasks over the
// new protocol, through the session of its CommonConfig that the process
// shares with the other users of that session. It is called before the
// sections start.
func (mgr *SecretManager) UseCore() error {
	server, _, _ := strings.Cut(mgr.cfg.Server, ";")
	e, err := acquireCore(mgr.cfg, server)
	if err != nil {
		return err
	}
	idle := time.Duration(mgr.cfg.RelayIdle) * time.Second
	mgr.coreVisitor = func(password, mode string) coreapi.Visitor {
		return e.outer.Visitor([]byte(password), mode, idle)
	}
	mgr.coreRelease = e.release
	return nil
}
