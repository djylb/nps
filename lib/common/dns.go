package common

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/miekg/dns"
	"github.com/xtaci/kcp-go/v5"
)

var customDnsAddr string

func SetCustomDNS(dnsAddr string) {
	if dnsAddr == "" {
		return
	}
	colonCount := strings.Count(dnsAddr, ":")
	if colonCount == 0 {
		dnsAddr += ":53"
	} else if colonCount > 1 && !strings.Contains(dnsAddr, "]:") {
		if strings.Contains(dnsAddr, "]") {
			dnsAddr += ":53"
		} else {
			dnsAddr = "[" + dnsAddr + "]:53"
		}
	}

	customDnsAddr = dnsAddr

	net.DefaultResolver = &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			return net.Dial(network, dnsAddr)
		},
	}
}

func GetCustomDNS() string {
	if customDnsAddr != "" {
		return customDnsAddr
	}
	return "8.8.8.8:53"
}

func GetFastAddr(addr string, testType string) (string, error) {
	dnsServer := GetCustomDNS()
	host := GetIpByAddr(addr)

	ip := net.ParseIP(host)
	if ip != nil {
		return addr, nil
	}

	port := GetPortByAddr(addr)
	//logs.Debug("port: %s", port)

	var ipv4List, ipv6List []string
	ipv4List, ipv6List, err := resolveDomain(host, ipv4List, ipv6List, dnsServer, 10)
	if err != nil {
		return addr, err
	}

	if len(ipv4List) == 0 && len(ipv6List) == 0 {
		return addr, fmt.Errorf("can not resolve %s", host)
	}

	ipList := append(ipv4List, ipv6List...)
	//logs.Debug("IP: %v %v", ipv4List, ipv6List)
	ipList = unique(ipList)
	//logs.Debug("IP unique: %v", ipList)

	bestIP, bestLatency := ipList[0], time.Duration(1<<63-1)
	//logs.Debug("IP: %s BestLatency: %d", bestIP, bestLatency)
	for _, ip := range ipList {
		latency, err := TestLatency(BuildAddress(ip, strconv.Itoa(port)), testType)
		//logs.Debug("IP: %s Latency: %d Err: %w", bestIP, latency, err)
		if err != nil {
			continue
		}
		if latency < bestLatency {
			bestIP, bestLatency = ip, latency
		}
	}
	//logs.Debug("Final Best IP: %s", bestIP)
	if bestLatency == time.Duration(1<<63-1) {
		return addr, nil
	}
	//logs.Debug("Final1 Best IP: %s", bestIP)
	return BuildAddress(bestIP, strconv.Itoa(port)), nil
}

func resolveDomain(domain string, ipv4List, ipv6List []string, dnsServer string, redirects int) ([]string, []string, error) {
	//logs.Debug("%s %v %v %s %d", domain, ipv4List, ipv6List, dnsServer, redirects)
	if redirects <= 0 {
		return ipv4List, ipv6List, fmt.Errorf("too many CNAME")
	}

	ipv4s, ipv6s, err := resolveIPs(domain, dnsServer)
	if err != nil {
		return ipv4List, ipv6List, err
	}

	ipv4List = append(ipv4List, ipv4s...)
	ipv6List = append(ipv6List, ipv6s...)
	//logs.Debug("IP: %v %v", ipv4List, ipv6List)
	cnameList, err := resolveRecords(domain, dns.TypeCNAME, dnsServer, true)
	//logs.Debug("Cname: %v", cnameList)
	if err != nil || len(cnameList) == 0 {
		return ipv4List, ipv6List, nil
	}

	for _, cname := range cnameList {
		ipv4List, ipv6List, err = resolveDomain(cname, ipv4List, ipv6List, dnsServer, redirects-1)
		if err != nil {
			continue
		}
	}

	return ipv4List, ipv6List, nil
}

func resolveIPs(domain, dnsServer string) (ipv4List, ipv6List []string, err error) {
	//logs.Debug("Domain: %s DNS: %s", domain, dnsServer)
	current := domain
	var nsHosts []string
	for {
		nsHosts, err = resolveRecords(current, dns.TypeNS, dnsServer, true)
		//logs.Debug("Domain: %s DNS: %s %v", current, dnsServer, err)
		if err != nil {
			return nil, nil, fmt.Errorf("lookup NS for %q failed: %w", current, err)
		}
		if len(nsHosts) > 0 {
			break
		}
		idx := strings.Index(current, ".")
		if idx < 0 {
			break
		}
		current = current[idx+1:]
	}
	//logs.Debug("NS: %v", nsHosts)
	if len(nsHosts) == 0 {
		return nil, nil, fmt.Errorf("no NS records found for %s (and its parents)", domain)
	}

	var nsServers []string
	for _, ns := range nsHosts {
		// IPv4
		if v4s, err := resolveRecords(ns, dns.TypeA, dnsServer, true); err == nil {
			for _, ip := range v4s {
				nsServers = append(nsServers, net.JoinHostPort(ip, "53"))
			}
		}
		// IPv6
		if v6s, err := resolveRecords(ns, dns.TypeAAAA, dnsServer, true); err == nil {
			for _, ip := range v6s {
				nsServers = append(nsServers, net.JoinHostPort(ip, "53"))
			}
		}
	}
	if len(nsServers) == 0 {
		return nil, nil, fmt.Errorf("failed to resolve any NS IPs for %s", domain)
	}
	//logs.Debug("NSS: %v", nsServers)

	for _, srv := range nsServers {
		if len(ipv4List) == 0 {
			if v4s, _ := resolveRecords(domain, dns.TypeA, srv, true); len(v4s) > 0 {
				ipv4List = v4s
			}
		}
		if len(ipv6List) == 0 {
			if v6s, _ := resolveRecords(domain, dns.TypeAAAA, srv, true); len(v6s) > 0 {
				ipv6List = v6s
			}
		}
		if len(ipv4List) > 0 && len(ipv6List) > 0 {
			break
		}
	}
	//logs.Debug("IP: %v %v", ipv4List, ipv6List)
	return ipv4List, ipv6List, nil
}

func resolveRecords(domain string, qtype uint16, server string, recurse bool) ([]string, error) {
	if !strings.Contains(server, ":") {
		server = net.JoinHostPort(server, "53")
	}
	c := new(dns.Client)
	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(domain), qtype)
	m.RecursionDesired = recurse

	resp, _, err := c.Exchange(m, server)
	if err != nil {
		return nil, err
	}

	var out []string
	for _, ans := range resp.Answer {
		switch rr := ans.(type) {
		case *dns.A:
			out = append(out, rr.A.String())
		case *dns.AAAA:
			out = append(out, rr.AAAA.String())
		case *dns.CNAME:
			out = append(out, rr.Target)
		case *dns.NS:
			out = append(out, rr.Ns)
		}
	}
	return out, nil
}

func TestLatency(addr string, testType string) (time.Duration, error) {
	start := time.Now()
	switch testType {
	case "tcp", "tls":
		conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
		if err != nil {
			return 0, err
		}
		defer conn.Close()
	case "kcp", "udp":
		conn, err := kcp.DialWithOptions(addr, nil, 0, 0)
		if err != nil {
			return 0, err
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	default:
		return 0, fmt.Errorf("unsupported test type: %s", testType)
	}
	return time.Since(start), nil
}

func unique(addrs []string) []string {
	seen := make(map[string]struct{}, len(addrs))
	var out []string
	for _, a := range addrs {
		if _, ok := seen[a]; !ok {
			seen[a] = struct{}{}
			out = append(out, a)
		}
	}
	return out
}
