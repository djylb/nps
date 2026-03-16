package p2p

import (
	"bytes"
	"context"
	"errors"
	"net"
	"time"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/logs"
)

func waitP2PHandshakeSeed(parentCtx context.Context, localConn net.PacketConn, sendRole string, readTimeout int, seed net.Addr) (remoteAddr, localAddr, role string, err error) {
	return waitP2PHandshakeWithSeed(parentCtx, localConn, sendRole, readTimeout, seed)
}

func waitP2PHandshake(parentCtx context.Context, localConn net.PacketConn, sendRole string, readTimeout int) (remoteAddr, localAddr, role string, err error) {
	return waitP2PHandshakeWithSeed(parentCtx, localConn, sendRole, readTimeout, nil)
}

func waitP2PHandshakeWithSeed(parentCtx context.Context, localConn net.PacketConn, sendRole string, readTimeout int, seed net.Addr) (remoteAddr, localAddr, role string, err error) {
	buf := common.BufPoolUdp.Get()
	defer common.BufPoolUdp.Put(buf)
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

	if seed != nil {
		sendRawBurst(bConnect, seed, 1)
		sendRawBurst(bSuccess, seed, 3)

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
			if errors.As(rerr, &ne) && ne.Timeout() {
				continue
			}
			if isIgnorableUDPIcmpError(rerr) {
				logs.Debug("[P2P] ignore transient udp read error role=%s local=%s err=%v", sendRole, localAddrStr, rerr)
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
			st.seenConnect = true

			logs.Trace("[P2P] recv CONNECT from=%s local=%s -> send SUCCESS burst=%d + spray", from, localAddrStr, p2pSuccBurstOnConnect)

			trySendSucc(st, addr, p2pSuccBurstOnConnect)
			startSpray(st, addr, bSuccess, p2pSpraySuccWindow, p2pSprayTick, p2pSpraySuccMax, false)

		case bytes.Equal(pkt, bSuccess):
			from := addr.String()
			st := getState(from)

			if sendRole == common.WORK_P2P_VISITOR {
				logs.Trace("[P2P] visitor recv SUCCESS from=%s local=%s -> send END burst=%d + spray",
					from, localAddrStr, p2pEndBurstOnSuccess)

				trySendEnd(st, addr, p2pEndBurstOnSuccess)
				startSpray(st, addr, bEnd, p2pSprayEndWindow, p2pSprayTick, p2pSprayEndMax, true)
				continue
			}

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
