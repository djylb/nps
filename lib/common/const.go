package common

const (
	CONN_DATA_SEQ    = "*#*" // Separator
	VERIFY_EER       = "vkey"
	VERIFY_SUCCESS   = "sucs"
	WORK_MAIN        = "main"
	WORK_CHAN        = "chan"
	WORK_VISITOR     = "vstr"
	WORK_CONFIG      = "conf"
	WORK_REGISTER    = "rgst"
	WORK_SECRET      = "sert"
	WORK_FILE        = "file"
	WORK_P2P         = "p2pm"
	WORK_P2P_SESSION = "p2sj"
	WORK_STATUS      = "stus"
	RES_MSG          = "msg0"
	RES_CLOSE        = "clse"
	P2P_PUNCH_START  = "p2st" // P2P invitation on the signal connection
	NEW_TASK         = "task"
	NEW_CONF         = "conf"
	NEW_HOST         = "host"
	CONN_ALL         = "all"
	CONN_TCP         = "tcp"
	CONN_UDP         = "udp"
	CONN_KCP         = "kcp"
	CONN_TLS         = "tls"
	CONN_QUIC        = "quic"
	CONN_WEB         = "web"
	CONN_WS          = "ws"
	CONN_WSS         = "wss"
	CONN_TEST        = "TST"
	CONN_ACK         = "ACK"
	PING             = "ping"
	PONG             = "pong"
	TEST             = "test"

	TOTP_SEQ = "totp:" // TOTP Separator

	UnauthorizedBytes = "HTTP/1.1 401 Unauthorized\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n" +
		"WWW-Authenticate: Basic realm=\"easyProxy\"\r\n" +
		"\r\n" +
		"401 Unauthorized"

	ProxyAuthRequiredBytes = "HTTP/1.1 407 Proxy Authentication Required\r\n" +
		"Proxy-Authenticate: Basic realm=\"Proxy\"\r\n" +
		"Content-Length: 0\r\n" +
		"Connection: close\r\n" +
		"\r\n"

	ConnectionFailBytes = "HTTP/1.1 404 Not Found\r\n" +
		"\r\n"

	IPv4DNS = "8.8.8.8:53"
	IPv6DNS = "[2400:3200::1]:53"
)

var DefaultPort = map[string]string{
	"tcp":  "8024",
	"kcp":  "8024",
	"tls":  "8025",
	"quic": "8025",
	"ws":   "80",
	"wss":  "443",
}
