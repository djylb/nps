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
)

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
