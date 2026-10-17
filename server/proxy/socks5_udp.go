package proxy

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/logs"
	"github.com/djylb/nps/lib/rate"
)

type socks5UDPPacket struct {
	buf []byte
	n   int
}

const socks5UDPPacketQueueSize = 1024

var (
	socks5UDPAssociateIdleTimeout = 3 * time.Minute
	socks5UDPSweepInterval        = 30 * time.Second
)

type socks5UDPRegistry struct {
	task            *file.Tunnel
	bridge          NetBridge
	allowLocalProxy bool
	listener        net.PacketConn
	ctx             context.Context
	cancel          context.CancelFunc
	closeOnce       sync.Once
	wg              sync.WaitGroup

	mu             sync.Mutex
	sessions       map[*socks5UDPSession]struct{}
	sessionsByAddr map[string]*socks5UDPSession
	sessionsByIP   map[string]map[*socks5UDPSession]struct{}
	pendingByIP    map[string][]*socks5UDPSession
}

func newSocks5UDPRegistry(bridge NetBridge, task *file.Tunnel, allowLocalProxy bool, addr string) (*socks5UDPRegistry, error) {
	listener, err := conn.NewUdpConnByAddr(addr)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &socks5UDPRegistry{
		task:            task,
		bridge:          bridge,
		allowLocalProxy: allowLocalProxy,
		listener:        listener,
		ctx:             ctx,
		cancel:          cancel,
		sessions:        make(map[*socks5UDPSession]struct{}),
		sessionsByAddr:  make(map[string]*socks5UDPSession),
		sessionsByIP:    make(map[string]map[*socks5UDPSession]struct{}),
		pendingByIP:     make(map[string][]*socks5UDPSession),
	}, nil
}

func (r *socks5UDPRegistry) start() {
	r.wg.Add(2)
	go r.readLoop()
	go r.cleanupLoop()
}

func (r *socks5UDPRegistry) LocalAddr() net.Addr {
	if r == nil || r.listener == nil {
		return nil
	}
	return r.listener.LocalAddr()
}

func (r *socks5UDPRegistry) newSession(control net.Conn, requestedPort int) (*socks5UDPSession, error) {
	session, err := newSocks5UDPSession(r, control, requestedPort)
	if err != nil {
		return nil, err
	}
	r.registerSession(session)
	return session, nil
}

func (r *socks5UDPRegistry) registerSession(session *socks5UDPSession) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.sessions[session] = struct{}{}
	r.addSessionIPLocked(session)
	r.addPendingSessionLocked(session)
}

func (r *socks5UDPRegistry) addSessionIPLocked(session *socks5UDPSession) {
	if r.sessionsByIP[session.clientIPKey] == nil {
		r.sessionsByIP[session.clientIPKey] = make(map[*socks5UDPSession]struct{})
	}
	r.sessionsByIP[session.clientIPKey][session] = struct{}{}
}

func (r *socks5UDPRegistry) addPendingSessionLocked(session *socks5UDPSession) {
	r.pendingByIP[session.clientIPKey] = append(r.pendingByIP[session.clientIPKey], session)
}

func (r *socks5UDPRegistry) unregisterSession(session *socks5UDPSession) {
	r.mu.Lock()
	defer r.mu.Unlock()

	delete(r.sessions, session)
	r.removeBoundSessionLocked(session)
	r.removePendingSessionLocked(session)
	r.removeSessionIPLocked(session)
}

func (r *socks5UDPRegistry) removeBoundSessionLocked(session *socks5UDPSession) {
	boundKey, _ := session.boundIdentity()
	if boundKey == "" {
		return
	}
	if current, ok := r.sessionsByAddr[boundKey]; ok && current == session {
		delete(r.sessionsByAddr, boundKey)
	}
}

func (r *socks5UDPRegistry) removeSessionIPLocked(session *socks5UDPSession) {
	sessions := r.sessionsByIP[session.clientIPKey]
	if len(sessions) == 0 {
		return
	}
	delete(sessions, session)
	if len(sessions) == 0 {
		delete(r.sessionsByIP, session.clientIPKey)
	}
}

func (r *socks5UDPRegistry) removePendingSessionLocked(session *socks5UDPSession) {
	r.pendingByIP[session.clientIPKey] = removePendingSession(r.pendingByIP[session.clientIPKey], session)
	if len(r.pendingByIP[session.clientIPKey]) == 0 {
		delete(r.pendingByIP, session.clientIPKey)
	}
}

func (r *socks5UDPRegistry) sessionCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.sessions)
}

func (r *socks5UDPRegistry) readLoop() {
	defer r.wg.Done()

	for {
		buf := common.BufPoolUdp.Get()
		n, addr, err := r.listener.ReadFrom(buf)
		if err != nil {
			common.BufPoolUdp.Put(buf)
			if r.ctx.Err() != nil || isClosedPacketConnError(err) {
				return
			}
			logs.Debug("socks5 udp listener read error: %v", err)
			continue
		}

		udpAddr, ok := addr.(*net.UDPAddr)
		if !ok || udpAddr == nil {
			common.BufPoolUdp.Put(buf)
			continue
		}
		if r.shouldDropClientDatagram(udpAddr) {
			common.BufPoolUdp.Put(buf)
			continue
		}

		session := r.pickSession(udpAddr)
		if session == nil {
			common.BufPoolUdp.Put(buf)
			logs.Debug("drop socks5 udp datagram from %v without active associate", udpAddr)
			continue
		}
		if !session.enqueue(buf, n) {
			common.BufPoolUdp.Put(buf)
		}
	}
}

func (r *socks5UDPRegistry) shouldDropClientDatagram(addr *net.UDPAddr) bool {
	if addr == nil || !r.bridge.IsServer() || r.task == nil || r.task.Client == nil {
		return false
	}
	addrString := addr.String()
	return IsGlobalBlackIp(addrString) || common.IsBlackIp(addrString, r.task.Client.VerifyKey, r.task.Client.BlackIpList)
}

func (r *socks5UDPRegistry) pickSession(addr *net.UDPAddr) *socks5UDPSession {
	if addr == nil {
		return nil
	}
	key := addr.String()
	ipKey := normalizeUDPIPKey(addr.IP)
	if ipKey == "" {
		return nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	session := r.selectSessionLocked(key, ipKey, addr.Port)
	if session == nil {
		return nil
	}
	r.bindSessionLocked(session, addr)
	return session
}

func (r *socks5UDPRegistry) selectSessionLocked(addrKey, ipKey string, port int) *socks5UDPSession {
	if session := r.sessionByAddrLocked(addrKey); session != nil {
		return session
	}
	if session := r.singleActiveSessionByIPLocked(ipKey); session != nil {
		return session
	}
	if session := r.uniqueHintMatchByIPLocked(ipKey, port); session != nil {
		return session
	}
	if len(r.sessionsByIP[ipKey]) == 0 {
		return r.singlePendingSessionByIPLocked(ipKey)
	}
	return nil
}

func (r *socks5UDPRegistry) sessionByAddrLocked(addrKey string) *socks5UDPSession {
	return r.sessionsByAddr[addrKey]
}

func (r *socks5UDPRegistry) singleActiveSessionByIPLocked(ipKey string) *socks5UDPSession {
	sessions := r.sessionsByIP[ipKey]
	if len(sessions) != 1 {
		return nil
	}
	for session := range sessions {
		return session
	}
	return nil
}

func (r *socks5UDPRegistry) uniqueHintMatchByIPLocked(ipKey string, port int) *socks5UDPSession {
	if port <= 0 {
		return nil
	}
	var match *socks5UDPSession
	for session := range r.sessionsByIP[ipKey] {
		if !session.matchesRequestedPort(port) {
			continue
		}
		if match != nil {
			return nil
		}
		match = session
	}
	for _, session := range r.pendingByIP[ipKey] {
		if !session.matchesRequestedPort(port) {
			continue
		}
		if match != nil && match != session {
			return nil
		}
		match = session
	}
	return match
}

func (r *socks5UDPRegistry) singlePendingSessionByIPLocked(ipKey string) *socks5UDPSession {
	pending := r.pendingByIP[ipKey]
	if len(pending) != 1 {
		return nil
	}
	return pending[0]
}

func (r *socks5UDPRegistry) bindSessionLocked(session *socks5UDPSession, addr *net.UDPAddr) {
	oldKey, _ := session.boundIdentity()
	boundKey := session.bindClientAddr(addr)
	if oldKey != "" && oldKey != boundKey {
		if current, ok := r.sessionsByAddr[oldKey]; ok && current == session {
			delete(r.sessionsByAddr, oldKey)
		}
	}
	r.sessionsByAddr[boundKey] = session
	r.removePendingSessionLocked(session)
}

func (r *socks5UDPRegistry) Close() error {
	r.closeOnce.Do(func() {
		r.cancel()
		if r.listener != nil {
			_ = r.listener.Close()
		}

		r.mu.Lock()
		sessions := make([]*socks5UDPSession, 0, len(r.sessions))
		for session := range r.sessions {
			sessions = append(sessions, session)
		}
		r.mu.Unlock()

		for _, session := range sessions {
			session.Close()
		}
		for _, session := range sessions {
			session.Wait()
		}
		r.wg.Wait()
	})
	return nil
}

func (r *socks5UDPRegistry) cleanupLoop() {
	defer r.wg.Done()

	ticker := time.NewTicker(socks5UDPSweepInterval)
	defer ticker.Stop()

	for {
		select {
		case <-r.ctx.Done():
			return
		case <-ticker.C:
			idleSessions := r.collectIdleSessions(time.Now(), socks5UDPAssociateIdleTimeout)
			for _, session := range idleSessions {
				logs.Debug("close idle socks5 udp associate: client=%s idle=%s", session.controlRemoteAddr(), session.idleFor(time.Now()))
				session.Close()
			}
		}
	}
}

func (r *socks5UDPRegistry) collectIdleSessions(now time.Time, idleTimeout time.Duration) []*socks5UDPSession {
	if idleTimeout <= 0 {
		return nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	sessions := make([]*socks5UDPSession, 0)
	for session := range r.sessions {
		if session.idleFor(now) >= idleTimeout {
			sessions = append(sessions, session)
		}
	}
	return sessions
}

type socks5UDPSession struct {
	registry      *socks5UDPRegistry
	control       net.Conn
	framed        *conn.FramedConn
	timeout       time.Duration
	clientIPKey   string
	requestedPort int
	packetCh      chan socks5UDPPacket
	done          chan struct{}
	closeOnce     sync.Once
	wg            sync.WaitGroup
	lastActiveNS  atomic.Int64

	queueMu sync.Mutex
	closed  bool

	mu        sync.RWMutex
	boundAddr *net.UDPAddr
	boundKey  string
}

func newSocks5UDPSession(registry *socks5UDPRegistry, control net.Conn, requestedPort int) (*socks5UDPSession, error) {
	if registry == nil {
		return nil, errors.New("nil socks5 udp registry")
	}
	if control == nil {
		return nil, errors.New("nil socks5 udp control conn")
	}
	if err := validateSocks5UDPTunnelTask(registry.task); err != nil {
		return nil, err
	}

	clientIP := common.NormalizeIP(common.ParseIPFromAddr(control.RemoteAddr().String()))
	if clientIP == nil {
		return nil, errors.New("parse socks5 udp client ip failed")
	}

	session := &socks5UDPSession{
		registry:      registry,
		control:       control,
		timeout:       socks5UDPAssociateIdleTimeout,
		clientIPKey:   clientIP.String(),
		requestedPort: requestedPort,
		packetCh:      make(chan socks5UDPPacket, socks5UDPPacketQueueSize),
		done:          make(chan struct{}),
	}
	if err := session.openTunnel(); err != nil {
		return nil, err
	}
	return session, nil
}

func validateSocks5UDPTunnelTask(task *file.Tunnel) error {
	switch {
	case task == nil:
		return errors.New("nil socks5 udp task")
	case task.Client == nil:
		return errors.New("nil socks5 udp task client")
	case task.Client.Cnf == nil:
		return errors.New("nil socks5 udp client config")
	case task.Target == nil:
		return errors.New("nil socks5 udp target")
	case task.Flow == nil:
		return errors.New("nil socks5 udp task flow")
	case task.Client.Flow == nil:
		return errors.New("nil socks5 udp client flow")
	default:
		return nil
	}
}

func (s *socks5UDPSession) openTunnel() error {
	task := s.registry.task
	link := conn.NewLink(
		"udp5",
		"",
		task.Client.Cnf.Crypt,
		task.Client.Cnf.Compress,
		s.control.RemoteAddr().String(),
		s.registry.allowLocalProxy && task.Target.LocalProxy,
	)
	link.Option.Timeout = s.timeout

	target, err := s.registry.bridge.SendLinkInfo(task.Client.Id, link, task)
	if err != nil {
		return err
	}
	timeoutConn := conn.NewTimeoutConn(target, link.Option.Timeout)
	rateConn := rate.NewRateConn(timeoutConn, task.Client.Rate)
	// The session owns the framed connection and closes it in Close.
	//noinspection GoResourceLeak
	flowConn := conn.NewFlowConn(rateConn, task.Flow, task.Client.Flow)
	s.framed = conn.WrapFramed(flowConn)
	return nil
}

func (s *socks5UDPSession) start() {
	s.touch()
	s.wg.Add(2)
	go s.writeToTunnelLoop()
	go s.readFromTunnelLoop()
}

func (s *socks5UDPSession) Wait() {
	s.wg.Wait()
}

func (s *socks5UDPSession) Close() {
	s.closeOnce.Do(func() {
		s.queueMu.Lock()
		s.closed = true
		close(s.done)
		close(s.packetCh)
		s.queueMu.Unlock()

		s.registry.unregisterSession(s)
		if s.framed != nil {
			_ = s.framed.Close()
		}
	})
}

func (s *socks5UDPSession) enqueue(buf []byte, n int) bool {
	s.queueMu.Lock()
	defer s.queueMu.Unlock()

	if s.closed {
		return false
	}
	select {
	case s.packetCh <- socks5UDPPacket{buf: buf, n: n}:
		s.touch()
		return true
	default:
		logs.Debug("drop socks5 udp datagram from %s due to full queue", s.controlRemoteAddr())
		return false
	}
}

func (s *socks5UDPSession) writeToTunnelLoop() {
	defer s.wg.Done()

	discardOnly := false
	for pkt := range s.packetCh {
		if pkt.n >= 3 && pkt.buf[2] != 0 {
			common.BufPoolUdp.Put(pkt.buf)
			logs.Warn("socks5 udp frag not supported, drop (frag=%d)", pkt.buf[2])
			continue
		}
		if pkt.n > conn.MaxFramePayload {
			common.BufPoolUdp.Put(pkt.buf)
			logs.Debug("socks5 udp datagram too large: %d > %d", pkt.n, conn.MaxFramePayload)
			continue
		}
		if discardOnly {
			common.BufPoolUdp.Put(pkt.buf)
			continue
		}
		if _, err := s.framed.Write(pkt.buf[:pkt.n]); err != nil {
			common.BufPoolUdp.Put(pkt.buf)
			logs.Debug("write socks5 udp frame to tunnel error: %v", err)
			discardOnly = true
			s.Close()
			continue
		}
		s.touch()
		common.BufPoolUdp.Put(pkt.buf)
	}
}

func (s *socks5UDPSession) readFromTunnelLoop() {
	defer s.wg.Done()

	buf := common.BufPool.Get()
	defer common.BufPool.Put(buf)

	for {
		n, err := s.framed.Read(buf)
		if err != nil || n <= 0 || n > len(buf) {
			logs.Debug("read socks5 udp frame from tunnel error: %v", err)
			s.Close()
			return
		}
		addr := s.clientAddr()
		if addr == nil {
			logs.Debug("drop socks5 udp response without bound client addr")
			continue
		}
		if _, err := s.registry.listener.WriteTo(buf[:n], addr); err != nil {
			logs.Warn("write socks5 udp response to client error: %v", err)
			s.Close()
			return
		}
		s.touch()
	}
}

func (s *socks5UDPSession) bindClientAddr(addr *net.UDPAddr) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.boundKey = addr.String()
	s.boundAddr = cloneUDPAddr(addr)
	return s.boundKey
}

func (s *socks5UDPSession) boundIdentity() (string, *net.UDPAddr) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.boundKey, cloneUDPAddr(s.boundAddr)
}

func (s *socks5UDPSession) clientAddr() *net.UDPAddr {
	_, addr := s.boundIdentity()
	return addr
}

func (s *socks5UDPSession) controlRemoteAddr() string {
	if s.control == nil || s.control.RemoteAddr() == nil {
		return "<nil>"
	}
	return s.control.RemoteAddr().String()
}

func (s *socks5UDPSession) Done() <-chan struct{} {
	return s.done
}

func (s *socks5UDPSession) touch() {
	s.lastActiveNS.Store(time.Now().UnixNano())
}

func (s *socks5UDPSession) idleFor(now time.Time) time.Duration {
	last := s.lastActiveNS.Load()
	if last == 0 {
		return 0
	}
	return now.Sub(time.Unix(0, last))
}

func (s *socks5UDPSession) matchesRequestedPort(port int) bool {
	return port > 0 && s.requestedPort > 0 && s.requestedPort == port
}

func removePendingSession(sessions []*socks5UDPSession, target *socks5UDPSession) []*socks5UDPSession {
	if len(sessions) == 0 {
		return sessions
	}
	out := sessions[:0]
	for _, session := range sessions {
		if session != target {
			out = append(out, session)
		}
	}
	return out
}

func normalizeUDPIPKey(ip net.IP) string {
	ip = common.NormalizeIP(ip)
	if ip == nil {
		return ""
	}
	return ip.String()
}

func cloneUDPAddr(addr *net.UDPAddr) *net.UDPAddr {
	if addr == nil {
		return nil
	}
	out := *addr
	if addr.IP != nil {
		out.IP = append(net.IP(nil), addr.IP...)
	}
	return &out
}

func isClosedPacketConnError(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, net.ErrClosed) || strings.Contains(err.Error(), "use of closed network connection")
}
