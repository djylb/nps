package p2p

import (
	"context"
	"errors"
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
		rAddr, lAddr, rRole, rErr := waitP2PHandshakeSeed(parentCtx, localConn, sendRole, 10, punchedAddr)
		if rErr == nil {
			winner = localConn
			return localConn, rAddr, lAddr, rRole, nil
		}
		logs.Info("[P2P] fast-path failed punched=%s err=%v, fallback to normal strategy", punchedAddr.String(), rErr)
	}

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
	if portRestrictedByProbe {
		selfHard = true
	}

	logs.Info("[P2P] nat peer=%s(%d,%v) self=%s(%d) peerLocal=%v forceHard=%v probePortRestricted=%v",
		natHintByInterval(peerInterval, hasPeerExt), peerInterval, peerRegular,
		natHintByInterval(selfInterval, hasSelfExt), selfInterval,
		peerLocal != "", forceHard, portRestrictedByProbe)

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

	fallbackDelay := p2pConeFallbackDelay
	if hasPeerExt && peerInterval != 0 {
		fallbackDelay = 0
	}
	if forceHard {
		fallbackDelay = 0
	}
	startFallbackRandomScan(parentCtx, &closed, localConn, peerExt1, peerExt2, peerExt3, fallbackDelay)

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

	rAddr, lAddr, rRole, rErr := waitP2PHandshake(parentCtx, localConn, sendRole, 10)
	if rErr == nil {
		winner = localConn
		return localConn, rAddr, lAddr, rRole, nil
	}
	return nil, "", "", sendRole, rErr
}
