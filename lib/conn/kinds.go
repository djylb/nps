package conn

// Transports a client connects to the bridge with.
const (
	TransportTCP  = "tcp"
	TransportKCP  = "kcp"
	TransportQUIC = "quic"
	TransportTLS  = "tls"
	TransportWS   = "ws"
	TransportWSS  = "wss"
)

// Kinds of the links a tunnel opens, as Link.ConnType names them.
const (
	LinkTCP  = "tcp"
	LinkUDP  = "udp"
	LinkUDP5 = "udp5" // SOCKS5 datagrams with their headers
	LinkFile = "file" // the file server of a client
	LinkPing = "ping"
)
