package httpproxy

import (
	"context"
	"html"
	"net/http"
	"net/http/httputil"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/djylb/netx/httprewrite"
	"github.com/djylb/nps/lib/model"
	"github.com/djylb/nps/lib/netaddr"
	"github.com/djylb/nps/lib/servercfg"
)

// A request to a host passes fixed stages, each reading the plan of the
// host and recording what it learnt in the proxyRequest:
//
//  1. entry: the host is looked up and the source lists checked;
//  2. scheme: ACME challenges are answered and plain HTTP is redirected to
//     HTTPS (AutoHttps), unless the request carries the HTTP-only password;
//  3. admission: the flow, time and connection limits are taken;
//  4. authentication: Basic credentials of the client, the host's accounts
//     and user accounts; an upgrade goes on without them, see forwardTunnel;
//  5. rewrite: the path is rewritten, after which a host with a redirect URL
//     answers the request, the URL expanded with the rewritten path;
//  6. upstream: the route and the server name of a TLS backend are picked;
//  7. forward: a regular request goes through the host's reverse proxy, an
//     upgrade or CONNECT through forwardTunnel. Both apply the same host and
//     header rules; where they differ the plan says so.
//
// Every setting of a host is read once, when its plan is compiled.

// hostPlan is what the proxy does with the requests of one host, compiled
// from the host's settings. It never changes: an edit of the host drops it
// from the Plans and the next request compiles a new one.
type hostPlan struct {
	host *model.Host

	// scheme stage
	acme      bool // answer ACME HTTP challenges (AutoSSL)
	autoHttps bool // redirect plain HTTP to HTTPS
	// originalPath sends the path before the rewrite as X-Original-Path;
	// CompatMode leaves it out.
	originalPath bool
	autoCORS     bool
	backendTLS   bool
	// upgradeForwardHeaders adds the forwarding headers to upgrades;
	// CompatMode leaves them out, while regular requests keep them.
	upgradeForwardHeaders bool

	// rewrite stage
	location    string       // the path prefix PathRewrite replaces
	pathRewrite string       // "" if the path is kept
	redirect    *redirectURL // answer with a 307 to it, nil if none
	hostChange  string       // the Host sent on, "" for the client's
	headerRules []httprewrite.Rule
	respRules   []httprewrite.Rule
	// altSvc is the Alt-Svc header that announces HTTP/3 on responses to
	// TLS requests, "" if HTTP/3 is off or CompatMode or HttpsJustProxy is
	// set.
	altSvc string

	// upstream and forward stages
	backendScheme string          // "https" if TargetIsHttps
	transports    *hostTransports // regular requests; CompatMode keeps no connection
	proxy         *httputil.ReverseProxy
}

// proxyRequest is one request on its way through the stages.
type proxyRequest struct {
	plan   *hostPlan
	remote string
	tls    bool // came over TLS: HTTPS or HTTP/3
	// httpOnly is set if the request carried the HTTP-only password: it is
	// not redirected to HTTPS and gets no forwarding headers.
	httpOnly bool
	authed   bool
	route    model.Route
	sni      string // server name for a TLS backend
}

type requestKey struct{}

func requestOf(ctx context.Context) *proxyRequest {
	return ctx.Value(requestKey{}).(*proxyRequest)
}

func (s *HttpServer) compilePlan(h *model.Host) *hostPlan {
	hv := h.View()
	p := &hostPlan{
		host:                  h,
		acme:                  s.autoSSL(hv),
		autoHttps:             hv.AutoHttps,
		location:              h.Location,
		pathRewrite:           hv.PathRewrite,
		originalPath:          !hv.CompatMode,
		hostChange:            hv.HostChange,
		headerRules:           parseHeaderRules(hv.HeaderChange),
		respRules:             parseHeaderRules(hv.RespHeaderChange),
		autoCORS:              hv.AutoCORS,
		backendScheme:         "http",
		backendTLS:            hv.TargetIsHttps,
		upgradeForwardHeaders: !hv.CompatMode,
		transports:            s.newTransports(hv.CompatMode),
	}
	if hv.RedirectURL != "" {
		p.redirect = compileRedirectURL(hv.RedirectURL)
	}
	if hv.TargetIsHttps {
		p.backendScheme = "https"
	}
	if s.Http3Port > 0 && !hv.HttpsJustProxy && !hv.CompatMode {
		p.altSvc = `h3=":` + s.Http3PortStr + `"; ma=86400`
	}
	p.proxy = s.newReverseProxy(p)
	return p
}

// CloseIdleConnections closes the idle backend connections of p.
func (p *hostPlan) CloseIdleConnections() { p.transports.CloseIdleConnections() }

// rewritePath applies PathRewrite to r.
func (p *hostPlan) rewritePath(r *http.Request) {
	if p.pathRewrite == "" || !strings.HasPrefix(r.URL.Path, p.location) {
		return
	}
	if p.originalPath {
		r.Header.Set("X-Original-Path", r.URL.Path)
	}
	r.URL.Path = p.pathRewrite + r.URL.Path[len(p.location):]
}

// rewriteHeaders applies the host and header rules to out, the request sent
// on, with rule variables read from in, the request as the client sent it.
// forward adds the forwarding headers if http_add_origin_header is on.
func (p *hostPlan) rewriteHeaders(out, in *http.Request, forward bool) {
	scheme := requestScheme(in)
	if p.hostChange != "" {
		out.Host = p.hostChange
		if out.Header.Get("Origin") != "" {
			out.Header.Set("Origin", scheme+"://"+p.hostChange)
		}
	}
	if forward && servercfg.Current().Proxy.AddOriginHeader {
		if out.Header.Get("X-Forwarded-Proto") == "" {
			out.Header.Set("X-Forwarded-Proto", scheme)
		}
		if out.Header.Get("X-Forwarded-Host") == "" && p.hostChange == "" {
			out.Header.Set("X-Forwarded-Host", in.Host)
		}
		out.Header.Set("X-Real-IP", netaddr.Host(in.RemoteAddr))
	}
	if len(p.headerRules) == 0 {
		return
	}
	c := httprewrite.Context{Request: in}
	_ = httprewrite.ApplyRequest(out, p.headerRules, &c)
}

// rewriteUpgrade applies the host and header rules to r, an upgrade sent on
// as it is.
func (p *hostPlan) rewriteUpgrade(r *http.Request, req *proxyRequest) {
	in := *r
	if len(p.headerRules) != 0 {
		in.Header = r.Header.Clone()
	}
	p.rewriteHeaders(r, &in, !req.httpOnly && p.upgradeForwardHeaders)
}

// applyResponseRules applies response header rules to resp.
func applyResponseRules(resp *http.Response, rules []httprewrite.Rule) {
	if len(rules) == 0 || resp == nil || resp.Request == nil {
		return
	}
	c := httprewrite.Context{Request: resp.Request, Response: resp}
	_ = httprewrite.ApplyHeader(resp.Header, rules, &c)
}

// redirectURL is a redirect target: a literal URL or a template of one.
type redirectURL struct {
	literal string
	tmpl    *httprewrite.Template
}

// compileRedirectURL compiles a redirect URL setting, its HTML entities
// decoded. A template that does not compile is used literally.
func compileRedirectURL(setting string) *redirectURL {
	val := html.UnescapeString(strings.TrimSpace(setting))
	u := &redirectURL{literal: val}
	if !strings.Contains(val, "${") {
		return u
	}
	t, err := httprewrite.Compile(val, ruleVars)
	if err != nil {
		proxyLog.Debug("redirect url used literally", "detail", val, "err", err)
		return u
	}
	u.tmpl = t
	return u
}

// expand returns the URL to redirect r to.
func (u *redirectURL) expand(r *http.Request) string {
	if u.tmpl == nil {
		return u.literal
	}
	c := httprewrite.Context{Request: r}
	if expanded, err := u.tmpl.Expand(&c); err == nil {
		return expanded
	}
	return u.literal
}

// Plans holds the compiled plan of each host. The server drops the plan of
// a host whenever the host is edited or deleted.
type Plans struct {
	plans sync.Map // host id -> *hostPlan
	mu    sync.Mutex
	// gen counts the drops, so that a plan compiled from settings read before
	// a drop is not kept after it.
	gen atomic.Uint64
}

func NewPlans() *Plans { return &Plans{} }

// get returns the plan of h, compiling it if there is none or the one there
// is of another entry with the same id.
func (c *Plans) get(h *model.Host, compile func(*model.Host) *hostPlan) *hostPlan {
	if v, ok := c.plans.Load(h.Id); ok {
		if p := v.(*hostPlan); p.host == h {
			return p
		}
	}
	gen := c.gen.Load()
	p := compile(h)
	c.mu.Lock()
	defer c.mu.Unlock()
	if v, ok := c.plans.Load(h.Id); ok {
		if cur := v.(*hostPlan); cur.host == h {
			p.CloseIdleConnections()
			return cur
		}
	}
	if c.gen.Load() != gen {
		// The host may have changed since compile read it: serve this request
		// with the plan, as if it had come before the change, but keep none.
		return p
	}
	if v, loaded := c.plans.Swap(h.Id, p); loaded {
		v.(*hostPlan).CloseIdleConnections()
	}
	return p
}

// Remove drops the plan of host id and closes its idle backend connections.
func (c *Plans) Remove(id int) {
	c.mu.Lock()
	c.gen.Add(1)
	v, ok := c.plans.LoadAndDelete(id)
	c.mu.Unlock()
	if ok {
		v.(*hostPlan).CloseIdleConnections()
	}
}

// drop removes p if it is still the plan of its host, after its backend
// failed.
func (c *Plans) drop(p *hostPlan) {
	c.mu.Lock()
	removed := c.plans.CompareAndDelete(p.host.Id, p)
	c.mu.Unlock()
	if removed {
		p.CloseIdleConnections()
	}
}

// Clear drops every plan.
func (c *Plans) Clear() {
	c.mu.Lock()
	c.gen.Add(1)
	var dropped []*hostPlan
	c.plans.Range(func(k, v any) bool {
		c.plans.Delete(k)
		dropped = append(dropped, v.(*hostPlan))
		return true
	})
	c.mu.Unlock()
	for _, p := range dropped {
		p.CloseIdleConnections()
	}
}
