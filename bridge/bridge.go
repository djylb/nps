package bridge

import (
	"context"
	"crypto/tls"
	"sync"
	"sync/atomic"
	"time"

	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/model"
	"github.com/djylb/nps/server/ports"

	"github.com/djylb/lifecycle"
	"github.com/djylb/netx"
	"github.com/djylb/nps/lib/acl"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/logs"
)

// Options are the settings a bridge is built with.
type Options struct {
	// DisconnectTime is how long, in seconds, a silent client tunnel lives.
	DisconnectTime int
	// SecureMode refuses the handshakes of npc versions that predate
	// secure mode.
	SecureMode bool
	// SelectMode is how a client with several nodes picks the node of a
	// new link.
	SelectMode SelectMode
	// Cert is the certificate of the TLS, WSS and P2P listeners and of the
	// encrypted links; npc pins it by the fingerprint the handshake sends.
	Cert tls.Certificate
	// NewUUID makes the server UUID a handshake sends; nil means a random
	// one (crypt.GetUUID).
	NewUUID func() string
	// AllowedPorts are the ports the tunnels npc publishes may listen on;
	// the zero value allows every port.
	AllowedPorts ports.Allowed
}

// bridgeHandshakeReadTimeout bounds each read of the client handshake.
const bridgeHandshakeReadTimeout = 10 * time.Second

// bridgeDispatchTimeout bounds handing work to the server loop, so that a
// stalled loop cannot pin client connections forever.
const bridgeDispatchTimeout = 5 * time.Second

// dispatch sends v on ch, giving up after bridgeDispatchTimeout.
func dispatch[T any](ch chan<- T, v T) bool {
	select {
	case ch <- v:
		return true
	default:
	}
	t := time.NewTimer(bridgeDispatchTimeout)
	defer t.Stop()
	select {
	case ch <- v:
		return true
	case <-t.C:
		return false
	}
}

type Bridge struct {
	presenceMu         sync.Mutex
	Client             *sync.Map
	VirtualTcpListener *netx.ChanListener
	VirtualTlsListener *netx.ChanListener
	VirtualWsListener  *netx.ChanListener
	VirtualWssListener *netx.ChanListener
	OpenHost           chan *model.Host
	OpenTask           chan *model.Tunnel
	CloseTask          chan *model.Tunnel
	CloseClient        chan int
	SecretChan         chan *conn.Secret
	runList            *sync.Map //map[int]interface{}
	store              Store
	disconnectTime     int
	secureMode         bool
	selectMode         SelectMode
	ports              ports.Allowed
	cert               tls.Certificate
	fingerprint        []byte // of cert
	newUUID            func() string
	routeMu            sync.Mutex                 // serializes publishing routes with pruning them
	closingMu          sync.Mutex                 // guards closing
	closing            map[int]struct{}           // clients left whose close signal is pending and that published nothing since
	p2p                atomic.Pointer[p2pService] // nil while P2P is off
	lookup             atomic.Pointer[TunnelLookup]
	webListener        atomic.Pointer[netx.ChanListener]
	ipRegListener      atomic.Pointer[netx.ChanListener]

	// scope runs the listeners and the background work and owns the
	// listeners; a listener that fails closes it. conns, its child, runs the
	// handling of the accepted connections and owns those still open; a
	// panic there is logged and leaves the bridge running.
	scope     *lifecycle.Scope
	conns     *lifecycle.Scope
	closeOnce sync.Once
}

// NewTunnel returns a bridge of the entries in store; runList holds the
// running tunnels. Changes of the stored entries are reported to it through
// EntriesChanged.
func NewTunnel(store Store, runList *sync.Map, opts Options) *Bridge {
	b := newBridge(runList, opts.DisconnectTime)
	b.store = store
	b.secureMode = opts.SecureMode
	b.selectMode = opts.SelectMode
	b.ports = opts.AllowedPorts
	b.cert = opts.Cert
	b.fingerprint = crypt.GetCertFingerprint(opts.Cert)
	if opts.NewUUID != nil {
		b.newUUID = opts.NewUUID
	}
	return b
}

// EntriesChanged has every node compare its health groups with the stored
// entries; the owner of the store calls it after tunnels or hosts changed.
// It returns promptly.
func (s *Bridge) EntriesChanged() { s.kickHealth() }

// GlobalSource returns the global source access lists, nil if there are
// none.
func (s *Bridge) GlobalSource() *acl.Source {
	if g := s.store.GetGlobal(); g != nil {
		return g.SourceACL()
	}
	return nil
}

func newBridge(runList *sync.Map, disconnectTime int) *Bridge {
	scope := lifecycle.New(context.Background(), lifecycle.CancelOnError().Expected(anyError))
	conns, _ := scope.Child("conns", lifecycle.Isolate(func(err error) { logs.Error("bridge: %v", err) }))
	return &Bridge{
		scope:          scope,
		conns:          conns,
		newUUID:        func() string { return crypt.GetUUID().String() },
		Client:         &sync.Map{},
		OpenHost:       make(chan *model.Host, 100),
		OpenTask:       make(chan *model.Tunnel, 100),
		CloseTask:      make(chan *model.Tunnel, 100),
		CloseClient:    make(chan int, 100),
		SecretChan:     make(chan *conn.Secret, 100),
		runList:        runList,
		disconnectTime: disconnectTime,
		closing:        make(map[int]struct{}),
	}
}

func (s *Bridge) DelClient(id int) {
	s.presenceMu.Lock()
	defer s.presenceMu.Unlock()
	if v, ok := s.Client.Load(id); ok {
		// Only remove the entry that was loaded: a client reconnecting with
		// the same id may already have replaced it and must stay registered.
		deleted := s.Client.CompareAndDelete(id, v)
		_ = v.(*Client).Close()
		if !deleted {
			return
		}

		s.recordConnection(id, false)
		if s.store.IsPubClient(id) {
			return
		}
		if c, err := s.store.GetClient(id); err == nil {
			s.closingMu.Lock()
			s.closing[id] = struct{}{}
			s.closingMu.Unlock()
			select {
			case s.CloseClient <- c.Id:
			default:
				logs.Warn("CloseClient channel is full, failed to send close signal for client %d", c.Id)
			}
		}
	}
}

// Connected reports whether client id has a session.
func (s *Bridge) Connected(id int) bool {
	_, ok := s.Client.Load(id)
	return ok
}

// CloseIfDisconnected runs cleanup, which removes what client id published,
// unless the client came back. A close signal is handled after the client
// left; by then it may have reconnected and published its entries again, which
// must stay. Publishing waits for cleanup, so entries published after the check
// are not removed either.
func (s *Bridge) CloseIfDisconnected(id int, cleanup func()) bool {
	s.routeMu.Lock()
	defer s.routeMu.Unlock()
	s.closingMu.Lock()
	_, pending := s.closing[id]
	delete(s.closing, id)
	s.closingMu.Unlock()
	if !pending || s.Connected(id) {
		return false
	}
	cleanup()
	return true
}

// cancelClose keeps a pending close signal of client id from removing what it
// publishes now: clients that publish before they connect their tunnel are not
// connected yet.
func (s *Bridge) cancelClose(id int) {
	s.closingMu.Lock()
	delete(s.closing, id)
	s.closingMu.Unlock()
}

func (s *Bridge) IsServer() bool {
	return true
}

// LinkCert is the certificate nps presents on the encrypted links.
func (s *Bridge) LinkCert() *tls.Certificate { return &s.cert }

// Registry mutations and their history events share a lock so rapid reconnects
// cannot publish an offline event after a newer connection has arrived.
func (s *Bridge) recordConnection(id int, online bool) {
	db := s.store
	c, err := db.GetClient(id)
	if err == nil {
		c.RecordConnection(online, time.Now())
	}
	db.ClientPresence(id, c, online)
}

func (s *Bridge) loadOrStoreClient(id int, client *Client) (any, bool) {
	s.presenceMu.Lock()
	defer s.presenceMu.Unlock()
	actual, loaded := s.Client.LoadOrStore(id, client)
	if !loaded {
		s.recordConnection(id, true)
	}
	return actual, loaded
}

// joinNode adds node, which holds a new connection, to client id, whose node
// of the same UUID takes the connection through addConn if there is one. A
// client being closed is replaced, so a node never joins one that is gone.
func (s *Bridge) joinNode(id int, uuid string, node *Node, addConn func(*Node)) (*Client, *Node) {
	for {
		// Either the client map owns the new client, or it is dropped: it holds only node.
		//noinspection GoResourceLeak
		client := NewClient(id, node, s.selectMode)
		v, loaded := s.loadOrStoreClient(id, client)
		if !loaded {
			client.MarkConnectedNow()
			return client, node
		}
		client = v.(*Client)
		client.MarkConnectedNow()
		client.RemoveOfflineNodesExcept(uuid, true)
		joined := node
		if n, ok := client.GetNodeByUUID(uuid); ok {
			joined = n
			addConn(n)
		} else {
			client.AddNode(node)
		}
		if !client.IsClosed() {
			client.MarkConnectedNow()
			return client, joined
		}
		s.Client.CompareAndDelete(id, client)
	}
}

func (s *Bridge) storeClient(id int, client *Client) {
	s.presenceMu.Lock()
	defer s.presenceMu.Unlock()
	s.Client.Store(id, client)
	s.recordConnection(id, true)
}
