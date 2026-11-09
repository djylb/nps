package service

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/djylb/nps/lib/model"
	"github.com/djylb/nps/lib/netaddr"
	"github.com/djylb/nps/lib/textutil"
)

// ErrPortRange is a port range submitted when adding a tunnel that cannot be
// expanded; the wrapping error says why.
var ErrPortRange = errors.New("invalid port range")

// PortError names the port of a batch whose check failed.
type PortError struct {
	Port int
	Err  error
}

func (e *PortError) Error() string { return "port " + strconv.Itoa(e.Port) + ": " + e.Err.Error() }
func (e *PortError) Unwrap() error { return e.Err }

// batchLock keeps two batches from passing the port checks against each other.
var batchLock sync.Mutex

// AddTunnels adds the tunnels of a submitted port field. A single port, or an
// empty field, is AddTunnel unchanged. A range or list (tcp and udp only)
// opens one tunnel per port with the settings of the form, the remark
// extended by "-port", and the target of each: a target line naming as many
// ports as the field pairs with them in order, one naming a single port
// serves them all. Either every tunnel is added or none is.
func (s DefaultIndexService) AddTunnels(input TunnelInput) ([]TunnelMutation, error) {
	spec := strings.TrimSpace(input.PortSpec)
	if !strings.ContainsAny(spec, ",-") {
		m, err := s.AddTunnel(input)
		if err != nil {
			return nil, err
		}
		return []TunnelMutation{m}, nil
	}
	ports := netaddr.GetPorts(spec)
	form := input.form()
	if len(ports) == 0 {
		return nil, fmt.Errorf("%w: %q holds no port; write ports as 8000-8100,9000", ErrPortRange, spec)
	}
	if len(ports) == 1 {
		form.Port = ports[0]
		m, err := s.AddTunnel(input)
		if err != nil {
			return nil, err
		}
		return []TunnelMutation{m}, nil
	}
	if form.Mode != "tcp" && form.Mode != "udp" {
		return nil, fmt.Errorf("%w: only tcp and udp tunnels take several ports, not %q", ErrPortRange, form.Mode)
	}
	if input.PortRangeMax > 0 && len(ports) > input.PortRangeMax {
		return nil, fmt.Errorf("%w: %d ports exceed the limit of %d per submission (web_tunnel_port_range_max)", ErrPortRange, len(ports), input.PortRangeMax)
	}
	targets, err := rangeTargets(form.Target, ports)
	if err != nil {
		return nil, err
	}

	batchLock.Lock()
	defer batchLock.Unlock()
	client, err := s.Backend.Repository.GetClient(input.ClientID)
	if err != nil {
		return nil, ErrClientNotFound
	}
	if limit := client.MaxTunnelNum; limit != 0 {
		if have := s.Backend.Repository.ClientEntryCount(client); have+len(ports) > limit {
			return nil, fmt.Errorf("%w: the client allows %d tunnels, has %d and %d more were asked", ErrTunnelLimitExceeded, limit, have, len(ports))
		}
	}

	base := form.Remark
	if form.Target == nil {
		form.Target = new(model.Target)
	}
	prepared := make([]*model.Tunnel, 0, len(ports))
	for i, port := range ports {
		form.Port = port
		form.Target.TargetStr = targets[i]
		if base == "" {
			form.Remark = strconv.Itoa(port)
		} else {
			form.Remark = base + "-" + strconv.Itoa(port)
		}
		t, err := s.prepareTunnel(input)
		if err != nil {
			return nil, &PortError{Port: port, Err: err}
		}
		prepared = append(prepared, t)
	}

	out := make([]TunnelMutation, 0, len(prepared))
	for i, t := range prepared {
		if err := s.createTunnel(t); err != nil {
			for _, done := range prepared[:i] {
				_ = s.Backend.Runtime.DeleteTunnel(done.Id)
			}
			return nil, &PortError{Port: t.Port, Err: err}
		}
		out = append(out, TunnelMutation{ID: t.Id, ClientID: input.ClientID, Mode: t.Mode})
	}
	return out, nil
}

// rangeTargets returns the target of each of ports from the submitted target.
// It must be one line: load-balancing targets, one per line, cannot be paired
// with a range. The line either names as many ports as ports, or one port that
// every tunnel then uses; a target without a port (a service name) is kept for
// all.
func rangeTargets(target *model.Target, ports []int) ([]string, error) {
	var lines []string
	if target != nil {
		for _, l := range strings.Split(textutil.NormalizeNewlines(target.TargetStr), "\n") {
			if l = strings.TrimSpace(l); l != "" {
				lines = append(lines, l)
			}
		}
	}
	if len(lines) != 1 {
		return nil, fmt.Errorf("%w: several ports need exactly one target line (load-balancing targets of several lines cannot be combined with a port range); "+
			"write host:port to send every port to it, or host:8000-8100,9000 naming as many ports as listen ports", ErrPortRange)
	}
	line := lines[0]
	host, portPart := "", line
	if i := strings.LastIndex(line, ":"); i >= 0 {
		host, portPart = line[:i], line[i+1:]
		if strings.Count(line, ":") > 1 && !strings.Contains(line, "]") {
			host, portPart = "", "" // a bare IPv6 address has no port
		}
	}
	nums := netaddr.GetPorts(portPart)
	matched, ok := model.MatchTargetPorts(ports, nums, true)
	out := make([]string, len(ports))
	switch {
	case ok:
		for i, p := range matched {
			out[i] = model.PortTaskTarget(host, p)
		}
	case len(nums) == 0:
		for i := range out {
			out[i] = line
		}
	default:
		return nil, fmt.Errorf("%w: %d listen ports but the target names %d ports; use one target port (host:port) for all of them, or exactly %d (host:8000-8100,9000)",
			ErrPortRange, len(ports), len(nums), len(ports))
	}
	return out, nil
}
