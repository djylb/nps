package proxy

import (
	"bytes"
	"io"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/file"
)

type testUDP5Bridge struct {
	peers chan net.Conn
}

func (b *testUDP5Bridge) SendLinkInfo(clientID int, link *conn.Link, t *file.Tunnel) (net.Conn, error) {
	serverSide, peerSide := net.Pipe()
	b.peers <- peerSide
	return serverSide, nil
}

func (b *testUDP5Bridge) IsServer() bool {
	return false
}

func (b *testUDP5Bridge) CliProcess(*conn.Conn, string) {}

type failBridge struct{}

func (failBridge) SendLinkInfo(clientID int, link *conn.Link, t *file.Tunnel) (net.Conn, error) {
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

	if _, err := c.Write([]byte{5, associateMethod, 0, ipV4, 0, 0, 0, 0, 0, 0}); err != nil {
		t.Fatalf("write udp associate: %v", err)
	}
	return readSocks5Reply(t, c)
}

func requestUDPAssociateFromIPv4(t *testing.T, c net.Conn, ip net.IP, port int) (string, int) {
	t.Helper()

	ip = ip.To4()
	if ip == nil {
		t.Fatal("requestUDPAssociateFromIPv4 requires IPv4 address")
	}
	req := []byte{5, associateMethod, 0, ipV4, ip[0], ip[1], ip[2], ip[3], byte(port >> 8), byte(port)}
	if _, err := c.Write(req); err != nil {
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
		conn, err := net.DialTimeout("tcp4", addr, 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()
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
	header := make([]byte, 4)
	if _, err := io.ReadFull(c, header); err != nil {
		t.Fatalf("read socks5 reply header: %v", err)
	}
	if header[0] != 5 || header[1] != succeeded {
		t.Fatalf("unexpected socks5 reply header %v", header)
	}

	var host string
	switch header[3] {
	case ipV4:
		addr := make([]byte, net.IPv4len)
		if _, err := io.ReadFull(c, addr); err != nil {
			t.Fatalf("read socks5 ipv4 addr: %v", err)
		}
		host = net.IP(addr).String()
	case ipV6:
		addr := make([]byte, net.IPv6len)
		if _, err := io.ReadFull(c, addr); err != nil {
			t.Fatalf("read socks5 ipv6 addr: %v", err)
		}
		host = net.IP(addr).String()
	default:
		t.Fatalf("unexpected atyp %d", header[3])
	}

	portBuf := make([]byte, 2)
	if _, err := io.ReadFull(c, portBuf); err != nil {
		t.Fatalf("read socks5 port: %v", err)
	}
	return host, int(portBuf[0])<<8 | int(portBuf[1])
}

func readSocks5ReplyCode(t *testing.T, c net.Conn) byte {
	t.Helper()
	header := make([]byte, 4)
	if _, err := io.ReadFull(c, header); err != nil {
		t.Fatalf("read socks5 reply header: %v", err)
	}
	replyCode := header[1]
	addrLen := 0
	switch header[3] {
	case ipV4:
		addrLen = net.IPv4len
	case ipV6:
		addrLen = net.IPv6len
	case domainName:
		var l [1]byte
		if _, err := io.ReadFull(c, l[:]); err != nil {
			t.Fatalf("read socks5 domain length: %v", err)
		}
		addrLen = int(l[0])
	default:
		t.Fatalf("unexpected atyp %d", header[3])
	}
	skip := make([]byte, addrLen+2)
	if _, err := io.ReadFull(c, skip); err != nil {
		t.Fatalf("read socks5 reply tail: %v", err)
	}
	return replyCode
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
	peerFramed := conn.WrapFramed(peerConn)

	udpConn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("ListenUDP() error = %v", err)
	}
	defer func() { _ = udpConn.Close() }()

	targetAddr, _ := net.ResolveUDPAddr("udp4", "8.8.8.8:53")
	var outbound bytes.Buffer
	if err := common.NewUDPDatagram(common.NewUDPHeader(0, 0, common.ToSocksAddr(targetAddr)), []byte("ping")).Write(&outbound); err != nil {
		t.Fatalf("build outbound udp datagram: %v", err)
	}
	if _, err := udpConn.WriteToUDP(outbound.Bytes(), &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: port}); err != nil {
		t.Fatalf("WriteToUDP() error = %v", err)
	}

	frameBuf := make([]byte, conn.MaxFramePayload)
	_ = peerConn.SetReadDeadline(time.Now().Add(time.Second))
	n, err := peerFramed.Read(frameBuf)
	if err != nil {
		t.Fatalf("read framed udp payload: %v", err)
	}
	tunneled, err := common.ReadUDPDatagram(bytes.NewReader(frameBuf[:n]))
	if err != nil {
		t.Fatalf("parse tunneled udp datagram: %v", err)
	}
	if tunneled.Header.Addr.String() != "8.8.8.8:53" {
		t.Fatalf("tunneled addr = %s, want 8.8.8.8:53", tunneled.Header.Addr.String())
	}
	if string(tunneled.Data) != "ping" {
		t.Fatalf("tunneled payload = %q, want ping", tunneled.Data)
	}

	replyAddr, _ := net.ResolveUDPAddr("udp4", "1.1.1.1:53")
	var inbound bytes.Buffer
	if err := common.NewUDPDatagram(common.NewUDPHeader(0, 0, common.ToSocksAddr(replyAddr)), []byte("pong")).Write(&inbound); err != nil {
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
	replyDatagram, err := common.ReadUDPDatagram(bytes.NewReader(udpBuf[:n]))
	if err != nil {
		t.Fatalf("parse udp reply datagram: %v", err)
	}
	if replyDatagram.Header.Addr.String() != "1.1.1.1:53" {
		t.Fatalf("reply addr = %s, want 1.1.1.1:53", replyDatagram.Header.Addr.String())
	}
	if string(replyDatagram.Data) != "pong" {
		t.Fatalf("reply payload = %q, want pong", replyDatagram.Data)
	}

	udpConn2, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("ListenUDP() second socket error = %v", err)
	}
	defer func() { _ = udpConn2.Close() }()

	targetAddr2, _ := net.ResolveUDPAddr("udp4", "9.9.9.9:9999")
	var outbound2 bytes.Buffer
	if err := common.NewUDPDatagram(common.NewUDPHeader(0, 0, common.ToSocksAddr(targetAddr2)), []byte("ping2")).Write(&outbound2); err != nil {
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
	tunneled2, err := common.ReadUDPDatagram(bytes.NewReader(frameBuf[:n]))
	if err != nil {
		t.Fatalf("parse rebound tunneled udp datagram: %v", err)
	}
	if tunneled2.Header.Addr.String() != "9.9.9.9:9999" {
		t.Fatalf("rebound tunneled addr = %s, want 9.9.9.9:9999", tunneled2.Header.Addr.String())
	}
	if string(tunneled2.Data) != "ping2" {
		t.Fatalf("rebound tunneled payload = %q, want ping2", tunneled2.Data)
	}

	replyAddr2, _ := net.ResolveUDPAddr("udp4", "2.2.2.2:2222")
	var inbound2 bytes.Buffer
	if err := common.NewUDPDatagram(common.NewUDPHeader(0, 0, common.ToSocksAddr(replyAddr2)), []byte("pong2")).Write(&inbound2); err != nil {
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
	replyDatagram2, err := common.ReadUDPDatagram(bytes.NewReader(udpBuf[:n]))
	if err != nil {
		t.Fatalf("parse rebound udp reply datagram: %v", err)
	}
	if replyDatagram2.Header.Addr.String() != "2.2.2.2:2222" {
		t.Fatalf("rebound reply addr = %s, want 2.2.2.2:2222", replyDatagram2.Header.Addr.String())
	}
	if string(replyDatagram2.Data) != "pong2" {
		t.Fatalf("rebound reply payload = %q, want pong2", replyDatagram2.Data)
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

	if _, err := clientSide.Write([]byte{5, connectMethod, 0, ipV4, 8, 8, 8, 8, 0, 53}); err != nil {
		t.Fatalf("write connect request: %v", err)
	}
	if replyCode := readSocks5ReplyCode(t, clientSide); replyCode != serverFailure {
		t.Fatalf("reply code = %d, want %d", replyCode, serverFailure)
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

	if _, err := clientSide.Write([]byte{5, connectMethod, 0, ipV4, 8, 8, 8, 8, 0, 53}); err != nil {
		t.Fatalf("write connect request: %v", err)
	}
	if replyCode := readSocks5ReplyCode(t, clientSide); replyCode != serverFailure {
		t.Fatalf("reply code = %d, want %d", replyCode, serverFailure)
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
	peerFramed := conn.WrapFramed(peerConn)

	targetAddr, _ := net.ResolveUDPAddr("udp4", "8.8.8.8:53")
	var outbound bytes.Buffer
	if err := common.NewUDPDatagram(common.NewUDPHeader(0, 0, common.ToSocksAddr(targetAddr)), []byte("ping")).Write(&outbound); err != nil {
		t.Fatalf("build outbound udp datagram: %v", err)
	}
	if _, err := udpConn.WriteToUDP(outbound.Bytes(), &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: port}); err != nil {
		t.Fatalf("WriteToUDP() error = %v", err)
	}
	frameBuf := make([]byte, conn.MaxFramePayload)
	_ = peerConn.SetReadDeadline(time.Now().Add(time.Second))
	n, err := peerFramed.Read(frameBuf)
	if err != nil {
		t.Fatalf("read framed udp payload: %v", err)
	}
	tunneled, err := common.ReadUDPDatagram(bytes.NewReader(frameBuf[:n]))
	if err != nil {
		t.Fatalf("parse tunneled udp datagram: %v", err)
	}
	if tunneled.Header.Addr.String() != "8.8.8.8:53" {
		t.Fatalf("tunneled addr = %s, want 8.8.8.8:53", tunneled.Header.Addr.String())
	}
	if string(tunneled.Data) != "ping" {
		t.Fatalf("tunneled payload = %q, want ping", tunneled.Data)
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

	peerFramed1 := conn.WrapFramed(peerConn1)
	peerFramed2 := conn.WrapFramed(peerConn2)
	frameBuf1 := make([]byte, conn.MaxFramePayload)
	frameBuf2 := make([]byte, conn.MaxFramePayload)

	targetAddr2, _ := net.ResolveUDPAddr("udp4", "9.9.9.9:9999")
	var outbound2 bytes.Buffer
	if err := common.NewUDPDatagram(common.NewUDPHeader(0, 0, common.ToSocksAddr(targetAddr2)), []byte("ping2")).Write(&outbound2); err != nil {
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
	tunneled2, err := common.ReadUDPDatagram(bytes.NewReader(frameBuf2[:n2]))
	if err != nil {
		t.Fatalf("parse second tunneled udp datagram: %v", err)
	}
	if tunneled2.Header.Addr.String() != "9.9.9.9:9999" {
		t.Fatalf("second tunneled addr = %s, want 9.9.9.9:9999", tunneled2.Header.Addr.String())
	}

	targetAddr1, _ := net.ResolveUDPAddr("udp4", "8.8.8.8:53")
	var outbound1 bytes.Buffer
	if err := common.NewUDPDatagram(common.NewUDPHeader(0, 0, common.ToSocksAddr(targetAddr1)), []byte("ping1")).Write(&outbound1); err != nil {
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
	tunneled1, err := common.ReadUDPDatagram(bytes.NewReader(frameBuf1[:n1]))
	if err != nil {
		t.Fatalf("parse first tunneled udp datagram: %v", err)
	}
	if tunneled1.Header.Addr.String() != "8.8.8.8:53" {
		t.Fatalf("first tunneled addr = %s, want 8.8.8.8:53", tunneled1.Header.Addr.String())
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
	peerFramed1 := conn.WrapFramed(peerConn1)

	targetAddr1, _ := net.ResolveUDPAddr("udp4", "8.8.8.8:53")
	var outbound1 bytes.Buffer
	if err := common.NewUDPDatagram(common.NewUDPHeader(0, 0, common.ToSocksAddr(targetAddr1)), []byte("bind1")).Write(&outbound1); err != nil {
		t.Fatalf("build first outbound udp datagram: %v", err)
	}
	if _, err := udpConn1.WriteToUDP(outbound1.Bytes(), &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: port}); err != nil {
		t.Fatalf("WriteToUDP() first socket error = %v", err)
	}

	frameBuf1 := make([]byte, conn.MaxFramePayload)
	_ = peerConn1.SetReadDeadline(time.Now().Add(time.Second))
	n1, err := peerFramed1.Read(frameBuf1)
	if err != nil {
		t.Fatalf("read first framed udp payload: %v", err)
	}
	tunneled1, err := common.ReadUDPDatagram(bytes.NewReader(frameBuf1[:n1]))
	if err != nil {
		t.Fatalf("parse first tunneled udp datagram: %v", err)
	}
	if tunneled1.Header.Addr.String() != "8.8.8.8:53" || string(tunneled1.Data) != "bind1" {
		t.Fatalf("unexpected first tunneled payload addr=%s data=%q", tunneled1.Header.Addr.String(), tunneled1.Data)
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
	peerFramed2 := conn.WrapFramed(peerConn2)

	udpConn1b, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("ListenUDP() rebound socket error = %v", err)
	}
	defer func() { _ = udpConn1b.Close() }()

	targetAddrRebind, _ := net.ResolveUDPAddr("udp4", "9.9.9.9:9999")
	var outboundRebind bytes.Buffer
	if err := common.NewUDPDatagram(common.NewUDPHeader(0, 0, common.ToSocksAddr(targetAddrRebind)), []byte("rebind")).Write(&outboundRebind); err != nil {
		t.Fatalf("build rebound outbound udp datagram: %v", err)
	}
	if _, err := udpConn1b.WriteToUDP(outboundRebind.Bytes(), &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: port}); err != nil {
		t.Fatalf("WriteToUDP() rebound socket error = %v", err)
	}

	_ = peerConn1.SetReadDeadline(time.Now().Add(250 * time.Millisecond))
	if _, err := peerFramed1.Read(frameBuf1); err == nil {
		t.Fatal("active session should not be rebound while another pending session exists on the same IP")
	}
	frameBuf2 := make([]byte, conn.MaxFramePayload)
	_ = peerConn2.SetReadDeadline(time.Now().Add(250 * time.Millisecond))
	if _, err := peerFramed2.Read(frameBuf2); err == nil {
		t.Fatal("pending session should not steal ambiguous datagram from active session")
	}

	targetAddrStillOld, _ := net.ResolveUDPAddr("udp4", "1.1.1.1:53")
	var outboundStillOld bytes.Buffer
	if err := common.NewUDPDatagram(common.NewUDPHeader(0, 0, common.ToSocksAddr(targetAddrStillOld)), []byte("old-port")).Write(&outboundStillOld); err != nil {
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
	tunneledStillOld, err := common.ReadUDPDatagram(bytes.NewReader(frameBuf1[:n1]))
	if err != nil {
		t.Fatalf("parse old-port tunneled udp datagram: %v", err)
	}
	if tunneledStillOld.Header.Addr.String() != "1.1.1.1:53" || string(tunneledStillOld.Data) != "old-port" {
		t.Fatalf("unexpected old-port tunneled payload addr=%s data=%q", tunneledStillOld.Header.Addr.String(), tunneledStillOld.Data)
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

	peerFramed1 := conn.WrapFramed(peerConn1)
	frameBuf1 := make([]byte, conn.MaxFramePayload)

	targetAddr1, _ := net.ResolveUDPAddr("udp4", "8.8.8.8:53")
	var outbound1 bytes.Buffer
	if err := common.NewUDPDatagram(common.NewUDPHeader(0, 0, common.ToSocksAddr(targetAddr1)), []byte("bind1")).Write(&outbound1); err != nil {
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
	tunneled1, err := common.ReadUDPDatagram(bytes.NewReader(frameBuf1[:n1]))
	if err != nil {
		t.Fatalf("parse first tunneled udp datagram: %v", err)
	}
	if tunneled1.Header.Addr.String() != "8.8.8.8:53" || string(tunneled1.Data) != "bind1" {
		t.Fatalf("unexpected first tunneled payload addr=%s data=%q", tunneled1.Header.Addr.String(), tunneled1.Data)
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

	peerFramed2 := conn.WrapFramed(peerConn2)
	frameBuf2 := make([]byte, conn.MaxFramePayload)

	targetAddr2, _ := net.ResolveUDPAddr("udp4", "9.9.9.9:9999")
	var outbound2 bytes.Buffer
	if err := common.NewUDPDatagram(common.NewUDPHeader(0, 0, common.ToSocksAddr(targetAddr2)), []byte("bind2")).Write(&outbound2); err != nil {
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
	tunneled2, err := common.ReadUDPDatagram(bytes.NewReader(frameBuf2[:n2]))
	if err != nil {
		t.Fatalf("parse second tunneled udp datagram: %v", err)
	}
	if tunneled2.Header.Addr.String() != "9.9.9.9:9999" || string(tunneled2.Data) != "bind2" {
		t.Fatalf("unexpected second tunneled payload addr=%s data=%q", tunneled2.Header.Addr.String(), tunneled2.Data)
	}

	targetAddrRebind, _ := net.ResolveUDPAddr("udp4", "1.1.1.1:53")
	var outboundRebind bytes.Buffer
	if err := common.NewUDPDatagram(common.NewUDPHeader(0, 0, common.ToSocksAddr(targetAddrRebind)), []byte("rebind")).Write(&outboundRebind); err != nil {
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
	tunneledRebind, err := common.ReadUDPDatagram(bytes.NewReader(frameBuf1[:n1]))
	if err != nil {
		t.Fatalf("parse rebound tunneled udp datagram: %v", err)
	}
	if tunneledRebind.Header.Addr.String() != "1.1.1.1:53" || string(tunneledRebind.Data) != "rebind" {
		t.Fatalf("unexpected rebound tunneled payload addr=%s data=%q", tunneledRebind.Header.Addr.String(), tunneledRebind.Data)
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
	if got := socks5ReplyAddrFromNetAddr(task, tcpLocal, udpLocal, 1080, publicClient); got.Host != "0.0.0.0" {
		t.Fatalf("reply host for public client = %q, want 0.0.0.0", got.Host)
	}
	if got := socks5ReplyAddrFromNetAddr(task, tcpLocal, udpLocal, 1080, net.ParseIP("10.0.0.9")); got.Host != "10.0.0.5" {
		t.Fatalf("reply host for LAN client = %q, want 10.0.0.5", got.Host)
	}
	task.ServerIp = "10.0.0.5"
	if got := socks5ReplyAddrFromNetAddr(task, tcpLocal, udpLocal, 1080, publicClient); got.Host != "10.0.0.5" {
		t.Fatalf("reply host with configured ServerIp = %q, want 10.0.0.5", got.Host)
	}
}
