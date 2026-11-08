package main

import (
	"context"
	goflag "flag"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	"github.com/djylb/nps/bridge"
	"github.com/djylb/nps/bridge/legacy"
	"github.com/djylb/nps/internal/cliargs"
	"github.com/djylb/nps/internal/logsetup"
	"github.com/djylb/nps/lib/acl"
	"github.com/djylb/nps/lib/clock"
	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/install"
	"github.com/djylb/nps/lib/logevent"
	"github.com/djylb/nps/lib/netaddr"
	"github.com/djylb/nps/lib/runenv"
	"github.com/djylb/nps/lib/servercfg"
	"github.com/djylb/nps/lib/version"
	"github.com/djylb/nps/server"
	"github.com/djylb/nps/server/connection"
	"github.com/djylb/nps/server/ports"
	"github.com/djylb/nps/server/stats"
	"github.com/djylb/nps/web/routers"
	"github.com/djylb/slogx"
	"github.com/gin-gonic/gin"
	"github.com/kardianos/service"
	flag "github.com/spf13/pflag"
)

var (
	log    = slogx.Module("server")
	cfgLog = slogx.Module("config")
)

var (
	confPath = flag.StringP("conf_path", "c", "", "Set Conf Path")
	ver      = flag.BoolP("version", "v", false, "Show Current Version")
	genTOTP  = flag.Bool("gen2fa", false, "Generate TOTP Secret")
	getTOTP  = flag.String("get2fa", "", "Get TOTP Code")
	noUpdate = flag.Bool("disable_remote_update", false, "Disable updating the server from the web interface (true or false)")
)

func main() {
	prg := &nps{exit: make(chan struct{}), stopped: make(chan struct{})}
	defer watchReload(prg.reloadConfig)()
	flag.CommandLine.SetNormalizeFunc(func(f *flag.FlagSet, name string) flag.NormalizedName {
		name = strings.ReplaceAll(name, "-", "_")
		name = strings.ReplaceAll(name, ".", "_")
		return flag.NormalizedName(name)
	})
	flag.CommandLine.SortFlags = false
	flag.CommandLine.SetInterspersed(true)
	flag.CommandLine.AddGoFlagSet(goflag.CommandLine)
	prepared, warnings := cliargs.Prepare(flag.CommandLine, os.Args[1:], cliargs.NPS, "service", "install", "uninstall", "start", "stop", "restart", "update", "update-force", "update-geo", "version")
	os.Args = append(os.Args[:1], prepared...)
	argWarnings = warnings
	flag.Parse()

	args := flag.Args()
	var cmd string
	if len(args) > 0 {
		cmd = args[0]
	}

	// gen TOTP
	if *genTOTP {
		crypt.PrintTOTPSecret()
		return
	}
	// get TOTP
	if *getTOTP != "" {
		crypt.PrintTOTPCode(*getTOTP)
		return
	}
	// show version
	if *ver || cmd == "version" {
		version.PrintVersion(legacy.CoreVersion(legacy.LatestIndex))
		return
	}

	if !knownCommand(cmd) {
		printUsage(cmd)
		os.Exit(2)
	}

	// set config path
	if cp := strings.TrimSpace(*confPath); cp != "" {
		if servercfg.IsSupportedConfigPath(cp) {
			servercfg.SetPreferredPath(cp)
			runenv.ConfPath = filepath.Dir(cp)
		} else {
			runenv.ConfPath = cp
		}
	}

	install.RecoverUpdate()
	svcConfig := newServiceConfig()

	// update needs no config, so a broken config file can still be replaced
	if cmd == "update" || cmd == "update-force" {
		_ = logsetup.Apply(logsetup.Options{})
		var s service.Service
		if svc, err := service.New(prg, svcConfig); err == nil {
			s = svc
		} else {
			log.Warn("service manager unavailable", "err", err)
		}
		res, err := install.Update(context.Background(), "nps", svcConfig.Name, install.UpdateOptions{Force: cmd == "update-force"})
		// Update reports its own failure as update_failed.
		if err == nil && res != nil {
			if err = install.RestartAfterUpdate(s, svcConfig.Name, res); err != nil {
				log.Error("restart after update failed", "err", err)
			}
		}
		if res != nil {
			install.WarnOldProcesses(res.Running)
		}
		if err != nil {
			os.Exit(1)
		}
		return
	}

	if err := servercfg.LoadDefault(); err != nil {
		fmt.Fprintln(os.Stderr, "load config file error:", err)
		os.Exit(1)
	}
	cfg := servercfg.Current()
	geoIPPath, geoSitePath := runenv.GetPath(cfg.App.GeoIPPath), runenv.GetPath(cfg.App.GeoSitePath)
	// before any access list is compiled
	acl.SetGeoData(geoIPPath, geoSitePath)
	acl.SetMaxRules(cfg.App.AclMaxRules)
	server.ApplyLimits(cfg)
	if cmd == "update-geo" {
		_ = logsetup.Apply(logsetup.Options{})
		var name string
		if len(args) > 1 {
			name = args[1]
		}
		files, err := install.PickGeoFiles(install.GeoFiles(geoIPPath, geoSitePath), name)
		if err != nil {
			log.Error("update geodata failed", "err", err)
			os.Exit(1)
		}
		// UpdateGeoData reports each failed file as source_failed.
		res, err := install.UpdateGeoData(files)
		if err != nil {
			os.Exit(1)
		}
		if len(res.Updated) == 0 {
			fmt.Println("Geodata is up to date")
		} else {
			fmt.Println("Geodata updated; a running nps applies it within a minute")
		}
		return
	}
	if strings.TrimSpace(os.Getenv("GIN_MODE")) == "" {
		gin.SetMode(gin.ReleaseMode)
	}

	pprofIp := cfg.App.PprofIP
	pprofPort := cfg.App.PprofPort
	if pprofPort != "0" {
		pprofAddr := net.JoinHostPort(pprofIp, pprofPort)
		runenv.InitPProfByAddr(pprofAddr)
	}

	err := clock.SetTimezone(cfg.App.Timezone)
	if err != nil {
		cfgLog.Warn("set timezone failed", "err", err)
	}
	netaddr.SetCustomDNS(cfg.App.DNSServer)
	logPath := cfg.Log.Path
	if logPath == "" || strings.EqualFold(logPath, "on") || strings.EqualFold(logPath, "true") {
		logPath = runenv.GetLogPath()
	}
	logOpts := logsetup.Options{
		Output: cfg.Log.Type, Format: cfg.Log.Format, Level: cfg.Log.Level,
		Path: logPath, Security: cfg.Log.Security, Dir: runenv.GetRunPath(),
		MaxSize: cfg.Log.MaxSize, MaxFiles: cfg.Log.MaxFiles, MaxDays: cfg.Log.MaxDays, Compress: cfg.Log.Compress, Memory: cfg.Log.Memory, Secrets: cfg.Log.Secrets, Instance: cfg.Log.Instance,
		Service: len(os.Args) > 1 && os.Args[1] == "service",
	}
	if err := logsetup.Apply(logOpts); err != nil {
		fmt.Fprintln(os.Stderr, "log:", err)
		os.Exit(1)
	}
	watchReopen()
	warnArgs(strings.EqualFold(cfg.Log.Type, "off"))
	for _, w := range cfg.Legacy {
		cfgLog.Warn("legacy config setting", "detail", w)
	}
	s, err := service.New(prg, svcConfig)
	if err != nil {
		log.Warn("service manager unavailable, running without it", "err", err)
		srv := prg.mustRun()
		<-srv.Done()
		if err := closeServer(srv); err != nil {
			log.Error("nps stopped", "err", err)
		}
		slogx.Default.Close()
		os.Exit(1)
	}

	if cmd != "" && cmd != "service" {
		switch cmd {
		case "install":
			// uninstall before
			_ = service.Control(s, "stop")
			_ = install.UninstallService(s, svcConfig.Name)

			binPath, err := install.NPS()
			if err != nil {
				log.Error("install failed", "err", err)
				os.Exit(1)
			}
			svcConfig.Executable = binPath
			s, err := service.New(prg, svcConfig)
			if err != nil {
				log.Error("create service failed", "err", err)
				return
			}
			if err := service.Control(s, "install"); err != nil {
				log.Error("install service failed", "err", err)
				os.Exit(1)
			}
			install.LinkServiceStart(svcConfig.Name)
			return
		case "start", "restart", "stop":
			if err := install.ControlService(s, svcConfig.Name, cmd); err != nil {
				log.Error("service control failed", "action", cmd, "valid_actions", service.ControlAction, "err", err)
			}
			return
		case "uninstall":
			if err := install.UninstallService(s, svcConfig.Name); err != nil {
				log.Error("service control failed", "action", cmd, "err", err)
			}
			return
		}
	}
	_ = s.Run()
}

// knownCommand reports whether cmd is a command of nps. No command and
// "service", which the service managers run, both start the server.
func knownCommand(cmd string) bool {
	switch cmd {
	case "", "service", "install", "uninstall", "start", "stop", "restart", "update", "update-force", "update-geo", "version":
		return true
	}
	return false
}

func printUsage(cmd string) {
	fmt.Fprintf(os.Stderr, "unknown command %q\n\nUsage: %s [flags] [command]\n\nWithout a command nps runs in the foreground.\n\nCommands:\n", cmd, filepath.Base(os.Args[0]))
	fmt.Fprint(os.Stderr, "  install       install the system service\n  uninstall     remove the system service\n  start         start the system service\n  stop          stop the system service\n  restart       restart the system service\n  update        update nps to the latest release\n  update-force  update nps even if it is up to date\n  update-geo    update geoip and the geodata files present, or the one named geoip or geosite\n  version       show the version, as -version\n  service       run as a service, used by the service manager\n\nFlags:\n")
	flag.PrintDefaults()
}

// reloadConfig reloads the config file into the running server.
func (p *nps) reloadConfig() {
	srv := p.srv.Load()
	if srv == nil {
		cfgLog.Info("config reload ignored", "reason", "not_running")
		return
	}
	srv.ReloadConfig()
}

func newServiceConfig() *service.Config {
	svcConfig := &service.Config{
		Name:        server.ServiceName,
		DisplayName: "nps内网穿透代理服务器",
		Description: "一款轻量级、功能强大的内网穿透代理服务器。支持tcp、udp流量转发，支持内网http代理、内网socks5代理，同时支持snappy压缩、站点保护、加密传输、多路复用、header修改等。支持web图形化管理，集成多用户模式。",
		Option:      make(service.KeyValue),
	}
	for _, v := range os.Args[1:] {
		switch v {
		case "install", "start", "stop", "uninstall", "restart":
			continue
		}
		svcConfig.Arguments = append(svcConfig.Arguments, v)
	}
	svcConfig.Arguments = append(svcConfig.Arguments, "service")
	install.ConfigureService(svcConfig)
	return svcConfig
}

// argWarnings are the warnings about command-line arguments of earlier
// versions or unknown ones (cliargs.Prepare); they are logged once logging
// is set up.
var argWarnings []string

// warnArgs logs argWarnings, or writes them to standard error when the log
// is off, as an OpenWrt init script sets, so that they still reach the
// system log.
func warnArgs(logOff bool) {
	for _, w := range argWarnings {
		if logOff {
			fmt.Fprintln(os.Stderr, "WARN", w)
			continue
		}
		cfgLog.Warn("command-line argument", "detail", w)
	}
}

type nps struct {
	exit    chan struct{} // closed when the service manager stops nps
	stopped chan struct{} // closed once the server has stopped after exit
	srv     atomic.Pointer[server.Server]
}

// stopTimeout bounds how long stopping waits for the server to close.
const stopTimeout = 10 * time.Second

func (p *nps) Start(s service.Service) error {
	_, _ = s.Status()
	go func() { _ = p.run() }()
	return nil
}
func (p *nps) Stop(s service.Service) error {
	_, _ = s.Status()
	close(p.exit)
	select {
	case <-p.stopped:
	case <-time.After(stopTimeout):
		log.Warn("nps did not stop in time", "dur_ms", stopTimeout.Milliseconds())
	}
	if service.Interactive() {
		os.Exit(0)
	}
	return nil
}

func (p *nps) run() error {
	defer func() {
		if err := recover(); err != nil {
			const size = 64 << 10
			buf := make([]byte, size)
			buf = buf[:runtime.Stack(buf, false)]
			log.Error("panic", "err", err, "detail", string(buf))
		}
	}()
	srv := p.mustRun()
	select {
	case <-p.exit:
	case <-srv.Done():
		if err := closeServer(srv); err != nil {
			log.Error("nps stopped", "err", err)
		}
		slogx.Default.Flush()
		os.Exit(1)
	}
	slogx.Emit(context.Background(), log, &logevent.Stopping, "version", version.VERSION, "role", "nps")
	_ = closeServer(srv)
	slogx.Default.Flush()
	close(p.stopped)
	return nil
}

// mustRun starts nps and ends the process with a non-zero code when that
// fails: nothing is served then.
func (p *nps) mustRun() *server.Server {
	srv, err := run()
	if err != nil {
		log.Error("nps failed to start", "err", err)
		os.Exit(1)
	}
	p.srv.Store(srv)
	return srv
}

// run starts the server as the configuration says.
func run() (*server.Server, error) {
	install.StartUpdate()
	cfg := servercfg.Current()
	db, err := file.Open(runenv.GetRunPath())
	if err != nil {
		return nil, err
	}
	cert, err := crypt.LoadKeyPair(cfg.Bridge.CertFile, cfg.Bridge.KeyFile)
	if err != nil {
		log.Warn("bridge certificate not loaded, using a generated one", "file", cfg.Bridge.CertFile, "err", err)
	}
	identity, err := crypt.NewIdentity(cert)
	if err != nil {
		return nil, err
	}
	selectMode, err := bridge.ParseSelectMode(cfg.Bridge.SelectMode)
	if err != nil {
		cfgLog.Warn("invalid client select mode, using default", "value", cfg.Bridge.SelectMode, "mode", selectMode.String())
	} else {
		log.Info("client select mode", "mode", selectMode.String())
	}
	accessLog, closeLog, err := openAccessLog(cfg.Log)
	if err != nil {
		cfgLog.Error("access log not opened", "file", cfg.Log.Access, "err", err)
	}
	closeAccessLog = closeLog
	srv := server.New(db, server.Config{
		AccessLog:           accessLog,
		DisconnectTimeout:   cfg.Runtime.DisconnectTimeout,
		SecureMode:          cfg.Security.SecureMode,
		SelectMode:          selectMode,
		DisableRemoteUpdate: *noUpdate,
		AllowedPorts:        ports.ParseAllowed(cfg.Feature.AllowPorts),
		Identity:            identity,
		Transports: bridge.Transports{
			TCP:  cfg.Bridge.ServerTCPEnabled,
			TLS:  cfg.Bridge.ServerTLSEnabled,
			WS:   cfg.Bridge.ServerWSEnabled,
			WSS:  cfg.Bridge.ServerWSSEnabled,
			KCP:  cfg.Bridge.ServerKCPEnabled,
			QUIC: cfg.Bridge.ServerQUICEnabled,
		},
	})
	handler, err := routers.Init(db, identity, srv)
	if err != nil {
		return nil, err
	}
	srv.SetWebHandler(handler)
	srv.AddConfigApplier(routers.ConfigApplier(db, identity, srv, srv.SetWebHandler))
	srv.SetIPRegisterHandler(routers.InitIPRegister(db, identity, srv))
	if srv.CanUpdateSelf() {
		// query the latest release now, so that the dashboard knows it
		install.LatestVersion()
	}
	slogx.Emit(context.Background(), log, &logevent.Started, "version", version.VERSION, "role", "nps", "pid", os.Getpid(), "file", servercfg.Path(), "min_client_version", srv.Bridge().MinClientVersion())
	server.ApplyNTP(cfg)
	if err := connection.InitConnectionService(); err != nil {
		return nil, err
	}
	legacy.SetPingInterval(time.Duration(cfg.Network.MuxPingInterval) * time.Second)
	stats.StartSystemInfo()
	file.ReportGeoData()
	go file.WatchAccessLists(context.Background(), time.Minute)
	if err := srv.Start(context.Background()); err != nil {
		_ = closeServer(srv)
		return nil, err
	}
	return srv, nil
}

// closeAccessLog closes the access_log file of the running server.
var closeAccessLog = func() {}

// closeServer closes srv, then the access log it writes to.
func closeServer(srv *server.Server) error {
	err := srv.Close()
	closeAccessLog()
	return err
}
