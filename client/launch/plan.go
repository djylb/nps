package launch

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/djylb/nps/lib/config"
	"github.com/djylb/nps/lib/connprofile"
)

// How long a remote source that failed is left alone.
var (
	SourceRetry = 30 * time.Second
	SourcePause = 5 * time.Minute
)

// Source is one input of the launch: Load returns its profiles.
type Source struct {
	Label string // for messages; holds no secret
	Load  func(context.Context) ([]Profile, error)
}

// Sources makes a source of each -launch value.
func Sources(values []string) []Source {
	out := make([]Source, len(values))
	for i, v := range values {
		label := "-launch #" + strconv.Itoa(i+1)
		if strings.HasPrefix(v, "@") {
			label += " (" + strings.TrimSpace(v[1:]) + ")"
		}
		out[i] = Source{Label: label, Load: func(ctx context.Context) ([]Profile, error) { return Resolve(ctx, v) }}
	}
	return out
}

// Plan is what should run now. Retry is positive while a remote source has
// nothing to offer: load again after it.
type Plan struct {
	Profiles []Profile
	Retry    time.Duration
}

type sourceState struct {
	Source
	good []Profile
	next time.Time // no new attempt before this after a failure
}

// Composer merges the sources and the -L accesses into a Plan, remembering
// the last good content of each remote source.
type Composer struct {
	sources []*sourceState
	locals  []Local
}

// NewComposer makes a Composer.
func NewComposer(sources []Source, locals []Local) *Composer {
	c := &Composer{locals: locals}
	for _, s := range sources {
		c.sources = append(c.sources, &sourceState{Source: s})
	}
	return c
}

// Load reads the sources and composes the plan. An error is invalid local
// input; a remote source that fails falls back to its last good content,
// and without one the plan goes on without it.
func (c *Composer) Load(ctx context.Context) (Plan, error) {
	var (
		plan    Plan
		groups  [][]Profile
		missing bool
	)
	now := time.Now()
	pending := func(until time.Time) {
		missing = true
		if d := until.Sub(now); plan.Retry == 0 || d < plan.Retry {
			plan.Retry = d
		}
	}
	for i, s := range c.sources {
		if now.Before(s.next) {
			if s.good == nil {
				pending(s.next)
			} else {
				groups = append(groups, s.good)
			}
			continue
		}
		profiles, err := s.Load(ctx)
		remote, isRemote := errors.AsType[*RemoteError](err)
		switch {
		case err == nil:
			for j := range profiles {
				profiles[j].Source = s.Label
			}
			s.good, s.next = profiles, time.Time{}
			groups = append(groups, profiles)
		case ctx.Err() != nil:
			return plan, ctx.Err()
		case !isRemote:
			return plan, fmt.Errorf("%s: %w", s.Label, err)
		case remote.Revoked:
			log.Warn("launch input revoked", "input", i+1, "retry_in_ms", SourcePause.Milliseconds())
			s.good, s.next = nil, now.Add(SourcePause)
			pending(s.next)
		case s.good == nil:
			log.Warn("launch input failed", "input", i+1, "retry_in_ms", SourceRetry.Milliseconds(), "err", err)
			s.next = now.Add(SourceRetry)
			pending(s.next)
		default:
			log.Warn("launch input failed", "input", i+1, "detail", "using the last good payload", "err", err)
			s.next = now.Add(SourceRetry)
			groups = append(groups, s.good)
		}
	}
	var err error
	plan.Profiles, err = compose(groups, c.locals, missing)
	return plan, err
}

func (p *Profile) clone() *Profile {
	q := *p
	cfg := *p.Config
	common := *p.Config.CommonConfig
	cfg.CommonConfig = &common
	cfg.Hosts, cfg.Tasks, cfg.LocalServer = slices.Clone(cfg.Hosts), slices.Clone(cfg.Tasks), slices.Clone(cfg.LocalServer)
	q.Config = &cfg
	return &q
}

// Key identifies the connection: same vkey and the same set of addresses.
func (p *Profile) Key() string {
	k, _ := p.identity()
	return k
}

// identity returns the key and a text that differs when the paths are
// written differently.
func (p *Profile) identity() (key, paths string) {
	c := p.Config.CommonConfig
	cands, err := connprofile.Parse(c.Server, c.Tp)
	if err != nil {
		return c.VKey + "\x00" + c.Server, c.Server
	}
	ids := make([]string, len(cands))
	full := make([]string, len(cands))
	for i, x := range cands {
		ids[i] = x.Carrier + "://" + x.Addr + x.Path
		x.Index, x.Raw = 0, ""
		full[i] = fmt.Sprintf("%+v", x)
	}
	slices.Sort(ids)
	slices.Sort(full)
	return c.VKey + "\x00" + strings.Join(ids, "|"), strings.Join(full, "|")
}

// sessionKeys are the [common] keys that apply to the whole connection.
var sessionKeys = []string{"pipes", "scheduler_up", "session_max_lanes", "relay_idle", "p2p_visitor_keep_server",
	"resident", "session_resume_timeout", "session_usage_interval", "new_protocol_pin", "proxy_url", "local_ip"}

func sessionValue(c *config.CommonConfig, key string) string {
	switch key {
	case "pipes":
		return c.Pipes
	case "scheduler_up":
		return c.SchedulerUp
	case "session_max_lanes":
		return strconv.Itoa(c.MaxLanes)
	case "relay_idle":
		return strconv.Itoa(c.RelayIdle)
	case "p2p_visitor_keep_server":
		return strconv.FormatBool(c.KeepServer)
	case "resident":
		return strconv.FormatBool(c.Resident)
	case "session_resume_timeout":
		return strconv.Itoa(c.ResumeTimeout)
	case "session_usage_interval":
		return strconv.Itoa(c.UsageInterval)
	case "new_protocol_pin":
		return c.Pin
	case "proxy_url":
		return c.ProxyUrl
	case "local_ip":
		return c.LocalIP
	}
	return ""
}

func setSession(dst, src *config.CommonConfig, key string) {
	switch key {
	case "pipes":
		dst.Pipes = src.Pipes
	case "scheduler_up":
		dst.SchedulerUp = src.SchedulerUp
	case "session_max_lanes":
		dst.MaxLanes = src.MaxLanes
	case "relay_idle":
		dst.RelayIdle = src.RelayIdle
	case "p2p_visitor_keep_server":
		dst.KeepServer = src.KeepServer
	case "resident":
		dst.Resident = src.Resident
	case "session_resume_timeout":
		dst.ResumeTimeout = src.ResumeTimeout
	case "session_usage_interval":
		dst.UsageInterval = src.UsageInterval
	case "new_protocol_pin":
		dst.Pin = src.Pin
	case "proxy_url":
		dst.ProxyUrl = src.ProxyUrl
	case "local_ip":
		dst.LocalIP = src.LocalIP
	}
}

// writtenKeys fills in which session keys p set, when its form could not
// say: those that are not the defaults.
func (p *Profile) writtenKeys() {
	if p.written == nil {
		p.written = make(map[string]bool)
		def := config.NewCommonConfig()
		for _, k := range sessionKeys {
			if sessionValue(p.Config.CommonConfig, k) != sessionValue(def, k) {
				p.written[k] = true
			}
		}
	}
	p.by = make(map[string]string, len(p.written))
	for k := range p.written {
		p.by[k] = p.Source
	}
}

// merge adds q, another writing of the connection a, to a.
func merge(a, q *Profile) error {
	if _, pa := a.identity(); func() bool { _, pq := q.identity(); return pa != pq }() {
		return fmt.Errorf("connection %s: its paths differ between %s and %s", a.label(), a.Source, q.Source)
	}
	for k := range q.written {
		if !a.written[k] {
			setSession(a.Config.CommonConfig, q.Config.CommonConfig, k)
			a.written = withKey(a.written, k)
			a.by[k] = q.by[k]
			continue
		}
		if sessionValue(a.Config.CommonConfig, k) != sessionValue(q.Config.CommonConfig, k) {
			return fmt.Errorf("connection %s: %s differs between %s and %s", a.label(), k, a.by[k], q.Source)
		}
	}
	if q.Name != "" && q.Name != a.Name {
		if a.Name == "" {
			a.Name = q.Name
		} else {
			a.aliases = append(a.aliases, q.Name)
		}
	}
	a.Config.Hosts = append(a.Config.Hosts, q.Config.Hosts...)
	a.Config.Tasks = append(a.Config.Tasks, q.Config.Tasks...)
	a.Config.LocalServer = append(a.Config.LocalServer, q.Config.LocalServer...)
	a.Direct = a.Direct && q.Direct
	a.combined = true
	a.writings = append(a.writings, q.writings...)
	return nil
}

func withKey(m map[string]bool, k string) map[string]bool {
	if m == nil {
		m = make(map[string]bool)
	}
	m[k] = true
	return m
}

func (p *Profile) label() string {
	if p.Name != "" {
		return strconv.Quote(p.Name)
	}
	return "of " + p.Source
}

// compose merges the writings of each connection, binds the -L accesses
// and checks the listeners. pending means a remote source is not known yet:
// an access that may belong to it waits instead of being an error.
func compose(groups [][]Profile, locals []Local, pending bool) ([]Profile, error) {
	var conns []*Profile
	byKey := make(map[string]*Profile)
	for _, group := range groups {
		for i := range group {
			p := group[i].clone()
			p.writtenKeys()
			p.writings = []writing{{p.Name, len(p.Config.LocalServer) > 0}}
			k := p.Key()
			if a, ok := byKey[k]; ok {
				if err := merge(a, p); err != nil {
					return nil, err
				}
				continue
			}
			byKey[k] = p
			conns = append(conns, p)
		}
	}
	names := make(map[string]*Profile)
	for _, p := range conns {
		for _, n := range append([]string{p.Name}, p.aliases...) {
			if n == "" {
				continue
			}
			if o, dup := names[n]; dup && o != p {
				return nil, fmt.Errorf("connection name %q is used by %s and %s", n, o.Source, p.Source)
			}
			names[n] = p
		}
	}
	for i, p := range conns {
		if p.Name == "" {
			p.Name, p.auto = "connection-"+strconv.Itoa(i+1), true
			names[p.Name] = p
		}
	}
	type listener struct {
		l      *config.LocalServer
		source string
	}
	var listeners []listener
	for _, p := range conns {
		for _, l := range p.Config.LocalServer {
			listeners = append(listeners, listener{l, p.Source})
		}
	}
	written := 0
	for _, p := range conns {
		written += len(p.writings)
	}
	for _, loc := range locals {
		var p *Profile
		switch {
		case loc.Via != "":
			if p = names[loc.Via]; p == nil {
				if pending {
					continue
				}
				return nil, fmt.Errorf("%s: no connection is named %q", loc.Source, loc.Via)
			}
		case pending:
			continue
		case len(conns) != 1 || len(conns[0].writings) != 1:
			return nil, fmt.Errorf("%s: via is required with %d connections", loc.Source, written)
		default:
			p = conns[0]
		}
		for i := range p.writings {
			if loc.Via == "" || p.writings[i].name == loc.Via {
				p.writings[i].access = true
			}
		}
		l, err := loc.Access.compile()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", loc.Source, err)
		}
		p.Config.LocalServer = append(p.Config.LocalServer, l)
		p.Direct, p.combined = false, true
		listeners = append(listeners, listener{l, loc.Source})
	}
	for i, a := range listeners {
		for _, b := range listeners[:i] {
			if clash(a.l, b.l) {
				return nil, fmt.Errorf("local port %d is used by both %s and %s", a.l.Port, b.source, a.source)
			}
		}
	}
	out := make([]Profile, len(conns))
	for i, p := range conns {
		if c := p.Config; p.combined && len(c.LocalServer) > 0 && len(c.Hosts)+len(c.Tasks) > 0 {
			return nil, fmt.Errorf("connection %s has both tunnels and local access from different sources: run them as separate connections", p.label())
		}
		if len(p.Config.LocalServer) > 0 {
			p.Hosting = p.Config.CommonConfig.Resident || slices.ContainsFunc(p.writings, func(w writing) bool { return !w.access })
		}
		out[i] = *p
	}
	if len(out) == 0 && !pending {
		return nil, errors.New("nothing to run: no connection was given")
	}
	return out, nil
}

// clash reports whether two listeners want the same port.
func clash(a, b *config.LocalServer) bool {
	if a.Port == 0 || a.Port != b.Port {
		return false
	}
	if !wildcard(a.Ip) && !wildcard(b.Ip) && a.Ip != b.Ip {
		return false
	}
	ta, ua := networks(a.Type)
	tb, ub := networks(b.Type)
	return ta && tb || ua && ub
}

func wildcard(ip string) bool { return ip == "" || ip == "0.0.0.0" || ip == "::" }

func networks(typ string) (tcp, udp bool) {
	return typ != config.LocalUDP, typ == config.LocalUDP || typ == config.LocalTCPUDP
}
