package client

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"time"

	"github.com/djylb/holepunch"
	"github.com/djylb/holepunch/portmapper"
	"github.com/djylb/nps/bridge/legacy"
	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/slogx"
	"github.com/quic-go/quic-go"
)

// p2pOptions are the holepunch options of both sides: this side's pin as
// transport data, the local address of local_ip, the port mapper and
// whether this side accepts a background session; the session logs to the
// p2p log with the peer field set to peer.
const p2pALPN = "h3"

var (
	p2pLog              = slogx.Module("p2p")
	errP2PTransportMode = errors.New("p2p session did not settle on a pinned QUIC transport")
)

// QuicConfig is the base of the QUIC configuration of a direct path.
var QuicConfig = &quic.Config{
	KeepAlivePeriod:    10 * time.Second,
	MaxIdleTimeout:     30 * time.Second,
	MaxIncomingStreams: 100000,
}

func p2pOptions(localIP string, mapper *portmapper.Mapper, background bool, peer string) holepunch.Options {
	opts := holepunch.Options{
		LocalAddr:        p2pLocalAddr(localIP),
		TransportMode:    legacy.P2PTransportMode,
		TransportData:    p2pLocalPin(),
		AcceptBackground: background,
		Logger:           p2pLog.With("route", peer),
	}
	if mapper != nil {
		opts.PortMapper = mapper
	}
	return opts
}

// p2pLocalAddr parses local_ip; anything that is not a unicast IP binds every
// address.
func p2pLocalAddr(localIP string) netip.Addr {
	a, err := netip.ParseAddr(strings.Trim(strings.TrimSpace(localIP), "[]"))
	if err != nil || a.IsUnspecified() || a.IsMulticast() {
		return netip.Addr{}
	}
	return a.Unmap()
}

// p2pQUICConfig is the QUIC configuration on a punched path; quicpath
// derives the keepalive, idle and handshake timeouts from the session.
func p2pQUICConfig() *quic.Config {
	return &quic.Config{MaxIncomingStreams: QuicConfig.MaxIncomingStreams}
}

// p2pLocalPin is this side's certificate fingerprint, sent to the peer as
// TransportData.
func p2pLocalPin() []byte {
	return crypt.GetCertFingerprint(*localCert())
}

// p2pServerTLS is the provider's QUIC TLS configuration: the visitor must
// present the certificate with pin, the fingerprint the visitor sent through
// the coordinator. No session tickets: every connection authenticates
// afresh.
func p2pServerTLS(pin []byte) *tls.Config {
	return crypt.PinFingerprint(pin).ServerConfig(&tls.Config{
		MinVersion:             tls.VersionTLS13,
		Certificates:           []tls.Certificate{*localCert()},
		SessionTicketsDisabled: true,
		NextProtos:             []string{p2pALPN},
	})
}

// p2pClientTLS is the visitor's QUIC TLS configuration: the provider's
// fingerprint, sent through the coordinator, replaces the chain
// verification.
func p2pClientTLS(pin []byte) *tls.Config {
	return crypt.PinFingerprint(pin).ClientConfig(&tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{*localCert()},
		ServerName:   crypt.GetFakeDomainName(),
		NextProtos:   []string{p2pALPN},
	})
}

// p2pPeerPin checks that sess settled on a pinned QUIC transport and
// returns the peer's pin.
func p2pPeerPin(sess *holepunch.Session) ([]byte, error) {
	res := sess.Result()
	if res.TransportMode != legacy.P2PTransportMode || len(res.PeerTransportData) == 0 {
		return nil, fmt.Errorf("%w: mode %q", errP2PTransportMode, res.TransportMode)
	}
	return res.PeerTransportData, nil
}
