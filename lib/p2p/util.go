package p2p

import (
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
	"github.com/djylb/nps/lib/logs"
	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
)

func isIgnorableUDPIcmpError(err error) bool {
	if err == nil {
		return false
	}
	errStr := strings.ToLower(err.Error())
	if strings.Contains(errStr, "connection refused") || strings.Contains(errStr, "connection reset by peer") {
		return true
	}
	if strings.Contains(errStr, "wsarecvfrom") && (strings.Contains(errStr, "10054") || strings.Contains(errStr, "wsaeconnreset")) {
		return true
	}
	return false
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

func shouldRunFallbackRandomScan(allowAggressivePrediction, forceHard, portRestrictedByProbe bool) bool {
	return allowAggressivePrediction || forceHard || portRestrictedByProbe
}

func pickPrimaryPunchTarget(exactTargets, predictionTargets []string, allowAggressivePrediction bool) string {
	if allowAggressivePrediction && len(predictionTargets) > 0 {
		return predictionTargets[0]
	}
	if len(exactTargets) > 0 {
		return exactTargets[0]
	}
	if len(predictionTargets) > 0 {
		return predictionTargets[0]
	}
	return ""
}

func buildPredictedPeerAddrs(peerExt1, peerExt2, peerExt3 string, interval int) []string {
	if interval == 0 {
		return nil
	}
	out := make([]string, 0, 6)
	for _, base := range []string{peerExt3, peerExt2, peerExt1} {
		if base == "" {
			continue
		}
		if next, err := getNextAddr(base, interval); err == nil && next != "" {
			out = append(out, next)
		}
		if prev, err := getNextAddr(base, -interval); err == nil && prev != "" {
			out = append(out, prev)
		}
	}
	return uniqAddrStrs(out...)
}

func buildSmallContiguousPorts(basePort, scanRange int) []int {
	if basePort <= 0 || scanRange <= 0 {
		return nil
	}
	out := make([]int, 0, scanRange*2+1)
	for d := 0; d <= scanRange; d++ {
		for _, p := range []int{basePort + d, basePort - d} {
			if p < 1 || p > 65535 {
				continue
			}
			out = append(out, p)
		}
	}
	uniq := make([]int, 0, len(out))
	seen := make(map[int]struct{}, len(out))
	for _, p := range out {
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		uniq = append(uniq, p)
	}
	return uniq
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

func pickUDPConnForTarget(localConn net.PacketConn, target *net.UDPAddr) *net.UDPConn {
	if localConn == nil || target == nil {
		return nil
	}
	if uc, ok := localConn.(*net.UDPConn); ok {
		return uc
	}
	type udpConnsProvider interface {
		UDPConns() []*net.UDPConn
	}
	provider, ok := localConn.(udpConnsProvider)
	if !ok {
		return nil
	}

	all := provider.UDPConns()
	if len(all) == 0 {
		return nil
	}

	want4 := target.IP != nil && target.IP.To4() != nil
	for _, uc := range all {
		if uc == nil {
			continue
		}
		la, ok := uc.LocalAddr().(*net.UDPAddr)
		if !ok || la == nil {
			continue
		}
		is4 := la.IP == nil || la.IP.To4() != nil
		if is4 == want4 {
			return uc
		}
	}
	return all[0]
}

func startPortRestrictedWarmup(ctx context.Context, closed *uint32, localConn net.PacketConn, target *net.UDPAddr) {
	if target == nil || localConn == nil {
		return
	}

	msg := bConnect
	if udpConn := pickUDPConnForTarget(localConn, target); udpConn != nil {
		usedLowTTL, aborted := runLowTTLWarmup(ctx, closed, localConn, target, udpConn, msg)
		if aborted {
			return
		}
		if !usedLowTTL {
			if aborted := sendWarmupBurst(ctx, closed, localConn, msg, target, p2pLowTTLBurst, p2pLowTTLGAP); aborted {
				return
			}
		}
	}

	_ = sendWarmupBurst(ctx, closed, localConn, msg, target, p2pConeBurstCount+2, 150*time.Millisecond)
}

func sendWarmupBurst(
	ctx context.Context,
	closed *uint32,
	localConn net.PacketConn,
	msg []byte,
	target *net.UDPAddr,
	count int,
	gap time.Duration,
) (aborted bool) {
	for i := 0; i < count; i++ {
		select {
		case <-ctx.Done():
			return true
		default:
		}
		if atomic.LoadUint32(closed) != 0 {
			return true
		}
		_, _ = localConn.WriteTo(msg, target)
		if gap > 0 {
			time.Sleep(gap)
		}
	}
	return false
}

func runLowTTLWarmup(
	ctx context.Context,
	closed *uint32,
	localConn net.PacketConn,
	target *net.UDPAddr,
	udpConn *net.UDPConn,
	msg []byte,
) (used, aborted bool) {
	isIPv4 := target.IP != nil && target.IP.To4() != nil
	if isIPv4 {
		pc4 := ipv4.NewPacketConn(udpConn)
		if pc4 == nil {
			return false, false
		}
		origTTL := p2pDefaultTTL
		if ttl, err := pc4.TTL(); err == nil && ttl > 0 {
			origTTL = ttl
		}
		if err := pc4.SetTTL(p2pLowTTLValue); err != nil {
			return false, false
		}
		defer restoreIPv4TTL(pc4, target, origTTL)

		if aborted := sendWarmupBurst(ctx, closed, localConn, msg, target, p2pLowTTLBurst, p2pLowTTLGAP); aborted {
			return true, true
		}
		time.Sleep(p2pLowTTLPause)
		return true, false
	}

	pc6 := ipv6.NewPacketConn(udpConn)
	if pc6 == nil {
		return false, false
	}
	origHop := p2pDefaultHopLimit
	if hop, err := pc6.HopLimit(); err == nil && hop > 0 {
		origHop = hop
	}
	if err := pc6.SetHopLimit(p2pLowTTLValue); err != nil {
		return false, false
	}
	defer restoreIPv6HopLimit(pc6, target, origHop)

	if aborted := sendWarmupBurst(ctx, closed, localConn, msg, target, p2pLowTTLBurst, p2pLowTTLGAP); aborted {
		return true, true
	}
	time.Sleep(p2pLowTTLPause)
	return true, false
}

func restoreIPv4TTL(pc4 *ipv4.PacketConn, target *net.UDPAddr, wantTTL int) {
	if pc4 == nil {
		return
	}
	if wantTTL <= 0 {
		wantTTL = p2pDefaultTTL
	}
	if err := pc4.SetTTL(wantTTL); err == nil {
		return
	} else {
		logs.Warn("[P2P] restore IPv4 TTL failed target=%v want=%d err=%v fallback=%d", target, wantTTL, err, p2pDefaultTTL)
	}
	if wantTTL != p2pDefaultTTL {
		if err := pc4.SetTTL(p2pDefaultTTL); err != nil {
			logs.Warn("[P2P] fallback restore IPv4 TTL failed target=%v fallback=%d err=%v", target, p2pDefaultTTL, err)
		}
	}
}

func restoreIPv6HopLimit(pc6 *ipv6.PacketConn, target *net.UDPAddr, wantHop int) {
	if pc6 == nil {
		return
	}
	if wantHop <= 0 {
		wantHop = p2pDefaultHopLimit
	}
	if err := pc6.SetHopLimit(wantHop); err == nil {
		return
	} else {
		logs.Warn("[P2P] restore IPv6 HopLimit failed target=%v want=%d err=%v fallback=%d", target, wantHop, err, p2pDefaultHopLimit)
	}
	if wantHop != p2pDefaultHopLimit {
		if err := pc6.SetHopLimit(p2pDefaultHopLimit); err != nil {
			logs.Warn("[P2P] fallback restore IPv6 HopLimit failed target=%v fallback=%d err=%v", target, p2pDefaultHopLimit, err)
		}
	}
}

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
