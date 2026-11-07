package httpproxy

import (
	"context"
	"errors"
	"fmt"
	"html"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/caddyserver/certmagic"
	"github.com/djylb/netx/httprewrite"
	"github.com/djylb/nps/lib/logs"
	"github.com/djylb/nps/lib/model"
	"github.com/djylb/nps/lib/proxy"
	"github.com/djylb/nps/lib/runenv"
	"github.com/djylb/nps/lib/servercfg"
	"github.com/djylb/nps/lib/textutil"
	"github.com/djylb/nps/server/connection"
)

type HttpProxy struct {
	*proxy.BaseServer
	store                 Store
	HttpServer            *HttpServer
	HttpsServer           *HttpsServer
	Http3Server           *Http3Server
	HttpPort              int
	HttpsPort             int
	Http3Port             int
	plans                 *Plans
	HttpOnlyPass          string
	HttpPortStr           string
	HttpsPortStr          string
	Http3PortStr          string
	TimeLimitErrorContent []byte
	FlowLimitErrorContent []byte
	Magic                 *certmagic.Config
	certCache             *certmagic.Cache // of Magic; Close stops it
	Acme                  *certmagic.ACMEIssuer
	ResponseHeaderTimeout time.Duration
	AddOrigin             bool
	Http3Bridge           bool
	ErrorAlways           bool
	ForceAutoSsl          bool
}

func NewHttpProxy(store Store, bridge proxy.NetBridge, task *model.Tunnel, httpPort, httpsPort, http3Port int, httpOnlyPass string, addOrigin, allowLocalProxy bool, plans *Plans) *HttpProxy {
	httpProxy := &HttpProxy{
		BaseServer:            proxy.NewBaseServer(bridge, task, allowLocalProxy),
		store:                 store,
		HttpPort:              httpPort,
		HttpsPort:             httpsPort,
		Http3Port:             http3Port,
		plans:                 plans,
		HttpOnlyPass:          httpOnlyPass,
		AddOrigin:             addOrigin,
		HttpPortStr:           strconv.Itoa(httpPort),
		HttpsPortStr:          strconv.Itoa(httpsPort),
		Http3PortStr:          strconv.Itoa(http3Port),
		Http3Bridge:           false,
		ForceAutoSsl:          false,
		ResponseHeaderTimeout: 100,
	}
	return httpProxy
}

func (s *HttpProxy) Start() error {
	cfg := servercfg.Current()
	var err error
	s.ErrorContent, err = os.ReadFile(runenv.ResolvePath(cfg.Proxy.ErrorPage))
	if err != nil {
		s.ErrorContent = []byte("nps 404")
	}
	s.TimeLimitErrorContent = s.loadOptionalErrorPage(cfg.Proxy.ErrorPageTimeLimit)
	s.FlowLimitErrorContent = s.loadOptionalErrorPage(cfg.Proxy.ErrorPageFlowLimit)
	s.ErrorAlways = cfg.Proxy.ErrorAlways

	if s.Bridge.IsServer() {
		s.Http3Bridge = cfg.Proxy.BridgeHTTP3
	}

	s.ForceAutoSsl = cfg.Proxy.ForceAutoSSL

	ca := certmagic.LetsEncryptStagingCA
	switch strings.ToLower(cfg.Proxy.SSL.CA) {
	case "letsencrypt", "le", "prod", "production":
		ca = certmagic.LetsEncryptProductionCA
	case "zerossl", "zero", "zs":
		ca = certmagic.ZeroSSLProductionCA
	case "googletrust", "google", "goog":
		ca = certmagic.GoogleTrustProductionCA
	}
	onDemand := &certmagic.OnDemandConfig{
		DecisionFunc: func(ctx context.Context, name string) error {
			h, err := s.store.FindCertByHost(name)
			if err != nil {
				return fmt.Errorf("unknown host %q", name)
			}
			if !h.AutoSSL {
				return fmt.Errorf("AutoSSL disabled for %q", name)
			}
			return nil
		},
	}
	// The proxy has a certificate cache of its own, stopped by Close, rather
	// than the process-wide default of certmagic.
	var magic *certmagic.Config
	cache := certmagic.NewCache(certmagic.CacheOptions{
		GetConfigForCert: func(certmagic.Certificate) (*certmagic.Config, error) { return magic, nil },
		Logger:           acmeZap(),
	})
	magic = certmagic.New(cache, certmagic.Config{
		Storage:  &certmagic.FileStorage{Path: runenv.ResolvePath(cfg.Proxy.SSL.Path)},
		Logger:   acmeZap(),
		OnDemand: onDemand,
		OnEvent:  certEvent,
	})
	acme := certmagic.NewACMEIssuer(magic, certmagic.ACMEIssuer{
		CA:     ca,
		Email:  cfg.Proxy.SSL.Email,
		Agreed: true,
		Logger: acmeZap(),
	})
	magic.Issuers = []certmagic.Issuer{acme}
	if ca == certmagic.ZeroSSLProductionCA {
		magic.Issuers = []certmagic.Issuer{&certmagic.ZeroSSLIssuer{APIKey: cfg.Proxy.SSL.ZeroSSLAPI}}
	}
	s.Magic, s.Acme = magic, acme
	if err := s.BindLocked(func() error { s.certCache = cache; return nil }); err != nil {
		cache.Stop()
		return err
	}

	s.ResponseHeaderTimeout = time.Duration(cfg.Proxy.ResponseTimeout) * time.Second

	return s.serve()
}

// serve binds and runs the HTTP, HTTPS and HTTP/3 servers. It returns the
// first error of any of them, or nil once all were closed. The three are one
// service: after an error the others may still run, and the caller stops them
// with Close, as Server.AddTunnel does before it drops the task.
func (s *HttpProxy) serve() error {
	var running sync.WaitGroup
	errs := make(chan error, 3)
	run := func(name string, start func() error) {
		running.Go(func() {
			if err := stopError(name, start()); err != nil {
				errs <- err
			}
		})
	}

	err := s.BindLocked(func() error {
		if s.HttpPort > 0 {
			httpListener, err := connection.Listen(connection.KindHTTP)
			if err != nil {
				return fmt.Errorf("start HTTP listener: %w", err)
			}
			s.HttpServer = NewHttpServer(s, httpListener)
			logs.Info("HTTP server listening on port %d", s.HttpPort)
			run("HTTP", s.HttpServer.Start)
		}

		if s.HttpsPort > 0 {
			httpsListener, err := connection.Listen(connection.KindHTTPS)
			if err != nil {
				return fmt.Errorf("start HTTPS listener: %w", err)
			}
			if s.HttpServer == nil {
				s.HttpServer = NewHttpServer(s, nil)
			}
			s.HttpsServer = NewHttpsServer(s.HttpServer, httpsListener)
			logs.Info("HTTPS server listening on port %d", s.HttpsPort)
			run("HTTPS", s.HttpsServer.Start)

			if s.Http3Port > 0 {
				http3PacketConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP(connection.Network().HTTPProxyIP), Port: s.Http3Port})
				if err != nil {
					return fmt.Errorf("start HTTP/3 listener: %w", err)
				}
				logs.Info("HTTP/3 server listening on port %d", s.Http3Port)
				s.Http3Server = NewHttp3Server(s.HttpsServer, http3PacketConn)
				run("HTTP/3", s.Http3Server.Start)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	go func() {
		running.Wait()
		close(errs)
	}()
	return <-errs
}

// stopError is the error of a server that stopped with err, nil if closing it
// stopped it.
func stopError(name string, err error) error {
	if err == nil || errors.Is(err, http.ErrServerClosed) || errors.Is(err, net.ErrClosed) {
		return nil
	}
	return fmt.Errorf("%s server stopped: %w", name, err)
}

func (s *HttpProxy) loadOptionalErrorPage(path string) []byte {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}

	content, err := os.ReadFile(runenv.ResolvePath(path))
	if err != nil {
		logs.Warn("Failed to load error page from %s: %v", path, err)
		return nil
	}
	return content
}

func (s *HttpProxy) Close() error {
	var httpServer *HttpServer
	var httpsServer *HttpsServer
	var http3Server *Http3Server
	var certCache *certmagic.Cache
	s.MarkClosed(func() {
		httpServer, httpsServer, http3Server = s.HttpServer, s.HttpsServer, s.Http3Server
		certCache, s.certCache = s.certCache, nil
	})
	if httpServer != nil {
		_ = httpServer.Close()
	}
	if httpsServer != nil {
		_ = httpsServer.Close()
	}
	if http3Server != nil {
		_ = http3Server.Close()
	}
	if certCache != nil {
		certCache.Stop()
	}
	s.plans.Clear()
	return nil
}

// parseHeaderRules parses header rules, one "Name: value" per line with
// HTML entities in values decoded. A line that does not parse, such as one
// with an unknown variable, is left out.
func parseHeaderRules(text string) []httprewrite.Rule {
	var rules []httprewrite.Rule
	for _, line := range textutil.SplitLines(text) {
		rs, err := httprewrite.ParseRules(html.UnescapeString(line), ruleVars)
		if err != nil {
			logs.Debug("header rule %q: %v", line, err)
			continue
		}
		rules = append(rules, rs...)
	}
	return rules
}

// ruleVars are the variables of header rules and redirect URLs besides the
// built-in ones of httprewrite.
var ruleVars = httprewrite.Vars{
	// remote_addr keeps the port, as ip:port, unlike the built-in one.
	"remote_addr": func(dst []byte, c *httprewrite.Context) []byte {
		if c.Client.Addr.IsValid() {
			return c.Client.Addr.AppendTo(dst)
		}
		if c.Request == nil {
			return dst
		}
		return append(dst, c.Request.RemoteAddr...)
	},
	"ssl":           sslVar,
	"forwarded_ssl": sslVar,
	"scheme_host": func(dst []byte, c *httprewrite.Context) []byte {
		if c.Request == nil {
			return dst
		}
		dst = append(dst, requestScheme(c.Request)...)
		return append(append(dst, "://"...), c.Request.Host...)
	},
	"server_port": func(dst []byte, c *httprewrite.Context) []byte {
		cfg := servercfg.Current()
		if c.Request != nil && c.Request.TLS != nil {
			return append(dst, proxyPortString(cfg.Network.HTTPSProxyPort, "443")...)
		}
		return append(dst, proxyPortString(cfg.Network.HTTPProxyPort, "80")...)
	},
	"server_port_http": func(dst []byte, _ *httprewrite.Context) []byte {
		return append(dst, proxyPortString(servercfg.Current().Network.HTTPProxyPort, "80")...)
	},
	"server_port_https": func(dst []byte, _ *httprewrite.Context) []byte {
		return append(dst, proxyPortString(servercfg.Current().Network.HTTPSProxyPort, "443")...)
	},
	"server_port_http3": func(dst []byte, _ *httprewrite.Context) []byte {
		cfg := servercfg.Current()
		return append(dst, proxyPortString(cfg.Network.HTTP3ProxyPort, proxyPortString(cfg.Network.HTTPSProxyPort, "443"))...)
	},
	"request_host": requestVar(func(r *http.Request) string { return r.Host }),
	"request_path": requestVar(func(r *http.Request) string { return r.URL.Path }),
	"origin":       requestVar(func(r *http.Request) string { return r.Header.Get("Origin") }),
	"user_agent":   requestVar(func(r *http.Request) string { return r.Header.Get("User-Agent") }),
	"status":       responseVar(func(r *http.Response) string { return r.Status }),
	"status_code":  responseVar(func(r *http.Response) string { return strconv.Itoa(r.StatusCode) }),
	"content_length": responseVar(func(r *http.Response) string {
		return strconv.FormatInt(r.ContentLength, 10)
	}),
	"content_type": responseVar(func(r *http.Response) string { return r.Header.Get("Content-Type") }),
	"via":          responseVar(func(r *http.Response) string { return r.Header.Get("Via") }),
	"date": func(dst []byte, c *httprewrite.Context) []byte {
		return ruleTime(c).UTC().AppendFormat(dst, http.TimeFormat)
	},
	"timestamp": func(dst []byte, c *httprewrite.Context) []byte {
		return strconv.AppendInt(dst, ruleTime(c).Unix(), 10)
	},
	"timestamp_ms": func(dst []byte, c *httprewrite.Context) []byte {
		return strconv.AppendInt(dst, ruleTime(c).UnixMilli(), 10)
	},
}

func sslVar(dst []byte, c *httprewrite.Context) []byte {
	if c.Request != nil && c.Request.TLS != nil {
		return append(dst, "on"...)
	}
	return append(dst, "off"...)
}

func requestScheme(r *http.Request) string {
	if r.TLS != nil {
		return "https"
	}
	return "http"
}

func requestVar(get func(*http.Request) string) httprewrite.Func {
	return func(dst []byte, c *httprewrite.Context) []byte {
		if c.Request == nil {
			return dst
		}
		return append(dst, get(c.Request)...)
	}
}

func responseVar(get func(*http.Response) string) httprewrite.Func {
	return func(dst []byte, c *httprewrite.Context) []byte {
		if c.Response == nil {
			return dst
		}
		return append(dst, get(c.Response)...)
	}
}

func ruleTime(c *httprewrite.Context) time.Time {
	if c.Time.IsZero() {
		return time.Now()
	}
	return c.Time
}

func proxyPortString(port int, fallback string) string {
	if port == 0 {
		return fallback
	}
	return strconv.Itoa(port)
}
