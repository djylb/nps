package bridge

import (
	"sync"
	"time"

	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/file"
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

type Bridge struct {
	Client             *sync.Map
	Register           *sync.Map
	VirtualTcpListener *conn.VirtualListener
	VirtualTlsListener *conn.VirtualListener
	VirtualWsListener  *conn.VirtualListener
	VirtualWssListener *conn.VirtualListener
	OpenHost           chan *file.Host
	OpenTask           chan *file.Tunnel
	CloseTask          chan *file.Tunnel
	CloseClient        chan int
	SecretChan         chan *conn.Secret
	ipVerify           bool
	runList            *sync.Map //map[int]interface{}
	disconnectTime     int
}

func NewTunnel(ipVerify bool, runList *sync.Map, disconnectTime int) *Bridge {
	return &Bridge{
		Client:         &sync.Map{},
		Register:       &sync.Map{},
		OpenHost:       make(chan *file.Host, 100),
		OpenTask:       make(chan *file.Tunnel, 100),
		CloseTask:      make(chan *file.Tunnel, 100),
		CloseClient:    make(chan int, 100),
		SecretChan:     make(chan *conn.Secret, 100),
		ipVerify:       ipVerify,
		runList:        runList,
		disconnectTime: disconnectTime,
	}
}
