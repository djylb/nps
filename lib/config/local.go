package config

// LocalServer is a point-to-point access listener of a visitor npc: a
// [secret...] section, the -password command line or a launch payload. It
// reaches the secret tunnel of another client whose password it gives,
// directly (hole punching) or relayed by nps as ConnMode says.
type LocalServer struct {
	Type       string // LocalTCP, LocalUDP, LocalTCPUDP, LocalMixed, LocalTransparent
	ConnMode   string // ConnAuto, ConnRelay, ConnDirect
	Port       int
	Ip         string // listen address; empty for all
	Password   string
	Target     string // the target the visitor names: within the tunnel's destination list, or one of its targets
	LocalProxy bool
}

// Listener types of a LocalServer.
const (
	LocalTCP         = "tcp"
	LocalUDP         = "udp"
	LocalTCPUDP      = "tcp_udp"
	LocalMixed       = "mixed"       // HTTP and SOCKS5 proxy
	LocalTransparent = "transparent" // transparent proxy (Linux)
)

// Connection modes of a LocalServer, as the later major version names them.
const (
	ConnAuto   = "auto"   // direct when hole punching succeeds, else relayed by nps
	ConnRelay  = "relay"  // always relayed by nps
	ConnDirect = "direct" // only direct; fails when hole punching does
)

// LocalTypes and ConnModes are the accepted values.
var (
	LocalTypes = []string{LocalTCP, LocalUDP, LocalTCPUDP, LocalMixed, LocalTransparent}
	ConnModes  = []string{ConnAuto, ConnRelay, ConnDirect}
)
