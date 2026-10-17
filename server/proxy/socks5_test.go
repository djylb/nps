package proxy

import (
	"bytes"
	"errors"
	"io"
	"net"
	"net/netip"
	"strconv"
	"testing"
	"time"

	"github.com/djylb/netx"
	"github.com/djylb/netx/socks5"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/file"
)

type testUDP5Bridge struct {
	peers chan net.Conn
}

func (b *testUDP5Bridge) SendLinkInfo(int, *conn.Link, *file.Tunnel) (net.Conn, error) {
	serverSide, peerSide := net.Pipe()
	b.peers <- peerSide
	return serverSide, nil
}

func (b *testUDP5Bridge) IsServer() bool {
	return false
}

func (b *testUDP5Bridge) CliProcess(*conn.Conn, string) {}

type failBridge struct{}

func (failBridge) SendLinkInfo(int, *conn.Link, *file.Tunnel) (net.Conn, error) {
	return nil, io.EOF
}

func (failBridge) IsServer() bool { return false }

func (failBridge) CliProcess(*conn.Conn, string) {}

func newTestSocks5Task(port int, mode string) *file.Tunnel {
	task := &file.Tunnel{
		Port:     port,
		ServerIp: "127.0.0.1",
		Mode:     mode,
		Client: &file.Client{
			Id:   1,
			Cnf:  &file.Config{},
			Flow: &file.Flow{},
		},
		Flow:   &file.Flow{},
		Target: &file.Target{},
	}
	if mode == "mixProxy" {
		task.Socks5Proxy = true
	}
	return task
}

func startTestTunnelModeServer(t *testing.T, task *file.Tunnel, bridge NetBridge) (*TunnelModeServer, chan error) {
	t.Helper()

	server := NewTunnelModeServer(ProcessMix, bridge, task, false)
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Start()
	}()
	waitForTCP(t, net.JoinHostPort("127.0.0.1", strconv.Itoa(task.Port)))
	return server, errCh
}

func stopTestTunnelModeServer(t *testing.T, server *TunnelModeServer, errCh chan error) {
	t.Helper()

	_ = server.Close()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("server.Start() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server did not stop in time")
	}
}

func dialSocks5NoAuth(t *testing.T, port int) net.Conn {
	t.Helper()

	tcpConn, err := net.DialTimeout("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), time.Second)
	if err != nil {
		t.Fatalf("DialTimeout() error = %v", err)
	}
	if _, err := tcpConn.Write([]byte{5, 1, 0}); err != nil {
		_ = tcpConn.Close()
		t.Fatalf("write greeting: %v", err)
	}
	negotiation := make([]byte, 2)
	if _, err := io.ReadFull(tcpConn, negotiation); err != nil {
		_ = tcpConn.Close()
		t.Fatalf("read negotiation reply: %v", err)
	}
	if !bytes.Equal(negotiation, []byte{5, 0}) {
		_ = tcpConn.Close()
		t.Fatalf("unexpected negotiation reply %v", negotiation)
	}
	return tcpConn
}

func dialSocks5ImplicitNoAuth(t *testing.T, port int) net.Conn {
	t.Helper()

	tcpConn, err := net.DialTimeout("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), time.Second)
	if err != nil {
		t.Fatalf("DialTimeout() error = %v", err)
	}
	if _, err := tcpConn.Write([]byte{5, 0}); err != nil {
		_ = tcpConn.Close()
		t.Fatalf("write greeting: %v", err)
	}
	negotiation := make([]byte, 2)
	if _, err := io.ReadFull(tcpConn, negotiation); err != nil {
		_ = tcpConn.Close()
		t.Fatalf("read negotiation reply: %v", err)
	}
	if !bytes.Equal(negotiation, []byte{5, 0}) {
		_ = tcpConn.Close()
		t.Fatalf("unexpected negotiation reply %v", negotiation)
	}
	return tcpConn
}

func requestUDPAssociate(t *testing.T, c net.Conn) (string, int) {
	t.Helper()

	if err := socks5.WriteRequest(c, socks5.CmdUDPAssociate, socks5.Addr{}); err != nil {
		t.Fatalf("write udp associate: %v", err)
	}
	return readSocks5Reply(t, c)
}

func requestUDPAssociateFromIPv4(t *testing.T, c net.Conn, ip net.IP, port int) (string, int) {
	t.Helper()

	addr, ok := netip.AddrFromSlice(ip.To4())
	if !ok {
		t.Fatal("requestUDPAssociateFromIPv4 requires IPv4 address")
	}
	if err := socks5.WriteRequest(c, socks5.CmdUDPAssociate, socks5.Addr{IP: addr, Port: uint16(port)}); err != nil {
		t.Fatalf("write udp associate: %v", err)
	}
	return readSocks5Reply(t, c)
}

func freeTCPPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	defer func() { _ = listener.Close() }()
	return listener.Addr().(*net.TCPAddr).Port
}

func waitForTCP(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		c, err := net.DialTimeout("tcp4", addr, 100*time.Millisecond)
		if err == nil {
			_ = c.Close()
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("server %s did not start listening in time: %v", addr, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func readSocks5Reply(t *testing.T, c net.Conn) (string, int) {
	t.Helper()
	bound, err := socks5.ReadReply(c)
	if err != nil {
		t.Fatalf("read socks5 reply: %v", err)
	}
	if !bound.IP.IsValid() {
		t.Fatalf("unexpected socks5 bound address %v", bound)
	}
	return bound.IP.String(), int(bound.Port)
}

func readSocks5ReplyCode(t *testing.T, c net.Conn) socks5.Reply {
	t.Helper()
	_, err := socks5.ReadReply(c)
	var replyErr *socks5.ReplyError
	switch {
	case err == nil:
		return socks5.ReplySucceeded
	case errors.As(err, &replyErr):
		return replyErr.Reply
	default:
		t.Fatalf("read socks5 reply: %v", err)
		return 0
	}
}

type testSocks5Datagram struct {
	addr socks5.Addr
	data []byte
}

func writeSocks5Datagram(w *bytes.Buffer, addr net.Addr, payload []byte) error {
	b, err := socks5.AppendDatagram(nil, socks5.AddrFromNetAddr(addr), payload)
	if err != nil {
		return err
	}
	_, err = w.Write(b)
	return err
}

func readSocks5Datagram(b []byte) (testSocks5Datagram, error) {
	addr, n, err := socks5.DecodeDatagram(b)
	if err != nil {
		return testSocks5Datagram{}, err
	}
	return testSocks5Datagram{addr: addr, data: b[n:]}, nil
}

func TestTunnelModeServerSocks5UDPUsesFixedPort(t *testing.T) {
	testTunnelModeServerSocks5UDPUsesFixedPort(t, "mixProxy")
}

func TestTunnelModeServerStandaloneSocks5UDPUsesFixedPort(t *testing.T) {
	testTunnelModeServerSocks5UDPUsesFixedPort(t, "socks5")
}

func testTunnelModeServerSocks5UDPUsesFixedPort(t *testing.T, mode string) {
	port := freeTCPPort(t)
	task := newTestSocks5Task(port, mode)
	bridge := &testUDP5Bridge{peers: make(chan net.Conn, 1)}
	server, errCh := startTestTunnelModeServer(t, task, bridge)
	defer stopTestTunnelModeServer(t, server, errCh)

	tcpConn := dialSocks5NoAuth(t, port)
	defer func() { _ = tcpConn.Close() }()

	replyHost, replyPort := requestUDPAssociate(t, tcpConn)
	if replyPort != port {
		t.Fatalf("udp associate port = %d, want fixed port %d", replyPort, port)
	}
	if replyHost == "" {
		t.Fatal("udp associate host should not be empty")
	}

	var peerConn net.Conn
	select {
	case peerConn = <-bridge.peers:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for udp5 tunnel")
	}
	defer func() { _ = peerConn.Close() }()
	peerFramed := netx.NewFramedConn(peerConn, netx.WithFramedDatagrams())

	udpConn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("ListenUDP() error = %v", err)
	}
	defer func() { _ = udpConn.Close() }()

	targetAddr, _ := net.ResolveUDPAddr("udp4", "8.8.8.8:53")
	var outbound bytes.Buffer
	if err := writeSocks5Datagram(&outbound, targetAddr, []byte("ping")); err != nil {
		t.Fatalf("build outbound udp datagram: %v", err)
	}
	if _, err := udpConn.WriteToUDP(outbound.Bytes(), &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: port}); err != nil {
		t.Fatalf("WriteToUDP() error = %v", err)
	}

	frameBuf := make([]byte, netx.MaxFramePayload)
	_ = peerConn.SetReadDeadline(time.Now().Add(time.Second))
	n, err := peerFramed.Read(frameBuf)
	if err != nil {
		t.Fatalf("read framed udp payload: %v", err)
	}
	tunneled, err := readSocks5Datagram(frameBuf[:n])
	if err != nil {
		t.Fatalf("parse tunneled udp datagram: %v", err)
	}
	if tunneled.addr.String() != "8.8.8.8:53" {
		t.Fatalf("tunneled addr = %s, want 8.8.8.8:53", tunneled.addr.String())
	}
	if string(tunneled.data) != "ping" {
		t.Fatalf("tunneled payload = %q, want ping", tunneled.data)
	}

	replyAddr, _ := net.ResolveUDPAddr("udp4", "1.1.1.1:53")
	var inbound bytes.Buffer
	if err := writeSocks5Datagram(&inbound, replyAddr, []byte("pong")); err != nil {
		t.Fatalf("build inbound udp datagram: %v", err)
	}
	if _, err := peerFramed.Write(inbound.Bytes()); err != nil {
		t.Fatalf("write framed inbound udp payload: %v", err)
	}

	udpBuf := make([]byte, 2048)
	_ = udpConn.SetReadDeadline(time.Now().Add(time.Second))
	n, _, err = udpConn.ReadFromUDP(udpBuf)
	if err != nil {
		t.Fatalf("ReadFromUDP() error = %v", err)
	}
	replyDatagram, err := readSocks5Datagram(udpBuf[:n])
	if err != nil {
		t.Fatalf("parse udp reply datagram: %v", err)
	}
	if replyDatagram.addr.String() != "1.1.1.1:53" {
		t.Fatalf("reply addr = %s, want 1.1.1.1:53", replyDatagram.addr.String())
	}
	if string(replyDatagram.data) != "pong" {
		t.Fatalf("reply payload = %q, want pong", replyDatagram.data)
	}

	udpConn2, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("ListenUDP() second socket error = %v", err)
	}
	defer func() { _ = udpConn2.Close() }()

	targetAddr2, _ := net.ResolveUDPAddr("udp4", "9.9.9.9:9999")
	var outbound2 bytes.Buffer
	if err := writeSocks5Datagram(&outbound2, targetAddr2, []byte("ping2")); err != nil {
		t.Fatalf("build second outbound udp datagram: %v", err)
	}
	if _, err := udpConn2.WriteToUDP(outbound2.Bytes(), &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: port}); err != nil {
		t.Fatalf("WriteToUDP() second socket error = %v", err)
	}

	_ = peerConn.SetReadDeadline(time.Now().Add(time.Second))
	n, err = peerFramed.Read(frameBuf)
	if err != nil {
		t.Fatalf("read rebound framed udp payload: %v", err)
	}
	tunneled2, err := readSocks5Datagram(frameBuf[:n])
	if err != nil {
		t.Fatalf("parse rebound tunneled udp datagram: %v", err)
	}
	if tunneled2.addr.String() != "9.9.9.9:9999" {
		t.Fatalf("rebound tunneled addr = %s, want 9.9.9.9:9999", tunneled2.addr.String())
	}
	if string(tunneled2.data) != "ping2" {
		t.Fatalf("rebound tunneled payload = %q, want ping2", tunneled2.data)
	}

	replyAddr2, _ := net.ResolveUDPAddr("udp4", "2.2.2.2:2222")
	var inbound2 bytes.Buffer
	if err := writeSocks5Datagram(&inbound2, replyAddr2, []byte("pong2")); err != nil {
		t.Fatalf("build rebound inbound udp datagram: %v", err)
	}
	if _, err := peerFramed.Write(inbound2.Bytes()); err != nil {
		t.Fatalf("write rebound inbound udp payload: %v", err)
	}

	_ = udpConn2.SetReadDeadline(time.Now().Add(time.Second))
	n, _, err = udpConn2.ReadFromUDP(udpBuf)
	if err != nil {
		t.Fatalf("ReadFromUDP() rebound socket error = %v", err)
	}
	replyDatagram2, err := readSocks5Datagram(udpBuf[:n])
	if err != nil {
		t.Fatalf("parse rebound udp reply datagram: %v", err)
	}
	if replyDatagram2.addr.String() != "2.2.2.2:2222" {
		t.Fatalf("rebound reply addr = %s, want 2.2.2.2:2222", replyDatagram2.addr.String())
	}
	if string(replyDatagram2.data) != "pong2" {
		t.Fatalf("rebound reply payload = %q, want pong2", replyDatagram2.data)
	}

	_ = udpConn.SetReadDeadline(time.Now().Add(250 * time.Millisecond))
	if _, _, err := udpConn.ReadFromUDP(udpBuf); err == nil {
		t.Fatal("previous UDP source port should not keep receiving after session is rebound")
	}

	_ = tcpConn.Close()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if server.socks5UDP != nil && server.socks5UDP.sessionCount() == 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("socks5 udp session was not cleaned up after closing control connection")
}

func TestTunnelModeServerSocks5ConnectFailureReturnsReply(t *testing.T) {
	serverSide, clientSide := net.Pipe()
	defer func() { _ = clientSide.Close() }()

	task := newTestSocks5Task(1080, "mixProxy")
	server := NewTunnelModeServer(ProcessMix, failBridge{}, task, false)

	done := make(chan struct{})
	go func() {
		server.handleConn(serverSide)
		close(done)
	}()

	if _, err := clientSide.Write([]byte{5, 1, 0}); err != nil {
		t.Fatalf("write greeting: %v", err)
	}
	negotiation := make([]byte, 2)
	if _, err := io.ReadFull(clientSide, negotiation); err != nil {
		t.Fatalf("read negotiation reply: %v", err)
	}
	if !bytes.Equal(negotiation, []byte{5, 0}) {
		t.Fatalf("unexpected negotiation reply %v", negotiation)
	}

	if err := socks5.WriteRequest(clientSide, socks5.CmdConnect, socks5.Addr{IP: netip.MustParseAddr("8.8.8.8"), Port: 53}); err != nil {
		t.Fatalf("write connect request: %v", err)
	}
	if replyCode := readSocks5ReplyCode(t, clientSide); replyCode != socks5.ReplyGeneralFailure {
		t.Fatalf("reply code = %v, want %v", replyCode, socks5.ReplyGeneralFailure)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("server did not finish failed connect handling in time")
	}
}

func TestTunnelModeServerSocks5NoAuthAcceptsEmptyMethodList(t *testing.T) {
	serverSide, clientSide := net.Pipe()
	defer func() { _ = clientSide.Close() }()

	task := newTestSocks5Task(1080, "mixProxy")
	server := NewTunnelModeServer(ProcessMix, failBridge{}, task, false)

	done := make(chan struct{})
	go func() {
		server.handleConn(serverSide)
		close(done)
	}()

	if _, err := clientSide.Write([]byte{5, 0}); err != nil {
		t.Fatalf("write greeting: %v", err)
	}
	negotiation := make([]byte, 2)
	if _, err := io.ReadFull(clientSide, negotiation); err != nil {
		t.Fatalf("read negotiation reply: %v", err)
	}
	if !bytes.Equal(negotiation, []byte{5, 0}) {
		t.Fatalf("unexpected negotiation reply %v", negotiation)
	}

	if err := socks5.WriteRequest(clientSide, socks5.CmdConnect, socks5.Addr{IP: netip.MustParseAddr("8.8.8.8"), Port: 53}); err != nil {
		t.Fatalf("write connect request: %v", err)
	}
	if replyCode := readSocks5ReplyCode(t, clientSide); replyCode != socks5.ReplyGeneralFailure {
		t.Fatalf("reply code = %v, want %v", replyCode, socks5.ReplyGeneralFailure)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("server did not finish implicit no-auth connect handling in time")
	}
}

func TestTunnelModeServerSocks5UDPAssociateAllowsSinglePendingPortMismatch(t *testing.T) {
	port := freeTCPPort(t)
	task := newTestSocks5Task(port, "mixProxy")
	bridge := &testUDP5Bridge{peers: make(chan net.Conn, 1)}
	server, errCh := startTestTunnelModeServer(t, task, bridge)
	defer stopTestTunnelModeServer(t, server, errCh)

	tcpConn := dialSocks5ImplicitNoAuth(t, port)
	defer func() { _ = tcpConn.Close() }()

	udpConn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("ListenUDP() error = %v", err)
	}
	defer func() { _ = udpConn.Close() }()

	replyHost, replyPort := requestUDPAssociateFromIPv4(t, tcpConn, net.ParseIP("10.0.0.2"), 54321)
	if replyPort != port {
		t.Fatalf("udp associate port = %d, want fixed port %d", replyPort, port)
	}
	if replyHost == "" {
		t.Fatal("udp associate host should not be empty")
	}

	var peerConn net.Conn
	select {
	case peerConn = <-bridge.peers:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for udp5 tunnel")
	}
	defer func() { _ = peerConn.Close() }()
	peerFramed := netx.NewFramedConn(peerConn, netx.WithFramedDatagrams())

	targetAddr, _ := net.ResolveUDPAddr("udp4", "8.8.8.8:53")
	var outbound bytes.Buffer
	if err := writeSocks5Datagram(&outbound, targetAddr, []byte("ping")); err != nil {
		t.Fatalf("build outbound udp datagram: %v", err)
	}
	if _, err := udpConn.WriteToUDP(outbound.Bytes(), &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: port}); err != nil {
		t.Fatalf("WriteToUDP() error = %v", err)
	}
	frameBuf := make([]byte, netx.MaxFramePayload)
	_ = peerConn.SetReadDeadline(time.Now().Add(time.Second))
	n, err := peerFramed.Read(frameBuf)
	if err != nil {
		t.Fatalf("read framed udp payload: %v", err)
	}
	tunneled, err := readSocks5Datagram(frameBuf[:n])
	if err != nil {
		t.Fatalf("parse tunneled udp datagram: %v", err)
	}
	if tunneled.addr.String() != "8.8.8.8:53" {
		t.Fatalf("tunneled addr = %s, want 8.8.8.8:53", tunneled.addr.String())
	}
	if string(tunneled.data) != "ping" {
		t.Fatalf("tunneled payload = %q, want ping", tunneled.data)
	}
}

func TestTunnelModeServerSocks5UDPDisambiguatesSameIPSessionsByRequestedPort(t *testing.T) {
	port := freeTCPPort(t)
	task := newTestSocks5Task(port, "mixProxy")
	bridge := &testUDP5Bridge{peers: make(chan net.Conn, 2)}
	server, errCh := startTestTunnelModeServer(t, task, bridge)
	defer stopTestTunnelModeServer(t, server, errCh)

	tcpConn1 := dialSocks5ImplicitNoAuth(t, port)
	defer func() { _ = tcpConn1.Close() }()
	tcpConn2 := dialSocks5ImplicitNoAuth(t, port)
	defer func() { _ = tcpConn2.Close() }()

	udpConn1, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("ListenUDP() first socket error = %v", err)
	}
	defer func() { _ = udpConn1.Close() }()

	udpConn2, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("ListenUDP() second socket error = %v", err)
	}
	defer func() { _ = udpConn2.Close() }()

	_, replyPort1 := requestUDPAssociateFromIPv4(t, tcpConn1, net.ParseIP("127.0.0.1"), udpConn1.LocalAddr().(*net.UDPAddr).Port)
	_, replyPort2 := requestUDPAssociateFromIPv4(t, tcpConn2, net.ParseIP("127.0.0.1"), udpConn2.LocalAddr().(*net.UDPAddr).Port)
	if replyPort1 != port || replyPort2 != port {
		t.Fatalf("udp associate ports = (%d, %d), want fixed port %d", replyPort1, replyPort2, port)
	}

	var peerConn1, peerConn2 net.Conn
	select {
	case peerConn1 = <-bridge.peers:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for first udp5 tunnel")
	}
	select {
	case peerConn2 = <-bridge.peers:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for second udp5 tunnel")
	}
	defer func() { _ = peerConn1.Close() }()
	defer func() { _ = peerConn2.Close() }()

	peerFramed1 := netx.NewFramedConn(peerConn1, netx.WithFramedDatagrams())
	peerFramed2 := netx.NewFramedConn(peerConn2, netx.WithFramedDatagrams())
	frameBuf1 := make([]byte, netx.MaxFramePayload)
	frameBuf2 := make([]byte, netx.MaxFramePayload)

	targetAddr2, _ := net.ResolveUDPAddr("udp4", "9.9.9.9:9999")
	var outbound2 bytes.Buffer
	if err := writeSocks5Datagram(&outbound2, targetAddr2, []byte("ping2")); err != nil {
		t.Fatalf("build second outbound udp datagram: %v", err)
	}
	if _, err := udpConn2.WriteToUDP(outbound2.Bytes(), &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: port}); err != nil {
		t.Fatalf("WriteToUDP() second socket error = %v", err)
	}

	_ = peerConn1.SetReadDeadline(time.Now().Add(250 * time.Millisecond))
	if _, err := peerFramed1.Read(frameBuf1); err == nil {
		t.Fatal("second session packet should not be routed to first tunnel")
	}

	_ = peerConn2.SetReadDeadline(time.Now().Add(time.Second))
	n2, err := peerFramed2.Read(frameBuf2)
	if err != nil {
		t.Fatalf("read second framed udp payload: %v", err)
	}
	tunneled2, err := readSocks5Datagram(frameBuf2[:n2])
	if err != nil {
		t.Fatalf("parse second tunneled udp datagram: %v", err)
	}
	if tunneled2.addr.String() != "9.9.9.9:9999" {
		t.Fatalf("second tunneled addr = %s, want 9.9.9.9:9999", tunneled2.addr.String())
	}

	targetAddr1, _ := net.ResolveUDPAddr("udp4", "8.8.8.8:53")
	var outbound1 bytes.Buffer
	if err := writeSocks5Datagram(&outbound1, targetAddr1, []byte("ping1")); err != nil {
		t.Fatalf("build first outbound udp datagram: %v", err)
	}
	if _, err := udpConn1.WriteToUDP(outbound1.Bytes(), &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: port}); err != nil {
		t.Fatalf("WriteToUDP() first socket error = %v", err)
	}

	_ = peerConn1.SetReadDeadline(time.Now().Add(time.Second))
	n1, err := peerFramed1.Read(frameBuf1)
	if err != nil {
		t.Fatalf("read first framed udp payload: %v", err)
	}
	tunneled1, err := readSocks5Datagram(frameBuf1[:n1])
	if err != nil {
		t.Fatalf("parse first tunneled udp datagram: %v", err)
	}
	if tunneled1.addr.String() != "8.8.8.8:53" {
		t.Fatalf("first tunneled addr = %s, want 8.8.8.8:53", tunneled1.addr.String())
	}
}

func TestTunnelModeServerSocks5UDPPendingSessionDoesNotStealActiveSession(t *testing.T) {
	port := freeTCPPort(t)
	task := newTestSocks5Task(port, "mixProxy")
	bridge := &testUDP5Bridge{peers: make(chan net.Conn, 2)}
	server, errCh := startTestTunnelModeServer(t, task, bridge)
	defer stopTestTunnelModeServer(t, server, errCh)

	tcpConn1 := dialSocks5ImplicitNoAuth(t, port)
	defer func() { _ = tcpConn1.Close() }()
	tcpConn2 := dialSocks5ImplicitNoAuth(t, port)
	defer func() { _ = tcpConn2.Close() }()

	udpConn1, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("ListenUDP() first socket error = %v", err)
	}
	defer func() { _ = udpConn1.Close() }()

	_, replyPort1 := requestUDPAssociateFromIPv4(t, tcpConn1, net.ParseIP("127.0.0.1"), udpConn1.LocalAddr().(*net.UDPAddr).Port)
	if replyPort1 != port {
		t.Fatalf("udp associate port = %d, want fixed port %d", replyPort1, port)
	}

	var peerConn1 net.Conn
	select {
	case peerConn1 = <-bridge.peers:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for first udp5 tunnel")
	}
	defer func() { _ = peerConn1.Close() }()
	peerFramed1 := netx.NewFramedConn(peerConn1, netx.WithFramedDatagrams())

	targetAddr1, _ := net.ResolveUDPAddr("udp4", "8.8.8.8:53")
	var outbound1 bytes.Buffer
	if err := writeSocks5Datagram(&outbound1, targetAddr1, []byte("bind1")); err != nil {
		t.Fatalf("build first outbound udp datagram: %v", err)
	}
	if _, err := udpConn1.WriteToUDP(outbound1.Bytes(), &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: port}); err != nil {
		t.Fatalf("WriteToUDP() first socket error = %v", err)
	}

	frameBuf1 := make([]byte, netx.MaxFramePayload)
	_ = peerConn1.SetReadDeadline(time.Now().Add(time.Second))
	n1, err := peerFramed1.Read(frameBuf1)
	if err != nil {
		t.Fatalf("read first framed udp payload: %v", err)
	}
	tunneled1, err := readSocks5Datagram(frameBuf1[:n1])
	if err != nil {
		t.Fatalf("parse first tunneled udp datagram: %v", err)
	}
	if tunneled1.addr.String() != "8.8.8.8:53" || string(tunneled1.data) != "bind1" {
		t.Fatalf("unexpected first tunneled payload addr=%s data=%q", tunneled1.addr.String(), tunneled1.data)
	}

	_, replyPort2 := requestUDPAssociateFromIPv4(t, tcpConn2, net.ParseIP("127.0.0.1"), 54321)
	if replyPort2 != port {
		t.Fatalf("udp associate port = %d, want fixed port %d", replyPort2, port)
	}

	var peerConn2 net.Conn
	select {
	case peerConn2 = <-bridge.peers:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for second udp5 tunnel")
	}
	defer func() { _ = peerConn2.Close() }()
	peerFramed2 := netx.NewFramedConn(peerConn2, netx.WithFramedDatagrams())

	udpConn1b, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("ListenUDP() rebound socket error = %v", err)
	}
	defer func() { _ = udpConn1b.Close() }()

	targetAddrRebind, _ := net.ResolveUDPAddr("udp4", "9.9.9.9:9999")
	var outboundRebind bytes.Buffer
	if err := writeSocks5Datagram(&outboundRebind, targetAddrRebind, []byte("rebind")); err != nil {
		t.Fatalf("build rebound outbound udp datagram: %v", err)
	}
	if _, err := udpConn1b.WriteToUDP(outboundRebind.Bytes(), &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: port}); err != nil {
		t.Fatalf("WriteToUDP() rebound socket error = %v", err)
	}

	_ = peerConn1.SetReadDeadline(time.Now().Add(250 * time.Millisecond))
	if _, err := peerFramed1.Read(frameBuf1); err == nil {
		t.Fatal("active session should not be rebound while another pending session exists on the same IP")
	}
	frameBuf2 := make([]byte, netx.MaxFramePayload)
	_ = peerConn2.SetReadDeadline(time.Now().Add(250 * time.Millisecond))
	if _, err := peerFramed2.Read(frameBuf2); err == nil {
		t.Fatal("pending session should not steal ambiguous datagram from active session")
	}

	targetAddrStillOld, _ := net.ResolveUDPAddr("udp4", "1.1.1.1:53")
	var outboundStillOld bytes.Buffer
	if err := writeSocks5Datagram(&outboundStillOld, targetAddrStillOld, []byte("old-port")); err != nil {
		t.Fatalf("build old-port outbound udp datagram: %v", err)
	}
	if _, err := udpConn1.WriteToUDP(outboundStillOld.Bytes(), &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: port}); err != nil {
		t.Fatalf("WriteToUDP() old-port socket error = %v", err)
	}

	_ = peerConn1.SetReadDeadline(time.Now().Add(time.Second))
	n1, err = peerFramed1.Read(frameBuf1)
	if err != nil {
		t.Fatalf("read old-port framed udp payload: %v", err)
	}
	tunneledStillOld, err := readSocks5Datagram(frameBuf1[:n1])
	if err != nil {
		t.Fatalf("parse old-port tunneled udp datagram: %v", err)
	}
	if tunneledStillOld.addr.String() != "1.1.1.1:53" || string(tunneledStillOld.data) != "old-port" {
		t.Fatalf("unexpected old-port tunneled payload addr=%s data=%q", tunneledStillOld.addr.String(), tunneledStillOld.data)
	}
}

func TestTunnelModeServerSocks5UDPActiveSessionRebindsByRequestedPortHint(t *testing.T) {
	port := freeTCPPort(t)
	task := newTestSocks5Task(port, "mixProxy")
	bridge := &testUDP5Bridge{peers: make(chan net.Conn, 2)}
	server, errCh := startTestTunnelModeServer(t, task, bridge)
	defer stopTestTunnelModeServer(t, server, errCh)

	tcpConn1 := dialSocks5ImplicitNoAuth(t, port)
	defer func() { _ = tcpConn1.Close() }()

	udpConn1, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("ListenUDP() first socket error = %v", err)
	}
	defer func() { _ = udpConn1.Close() }()

	udpConn1b, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("ListenUDP() rebound socket error = %v", err)
	}
	defer func() { _ = udpConn1b.Close() }()

	udpConn2, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("ListenUDP() second socket error = %v", err)
	}
	defer func() { _ = udpConn2.Close() }()

	_, replyPort1 := requestUDPAssociateFromIPv4(t, tcpConn1, net.ParseIP("127.0.0.1"), udpConn1b.LocalAddr().(*net.UDPAddr).Port)
	if replyPort1 != port {
		t.Fatalf("udp associate port = %d, want fixed port %d", replyPort1, port)
	}

	var peerConn1 net.Conn
	select {
	case peerConn1 = <-bridge.peers:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for first udp5 tunnel")
	}
	defer func() { _ = peerConn1.Close() }()

	peerFramed1 := netx.NewFramedConn(peerConn1, netx.WithFramedDatagrams())
	frameBuf1 := make([]byte, netx.MaxFramePayload)

	targetAddr1, _ := net.ResolveUDPAddr("udp4", "8.8.8.8:53")
	var outbound1 bytes.Buffer
	if err := writeSocks5Datagram(&outbound1, targetAddr1, []byte("bind1")); err != nil {
		t.Fatalf("build first outbound udp datagram: %v", err)
	}
	if _, err := udpConn1.WriteToUDP(outbound1.Bytes(), &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: port}); err != nil {
		t.Fatalf("WriteToUDP() first socket error = %v", err)
	}
	_ = peerConn1.SetReadDeadline(time.Now().Add(time.Second))
	n1, err := peerFramed1.Read(frameBuf1)
	if err != nil {
		t.Fatalf("read first framed udp payload: %v", err)
	}
	tunneled1, err := readSocks5Datagram(frameBuf1[:n1])
	if err != nil {
		t.Fatalf("parse first tunneled udp datagram: %v", err)
	}
	if tunneled1.addr.String() != "8.8.8.8:53" || string(tunneled1.data) != "bind1" {
		t.Fatalf("unexpected first tunneled payload addr=%s data=%q", tunneled1.addr.String(), tunneled1.data)
	}

	tcpConn2 := dialSocks5ImplicitNoAuth(t, port)
	defer func() { _ = tcpConn2.Close() }()

	_, replyPort2 := requestUDPAssociateFromIPv4(t, tcpConn2, net.ParseIP("127.0.0.1"), udpConn2.LocalAddr().(*net.UDPAddr).Port)
	if replyPort2 != port {
		t.Fatalf("udp associate port = %d, want fixed port %d", replyPort2, port)
	}

	var peerConn2 net.Conn
	select {
	case peerConn2 = <-bridge.peers:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for second udp5 tunnel")
	}
	defer func() { _ = peerConn2.Close() }()

	peerFramed2 := netx.NewFramedConn(peerConn2, netx.WithFramedDatagrams())
	frameBuf2 := make([]byte, netx.MaxFramePayload)

	targetAddr2, _ := net.ResolveUDPAddr("udp4", "9.9.9.9:9999")
	var outbound2 bytes.Buffer
	if err := writeSocks5Datagram(&outbound2, targetAddr2, []byte("bind2")); err != nil {
		t.Fatalf("build second outbound udp datagram: %v", err)
	}
	if _, err := udpConn2.WriteToUDP(outbound2.Bytes(), &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: port}); err != nil {
		t.Fatalf("WriteToUDP() second socket error = %v", err)
	}
	_ = peerConn2.SetReadDeadline(time.Now().Add(time.Second))
	n2, err := peerFramed2.Read(frameBuf2)
	if err != nil {
		t.Fatalf("read second framed udp payload: %v", err)
	}
	tunneled2, err := readSocks5Datagram(frameBuf2[:n2])
	if err != nil {
		t.Fatalf("parse second tunneled udp datagram: %v", err)
	}
	if tunneled2.addr.String() != "9.9.9.9:9999" || string(tunneled2.data) != "bind2" {
		t.Fatalf("unexpected second tunneled payload addr=%s data=%q", tunneled2.addr.String(), tunneled2.data)
	}

	targetAddrRebind, _ := net.ResolveUDPAddr("udp4", "1.1.1.1:53")
	var outboundRebind bytes.Buffer
	if err := writeSocks5Datagram(&outboundRebind, targetAddrRebind, []byte("rebind")); err != nil {
		t.Fatalf("build rebound outbound udp datagram: %v", err)
	}
	if _, err := udpConn1b.WriteToUDP(outboundRebind.Bytes(), &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: port}); err != nil {
		t.Fatalf("WriteToUDP() rebound socket error = %v", err)
	}

	_ = peerConn2.SetReadDeadline(time.Now().Add(250 * time.Millisecond))
	if _, err := peerFramed2.Read(frameBuf2); err == nil {
		t.Fatal("rebound packet should not be routed to second tunnel")
	}

	_ = peerConn1.SetReadDeadline(time.Now().Add(time.Second))
	n1, err = peerFramed1.Read(frameBuf1)
	if err != nil {
		t.Fatalf("read rebound framed udp payload: %v", err)
	}
	tunneledRebind, err := readSocks5Datagram(frameBuf1[:n1])
	if err != nil {
		t.Fatalf("parse rebound tunneled udp datagram: %v", err)
	}
	if tunneledRebind.addr.String() != "1.1.1.1:53" || string(tunneledRebind.data) != "rebind" {
		t.Fatalf("unexpected rebound tunneled payload addr=%s data=%q", tunneledRebind.addr.String(), tunneledRebind.data)
	}
}

func TestTunnelModeServerSocks5UDPIdleSessionExpires(t *testing.T) {
	oldIdleTimeout := socks5UDPAssociateIdleTimeout
	oldSweepInterval := socks5UDPSweepInterval
	socks5UDPAssociateIdleTimeout = 200 * time.Millisecond
	socks5UDPSweepInterval = 50 * time.Millisecond
	defer func() {
		socks5UDPAssociateIdleTimeout = oldIdleTimeout
		socks5UDPSweepInterval = oldSweepInterval
	}()

	port := freeTCPPort(t)
	task := newTestSocks5Task(port, "mixProxy")
	bridge := &testUDP5Bridge{peers: make(chan net.Conn, 1)}
	server, errCh := startTestTunnelModeServer(t, task, bridge)
	defer stopTestTunnelModeServer(t, server, errCh)

	tcpConn := dialSocks5NoAuth(t, port)
	defer func() { _ = tcpConn.Close() }()

	_, replyPort := requestUDPAssociate(t, tcpConn)
	if replyPort != port {
		t.Fatalf("udp associate port = %d, want fixed port %d", replyPort, port)
	}

	select {
	case peerConn := <-bridge.peers:
		defer func() { _ = peerConn.Close() }()
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for udp5 tunnel")
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if server.socks5UDP != nil && server.socks5UDP.sessionCount() == 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("idle socks5 udp session was not cleaned up in time")
}

func TestSocks5UDPReplyAddrWithholdsPrivateAddressFromPublicClient(t *testing.T) {
	tcpLocal := &net.TCPAddr{IP: net.ParseIP("10.0.0.5"), Port: 1080}
	udpLocal := &net.UDPAddr{IP: net.IPv4zero, Port: 1080}
	publicClient := net.ParseIP("8.8.8.8")

	task := &file.Tunnel{Port: 1080}
	if got := socks5ReplyAddrFromNetAddr(task, tcpLocal, udpLocal, 1080, publicClient); got.IP.String() != "0.0.0.0" {
		t.Fatalf("reply host for public client = %s, want 0.0.0.0", got.IP)
	}
	if got := socks5ReplyAddrFromNetAddr(task, tcpLocal, udpLocal, 1080, net.ParseIP("10.0.0.9")); got.IP.String() != "10.0.0.5" {
		t.Fatalf("reply host for LAN client = %s, want 10.0.0.5", got.IP)
	}
	task.ServerIp = "10.0.0.5"
	if got := socks5ReplyAddrFromNetAddr(task, tcpLocal, udpLocal, 1080, publicClient); got.IP.String() != "10.0.0.5" {
		t.Fatalf("reply host with configured ServerIp = %s, want 10.0.0.5", got.IP)
	}
}

// startSocks5Handshake serves one connection of task over a pipe and returns
// the client side with a channel closed when the server is done.
func startSocks5Handshake(t *testing.T, task *file.Tunnel) (net.Conn, chan struct{}) {
	t.Helper()
	serverSide, clientSide := net.Pipe()
	t.Cleanup(func() { _ = clientSide.Close() })
	server := NewTunnelModeServer(ProcessMix, failBridge{}, task, false)
	done := make(chan struct{})
	go func() {
		server.handleConn(serverSide)
		close(done)
	}()
	_ = clientSide.SetDeadline(time.Now().Add(3 * time.Second))
	return clientSide, done
}

func waitSocks5Done(t *testing.T, done chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("server did not finish in time")
	}
}

func newAuthSocks5Task() *file.Tunnel {
	task := newTestSocks5Task(1080, "socks5")
	task.Client.Cnf.U = "user"
	task.Client.Cnf.P = "secret"
	return task
}

func TestTunnelModeServerSocks5UserPass(t *testing.T) {
	for _, tc := range []struct {
		name     string
		password string
		ok       bool
	}{
		{"valid", "secret", true},
		{"wrong password", "nope", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, done := startSocks5Handshake(t, newAuthSocks5Task())
			if err := socks5.WriteMethods(c, socks5.MethodNoAuth, socks5.MethodUserPass); err != nil {
				t.Fatal(err)
			}
			if m, err := socks5.ReadMethod(c); err != nil || m != socks5.MethodUserPass {
				t.Fatalf("selected method %v, %v; want username/password", m, err)
			}
			if err := socks5.WriteUserPass(c, "user", tc.password); err != nil {
				t.Fatal(err)
			}
			err := socks5.ReadUserPassStatus(c)
			if tc.ok {
				if err != nil {
					t.Fatalf("auth status: %v", err)
				}
				if err := socks5.WriteRequest(c, socks5.CmdConnect, socks5.Addr{Name: "example.com", Port: 443}); err != nil {
					t.Fatal(err)
				}
				if rep := readSocks5ReplyCode(t, c); rep != socks5.ReplyGeneralFailure {
					t.Fatalf("reply = %v, want %v", rep, socks5.ReplyGeneralFailure)
				}
			} else {
				if !errors.Is(err, socks5.ErrAuthFailed) {
					t.Fatalf("auth status error = %v, want %v", err, socks5.ErrAuthFailed)
				}
				if _, err := c.Read(make([]byte, 1)); err == nil {
					t.Fatal("connection still open after failed authentication")
				}
			}
			waitSocks5Done(t, done)
		})
	}
}

func TestTunnelModeServerSocks5AuthRequiredRejectsNoAuthOnly(t *testing.T) {
	c, done := startSocks5Handshake(t, newAuthSocks5Task())
	if err := socks5.WriteMethods(c, socks5.MethodNoAuth); err != nil {
		t.Fatal(err)
	}
	if _, err := socks5.ReadMethod(c); !errors.Is(err, socks5.ErrNoAcceptableMethod) {
		t.Fatalf("method selection error = %v, want %v", err, socks5.ErrNoAcceptableMethod)
	}
	waitSocks5Done(t, done)
}

func TestTunnelModeServerSocks5NoAuthAcceptsUserPassOnlyClient(t *testing.T) {
	c, done := startSocks5Handshake(t, newTestSocks5Task(1080, "socks5"))
	if err := socks5.WriteMethods(c, socks5.MethodUserPass); err != nil {
		t.Fatal(err)
	}
	if m, err := socks5.ReadMethod(c); err != nil || m != socks5.MethodUserPass {
		t.Fatalf("selected method %v, %v; want username/password", m, err)
	}
	// Version 5 instead of 1, as some clients send.
	if _, err := c.Write([]byte{5, 1, 'u', 1, 'p'}); err != nil {
		t.Fatal(err)
	}
	if err := socks5.ReadUserPassStatus(c); err != nil {
		t.Fatalf("auth status: %v", err)
	}
	if err := socks5.WriteRequest(c, socks5.CmdConnect, socks5.Addr{IP: netip.MustParseAddr("2001:db8::1"), Port: 80}); err != nil {
		t.Fatal(err)
	}
	if rep := readSocks5ReplyCode(t, c); rep != socks5.ReplyGeneralFailure {
		t.Fatalf("reply = %v, want %v", rep, socks5.ReplyGeneralFailure)
	}
	waitSocks5Done(t, done)
}

func TestTunnelModeServerSocks5RequestErrors(t *testing.T) {
	for _, tc := range []struct {
		name    string
		request []byte
		want    socks5.Reply
	}{
		{"unknown address type", []byte{5, 1, 0, 9, 0}, socks5.ReplyAddrTypeNotSupported},
		{"empty domain", []byte{5, 1, 0, 3, 0, 0, 80}, socks5.ReplyGeneralFailure},
		{"bind", []byte{5, 2, 0, 1, 127, 0, 0, 1, 0, 80}, socks5.ReplyCommandNotSupported},
		// A non-zero reserved byte is tolerated: the request reaches the
		// tunnel, which fails in this test.
		{"reserved byte set", []byte{5, 1, 1, 1, 127, 0, 0, 1, 0, 80}, socks5.ReplyGeneralFailure},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, done := startSocks5Handshake(t, newTestSocks5Task(1080, "socks5"))
			if _, err := c.Write([]byte{5, 1, 0}); err != nil {
				t.Fatal(err)
			}
			if m, err := socks5.ReadMethod(c); err != nil || m != socks5.MethodNoAuth {
				t.Fatalf("selected method %v, %v", m, err)
			}
			if _, err := c.Write(tc.request); err != nil {
				t.Fatal(err)
			}
			if rep := readSocks5ReplyCode(t, c); rep != tc.want {
				t.Fatalf("reply = %v, want %v", rep, tc.want)
			}
			waitSocks5Done(t, done)
		})
	}
}

func TestSocks5ReplyAddrIPv6Client(t *testing.T) {
	tcpLocal := &net.TCPAddr{IP: net.ParseIP("2001:db8::5"), Port: 1080}
	got := socks5ReplyAddrFromNetAddr(&file.Tunnel{Port: 1080}, tcpLocal, nil, 1080, net.ParseIP("2001:db8::9"))
	if got.String() != "[2001:db8::5]:1080" {
		t.Fatalf("reply addr = %v", got)
	}
	got = socks5ReplyAddrFromNetAddr(&file.Tunnel{Port: 1080}, &net.TCPAddr{IP: net.IPv4(10, 0, 0, 5), Port: 1080}, nil, 1080, net.ParseIP("2001:db8::9"))
	if b, _ := got.AppendBinary(nil); !bytes.Equal(b, append(append([]byte{4}, make([]byte, 16)...), 0x04, 0x38)) {
		t.Fatalf("IPv6 client without IPv6 local address: encoded %x, want the IPv6 zero address", b)
	}
}
