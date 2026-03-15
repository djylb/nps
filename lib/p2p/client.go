package p2p

import (
	"bytes"
	"context"
	"errors"
	"net"
	"strings"
	"time"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/logs"
)

func HandleUDP(
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
	var altPortProbeSeen bool
	serverPort := common.GetPortByAddr(rAddr)

	buf := common.BufPoolUdp.Get()
	defer common.BufPoolUdp.Put(buf)

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
			if errors.As(rerr, &ne) && ne.Timeout() {
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
		if isP2PNATProbePacket(pkt) {
			altPortProbeSeen = true
			logs.Trace("[P2P] received nat-probe from=%s local=%s", fromAddr.String(), localConn.LocalAddr().String())
			continue
		}

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
	portRestrictedByProbe := !altPortProbeSeen
	if portRestrictedByProbe {
		forceHard = true
	}

	logs.Debug("[P2P] collected peerExt=[%s,%s,%s] selfExt=[%s,%s,%s] peerLocal=%s punched=%v forceHard=%v altProbeSeen=%v",
		peerExt1, peerExt2, peerExt3,
		selfExt1, selfExt2, selfExt3,
		peerLocal, punchedAddr != nil, forceHard, altPortProbeSeen)

	winConn, remoteAddress, localAddress, role, err := sendP2PTestMsg(
		parentCtx,
		localConn,
		sendRole,
		peerExt1, peerExt2, peerExt3,
		peerLocal,
		selfExt1, selfExt2, selfExt3,
		punchedAddr,
		forceHard,
		portRestrictedByProbe,
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

	ipV4, errV4 := common.GetLocalUdp4IP()
	if errV4 == nil && ipV4 != nil && !common.IsZeroIP(ipV4) {
		a := net.JoinHostPort(ipV4.String(), port)
		if a != "" && !common.InStrArr(out, a) {
			out = append(out, a)
		}
	}

	ipV6, errV6 := common.GetLocalUdp6IP()
	if errV6 == nil && ipV6 != nil && !common.IsZeroIP(ipV6) {
		a := net.JoinHostPort(ipV6.String(), port)
		if a != "" && !common.InStrArr(out, a) {
			out = append(out, a)
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

func isP2PNATProbePacket(pkt []byte) bool {
	parts := strings.Split(string(pkt), common.CONN_DATA_SEQ)
	for len(parts) > 0 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	return len(parts) > 0 && parts[0] == common.WORK_P2P_NAT_PROBE
}
