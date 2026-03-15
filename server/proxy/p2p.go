package proxy

import (
	"bytes"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/logs"
)

type P2PServer struct {
	*BaseServer
	p2pPort  int
	sessions sync.Map // key string → *session
	listener *net.UDPConn
}

type session struct {
	mu            sync.Mutex
	visitorAddr   *net.UDPAddr
	providerAddr  *net.UDPAddr
	visitorLocal  string
	providerLocal string
	visitorMode   string
	providerMode  string
	visitorData   string
	providerData  string
	timer         *time.Timer
	once          sync.Once
}

func NewP2PServer(p2pPort int) *P2PServer {
	return &P2PServer{p2pPort: p2pPort}
}

func (s *P2PServer) Start() error {
	logs.Info("start p2p server port %d", s.p2pPort)
	var err error
	s.listener, err = net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("0.0.0.0"), Port: s.p2pPort})
	if err != nil {
		return err
	}
	for {
		buf := common.BufPoolUdp.Get()
		n, addr, err := s.listener.ReadFromUDP(buf)
		if err != nil {
			common.BufPoolUdp.Put(buf)
			if strings.Contains(err.Error(), "use of closed network connection") {
				break
			}
			continue
		}
		s.handleP2P(addr, buf[:n])
		common.BufPoolUdp.Put(buf)
	}
	return nil
}

func (s *P2PServer) Close() error {
	if s.listener != nil {
		return s.listener.Close()
	}
	return nil
}

func (s *P2PServer) handleP2P(addr *net.UDPAddr, data []byte) {
	logs.Trace("P2P receive data %s from %v", data, addr)
	chunks := bytes.Split(data, []byte(common.CONN_DATA_SEQ))
	if len(chunks) < 2 {
		return
	}
	key := string(chunks[0])
	role := string(chunks[1])
	var localStr string
	if len(chunks) >= 3 {
		localStr = string(chunks[2])
	}
	var modeStr string
	if len(chunks) >= 4 {
		modeStr = string(chunks[3])
	}
	var dataStr string
	if len(chunks) >= 5 {
		dataStr = string(chunks[4])
	}

	t := file.GetDb().GetTaskByMd5Password(key)
	if t == nil {
		logs.Error("p2p error, failed to match the key successfully")
		return
	}
	t.AddConn()
	defer t.CutConn()

	v, _ := s.sessions.LoadOrStore(key, &session{})
	sess := v.(*session)

	sess.mu.Lock()
	defer sess.mu.Unlock()
	logs.Trace("P2P %s [%s] from %v (local %q) mode[%s] data[%s]", role, key, addr, localStr, modeStr, dataStr)

	switch role {
	case common.WORK_P2P_VISITOR:
		sess.visitorAddr = addr
		sess.visitorLocal = localStr
		sess.visitorMode = modeStr
		sess.visitorData = dataStr
	case common.WORK_P2P_PROVIDER:
		sess.providerAddr = addr
		sess.providerLocal = localStr
		sess.providerMode = modeStr
		sess.providerData = dataStr
	default:
		sess.providerAddr = addr
		sess.providerLocal = localStr
		sess.providerMode = modeStr
		sess.providerData = dataStr
	}

	if sess.visitorAddr != nil && sess.providerAddr != nil {
		s.sendAltPortProbe(sess.visitorAddr, sess.providerAddr, common.WORK_P2P_VISITOR)
		s.sendAltPortProbe(sess.providerAddr, sess.visitorAddr, common.WORK_P2P_PROVIDER)

		var visitorLocalChosen string
		var providerLocalChosen string
		if sess.visitorLocal != "" && sess.providerLocal != "" {
			visitorLocalChosen = common.ChooseLocalAddrForPeer(sess.visitorLocal, sess.providerLocal)
			providerLocalChosen = common.ChooseLocalAddrForPeer(sess.providerLocal, sess.visitorLocal)
		}

		var toVisitor []byte
		var toProvider []byte
		if sess.visitorMode != "" && sess.providerMode != "" {
			if sess.visitorData != "" || sess.providerData != "" {
				toVisitor = common.GetWriteStr(sess.providerAddr.String(), providerLocalChosen, sess.providerMode, sess.providerData, sess.visitorAddr.String())
				toProvider = common.GetWriteStr(sess.visitorAddr.String(), visitorLocalChosen, sess.visitorMode, sess.visitorData, sess.providerAddr.String())
			} else {
				toVisitor = common.GetWriteStr(sess.providerAddr.String(), providerLocalChosen, sess.providerMode)
				toProvider = common.GetWriteStr(sess.visitorAddr.String(), visitorLocalChosen, sess.visitorMode)
			}
		} else if sess.visitorLocal != "" && sess.providerLocal != "" {
			toVisitor = common.GetWriteStr(sess.providerAddr.String(), providerLocalChosen)
			toProvider = common.GetWriteStr(sess.visitorAddr.String(), visitorLocalChosen)
		} else {
			toVisitor = []byte(sess.providerAddr.String())
			toProvider = []byte(sess.visitorAddr.String())
		}
		for i := 0; i < 3; i++ {
			if _, err := s.listener.WriteTo(toVisitor, sess.visitorAddr); err != nil {
				logs.Warn("failed to send to visitor %v: %v", sess.visitorAddr, err)
			}
			if _, err := s.listener.WriteTo(toProvider, sess.providerAddr); err != nil {
				logs.Warn("failed to send to provider %v: %v", sess.providerAddr, err)
			}
		}
		logs.Trace("sent P2P addresses visitor=%v (%q) provider=%v (%q)", sess.visitorAddr, sess.visitorLocal, sess.providerAddr, sess.providerLocal)
		if sess.timer != nil {
			sess.timer.Stop()
			sess.timer = nil
		}
		s.sessions.Delete(key)
	} else {
		sess.once.Do(func() {
			sess.timer = time.AfterFunc(20*time.Second, func() {
				s.sessions.Delete(key)
			})
		})
	}
}

func (s *P2PServer) sendAltPortProbe(target *net.UDPAddr, peer *net.UDPAddr, role string) {
	if target == nil {
		return
	}
	probeConn, err := net.ListenUDP("udp", nil)
	if err != nil {
		logs.Warn("[P2P] failed to create nat-probe socket for %v: %v", target, err)
		return
	}
	defer func() { _ = probeConn.Close() }()

	peerAddr := ""
	if peer != nil {
		peerAddr = peer.String()
	}
	payload := common.GetWriteStr(common.WORK_P2P_NAT_PROBE, role, peerAddr)
	for i := 0; i < 2; i++ {
		if _, err := probeConn.WriteToUDP(payload, target); err != nil {
			logs.Trace("[P2P] nat-probe send failed to %v: %v", target, err)
		}
	}
}
