package client

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"net"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/logs"
)

const (
	// server exchange
	p2pServerWaitTimeout        = 30 * time.Second
	p2pServerReadStep           = 1 * time.Second
	p2pServerCollectMoreTimeout = 8 * time.Second

	// handshake read loop
	p2pHandshakeReadMax = 1500 * time.Millisecond

	// strategy A (open many local ports)
	p2pStrategyAConnCount = 256

	// base send
	p2pConeSendTick   = 500 * time.Millisecond
	p2pConeBurstCount = 3
	p2pConeBurstGap   = 80 * time.Millisecond

	// near scan (regular ports change)
	p2pConeNearScanCount = 128
	p2pConeNearScanRange = 256
	p2pConeNearScanTick  = 1500 * time.Millisecond

	// heavy random scan fallback
	p2pConeFallbackDelay = 1800 * time.Millisecond
	p2pConeFallbackCount = 512
	p2pConeFallbackTick  = 2 * time.Second

	// extra listen ports when self seems symmetric-ish (receiver-like)
	p2pSelfHardExtraListenCount = 128

	// handshake budgets / throttling
	p2pSuccMinInterval = 200 * time.Millisecond
	p2pEndMinInterval  = 200 * time.Millisecond

	p2pSuccBurstOnConnect = 4
	p2pSuccEchoOnSuccess  = 2
	p2pEndBurstOnSuccess  = 6
	p2pEndBurstOnEndAck   = 2

	p2pMaxSuccPacketsPerPeer = 20
	p2pMaxEndPacketsPerPeer  = 20

	p2pSprayTick       = 200 * time.Millisecond
	p2pSpraySuccMax    = 6
	p2pSprayEndMax     = 6
	p2pSpraySuccWindow = 1600 * time.Millisecond
	p2pSprayEndWindow  = 1600 * time.Millisecond
	p2pSpraySeedSucc   = 2
	p2pSpraySeedWindow = 900 * time.Millisecond
)

var (
	bConnDataSeq = []byte(common.CONN_DATA_SEQ)
	bConnect     = []byte(common.WORK_P2P_CONNECT)
	bSuccess     = []byte(common.WORK_P2P_SUCCESS)
	bEnd         = []byte(common.WORK_P2P_END)
)

func handleP2PUdp(
	pCtx context.Context,
	localAddr, rAddr, md5Password, sendRole, sendMode, sendData string,
) (c net.PacketConn, remoteAddress, localAddress, role, mode, data string, err error) {
	localAddress = localAddr

	parentCtx, parentCancel := context.WithTimeout(pCtx, p2pServerWaitTimeout)
	defer parentCancel()

	localConn, err := conn.NewUdpConnByAddr(localAddr)
	if err != nil {
		logs.Error("[P2P] start fail newUdpConn localWant=%s err=%v", localAddr, err)
		return
	}

	handedOff := false
	defer func() {
		if !handedOff {
			_ = localConn.Close()
		}
	}()

	port := common.GetPortStrByAddr(localAddr)
	if port == "" || port == "0" {
		port = common.GetPortStrByAddr(localConn.LocalAddr().String())
	}
	localCandidates := buildP2PLocalStr(port)
	if localCandidates == "" {
		// fallback: at least report one addr
		localCandidates = localAddr
	}

	logs.Debug("[P2P] start role=%s local=%s server=%s port=%s candidates=%s mode=%s dataLen=%d", sendRole, localConn.LocalAddr().String(), rAddr, port, localCandidates, sendMode, len(sendData))

	for seq := 0; seq < 3; seq++ {
		if err = getRemoteAddressFromServer(rAddr, localCandidates, localConn, md5Password, sendRole, sendMode, sendData, seq); err != nil {
			logs.Error("[P2P] getRemoteAddressFromServer seq=%d err=%v", seq, err)
			return
		}
	}

	var peerExt1, peerExt2, peerExt3 string
	var selfExt1, selfExt2, selfExt3 string
	var peerLocal string
	serverPort := common.GetPortByAddr(rAddr)

	buf := common.BufPoolUdp.Get().([]byte)
	defer common.PutBufPoolUdp(buf)

	var punchedAddr net.Addr

	var gotFirstAt time.Time
	var collectUntil time.Time

	peerGroupCount := func() int {
		n := 0
		if peerExt1 != "" {
			n++
		}
		if peerExt2 != "" {
			n++
		}
		if peerExt3 != "" {
			n++
		}
		return n
	}

	for {
		select {
		case <-parentCtx.Done():
			err = parentCtx.Err()
			logs.Error("[P2P] wait server reply timeout local=%s server=%s err=%v", localConn.LocalAddr().String(), rAddr, err)
			return
		default:
		}

		_ = localConn.SetReadDeadline(time.Now().Add(p2pServerReadStep))
		n, fromAddr, rerr := localConn.ReadFrom(buf)
		_ = localConn.SetReadDeadline(time.Time{})

		if rerr != nil {
			var ne net.Error
			if errors.As(rerr, &ne) && (ne.Timeout() || ne.Temporary()) {
				if !gotFirstAt.IsZero() && time.Now().After(collectUntil) {
					break
				}
				continue
			}
			err = rerr
			logs.Error("[P2P] read server reply failed local=%s err=%v", localConn.LocalAddr().String(), err)
			return
		}

		pkt := buf[:n]

		// punched-in fast path
		if bytes.Equal(pkt, bConnect) {
			punchedAddr = fromAddr
			logs.Debug("[P2P] punched-in CONNECT from=%s local=%s", fromAddr.String(), localConn.LocalAddr().String())
			_, _ = localConn.WriteTo(bSuccess, fromAddr)
			break
		}

		raw := string(pkt)
		peerExt, pLocal, m, d, selfExt := parseP2PServerReply(raw)
		if peerExt == "" {
			continue
		}

		if gotFirstAt.IsZero() {
			gotFirstAt = time.Now()
			collectUntil = gotFirstAt.Add(p2pServerCollectMoreTimeout)
		}

		if pLocal != "" && peerLocal == "" {
			peerLocal = pLocal
		}
		if m != "" {
			mode = m
		}
		if d != "" {
			data = d
		}

		fromPort := common.GetPortByAddr(fromAddr.String())
		switch fromPort {
		case serverPort:
			peerExt1 = peerExt
			if selfExt != "" {
				selfExt1 = selfExt
			}
		case serverPort + 1:
			peerExt2 = peerExt
			if selfExt != "" {
				selfExt2 = selfExt
			}
		case serverPort + 2:
			peerExt3 = peerExt
			if selfExt != "" {
				selfExt3 = selfExt
			}
		}

		logs.Trace("[P2P] server-reply from=%s peerExt=%s peerLocal=%s selfExt=%s mode=%s dataLen=%d", fromAddr.String(), peerExt, pLocal, selfExt, m, len(d))

		// full collected
		if peerExt1 != "" && peerExt2 != "" && peerExt3 != "" {
			break
		}

		// collect-more timeout: once got at least one reply
		if !gotFirstAt.IsZero() && time.Now().After(collectUntil) {
			break
		}
	}

	// Decide forceHard and normalize 2-group case
	forceHard := false
	if punchedAddr == nil {
		cnt := peerGroupCount()
		if cnt == 1 && !gotFirstAt.IsZero() && time.Now().After(collectUntil) {
			forceHard = true
		} else if cnt == 2 {
			peerExt1, peerExt2, peerExt3 = fillTripletByPortDiff(peerExt1, peerExt2, peerExt3)
			selfExt1, selfExt2, selfExt3 = fillTripletByPortDiff(selfExt1, selfExt2, selfExt3)
		}
	}

	logs.Debug("[P2P] collected peerExt=[%s,%s,%s] selfExt=[%s,%s,%s] peerLocal=%s punched=%v forceHard=%v",
		peerExt1, peerExt2, peerExt3,
		selfExt1, selfExt2, selfExt3,
		peerLocal, punchedAddr != nil, forceHard)

	winConn, remoteAddress, localAddress, role, err := sendP2PTestMsg(
		parentCtx,
		localConn,
		sendRole,
		peerExt1, peerExt2, peerExt3,
		peerLocal,
		selfExt1, selfExt2, selfExt3,
		punchedAddr,
		forceHard,
	)
	if err != nil {
		logs.Error("[P2P] sendP2PTestMsg failed local=%s err=%v", localConn.LocalAddr().String(), err)
		return
	}

	if localAddr != localAddress {
		logs.Trace("[P2P] LocalAddr changed: want=%s actual=%s", localAddr, localAddress)
	}

	network, fixedLocal, ferr := common.FixUdpListenAddrForRemote(remoteAddress, localAddress)
	if ferr != nil {
		err = ferr
		logs.Error("[P2P] fix listen addr failed remote=%s local=%s err=%v", remoteAddress, localAddress, err)
		return
	}
	if fixedLocal != localAddress {
		logs.Trace("[P2P] fix listen addr remote=%s local=%s -> %s", remoteAddress, localAddress, fixedLocal)
		localAddress = fixedLocal
	}
	if network == "" {
		network = "udp"
	}

	needRecreate := false
	if _, ok := winConn.(*conn.SmartUdpConn); ok {
		needRecreate = true
	}
	if winConn.LocalAddr() == nil || winConn.LocalAddr().String() != localAddress {
		needRecreate = true
	}

	if needRecreate {
		_ = winConn.Close()
		c, err = net.ListenPacket("udp", localAddress)
		if err != nil {
			logs.Error("[P2P] net.ListenPacket failed network=%s local=%s err=%v", network, localAddress, err)
			return
		}
		logs.Debug("[P2P] recreate conn local=%s network=%s", localAddress, network)
	} else {
		c = winConn
	}

	handedOff = true
	logs.Info("[P2P] connected role=%s remote=%s local=%s", role, remoteAddress, localAddress)
	return
}

func buildP2PLocalStr(port string) string {
	if port == "" || port == "0" {
		return ""
	}
	out := make([]string, 0, 2)

	tmpConnV4, errV4 := common.GetLocalUdp4Addr()
	if errV4 == nil && tmpConnV4 != nil {
		if la, ok := tmpConnV4.LocalAddr().(*net.UDPAddr); ok && la != nil && la.IP != nil && !common.IsZeroIP(la.IP) {
			a := net.JoinHostPort(la.IP.String(), port)
			if a != "" && !common.InStrArr(out, a) {
				out = append(out, a)
			}
		}
	}

	tmpConnV6, errV6 := common.GetLocalUdp6Addr()
	if errV6 == nil && tmpConnV6 != nil {
		if la, ok := tmpConnV6.LocalAddr().(*net.UDPAddr); ok && la != nil && la.IP != nil && !common.IsZeroIP(la.IP) {
			a := net.JoinHostPort(la.IP.String(), port)
			if a != "" && !common.InStrArr(out, a) {
				out = append(out, a)
			}
		}
	}

	if len(out) == 0 {
		return ""
	}
	return strings.Join(out, ",")
}

func getRemoteAddressFromServer(
	rAddr, localCandidates string,
	localConn net.PacketConn,
	md5Password, role, mode, data string,
	add int,
) error {
	next, err := getNextAddr(rAddr, add)
	if err != nil {
		return err
	}
	addr, err := net.ResolveUDPAddr("udp", next)
	if err != nil {
		return err
	}
	payload := common.GetWriteStr(md5Password, role, localCandidates, mode, data)
	if _, err := localConn.WriteTo(payload, addr); err != nil {
		return err
	}
	//logs.Trace("[P2P] sent req to server=%s local=%s add=%d candidates=%s", addr.String(), localConn.LocalAddr().String(), add, localCandidates)
	return nil
}

func parseP2PServerReply(raw string) (peerExt, peerLocal, mode, data, selfExt string) {
	parts := strings.Split(raw, common.CONN_DATA_SEQ)
	for len(parts) > 0 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	if len(parts) == 0 {
		return
	}

	peerExt = common.ValidateAddr(parts[0])
	if peerExt == "" {
		return "", "", "", "", ""
	}

	if len(parts) >= 2 {
		peerLocal = common.ValidateAddr(parts[1])
	}
	if len(parts) >= 3 {
		mode = parts[2]
	}
	if len(parts) >= 4 {
		data = parts[3]
	}
	if len(parts) >= 5 {
		selfExt = common.ValidateAddr(parts[4])
	}
	return
}

func sendP2PTestMsg(
	pCtx context.Context,
	localConn net.PacketConn,
	sendRole string,
	peerExt1, peerExt2, peerExt3 string,
	peerLocal string,
	selfExt1, selfExt2, selfExt3 string,
	punchedAddr net.Addr,
	forceHard bool,
) (winConn net.PacketConn, remoteAddr, localAddr, role string, err error) {
	parentCtx, parentCancel := context.WithCancel(pCtx)

	var closed uint32
	connList := []net.PacketConn{localConn}
	var winner net.PacketConn

	defer func() {
		atomic.StoreUint32(&closed, 1)
		parentCancel()

		if winner != nil {
			for _, c := range connList {
				if c == winner {
					continue
				}
				_ = c.Close()
			}
			return
		}
		for _, c := range connList {
			_ = c.Close()
		}
	}()

	// fast path: already punched in
	if punchedAddr != nil {
		logs.Debug("[P2P] fast-path punched=%s", punchedAddr.String())
		rAddr, lAddr, rRole, rErr := waitP2PHandshakeSeed(parentCtx, localConn, sendRole, 10, punchedAddr)
		if rErr == nil {
			winner = localConn
			return localConn, rAddr, lAddr, rRole, nil
		}
		logs.Info("[P2P] fast-path failed punched=%s err=%v, fallback to normal strategy", punchedAddr.String(), rErr)
	}

	// try peer local first
	if peerLocal != "" {
		logs.Debug("[P2P] peerLocal=%s", peerLocal)
		go func() {
			remoteUdpLocal, rerr := net.ResolveUDPAddr("udp", peerLocal)
			if rerr != nil {
				logs.Error("[P2P] resolve peerLocal failed peerLocal=%s err=%v", peerLocal, rerr)
				return
			}
			for i := 20; i > 0; i-- {
				select {
				case <-parentCtx.Done():
					return
				default:
				}
				if atomic.LoadUint32(&closed) != 0 {
					return
				}
				_, _ = localConn.WriteTo(bConnect, remoteUdpLocal)
				time.Sleep(100 * time.Millisecond)
			}
		}()
	}

	hasPeerExt := peerExt1 != "" && peerExt2 != "" && peerExt3 != ""
	peerInterval := 0
	if hasPeerExt {
		peerInterval, err = getAddrInterval(peerExt1, peerExt2, peerExt3)
		if err != nil {
			hasPeerExt = false
			peerInterval = 0
		}
	}

	hasSelfExt := selfExt1 != "" && selfExt2 != "" && selfExt3 != ""
	selfInterval := 0
	if hasSelfExt {
		selfInterval, err = getAddrInterval(selfExt1, selfExt2, selfExt3)
		if err != nil {
			hasSelfExt = false
			selfInterval = 0
		}
	}

	peerRegular := isRegularStep(peerInterval, hasPeerExt)
	selfHard := hasSelfExt && selfInterval != 0
	if forceHard {
		selfHard = true
	}

	logs.Info("[P2P] nat peer=%s(%d,%v) self=%s(%d) peerLocal=%v forceHard=%v",
		natHintByInterval(peerInterval, hasPeerExt), peerInterval, peerRegular,
		natHintByInterval(selfInterval, hasSelfExt), selfInterval,
		peerLocal != "", forceHard)

	// predicted target: pick any non-empty peerExt for baseUDP
	predictedStr := ""
	if peerExt3 != "" {
		predictedStr = peerExt3
	} else if peerExt2 != "" {
		predictedStr = peerExt2
	} else if peerExt1 != "" {
		predictedStr = peerExt1
	}
	if predictedStr != "" && hasPeerExt {
		if s, e := getNextAddr(peerExt3, peerInterval); e == nil && s != "" {
			predictedStr = s
		}
	}
	targets := uniqAddrStrs(predictedStr, peerExt1, peerExt2, peerExt3)

	startTickerSender := func(interval time.Duration, fn func()) {
		go func() {
			ticker := time.NewTicker(interval)
			defer ticker.Stop()
			for {
				select {
				case <-parentCtx.Done():
					return
				case <-ticker.C:
					if atomic.LoadUint32(&closed) != 0 {
						return
					}
					fn()
				}
			}
		}()
	}

	// (1) Base: burst to all candidates + tick to predicted
	baseUDP := resolveUDPAddr(predictedStr)
	if len(targets) > 0 {
		go func() {
			for _, t := range targets {
				ua := resolveUDPAddr(t)
				if ua == nil {
					continue
				}
				_ = sendBurstWithGap(localConn, bConnect, ua, p2pConeBurstCount, p2pConeBurstGap)
			}
		}()
	}
	if baseUDP != nil {
		startTickerSender(p2pConeSendTick, func() {
			_, _ = localConn.WriteTo(bConnect, baseUDP)
		})
	}

	// (2) strategy A or "listen random ports" fallback
	isStrategyA := hasPeerExt && hasSelfExt && peerInterval == 0 && selfInterval != 0 && baseUDP != nil
	if isStrategyA {
		logs.Debug("[P2P] strategy=A open-many-listen target=%s", baseUDP.String())
		extra, e := openRandomListenConnsForTarget(baseUDP, p2pStrategyAConnCount)
		if e == nil && len(extra) > 0 {
			connList = append(connList, extra...)
		}
		startTickerSender(500*time.Millisecond, func() {
			for _, c := range connList {
				_, _ = c.WriteTo(bConnect, baseUDP)
			}
		})
	} else if selfHard && baseUDP != nil {
		logs.Debug("[P2P] fallback: self hard-ish => open extra listen=%d target=%s", p2pSelfHardExtraListenCount, baseUDP.String())
		extra, e := openRandomListenConnsForTarget(baseUDP, p2pSelfHardExtraListenCount)
		if e == nil && len(extra) > 0 {
			connList = append(connList, extra...)
		}
		startTickerSender(600*time.Millisecond, func() {
			for _, c := range connList {
				_, _ = c.WriteTo(bConnect, baseUDP)
			}
		})
	}

	// (3) near-scan when peer ports change seems regular
	if baseUDP != nil && peerRegular {
		ip := hostOnly(peerExt2)
		if ip == "" {
			ip = hostOnly(peerExt3)
		}
		if ip != "" {
			predPort := common.GetPortByAddr(baseUDP.String())
			minP := common.Max(1, predPort-p2pConeNearScanRange)
			maxP := common.Min(65535, predPort+p2pConeNearScanRange)
			ports := getRandomUniquePorts(p2pConeNearScanCount, minP, maxP)

			nearAddrs := make([]*net.UDPAddr, 0, len(ports))
			for _, p := range ports {
				ua, e := net.ResolveUDPAddr("udp", net.JoinHostPort(ip, strconv.Itoa(p)))
				if e == nil && ua != nil {
					nearAddrs = append(nearAddrs, ua)
				}
			}

			go func() {
				for _, ua := range nearAddrs {
					_, _ = localConn.WriteTo(bConnect, ua)
				}
			}()

			startTickerSender(p2pConeNearScanTick, func() {
				for _, ua := range nearAddrs {
					_, _ = localConn.WriteTo(bConnect, ua)
				}
			})
		}
	}

	// (4) heavy random scan fallback (start earlier if peer looks symmetric-ish OR forceHard)
	fallbackDelay := p2pConeFallbackDelay
	if hasPeerExt && peerInterval != 0 {
		fallbackDelay = 0
	}
	if forceHard {
		fallbackDelay = 0
	}
	startFallbackRandomScan(parentCtx, &closed, localConn, peerExt1, peerExt2, peerExt3, fallbackDelay)

	// (5) keep old strategy B as extra layer (peer hard-ish, self stable-ish)
	if hasPeerExt && hasSelfExt && peerInterval != 0 && selfInterval == 0 {
		logs.Debug("[P2P] strategy=B peer hard-ish, self easy-ish => broad random scan")
		go func() {
			ip := hostOnly(peerExt2)
			if ip == "" {
				return
			}
			ports := getRandomUniquePorts(1000, 1, 65535)
			udpAddrs := make([]*net.UDPAddr, 0, len(ports))
			for _, p := range ports {
				ra, e := net.ResolveUDPAddr("udp", net.JoinHostPort(ip, strconv.Itoa(p)))
				if e == nil && ra != nil {
					udpAddrs = append(udpAddrs, ra)
				}
			}

			for _, ra := range udpAddrs {
				_, _ = localConn.WriteTo(bConnect, ra)
			}

			ticker := time.NewTicker(2 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-parentCtx.Done():
					return
				case <-ticker.C:
					if atomic.LoadUint32(&closed) != 0 {
						return
					}
					for _, ra := range udpAddrs {
						_, _ = localConn.WriteTo(bConnect, ra)
					}
				}
			}
		}()
	}

	// wait handshake (race when multiple conns exist)
	if len(connList) > 1 {
		type P2PResult struct {
			Conn       net.PacketConn
			RemoteAddr string
			LocalAddr  string
			Role       string
		}
		resultChan := make(chan P2PResult, 1)

		for _, c := range connList {
			go func(cc net.PacketConn) {
				rAddr, lAddr, rRole, rErr := waitP2PHandshake(parentCtx, cc, sendRole, 10)
				if rErr == nil {
					select {
					case resultChan <- P2PResult{Conn: cc, RemoteAddr: rAddr, LocalAddr: lAddr, Role: rRole}:
					default:
					}
				}
			}(c)
		}

		select {
		case res := <-resultChan:
			parentCancel()
			for _, c := range connList {
				_ = c.SetReadDeadline(time.Now())
			}
			winner = res.Conn
			return res.Conn, res.RemoteAddr, res.LocalAddr, res.Role, nil
		case <-parentCtx.Done():
			return nil, "", localConn.LocalAddr().String(), sendRole, errors.New("connect to the target failed, maybe the nat type is not support p2p")
		}
	}

	// single conn
	rAddr, lAddr, rRole, rErr := waitP2PHandshake(parentCtx, localConn, sendRole, 10)
	if rErr == nil {
		winner = localConn
		return localConn, rAddr, lAddr, rRole, nil
	}
	return nil, "", "", sendRole, rErr
}

func waitP2PHandshakeSeed(parentCtx context.Context, localConn net.PacketConn, sendRole string, readTimeout int, seed net.Addr) (remoteAddr, localAddr, role string, err error) {
	return waitP2PHandshakeWithSeed(parentCtx, localConn, sendRole, readTimeout, seed)
}

func waitP2PHandshake(parentCtx context.Context, localConn net.PacketConn, sendRole string, readTimeout int) (remoteAddr, localAddr, role string, err error) {
	return waitP2PHandshakeWithSeed(parentCtx, localConn, sendRole, readTimeout, nil)
}

func waitP2PHandshakeWithSeed(parentCtx context.Context, localConn net.PacketConn, sendRole string, readTimeout int, seed net.Addr) (remoteAddr, localAddr, role string, err error) {
	buf := common.BufPoolUdp.Get().([]byte)
	defer common.PutBufPoolUdp(buf)
	localAddrStr := localConn.LocalAddr().String()

	isServerAnnounce := func(pkt []byte) bool {
		return bytes.Contains(pkt, bConnDataSeq)
	}

	sendRawBurst := func(msg []byte, a net.Addr, burst int) {
		if a == nil || burst <= 0 {
			return
		}
		for i := 0; i < burst; i++ {
			_, _ = localConn.WriteTo(msg, a)
		}
	}

	type peerState struct {
		lastSuccSend time.Time
		lastEndSend  time.Time
		succSent     int
		endSent      int

		seenConnect bool
		succSpray   bool
		endSpray    bool
	}
	states := make(map[string]*peerState, 32)
	getState := func(k string) *peerState {
		if s, ok := states[k]; ok {
			return s
		}
		s := &peerState{}
		states[k] = s
		return s
	}

	trySendSucc := func(st *peerState, addr net.Addr, burst int) {
		if st == nil || addr == nil || burst <= 0 {
			return
		}
		now := time.Now()
		if st.succSent >= p2pMaxSuccPacketsPerPeer {
			return
		}
		if now.Sub(st.lastSuccSend) < p2pSuccMinInterval {
			return
		}
		st.lastSuccSend = now

		if st.succSent+burst > p2pMaxSuccPacketsPerPeer {
			burst = p2pMaxSuccPacketsPerPeer - st.succSent
		}
		if burst <= 0 {
			return
		}
		st.succSent += burst
		sendRawBurst(bSuccess, addr, burst)
	}

	trySendEnd := func(st *peerState, addr net.Addr, burst int) {
		if st == nil || addr == nil || burst <= 0 {
			return
		}
		now := time.Now()
		if st.endSent >= p2pMaxEndPacketsPerPeer {
			return
		}
		if now.Sub(st.lastEndSend) < p2pEndMinInterval {
			return
		}
		st.lastEndSend = now

		if st.endSent+burst > p2pMaxEndPacketsPerPeer {
			burst = p2pMaxEndPacketsPerPeer - st.endSent
		}
		if burst <= 0 {
			return
		}
		st.endSent += burst
		sendRawBurst(bEnd, addr, burst)
	}

	startSpray := func(st *peerState, addr net.Addr, msg []byte, window time.Duration, tick time.Duration, maxCount int, markEnd bool) {
		if st == nil || addr == nil || window <= 0 || tick <= 0 || maxCount <= 0 {
			return
		}
		if markEnd {
			if st.endSpray {
				return
			}
			st.endSpray = true
		} else {
			if st.succSpray {
				return
			}
			st.succSpray = true
		}

		go func(a net.Addr) {
			deadline := time.Now().Add(window)
			t := time.NewTicker(tick)
			defer t.Stop()

			sent := 0
			for {
				select {
				case <-parentCtx.Done():
					return
				case <-t.C:
					if time.Now().After(deadline) || sent >= maxCount {
						return
					}
					_, _ = localConn.WriteTo(msg, a)
					sent++
				}
			}
		}(addr)
	}

	// seed: kick-start both directions
	if seed != nil {
		sendRawBurst(bConnect, seed, 1)
		sendRawBurst(bSuccess, seed, 3)

		// short seed spray: helps “port-restricted / cone” environments where 1-3 packets are not enough
		go func(a net.Addr) {
			deadline := time.Now().Add(p2pSpraySeedWindow)
			t := time.NewTicker(p2pSprayTick)
			defer t.Stop()
			sent := 0
			for {
				select {
				case <-parentCtx.Done():
					return
				case <-t.C:
					if time.Now().After(deadline) || sent >= p2pSpraySeedSucc {
						return
					}
					_, _ = localConn.WriteTo(bSuccess, a)
					sent++
				}
			}
		}(seed)
	}

	if readTimeout <= 0 {
		readTimeout = 10
	}
	logs.Trace("[P2P] handshake wait role=%s local=%s timeout=%ds", sendRole, localAddrStr, readTimeout)

	wantRole := common.WORK_P2P_PROVIDER
	if sendRole == common.WORK_P2P_VISITOR {
		wantRole = common.WORK_P2P_VISITOR
	}

	for {
		select {
		case <-parentCtx.Done():
			logs.Error("[P2P] handshake fail role=%s local=%s err=%v", sendRole, localAddrStr, parentCtx.Err())
			return "", localAddrStr, sendRole, errors.New("connect to the target failed, maybe the nat type is not support p2p")
		default:
		}

		_ = localConn.SetReadDeadline(time.Now().Add(p2pHandshakeReadMax))
		n, addr, rerr := localConn.ReadFrom(buf)
		_ = localConn.SetReadDeadline(time.Time{})
		if rerr != nil {
			var ne net.Error
			if errors.As(rerr, &ne) && (ne.Timeout() || ne.Temporary()) {
				continue
			}
			logs.Error("[P2P] handshake read fail role=%s local=%s err=%v", sendRole, localAddrStr, rerr)
			return "", localAddrStr, sendRole, rerr
		}

		pkt := buf[:n]
		if isServerAnnounce(pkt) {
			continue
		}

		switch {
		case bytes.Equal(pkt, bConnect):
			from := addr.String()
			st := getState(from)
			// CONNECT -> SUCCESS (reply immediately + short spray window)
			st.seenConnect = true

			logs.Trace("[P2P] recv CONNECT from=%s local=%s -> send SUCCESS burst=%d + spray", from, localAddrStr, p2pSuccBurstOnConnect)

			trySendSucc(st, addr, p2pSuccBurstOnConnect)
			startSpray(st, addr, bSuccess, p2pSpraySuccWindow, p2pSprayTick, p2pSpraySuccMax, false)

		case bytes.Equal(pkt, bSuccess):
			from := addr.String()
			st := getState(from)
			// SUCCESS handling diverges by role, but BOTH SIDES now push END to avoid deadlock.

			if sendRole == common.WORK_P2P_VISITOR {
				// visitor: SUCCESS -> END (strong redundancy: burst + spray)
				logs.Trace("[P2P] visitor recv SUCCESS from=%s local=%s -> send END burst=%d + spray",
					from, localAddrStr, p2pEndBurstOnSuccess)

				trySendEnd(st, addr, p2pEndBurstOnSuccess)
				startSpray(st, addr, bEnd, p2pSprayEndWindow, p2pSprayTick, p2pSprayEndMax, true)
				// visitor still waits for END to confirm both-way
				continue
			}

			// provider:
			logs.Trace("[P2P] provider recv SUCCESS from=%s local=%s -> echo SUCCESS=%d + push END=%d", from, localAddrStr, p2pSuccEchoOnSuccess, p2pEndBurstOnSuccess)

			trySendSucc(st, addr, p2pSuccEchoOnSuccess)
			trySendEnd(st, addr, p2pEndBurstOnSuccess)
			startSpray(st, addr, bEnd, p2pSprayEndWindow, p2pSprayTick, p2pSprayEndMax, true)

			_, fixedLocal, ferr := common.FixUdpListenAddrForRemote(from, localAddrStr)
			if ferr != nil {
				return "", "", sendRole, ferr
			}
			logs.Debug("[P2P] handshake OK (provider-on-success) role=%s remote=%s local=%s", wantRole, from, fixedLocal)
			return from, fixedLocal, wantRole, nil

		case bytes.Equal(pkt, bEnd):
			from := addr.String()
			st := getState(from)
			// END: strongest evidence. ack a little then accept.
			logs.Trace("[P2P] recv END from=%s local=%s -> ack END=%d then accept", from, localAddrStr, p2pEndBurstOnEndAck)

			trySendEnd(st, addr, p2pEndBurstOnEndAck)

			_, fixedLocal, ferr := common.FixUdpListenAddrForRemote(from, localAddrStr)
			if ferr != nil {
				return "", "", sendRole, ferr
			}
			logs.Debug("[P2P] handshake OK role=%s remote=%s local=%s", wantRole, from, fixedLocal)
			return from, fixedLocal, wantRole, nil

		default:
			continue
		}
	}
}

func getNextAddr(addr string, n int) (string, error) {
	lastColonIndex := strings.LastIndex(addr, ":")
	if lastColonIndex == -1 {
		return "", fmt.Errorf("the format of %s is incorrect", addr)
	}
	host := addr[:lastColonIndex]
	portStr := addr[lastColonIndex+1:]
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return "", err
	}
	return host + ":" + strconv.Itoa(port+n), nil
}

func getAddrInterval(addr1, addr2, addr3 string) (int, error) {
	p1 := common.GetPortByAddr(addr1)
	if p1 == 0 {
		return 0, fmt.Errorf("the format of %s incorrect", addr1)
	}
	p2 := common.GetPortByAddr(addr2)
	if p2 == 0 {
		return 0, fmt.Errorf("the format of %s incorrect", addr2)
	}
	p3 := common.GetPortByAddr(addr3)
	if p3 == 0 {
		return 0, fmt.Errorf("the format of %s incorrect", addr3)
	}

	interVal := int(math.Floor(math.Min(math.Abs(float64(p3-p2)), math.Abs(float64(p2-p1)))))
	if p3-p1 < 0 {
		return -interVal, nil
	}
	return interVal, nil
}

func getRandomUniquePorts(count, min, max int) []int {
	if min > max {
		min, max = max, min
	}
	rng := max - min + 1
	if rng <= 0 || count <= 0 {
		return nil
	}
	if count > rng {
		count = rng
	}

	out := make([]int, 0, count)
	seen := make(map[int]struct{}, count*2)
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	for len(out) < count {
		p := r.Intn(rng) + min
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	return out
}

func natHintByInterval(interval int, has bool) string {
	if !has {
		return "unknown"
	}
	if interval == 0 {
		return "cone-ish"
	}
	return "symmetric-ish"
}

func uniqAddrStrs(ss ...string) []string {
	out := make([]string, 0, len(ss))
	seen := make(map[string]struct{}, len(ss))
	for _, s := range ss {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

func resolveUDPAddr(s string) *net.UDPAddr {
	if s == "" {
		return nil
	}
	ua, err := net.ResolveUDPAddr("udp", s)
	if err != nil {
		return nil
	}
	return ua
}

func hostOnly(addr string) string {
	if addr == "" {
		return ""
	}
	h, _, err := net.SplitHostPort(addr)
	if err == nil {
		return h
	}
	return common.RemovePortFromHost(addr)
}

func isRegularStep(interval int, has bool) bool {
	if !has {
		return false
	}
	if interval == 0 {
		return false
	}
	a := interval
	if a < 0 {
		a = -a
	}
	return a >= 1 && a <= 5
}

func sendBurstWithGap(c net.PacketConn, msg []byte, a net.Addr, burst int, gap time.Duration) error {
	if c == nil || a == nil || burst <= 0 {
		return nil
	}
	if gap <= 0 {
		for i := 0; i < burst; i++ {
			if _, e := c.WriteTo(msg, a); e != nil {
				return e
			}
		}
		return nil
	}
	for i := 0; i < burst; i++ {
		if _, e := c.WriteTo(msg, a); e != nil {
			return e
		}
		time.Sleep(gap)
	}
	return nil
}

func openRandomListenConnsForTarget(target *net.UDPAddr, count int) ([]net.PacketConn, error) {
	if target == nil || count <= 0 {
		return nil, nil
	}
	want4 := target.IP != nil && target.IP.To4() != nil

	network := "udp6"
	var lip net.IP
	var err error
	if want4 {
		network = "udp4"
		lip, err = common.GetLocalUdp4IP()
	} else {
		lip, err = common.GetLocalUdp6IP()
	}
	if err != nil || lip == nil || common.IsZeroIP(lip) || lip.IsUnspecified() {
		return nil, errors.New("no usable local ip")
	}
	lip = common.NormalizeIP(lip)

	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	out := make([]net.PacketConn, 0, count)
	for i := 0; i < count; i++ {
		uc, ee := net.ListenUDP(network, &net.UDPAddr{IP: lip, Port: 0})
		if ee != nil {
			continue
		}
		out = append(out, uc)
		time.Sleep(time.Duration(r.Intn(4)+1) * time.Millisecond)
	}
	return out, nil
}

func startFallbackRandomScan(
	ctx context.Context,
	closed *uint32,
	localConn net.PacketConn,
	peerExt1, peerExt2, peerExt3 string,
	delay time.Duration,
) {
	ip := hostOnly(peerExt2)
	if ip == "" {
		ip = hostOnly(peerExt3)
	}
	if ip == "" {
		ip = hostOnly(peerExt1)
	}
	if ip == "" {
		return
	}

	go func() {
		if delay > 0 {
			timer := time.NewTimer(delay)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
			}
		}

		if atomic.LoadUint32(closed) != 0 {
			return
		}

		ports := getRandomUniquePorts(p2pConeFallbackCount, 1, 65535)
		udpAddrs := make([]*net.UDPAddr, 0, len(ports))
		for _, p := range ports {
			ua, e := net.ResolveUDPAddr("udp", net.JoinHostPort(ip, strconv.Itoa(p)))
			if e == nil && ua != nil {
				udpAddrs = append(udpAddrs, ua)
			}
		}

		for _, ua := range udpAddrs {
			_, _ = localConn.WriteTo(bConnect, ua)
		}

		ticker := time.NewTicker(p2pConeFallbackTick)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if atomic.LoadUint32(closed) != 0 {
					return
				}
				for _, ua := range udpAddrs {
					_, _ = localConn.WriteTo(bConnect, ua)
				}
			}
		}
	}()
}

// fillTripletByPortDiff:
// - if already 3 => keep
// - if 2 => synthesize the missing one based on port difference (so getAddrInterval can work)
// - if 0/1 => keep (do NOT fake, so hasPeerExt stays false)
func fillTripletByPortDiff(a1, a2, a3 string) (b1, b2, b3 string) {
	b1, b2, b3 = a1, a2, a3
	cnt := 0
	if b1 != "" {
		cnt++
	}
	if b2 != "" {
		cnt++
	}
	if b3 != "" {
		cnt++
	}
	if cnt != 2 {
		return
	}

	getPort := func(s string) int { return common.GetPortByAddr(s) }
	hostFrom := func(prefer ...string) string {
		for _, s := range prefer {
			if s == "" {
				continue
			}
			h := hostOnly(s)
			if h != "" {
				return h
			}
		}
		return ""
	}
	clampPort := func(p int) int {
		if p < 1 {
			return 1
		}
		if p > 65535 {
			return 65535
		}
		return p
	}
	makeAddr := func(h string, p int) string {
		if h == "" || p <= 0 {
			return ""
		}
		return net.JoinHostPort(h, strconv.Itoa(clampPort(p)))
	}

	switch {
	case b1 == "":
		// have b2,b3 => infer b1 = b2 - (b3-b2)
		p2, p3 := getPort(b2), getPort(b3)
		if p2 == 0 || p3 == 0 {
			return
		}
		d := p3 - p2
		h := hostFrom(b2, b3)
		if d == 0 {
			b1 = b2
		} else {
			b1 = makeAddr(h, p2-d)
		}
	case b2 == "":
		// have b1,b3 => infer b2 = b1 + (b3-b1)/2
		p1, p3 := getPort(b1), getPort(b3)
		if p1 == 0 || p3 == 0 {
			return
		}
		d := (p3 - p1) / 2
		h := hostFrom(b1, b3)
		if d == 0 {
			b2 = b1
		} else {
			b2 = makeAddr(h, p1+d)
		}
	case b3 == "":
		// have b1,b2 => infer b3 = b2 + (b2-b1)
		p1, p2 := getPort(b1), getPort(b2)
		if p1 == 0 || p2 == 0 {
			return
		}
		d := p2 - p1
		h := hostFrom(b1, b2)
		if d == 0 {
			b3 = b2
		} else {
			b3 = makeAddr(h, p2+d)
		}
	}
	return
}
