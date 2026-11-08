package client

import (
	"context"
	"crypto/tls"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/netaddr"
	"github.com/djylb/slogx"
)

var log = slogx.Module("client")

// localCert is the certificate npc presents on the encrypted links its
// visitors open, made once per process on first use.
var localCert = sync.OnceValue(func() *tls.Certificate {
	id, err := crypt.NewIdentity(tls.Certificate{})
	if err != nil {
		log.Error("create certificate failed", "err", err)
		return &tls.Certificate{}
	}
	return &id.Cert
})

// NowStatus is 1 while a session to the server is up.
var NowStatus atomic.Int32

// dialExit connects to a target as an exit does: from localIP when
// -local_ip_forward asks for it and the target is public.
func dialExit(ctx context.Context, network, host, localIP string, timeout time.Duration) (net.Conn, error) {
	d := net.Dialer{Timeout: timeout}
	if LocalIPForward && localIP != "" && netaddr.IsPublicHost(host) {
		if network == conn.LinkUDP {
			d.LocalAddr = netaddr.BuildUDPBindAddr(localIP)
		} else {
			d.LocalAddr = netaddr.BuildTCPBindAddr(localIP)
		}
	}
	return d.DialContext(ctx, network, host)
}
