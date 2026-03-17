package p2p

import (
	"context"
	"net"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/logs"
)

func sendP2PTestMsg(
	pCtx context.Context,
	localConn net.PacketConn,
	sendRole string,
	peerExt1, peerExt2, peerExt3 string,
	peerLocal string,
	selfExt1, selfExt2, selfExt3 string,
	punchedAddr net.Addr,
	forceHard bool,
	portRestrictedByProbe bool,
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

	if punchedAddr != nil {
		logs.Debug("[P2P] fast-path punched=%s", punchedAddr.String())
		rAddr, lAddr, rRole, rErr := waitP2PHandshakeSeed(parentCtx, localConn, sendRole, p2pHandshakeTimeout, punchedAddr)
		if rErr == nil {
			winner = localConn
			return localConn, rAddr, lAddr, rRole, nil
		}
		logs.Info("[P2P] fast-path failed punched=%s err=%v, fallback to normal strategy", punchedAddr.String(), rErr)
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
	plan := selectPunchPlan(hasPeerExt, hasSelfExt, peerInterval, selfInterval, forceHard, portRestrictedByProbe)
	selfHard := hasSelfExt && selfInterval != 0
	if forceHard || portRestrictedByProbe || plan.MappingConfidenceLow {
		selfHard = true
	}

	allowAggressivePrediction := plan.EnableAggressivePredict
	allowConservativePrediction := plan.EnableConservativePredict
	normalizedPeerInterval := plan.NormalizedInterval

	logs.Info("[P2P] nat peer=%s(%d,%v) self=%s(%d) peerLocal=%v forceHard=%v probePortRestricted=%v mappingConfidenceLow=%v allowAggressivePrediction=%v allowConservativePrediction=%v strategy.targetSpray=%v strategy.birthday=%v",
		natHintByInterval(peerInterval, hasPeerExt), peerInterval, peerRegular,
		natHintByInterval(selfInterval, hasSelfExt), selfInterval,
		peerLocal != "", forceHard, portRestrictedByProbe, plan.MappingConfidenceLow, allowAggressivePrediction, allowConservativePrediction, plan.UseTargetSpray, plan.UseBirthdayAttack)

	exactTargets := uniqAddrStrs(peerExt3, peerExt2, peerExt1)
	predictionTargets := buildPredictedPeerAddrs(peerExt1, peerExt2, peerExt3, normalizedPeerInterval)
	baseAddrStr := pickPrimaryPunchTarget(exactTargets, predictionTargets, allowAggressivePrediction)
	targets := append([]string{}, exactTargets...)
	if allowAggressivePrediction {
		targets = uniqAddrStrs(append(append([]string{}, predictionTargets...), exactTargets...)...)
	} else if allowConservativePrediction && len(predictionTargets) > 0 {
		// keep exact endpoint as primary in NAT3/unknown cases; add only a tiny prediction probe set
		targets = uniqAddrStrs(append(targets, predictionTargets[0])...)
	}

	var peerLocalUDP *net.UDPAddr
	if peerLocal != "" {
		logs.Debug("[P2P] peerLocal=%s", peerLocal)
		peerLocalUDP, err = net.ResolveUDPAddr("udp", peerLocal)
		if err != nil {
			logs.Error("[P2P] resolve peerLocal failed peerLocal=%s err=%v", peerLocal, err)
			peerLocalUDP = nil
		}
	}

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

	baseUDP := resolveUDPAddr(baseAddrStr)
	if peerLocalUDP != nil {
		go func(remoteUDP *net.UDPAddr) {
			for i := 20; i > 0; i-- {
				select {
				case <-parentCtx.Done():
					return
				default:
				}
				if atomic.LoadUint32(&closed) != 0 {
					return
				}
				_, _ = localConn.WriteTo(bConnect, remoteUDP)
				time.Sleep(100 * time.Millisecond)
			}
		}(peerLocalUDP)
	}

	if baseUDP != nil && (forceHard || portRestrictedByProbe) {
		logs.Debug("[P2P] start low-ttl warmup target=%s forceHard=%v probePortRestricted=%v", baseUDP.String(), forceHard, portRestrictedByProbe)
		startPortRestrictedWarmup(parentCtx, &closed, localConn, baseUDP)
	}
	targetUDPAddrs := make([]*net.UDPAddr, 0, len(targets))
	for _, t := range targets {
		ua := resolveUDPAddr(t)
		if ua != nil {
			targetUDPAddrs = append(targetUDPAddrs, ua)
		}
	}
	if len(targetUDPAddrs) > 0 {
		go func() {
			for _, ua := range targetUDPAddrs {
				_ = sendBurstWithGap(localConn, bConnect, ua, p2pConeBurstCount, p2pConeBurstGap)
			}
		}()
		if len(targetUDPAddrs) > 1 {
			startTickerSender(p2pConeMultiSendTick, func() {
				for _, ua := range targetUDPAddrs {
					_, _ = localConn.WriteTo(bConnect, ua)
				}
			})
		}
	}
	if baseUDP != nil {
		startTickerSender(p2pConeSendTick, func() {
			_, _ = localConn.WriteTo(bConnect, baseUDP)
		})
	}

	if allowConservativePrediction && !allowAggressivePrediction && baseUDP != nil {
		ip := hostOnly(baseUDP.String())
		basePort := common.GetPortByAddr(baseUDP.String())
		contigPorts := buildSmallContiguousPorts(basePort, p2pConeSmallContigRange)
		contigAddrs := make([]*net.UDPAddr, 0, len(contigPorts))
		for _, p := range contigPorts {
			ua, e := net.ResolveUDPAddr("udp", net.JoinHostPort(ip, strconv.Itoa(p)))
			if e == nil && ua != nil {
				contigAddrs = append(contigAddrs, ua)
			}
		}
		if len(contigAddrs) > 0 {
			go func() {
				for _, ua := range contigAddrs {
					_, _ = localConn.WriteTo(bConnect, ua)
				}
			}()
			startTickerSender(p2pConeSmallContigSendTick, func() {
				for _, ua := range contigAddrs {
					_, _ = localConn.WriteTo(bConnect, ua)
				}
			})
		}
	}

	if (selfHard || plan.UseTargetSpray) && baseUDP != nil {
		logs.Debug("[P2P] spray.mode=target base=%s interval=%d rounds=%d burst=%d", baseUDP.String(), normalizedPeerInterval, p2pTargetSprayRounds, p2pTargetSprayBurst)
		startTargetPortSpray(parentCtx, &closed, localConn, baseUDP, normalizedPeerInterval, p2pConeNearScanCount, p2pConeNearScanTick)
	}

	if allowAggressivePrediction && baseUDP != nil && peerRegular {
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

	fallbackDelay := p2pConeFallbackDelay
	if hasPeerExt && peerInterval != 0 {
		fallbackDelay = 0
	}
	if forceHard {
		fallbackDelay = 0
	}
	if shouldRunFallbackRandomScan(allowAggressivePrediction, allowConservativePrediction, forceHard, portRestrictedByProbe) || plan.MappingConfidenceLow {
		startFallbackRandomScan(parentCtx, &closed, localConn, peerExt1, peerExt2, peerExt3, normalizedPeerInterval, fallbackDelay)
	}

	if hasPeerExt && hasSelfExt && peerInterval != 0 && selfInterval == 0 {
		logs.Debug("[P2P] strategy=B peer hard-ish, self easy-ish => broad random scan")
		go func() {
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

			var udpAddrs []*net.UDPAddr
			predPort := common.GetPortByAddr(baseAddrStr)
			if len(predictionTargets) > 0 {
				if pp := common.GetPortByAddr(predictionTargets[0]); pp > 0 {
					predPort = pp
				}
			}

			if predPort > 0 {
				minP := common.Max(1, predPort-300)
				maxP := common.Min(65535, predPort+300)
				nearPorts := getRandomUniquePorts(150, minP, maxP)
				for _, p := range nearPorts {
					ra, e := net.ResolveUDPAddr("udp", net.JoinHostPort(ip, strconv.Itoa(p)))
					if e == nil && ra != nil {
						udpAddrs = append(udpAddrs, ra)
					}
				}
			}

			ports := getRandomUniquePorts(850, 1, 65535)
			for _, p := range ports {
				ra, e := net.ResolveUDPAddr("udp", net.JoinHostPort(ip, strconv.Itoa(p)))
				if e == nil && ra != nil {
					udpAddrs = append(udpAddrs, ra)
				}
			}

			sendBatch := func() {
				for i, ra := range udpAddrs {
					_, _ = localConn.WriteTo(bConnect, ra)
					if i > 0 && i%40 == 0 {
						time.Sleep(5 * time.Millisecond)
					}
				}
			}

			sendBatch()

			ticker := time.NewTicker(1500 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-parentCtx.Done():
					return
				case <-ticker.C:
					if atomic.LoadUint32(&closed) != 0 {
						return
					}
					sendBatch()
				}
			}
		}()
	}

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
				rAddr, lAddr, rRole, rErr := waitP2PHandshake(parentCtx, cc, sendRole, plan.HandshakeTimeoutSec)
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
			return nil, "", localConn.LocalAddr().String(), sendRole, mapP2PContextError(parentCtx.Err())
		}
	}

	rAddr, lAddr, rRole, rErr := waitP2PHandshake(parentCtx, localConn, sendRole, plan.HandshakeTimeoutSec)
	if rErr == nil {
		winner = localConn
		return localConn, rAddr, lAddr, rRole, nil
	}
	return nil, "", "", sendRole, rErr
}
