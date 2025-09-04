package client

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/logs"
)

func handleP2PUdp(pCtx context.Context, localAddr, rAddr, md5Password, sendRole, sendMode, sendData string,
) (c net.PacketConn, remoteAddress, localAddress, role, mode, data string, err error) {
	localAddress = localAddr
	parentCtx, parentCancel := context.WithTimeout(pCtx, 30*time.Second)
	defer parentCancel()
	localConn, err := conn.NewUdpConnByAddr(localAddr)
	if err != nil {
		return
	}
	defer localConn.Close()
	for seq := 0; seq < 3; seq++ {
		if err = getRemoteAddressFromServer(rAddr, localAddr, localConn, md5Password, sendRole, sendMode, sendData, seq); err != nil {
			logs.Error("%v", err)
			return
		}
	}
	var remoteAddr1, remoteAddr2, remoteAddr3, remoteLocal string
	//logs.Debug("get remote address from server")
Loop:
	for {
		select {
		case <-parentCtx.Done():
			return
		default:
		}
		buf := make([]byte, 1024)
		_ = localConn.SetReadDeadline(time.Now().Add(time.Second * 5))
		n, addr, er := localConn.ReadFrom(buf)
		_ = localConn.SetReadDeadline(time.Time{})
		if er != nil {
			err = er
			return
		}
		parts := strings.Split(string(buf[:n]), common.CONN_DATA_SEQ)
		payload := common.ValidateAddr(parts[0])
		if len(parts) >= 2 {
			remoteLocal = common.ValidateAddr(parts[1])
		}
		if len(parts) >= 3 {
			mode = parts[2]
		}
		if len(parts) >= 4 {
			data = parts[3]
		}
		rPort := common.GetPortByAddr(rAddr)
		//rAddr2, _ := getNextAddr(rAddr, 1)
		//rAddr3, _ := getNextAddr(rAddr, 2)

		switch common.GetPortByAddr(addr.String()) {
		case rPort:
			remoteAddr1 = payload
		case rPort + 1:
			remoteAddr2 = payload
		case rPort + 2:
			remoteAddr3 = payload
		}
		//logs.Debug("buf: %s", buf)
		if string(buf[:n]) == common.WORK_P2P_CONNECT {
			break Loop
		}
		//logs.Debug("addr: %s", addr.String())
		//logs.Debug("rAddr1: %s rAddr2: %s rAddr3: %s", rAddr, rAddr2, rAddr3)
		//logs.Debug("remoteAddr1: %s remoteAddr2: %s remoteAddr3: %s remoteLocal: %s", remoteAddr1, remoteAddr2, remoteAddr3, remoteLocal)
		if remoteAddr1 != "" && remoteAddr2 != "" && remoteAddr3 != "" {
			//logs.Debug("remoteAddr1: %s remoteAddr2: %s remoteAddr3: %s remoteLocal: %s", remoteAddr1, remoteAddr2, remoteAddr3, remoteLocal)
			break
		}
	}
	if remoteAddress, localAddress, role, err = sendP2PTestMsg(parentCtx, localConn, sendRole, remoteAddr1, remoteAddr2, remoteAddr3, remoteLocal); err != nil {
		return
	}
	if localAddr != localAddress {
		logs.Trace("LocalAddr: %s %s", localAddr, localAddress)
	}
	//logs.Error("LocalAddr: %s %s", localAddr, localAddress)
	c, err = net.ListenPacket("udp", localAddress)
	//port := common.GetPortStrByAddr(localAddress)
	//if strings.Contains(remoteAddress, "[") {
	//	c, err = net.ListenPacket("udp6", ":"+port)
	//} else {
	//	c, err = net.ListenPacket("udp4", ":"+port)
	//}
	return
}

func getRemoteAddressFromServer(rAddr, localAddr string, localConn net.PacketConn, md5Password, role, mode, data string, add int) error {
	rAddr, err := getNextAddr(rAddr, add)
	if err != nil {
		logs.Error("%v", err)
		return err
	}
	addr, err := net.ResolveUDPAddr("udp", rAddr)
	if err != nil {
		return err
	}
	if _, err := localConn.WriteTo(common.GetWriteStr(md5Password, role, localAddr, mode, data), addr); err != nil {
		return err
	}
	return nil
}

func sendP2PTestMsg(pCtx context.Context, localConn net.PacketConn, sendRole, remoteAddr1, remoteAddr2, remoteAddr3, remoteLocal string) (remoteAddr, localAddr, role string, err error) {
	defer localConn.Close()
	isClose := false
	defer func() { isClose = true }()
	parentCtx, parentCancel := context.WithCancel(pCtx)
	defer parentCancel()
	//logs.Trace("%s %s %s %s", remoteAddr3, remoteAddr2, remoteAddr1, remoteLocal)
	if remoteLocal != "" {
		go func() {
			remoteUdpLocal, err := net.ResolveUDPAddr("udp", remoteLocal)
			if err != nil {
				return
			}
			for i := 20; i > 0; i-- {
				select {
				case <-parentCtx.Done():
					return
				default:
				}
				if _, err := localConn.WriteTo([]byte(common.WORK_P2P_CONNECT), remoteUdpLocal); err != nil {
					return
				}
				time.Sleep(time.Millisecond * 100)
			}
		}()
	}
	if remoteAddr1 != "" && remoteAddr2 != "" && remoteAddr3 != "" {
		interval, err := getAddrInterval(remoteAddr1, remoteAddr2, remoteAddr3)
		if err != nil {
			return "", localConn.LocalAddr().String(), sendRole, err
		}
		go func() {
			addr, err := getNextAddr(remoteAddr3, interval)
			if err != nil {
				return
			}
			remoteUdpAddr, err := net.ResolveUDPAddr("udp", addr)
			if err != nil {
				return
			}
			logs.Trace("try send test packet to target %s", addr)
			ticker := time.NewTicker(time.Millisecond * 500)
			defer ticker.Stop()
			for {
				select {
				case <-parentCtx.Done():
					return
				case <-ticker.C:
					if isClose {
						return
					}
					if _, err := localConn.WriteTo([]byte(common.WORK_P2P_CONNECT), remoteUdpAddr); err != nil {
						return
					}
				}
			}
		}()
		if interval != 0 {
			ip := common.RemovePortFromHost(remoteAddr2)
			p1 := common.GetPortByAddr(remoteAddr1)
			p2 := common.GetPortByAddr(remoteAddr2)
			p3 := common.GetPortByAddr(remoteAddr3)
			go func() {
				startPort := p3
				endPort := startPort + (interval * 50)
				if (p1 < p3 && p3 < p2) || (p1 > p3 && p3 > p2) {
					endPort = endPort + (p2 - p3)
				}
				endPort = common.GetPort(endPort)
				logs.Debug("Start Port: %d, End Port: %d, Interval: %d", startPort, endPort, interval)
				ports := getRandomPortArr(startPort, endPort)
				ctx, cancel := context.WithCancel(parentCtx)
				defer cancel()
				for i := 0; i <= 50; i++ {
					go func(port int) {
						trueAddress := ip + ":" + strconv.Itoa(port)
						logs.Trace("try send test packet to target %s", trueAddress)
						remoteUdpAddr, err := net.ResolveUDPAddr("udp", trueAddress)
						if err != nil {
							return
						}
						ticker := time.NewTicker(time.Second * 2)
						defer ticker.Stop()
						for {
							select {
							case <-ctx.Done():
								return
							case <-ticker.C:
								if isClose {
									return
								}
								if _, err := localConn.WriteTo([]byte(common.WORK_P2P_CONNECT), remoteUdpAddr); err != nil {
									return
								}
							}
						}
					}(ports[i])
					time.Sleep(time.Millisecond * 10)
				}
			}()
		}
	}

	buf := make([]byte, 10)
Loop:
	for {
		select {
		case <-parentCtx.Done():
			break Loop
		default:
		}
		_ = localConn.SetReadDeadline(time.Now().Add(time.Second * 10))
		n, addr, err := localConn.ReadFrom(buf)
		_ = localConn.SetReadDeadline(time.Time{})
		if err != nil {
			break
		}

		switch string(buf[:n]) {
		case common.WORK_P2P_SUCCESS:
			for i := 20; i > 0; i-- {
				if _, err = localConn.WriteTo([]byte(common.WORK_P2P_END), addr); err != nil {
					return "", localConn.LocalAddr().String(), sendRole, err
				}
			}
			if sendRole == common.WORK_P2P_VISITOR {
				for {
					select {
					case <-parentCtx.Done():
						break Loop
					default:
					}
					_ = localConn.SetReadDeadline(time.Now().Add(time.Second))
					n, addr, err := localConn.ReadFrom(buf)
					_ = localConn.SetReadDeadline(time.Time{})
					if err != nil {
						break
					}
					switch string(buf[:n]) {
					case common.WORK_P2P_END:
						logs.Debug("Remotely Address %v Reply Packet Successfully Received", addr)
						return addr.String(), localConn.LocalAddr().String(), common.WORK_P2P_VISITOR, nil
					default:
						continue
					}
				}
			}
			return addr.String(), localConn.LocalAddr().String(), common.WORK_P2P_PROVIDER, nil
		case common.WORK_P2P_END:
			logs.Debug("Remotely Address %v Reply Packet Successfully Received", addr)
			return addr.String(), localConn.LocalAddr().String(), common.WORK_P2P_VISITOR, nil
		case common.WORK_P2P_CONNECT:
			go func() {
				for i := 20; i > 0; i-- {
					select {
					case <-parentCtx.Done():
						return
					default:
					}
					logs.Debug("try send receive success packet to target %v", addr)
					if _, err = localConn.WriteTo([]byte(common.WORK_P2P_SUCCESS), addr); err != nil {
						return
					}
					time.Sleep(time.Second)
				}
			}()
		default:
			continue
		}
	}
	return "", localConn.LocalAddr().String(), sendRole, errors.New("connect to the target failed, maybe the nat type is not support p2p")
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

func getRandomPortArr(min, max int) []int {
	if min > max {
		min, max = max, min
	}
	length := max - min + 1
	addrAddr := make([]int, length)
	for i := 0; i < length; i++ {
		addrAddr[i] = max - i
	}
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	for i := length - 1; i > 0; i-- {
		j := r.Intn(i + 1)
		addrAddr[i], addrAddr[j] = addrAddr[j], addrAddr[i]
	}
	return addrAddr
}
