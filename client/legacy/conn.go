// Package legacyclient is the old protocol as npc spoke it: the handshake that
// npc status and npc register still use, and a client for tests of a server
// that accepts old npc. Remove it in the next major version.
package legacyclient

import (
	"bytes"
	"cmp"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"errors"
	"fmt"
	"math/rand"
	"net"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/djylb/slogx"

	"github.com/djylb/netx/tlsconn"
	"github.com/djylb/nps/bridge/legacy"
	"github.com/djylb/nps/lib/clock"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/connprofile"
	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/netaddr"
	"github.com/djylb/nps/lib/version"
	"github.com/quic-go/quic-go"
	"github.com/xtaci/kcp-go/v5"
)

const MaxPad = 64

const dialTimeout = 10 * time.Second

// protocolIndex is the protocol version spoken. Tests set older ones.
var protocolIndex = legacy.LatestIndex
var SkipTLSVerify = false

var QuicConfig = &quic.Config{
	KeepAlivePeriod:    10 * time.Second,
	MaxIdleTimeout:     30 * time.Second,
	MaxIncomingStreams: 100000,
}

var log = slogx.Module("client")

// GetTaskStatus prints the tunnels and hosts the server runs for vKey.
func GetTaskStatus(server string, vKey string, tp string, proxyUrl string, localIP string) error {
	c, uuid, err := NewConn(tp, vKey, server, proxyUrl, localIP)
	if err != nil {
		return fmt.Errorf("Failed to connect: %w", err)
	}
	//defer c.Close()
	err = SendType(c, legacy.WorkConfig, uuid)
	if err != nil {
		return fmt.Errorf("Failed to send type: %w", err)
	}
	if _, err := c.BufferWrite([]byte(legacy.WorkStatus)); err != nil {
		return fmt.Errorf("Failed to write WorkStatus: %w", err)
	}
	if _, err := c.Write([]byte(legacy.KeyDigest(vKey))); err != nil {
		return fmt.Errorf("Failed to write auth key: %w", err)
	}
	_ = c.SetDeadline(time.Now().Add(30 * time.Second))
	var isPub bool
	if err := binary.Read(c, binary.LittleEndian, &isPub); err != nil {
		return fmt.Errorf("Failed to read status: %w", err)
	}
	parts, err := legacy.ReadStatusList(c)
	if err != nil {
		return fmt.Errorf("Failed to read status list: %w", err)
	}
	fmt.Println("===== Active Tunnels/Hosts =====")
	fmt.Printf("Total active: %d\n", len(parts))
	for i, name := range parts {
		display := name
		if display == "" {
			display = "(no remark)"
		}
		fmt.Printf("  %d. %s\n", i+1, display)
	}
	return nil
}

// RegisterLocalIp registers the public address of this host with the server
// for hour hours.
func RegisterLocalIp(server string, vKey string, tp string, proxyUrl string, localIP string, hour int) error {
	c, uuid, err := NewConn(tp, vKey, server, proxyUrl, localIP)
	if err != nil {
		return err
	}
	//defer c.Close()
	err = SendType(c, legacy.WorkRegister, uuid)
	if err != nil {
		return err
	}
	if err := binary.Write(c, binary.LittleEndian, int32(hour)); err != nil {
		return err
	}
	log.Info("local ip registered", "server", server, "dur_ms", int64(hour)*time.Hour.Milliseconds())
	return nil
}

func VerifyState(state tls.ConnectionState, host string) (fingerprint []byte, verified bool) {
	if len(state.PeerCertificates) == 0 {
		return nil, false
	}
	leaf := state.PeerCertificates[0]
	inter := x509.NewCertPool()
	for _, cert := range state.PeerCertificates[1:] {
		inter.AddCert(cert)
	}
	roots, _ := x509.SystemCertPool()
	opts := x509.VerifyOptions{
		DNSName:       host,
		Roots:         roots,
		Intermediates: inter,
	}
	if _, err := leaf.Verify(opts); err != nil {
		verified = false
	} else {
		verified = true
	}
	sum := tlsconn.Fingerprint(leaf.Raw)
	return sum[:], verified
}

// NewConn Create a new connection with the server and verify it
func NewConn(tp string, vkey string, server string, proxyUrl string, localIP string) (*conn.Conn, string, error) {
	c, uuid, _, err := dialServer(tp, vkey, server, proxyUrl, localIP, nil)
	return c, uuid, err
}

// serverInfo is what a verified bridge connection tells about the server.
type serverInfo struct {
	// fingerprint is the SHA-256 of the bridge certificate, sent encrypted
	// with the vkey during the handshake; nil before protocol 0.28.0.
	fingerprint []byte
	// ip is the server address the connection reached, empty when it went
	// through a proxy (proxy_url, or a WebSocket bridge that may sit behind
	// a reverse proxy or CDN).
	ip string
	// via is the carrier candidate that connected.
	via connprofile.Candidate
}

var (
	// lastCandidate remembers, per profile (tp + " " + server), the key of
	// the candidate that connected last, which the next dial tries first.
	lastCandidate sync.Map
	// dirWarned says once that up and down candidates are skipped.
	dirWarned sync.Once
)

// dialServer is NewConn returning the server information as well. server is
// a profile (see connprofile): its candidates are tried in connprofile.Order
// until one connects; failed, when set, is told of each one that does not.
func dialServer(tp string, vkey string, server string, proxyUrl string, localIP string, failed func(connprofile.Candidate, error)) (*conn.Conn, string, serverInfo, error) {
	cands, err := connprofile.Parse(server, tp)
	if err != nil {
		return nil, "", serverInfo{}, err
	}
	for i := range cands {
		if cands[i].ALPNDefaulted {
			cands[i].ALPN = []string{"nps"}
		}
	}
	profileKey := tp + " " + server
	last, _ := lastCandidate.Load(profileKey)
	lastKey, _ := last.(string)
	var errs []error
	for _, cand := range connprofile.Order(cands, lastKey) {
		if !cand.Starts() {
			// one direction only: a member of a multi-pipe session
			dirWarned.Do(func() {
				log.Warn("one-direction candidates need zmux; skipped", "server", cand.Addr, "carrier", cand.Carrier)
			})
			continue
		}
		c, uuid, info, err := dialCandidate(cand, vkey, proxyUrl, localIP)
		if err == nil {
			lastCandidate.Store(profileKey, cand.Key())
			info.via = cand
			return c, uuid, info, nil
		}
		if failed != nil {
			failed(cand, err)
		}
		if len(cands) == 1 {
			return nil, "", serverInfo{}, err
		}
		log.Debug("server candidate failed", "server", cand.Addr, "carrier", cand.Carrier, "err", err)
		errs = append(errs, fmt.Errorf("%s: %w", cand.Raw, err))
	}
	return nil, "", serverInfo{}, errors.Join(errs...)
}

// dialCandidate connects through one carrier candidate.
func dialCandidate(cand connprofile.Candidate, vkey, proxyUrl, localIP string) (c *conn.Conn, uuid string, info serverInfo, err error) {
	tp := strings.ToLower(cand.Carrier)
	for _, target := range dialTargets(tp, cand.Addr) {
		c, uuid, info, err = dialBridge(tp, vkey, target, cand.Path, cand.ALPN, cand.Host, cand.SNI, proxyUrl, localIP)
		if err == nil {
			return c, uuid, info, nil
		}
		log.Debug("dial server failed", "carrier", tp, "server", target, "err", err)
	}
	return nil, "", serverInfo{}, err
}

// dialTargets lists the addresses to try for server. TCP dials try every
// address of a name themselves. A UDP dial takes only the first one, and only
// the handshake tells whether it is alive, so UDP transports try each in turn.
func dialTargets(tp, server string) []string {
	switch tp {
	case conn.TransportTCP, conn.TransportTLS, conn.TransportWS, conn.TransportWSS:
		return []string{server}
	}
	host, port, err := net.SplitHostPort(server)
	if err != nil || net.ParseIP(host) != nil {
		return []string{server}
	}
	ctx, cancel := context.WithTimeout(context.Background(), dialTimeout)
	defer cancel()
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
	if err != nil || len(ips) == 0 {
		// The dial reports the lookup error.
		return []string{server}
	}
	slices.SortStableFunc(ips, func(a, b net.IP) int {
		return cmp.Compare(ipFamilyOrder(a), ipFamilyOrder(b))
	})
	targets := make([]string, len(ips))
	for i, ip := range ips {
		targets[i] = net.JoinHostPort(ip.String(), port)
	}
	return targets
}

func ipFamilyOrder(ip net.IP) int {
	if ip.To4() != nil {
		return 0
	}
	return 1
}

// dialBridge connects to one server address and verifies the connection.
func dialBridge(tp, vkey, server, path string, alpn []string, host, sni, proxyUrl, localIP string) (*conn.Conn, string, serverInfo, error) {
	var err error
	var connection net.Conn
	var sess *kcp.UDPSession
	var isTls = false
	var tlsVerify = false
	var tlsFp []byte

	timeout := dialTimeout

	switch tp {
	case "tcp":
		connection, err = conn.DialProxy(proxyUrl, server, timeout, localIP)
	case "tls":
		isTls = true
		conf := &tls.Config{
			InsecureSkipVerify: true,
			ServerName:         sni,
		}
		conf.NextProtos = alpn
		rawConn, err := conn.DialProxy(proxyUrl, server, timeout, localIP)
		if err != nil {
			return nil, "", serverInfo{}, err
		}
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		tlsConn, err := tlsconn.Client(ctx, rawConn, conf)
		cancel()
		if err != nil {
			return nil, "", serverInfo{}, err
		}
		connection = tlsConn
		tlsFp, tlsVerify = VerifyState(tlsConn.ConnectionState(), sni)
	case "ws":
		rawConn, err := conn.DialProxy(proxyUrl, server, timeout, localIP)
		if err != nil {
			return nil, "", serverInfo{}, err
		}
		connection, err = conn.DialWS(rawConn, "ws://"+server+path, host, timeout)
		if err != nil {
			return nil, "", serverInfo{}, err
		}
	case "wss":
		isTls = true
		rawConn, err := conn.DialProxy(proxyUrl, server, timeout, localIP)
		if err != nil {
			return nil, "", serverInfo{}, err
		}
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		// this version's WebSocket runs over http/1.1 only
		conf := &tls.Config{InsecureSkipVerify: true, ServerName: sni, NextProtos: []string{"http/1.1"}}
		tlsConn, err := tlsconn.Client(ctx, rawConn, conf)
		cancel()
		if err != nil {
			return nil, "", serverInfo{}, err
		}
		connection, err = conn.DialWS(tlsConn, "wss://"+server+path, host, timeout)
		if err != nil {
			_ = tlsConn.Close()
			return nil, "", serverInfo{}, err
		}
		tlsFp, tlsVerify = VerifyState(tlsConn.ConnectionState(), sni)
	case conn.TransportQUIC:
		isTls = true
		tlsCfg := &tls.Config{
			InsecureSkipVerify: true,
			ServerName:         sni,
			NextProtos:         alpn,
		}
		ctx := context.Background()
		sess, err := conn.DialQuicWithLocalIP(ctx, server, tlsCfg, QuicConfig, localIP)
		if err != nil {
			return nil, "", serverInfo{}, fmt.Errorf("quic dial error: %w", err)
		}
		state := sess.ConnectionState().TLS
		tlsFp, tlsVerify = VerifyState(state, sni)
		stream, err := sess.OpenStreamSync(ctx)
		if err != nil {
			_ = sess.CloseWithError(0, "")
			return nil, "", serverInfo{}, fmt.Errorf("quic open stream error: %w", err)
		}
		connection = conn.NewQuicAutoCloseConn(stream, sess)
	default:
		sess, err = conn.DialKCPWithLocalIP(server, localIP)
		if err == nil {
			connection = sess
		}
	}

	if err != nil {
		return nil, "", serverInfo{}, err
	}
	if connection == nil {
		return nil, "", serverInfo{}, fmt.Errorf("newConn: unexpected nil connection for tp=%q server=%q", tp, server)
	}

	return handshakeConn(connection, tp, vkey, isTls, tlsVerify, tlsFp, proxyUrl)
}

// HandshakeHooks fix the time and address a handshake sends, for tests that
// record it.
var HandshakeHooks = struct {
	Timestamp  func() int64
	OutboundIP func() net.IP
	PadLen     func() int
}{
	Timestamp:  func() int64 { return clock.Now().Unix() - int64(rand.Intn(6)) },
	OutboundIP: netaddr.GetOutboundIP,
	PadLen:     func() int { return rand.Intn(MaxPad) },
}

// handshakeConn verifies connection, which reached the server through tp.
func handshakeConn(connection net.Conn, tp, vkey string, isTls, tlsVerify bool, tlsFp []byte, proxyUrl string) (*conn.Conn, string, serverInfo, error) {
	var info serverInfo
	var err error
	_ = connection.SetDeadline(time.Now().Add(dialTimeout))
	defer func() { _ = connection.SetDeadline(time.Time{}) }()

	c := conn.NewConn(connection)
	res, err := legacy.ClientHandshake(c, legacy.ClientConfig{
		Index:         protocolIndex,
		ClientVersion: version.VERSION,
		Key:           vkey,
		Transport:     tp,
		LocalIP:       HandshakeHooks.OutboundIP(),
		Timestamp:     HandshakeHooks.Timestamp,
		PadLen:        HandshakeHooks.PadLen,
		VerifyFingerprint: func(fp []byte) error {
			if !SkipTLSVerify && isTls && !tlsVerify && !bytes.Equal(fp, tlsFp) {
				log.Warn("server certificate verification failed", "server", connection.RemoteAddr().String(), "detail", "set -skip_verify=true to skip it")
				return errors.New("validation cert incorrect")
			}
			crypt.AddTrustedCert(vkey, fp)
			return nil
		},
	})
	if err != nil {
		_ = c.Close()
		switch {
		case errors.Is(err, legacy.ErrUnauthorized):
			log.Error("server rejected the vkey", "server", connection.RemoteAddr().String(), "vkey", slogx.Redacted(vkey), "err", err)
			return nil, "", serverInfo{}, errors.New("validation key incorrect")
		case errors.Is(err, legacy.ErrVersionMismatch):
			log.Warn("client and server versions do not match", "server", connection.RemoteAddr().String(), "version", legacy.CoreVersion(protocolIndex))
		}
		return nil, "", serverInfo{}, err
	}
	if res.VersionWarning {
		log.Warn("client and server versions do not match", "server", connection.RemoteAddr().String(), "version", legacy.CoreVersion(protocolIndex))
	}
	uuid := res.ServerUUID
	info.fingerprint = res.Fingerprint
	if proxyUrl == "" && tp != conn.TransportWS && tp != conn.TransportWSS {
		if ap, perr := netip.ParseAddrPort(connection.RemoteAddr().String()); perr == nil {
			info.ip = ap.Addr().Unmap().String()
		}
	}
	return c, uuid, info, err
}

// clientCaps returns what this client speaks.
func clientCaps() legacy.Caps { return legacy.CapsOf(protocolIndex, version.VERSION) }

func SendType(c *conn.Conn, connType, uuid string) error {
	if c == nil {
		return fmt.Errorf("sendType: nil conn (connType=%s uuid=%s)", connType, uuid)
	}
	_ = c.SetWriteDeadline(time.Now().Add(dialTimeout))
	defer func() { _ = c.SetWriteDeadline(time.Time{}) }()
	if err := legacy.WriteWork(c, clientCaps(), connType, uuid); err != nil {
		_ = c.Close()
		return err
	}
	c.SetAlive()
	return nil
}
