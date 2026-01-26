package httpproxy

import (
	"context"
	"fmt"
	"html"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/beego/beego"
	"github.com/caddyserver/certmagic"
	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/index"
	"github.com/djylb/nps/lib/logs"
	"github.com/djylb/nps/server/connection"
	"github.com/djylb/nps/server/proxy"
)

type HttpProxy struct {
	*proxy.BaseServer
	HttpServer     *HttpServer
	HttpsServer    *HttpsServer
	Http3Server    *Http3Server
	HttpPort       int
	HttpsPort      int
	Http3Port      int
	HttpProxyCache *index.AnyIntIndex
	HttpOnlyPass   string
	AddOrigin      bool
	HttpPortStr    string
	HttpsPortStr   string
	Http3PortStr   string
	Http3Bridge    bool
	ErrorAlways    bool
	ForceAutoSsl   bool
	Magic          *certmagic.Config
	Acme           *certmagic.ACMEIssuer
}

func NewHttpProxy(bridge proxy.NetBridge, task *file.Tunnel, httpPort, httpsPort, http3Port int, httpOnlyPass string, addOrigin, allowLocalProxy bool, httpProxyCache *index.AnyIntIndex) *HttpProxy {
	httpProxy := &HttpProxy{
		BaseServer:     proxy.NewBaseServer(bridge, task, allowLocalProxy),
		HttpPort:       httpPort,
		HttpsPort:      httpsPort,
		Http3Port:      http3Port,
		HttpProxyCache: httpProxyCache,
		HttpOnlyPass:   httpOnlyPass,
		AddOrigin:      addOrigin,
		HttpPortStr:    strconv.Itoa(httpPort),
		HttpsPortStr:   strconv.Itoa(httpsPort),
		Http3PortStr:   strconv.Itoa(http3Port),
		Http3Bridge:    false,
		ForceAutoSsl:   false,
	}
	return httpProxy
}

func (s *HttpProxy) Start() error {
	var err error
	s.ErrorContent, err = common.ReadAllFromFile(common.ResolvePath(beego.AppConfig.DefaultString("error_page", "web/static/page/error.html")))
	if err != nil {
		s.ErrorContent = []byte("nps 404")
	}
	s.ErrorAlways = beego.AppConfig.DefaultBool("error_always", false)

	if s.Bridge.IsServer() {
		s.Http3Bridge = beego.AppConfig.DefaultBool("bridge_http3", true)
	}

	s.ForceAutoSsl = beego.AppConfig.DefaultBool("force_auto_ssl", false)

	certmagic.Default.Logger = logs.ZapLogger
	certmagic.DefaultACME.Agreed = true
	certmagic.DefaultACME.Email = beego.AppConfig.String("ssl_email")
	switch strings.ToLower(beego.AppConfig.DefaultString("ssl_ca", "LetsEncrypt")) {
	case "letsencrypt", "le", "prod", "production":
		certmagic.DefaultACME.CA = certmagic.LetsEncryptProductionCA
	case "zerossl", "zero", "zs":
		certmagic.DefaultACME.CA = certmagic.ZeroSSLProductionCA
	case "googletrust", "google", "goog":
		certmagic.DefaultACME.CA = certmagic.GoogleTrustProductionCA
	default:
		certmagic.DefaultACME.CA = certmagic.LetsEncryptStagingCA
	}
	certmagic.Default.Storage = &certmagic.FileStorage{
		Path: common.ResolvePath(beego.AppConfig.DefaultString("ssl_path", "ssl")),
	}
	s.Magic = certmagic.NewDefault()
	if certmagic.DefaultACME.CA == certmagic.ZeroSSLProductionCA {
		s.Magic.Issuers = []certmagic.Issuer{
			&certmagic.ZeroSSLIssuer{
				APIKey: beego.AppConfig.String("ssl_zerossl_api"),
			},
		}
	}
	s.Magic.OnDemand = &certmagic.OnDemandConfig{
		DecisionFunc: func(ctx context.Context, name string) error {
			h, err := file.GetDb().FindCertByHost(name)
			if err != nil {
				return fmt.Errorf("unknown host %q", name)
			}
			if !h.AutoSSL {
				return fmt.Errorf("AutoSSL disabled for %q", name)
			}
			return nil
		},
	}
	s.Acme = certmagic.NewACMEIssuer(s.Magic, certmagic.DefaultACME)
	// Start Server
	if s.HttpPort > 0 {
		httpListener, err := connection.GetHttpListener()
		if err != nil {
			logs.Error("Failed to start HTTP listener: %v", err)
			os.Exit(0)
		}
		s.HttpServer = NewHttpServer(s, httpListener)
		logs.Info("HTTP server listening on port %d", s.HttpPort)
		go func() {
			if err := s.HttpServer.Start(); err != nil {
				logs.Error("HTTP server stopped: %v", err)
				os.Exit(0)
			}
		}()
	}

	if s.HttpsPort > 0 {
		httpsListener, err := connection.GetHttpsListener()
		if err != nil {
			logs.Error("Failed to start HTTPS listener: %v", err)
			os.Exit(0)
		}
		if s.HttpServer == nil {
			s.HttpServer = NewHttpServer(s, nil)
		}
		s.HttpsServer = NewHttpsServer(s.HttpServer, httpsListener)
		logs.Info("HTTPS server listening on port %d", s.HttpsPort)
		go func() {
			if err := s.HttpsServer.Start(); err != nil {
				logs.Error("HTTPS server stopped: %v", err)
				os.Exit(0)
			}
		}()

		if s.Http3Port > 0 {
			http3PacketConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP(connection.HttpIp), Port: s.Http3Port})
			if err != nil {
				logs.Error("Failed to start HTTP/3 listener: %v", err)
				os.Exit(0)
			}
			logs.Info("HTTP/3 server listening on port %d", s.Http3Port)
			s.Http3Server = NewHttp3Server(s.HttpsServer, http3PacketConn)
			go func() {
				if err := s.Http3Server.Start(); err != nil {
					logs.Error("HTTP/3 server stopped: %v", err)
					os.Exit(0)
				}
			}()
		}
	}
	return nil
}

func (s *HttpProxy) Close() error {
	if s.HttpServer != nil {
		_ = s.HttpServer.Close()
	}
	if s.HttpsServer != nil {
		_ = s.HttpsServer.Close()
	}
	if s.Http3Server != nil {
		_ = s.Http3Server.Close()
	}
	s.HttpProxyCache.Clear()
	return nil
}

// ChangeHostAndHeader Change headers and host of request
func (s *HttpProxy) ChangeHostAndHeader(r *http.Request, host string, header string, httpOnly bool) {
	// 设置 Host 头部信息
	scheme := "http"
	ssl := "off"
	serverPort := beego.AppConfig.DefaultString("http_proxy_port", "80")
	if r.TLS != nil {
		scheme = "https"
		ssl = "on"
		serverPort = beego.AppConfig.DefaultString("https_proxy_port", "443")
	}
	// Host 不带端口
	origHost := r.Host
	hostOnly := common.RemovePortFromHost(origHost)

	// 替换 Host
	if host != "" {
		r.Host = host
		if orig := r.Header.Get("Origin"); orig != "" {
			r.Header.Set("Origin", scheme+"://"+host)
		}
	}

	// 获取请求的客户端 IP Port
	remoteAddr := r.RemoteAddr
	clientIP := common.GetIpByAddr(remoteAddr)
	clientPort := common.GetPortStrByAddr(remoteAddr)

	//logs.Debug("get X-Remote-IP = " + clientIP)

	// 获取 X-Forwarded-For 头部的先前值
	proxyAddXFF := clientIP
	if prior, ok := r.Header["X-Forwarded-For"]; ok {
		proxyAddXFF = strings.Join(prior, ", ") + ", " + clientIP
	}

	//logs.Debug("get X-Forwarded-For = " + proxyAddXFF)

	// 判断是否需要添加真实 IP 信息
	var addOrigin bool
	if !httpOnly {
		addOrigin, _ = beego.AppConfig.Bool("http_add_origin_header")
		//r.Header.Set("X-Forwarded-For", proxyAddXFF)
	} else {
		addOrigin = false
	}

	// 添加头部信息
	if addOrigin {
		if r.Header.Get("X-Forwarded-Proto") == "" {
			r.Header.Set("X-Forwarded-Proto", scheme)
		}
		if r.Header.Get("X-Forwarded-Host") == "" && host == "" {
			r.Header.Set("X-Forwarded-Host", origHost)
		}
		//r.Header.Set("X-Forwarded-For", clientIP)
		r.Header.Set("X-Real-IP", clientIP)
	}

	if header == "" {
		return
	}

	expandVars := func(val string) string {
		rep := strings.NewReplacer(
			// 协议/SSL
			"${scheme}", scheme,
			"${ssl}", ssl,
			"${forwarded_ssl}", ssl,

			// 主机
			"${host}", hostOnly,
			"${http_host}", origHost,

			// 客户端
			"${remote_addr}", remoteAddr,
			"${remote_ip}", clientIP,
			"${remote_port}", clientPort,
			"${proxy_add_x_forwarded_for}", proxyAddXFF,

			// URL 相关
			"${request_uri}", r.RequestURI, // 包括 ?args
			"${uri}", r.URL.Path, // 不含 args
			"${args}", r.URL.RawQuery, // 不含 “?”
			"${query_string}", r.URL.RawQuery, // 同 $args
			"${scheme_host}", scheme+"://"+origHost, // 组合变量

			// 连接头
			"${http_upgrade}", r.Header.Get("Upgrade"),
			"${http_connection}", r.Header.Get("Connection"),

			// 端口
			"${server_port}", serverPort,

			// Range 相关
			"${http_range}", r.Header.Get("Range"),
			"${http_if_range}", r.Header.Get("If-Range"),
		)
		return rep.Replace(val)
	}

	// 设置自定义头部信息
	h := strings.Split(strings.ReplaceAll(header, "\r\n", "\n"), "\n")
	for _, v := range h {
		hd := strings.SplitN(v, ":", 2)
		if len(hd) == 2 {
			key := strings.TrimSpace(hd[0])
			if key == "" {
				continue
			}
			val := strings.TrimSpace(hd[1])
			val = html.UnescapeString(val)
			if val == "${unset}" {
				if !strings.EqualFold(key, "Host") {
					r.Header.Del(key)
				}
				continue
			}
			val = expandVars(val)
			r.Header.Set(key, val)
		}
	}
}

// ChangeResponseHeader Change headers of response
func (s *HttpProxy) ChangeResponseHeader(resp *http.Response, header string) {
	if header == "" {
		return
	}

	if resp == nil || resp.Request == nil {
		return
	}

	httpPort := beego.AppConfig.DefaultString("http_proxy_port", "80")
	httpsPort := beego.AppConfig.DefaultString("https_proxy_port", "443")
	http3Port := beego.AppConfig.DefaultString("http3_proxy_port", httpsPort)

	scheme := "http"
	ssl := "off"
	serverPort := httpPort
	if resp.Request.TLS != nil {
		scheme = "https"
		ssl = "on"
		serverPort = httpsPort
	}

	origHost := resp.Request.Host
	hostOnly := common.RemovePortFromHost(origHost)

	remoteAddr := resp.Request.RemoteAddr
	clientIP := common.GetIpByAddr(remoteAddr)
	clientPort := common.GetPortStrByAddr(remoteAddr)

	timeNow := time.Now()

	expandVars := func(val string) string {
		rep := strings.NewReplacer(
			// Protocol/SSL
			"${scheme}", scheme,
			"${ssl}", ssl,

			// Ports
			"${server_port}", serverPort,
			"${server_port_http}", httpPort,
			"${server_port_https}", httpsPort,
			"${server_port_http3}", http3Port,

			// Host info
			"${host}", hostOnly,
			"${http_host}", origHost,

			// Client info
			"${remote_addr}", remoteAddr,
			"${remote_ip}", clientIP,
			"${remote_port}", clientPort,

			// Request info
			"${request_method}", resp.Request.Method,
			"${request_host}", resp.Request.Host,
			"${request_uri}", resp.Request.RequestURI,
			"${request_path}", resp.Request.URL.Path,
			"${uri}", resp.Request.URL.Path,
			"${query_string}", resp.Request.URL.RawQuery,
			"${args}", resp.Request.URL.RawQuery,
			"${origin}", resp.Request.Header.Get("Origin"),
			"${user_agent}", resp.Request.Header.Get("User-Agent"),
			"${http_referer}", resp.Request.Header.Get("Referer"),
			"${scheme_host}", scheme+"://"+origHost,

			// Response info
			"${status}", resp.Status,
			"${status_code}", strconv.Itoa(resp.StatusCode),
			"${content_length}", strconv.FormatInt(resp.ContentLength, 10),
			"${content_type}", resp.Header.Get("Content-Type"),
			"${via}", resp.Header.Get("Via"),

			// Time variables
			"${date}", timeNow.UTC().Format(http.TimeFormat),
			"${timestamp}", strconv.FormatInt(timeNow.UTC().Unix(), 10),
			"${timestamp_ms}", strconv.FormatInt(timeNow.UTC().UnixNano()/1e6, 10),
		)
		return rep.Replace(val)
	}

	// 设置自定义头部信息
	h := strings.Split(strings.ReplaceAll(header, "\r\n", "\n"), "\n")
	for _, v := range h {
		hd := strings.SplitN(v, ":", 2)
		if len(hd) == 2 {
			key := strings.TrimSpace(hd[0])
			if key == "" {
				continue
			}
			val := strings.TrimSpace(hd[1])
			val = html.UnescapeString(val)
			if val == "${unset}" {
				resp.Header.Del(key)
				continue
			}
			val = expandVars(val)
			resp.Header.Set(key, val)
		}
	}
}

// ChangeRedirectURL Change redirect URL
func (s *HttpProxy) ChangeRedirectURL(r *http.Request, url string) string {
	val := strings.TrimSpace(url)
	val = html.UnescapeString(val)

	if !strings.Contains(val, "${") {
		return val
	}

	// 设置 Host 头部信息
	scheme := "http"
	ssl := "off"
	serverPort := beego.AppConfig.DefaultString("http_proxy_port", "80")
	if r.TLS != nil {
		scheme = "https"
		ssl = "on"
		serverPort = beego.AppConfig.DefaultString("https_proxy_port", "443")
	}

	// Host 不带端口
	origHost := r.Host
	hostOnly := common.RemovePortFromHost(origHost)

	// 获取请求的客户端 IP Port
	remoteAddr := r.RemoteAddr
	clientIP := common.GetIpByAddr(remoteAddr)
	clientPort := common.GetPortStrByAddr(remoteAddr)

	// 获取 X-Forwarded-For 头部的先前值
	proxyAddXFF := clientIP
	if prior, ok := r.Header["X-Forwarded-For"]; ok {
		proxyAddXFF = strings.Join(prior, ", ") + ", " + clientIP
	}

	rep := strings.NewReplacer(
		// 协议/SSL
		"${scheme}", scheme,
		"${ssl}", ssl,
		"${forwarded_ssl}", ssl,

		// 主机
		"${host}", hostOnly,
		"${http_host}", origHost,

		// 客户端
		"${remote_addr}", remoteAddr,
		"${remote_ip}", clientIP,
		"${remote_port}", clientPort,
		"${proxy_add_x_forwarded_for}", proxyAddXFF,

		// URL 相关
		"${request_uri}", r.RequestURI, // 包括 ?args
		"${uri}", r.URL.Path, // 不含 args
		"${args}", r.URL.RawQuery, // 不含 “?”
		"${query_string}", r.URL.RawQuery, // 同 $args
		"${scheme_host}", scheme+"://"+origHost, // 组合变量

		// 端口
		"${server_port}", serverPort,
	)

	return rep.Replace(val)
}
