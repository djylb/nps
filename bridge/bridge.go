package bridge

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/djylb/netx"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/logs"
)

var (
	ServerTcpEnable  = false
	ServerKcpEnable  = false
	ServerQuicEnable = false
	ServerTlsEnable  = false
	ServerWsEnable   = false
	ServerWssEnable  = false
	ServerSecureMode = false
)

var bridgeHandshakeReadTimeout time.Duration = 10

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
	OpenHost           chan *file.Host
	OpenTask           chan *file.Tunnel
	CloseTask          chan *file.Tunnel
	CloseClient        chan int
	SecretChan         chan *conn.Secret
	runList            *sync.Map //map[int]interface{}
	disconnectTime     int
	p2p                atomic.Pointer[p2pService] // nil while P2P is off
}

func NewTunnel(runList *sync.Map, disconnectTime int) *Bridge {
	return &Bridge{
		Client:         &sync.Map{},
		OpenHost:       make(chan *file.Host, 100),
		OpenTask:       make(chan *file.Tunnel, 100),
		CloseTask:      make(chan *file.Tunnel, 100),
		CloseClient:    make(chan int, 100),
		SecretChan:     make(chan *conn.Secret, 100),
		runList:        runList,
		disconnectTime: disconnectTime,
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
		if file.GetDb().IsPubClient(id) {
			return
		}
		if c, err := file.GetDb().GetClient(id); err == nil {
			select {
			case s.CloseClient <- c.Id:
			default:
				logs.Warn("CloseClient channel is full, failed to send close signal for client %d", c.Id)
			}
		}
	}
}

func (s *Bridge) IsServer() bool {
	return true
}

// Registry mutations and their history events share a lock so rapid reconnects
// cannot publish an offline event after a newer connection has arrived.
func (s *Bridge) recordConnection(id int, online bool) {
	if c, err := file.GetDb().GetClient(id); err == nil {
		c.RecordConnection(online, time.Now())
	}
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

func (s *Bridge) storeClient(id int, client *Client) {
	s.presenceMu.Lock()
	defer s.presenceMu.Unlock()
	s.Client.Store(id, client)
	s.recordConnection(id, true)
}
