package main

import (
	"context"
	goflag "flag"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	"github.com/djylb/nps/bridge"
	"github.com/djylb/nps/bridge/legacy"
	"github.com/djylb/nps/lib/acl"
	"github.com/djylb/nps/lib/clock"
	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/install"
	"github.com/djylb/nps/lib/logs"
	"github.com/djylb/nps/lib/model"
	"github.com/djylb/nps/lib/netaddr"
	"github.com/djylb/nps/lib/runenv"
	"github.com/djylb/nps/lib/servercfg"
	"github.com/djylb/nps/lib/version"
	"github.com/djylb/nps/server"
	"github.com/djylb/nps/server/connection"
	"github.com/djylb/nps/server/ports"
	"github.com/djylb/nps/server/stats"
	"github.com/djylb/nps/web/routers"
	"github.com/gin-gonic/gin"
	"github.com/kardianos/service"
	flag "github.com/spf13/pflag"
)

var (
	logLevel string
	confPath = flag.StringP("conf_path", "c", "", "Set Conf Path")
	ver      = flag.BoolP("version", "v", false, "Show Current Version")
	genTOTP  = flag.Bool("gen2fa", false, "Generate TOTP Secret")
	getTOTP  = flag.String("get2fa", "", "Get TOTP Code")
	noUpdate = flag.Bool("disable_remote_update", false, "Disable updating the server from the web interface (true or false)")
)

func main() {
	prg := &nps{exit: make(chan struct{}), stopped: make(chan struct{})}
	watchReload(prg.reloadConfig)
	flag.CommandLine.SetNormalizeFunc(func(f *flag.FlagSet, name string) flag.NormalizedName {
		name = strings.ReplaceAll(name, "-", "_")
		name = strings.ReplaceAll(name, ".", "_")
		return flag.NormalizedName(name)
	})
	normalizeLegacyLongFlags()
	flag.CommandLine.SortFlags = false
	flag.CommandLine.SetInterspersed(true)
	flag.CommandLine.AddGoFlagSet(goflag.CommandLine)
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
	if *ver {
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
		logs.Init("stdout", "info", "", 0, 0, 0, false, true)
		var s service.Service
		if svc, err := service.New(prg, svcConfig); err == nil {
			s = svc
		} else {
			logs.Warn("service function disabled %v", err)
		}
		res, err := install.Update(context.Background(), "nps", svcConfig.Name, install.UpdateOptions{Force: cmd == "update-force"})
		if err == nil && res != nil {
			err = install.RestartAfterUpdate(s, svcConfig.Name, res.Replaced)
		}
		if res != nil {
			install.WarnOldProcesses(res.Running)
		}
		if err != nil {
			logs.Error("update failed: %v", err)
			os.Exit(1)
		}
		return
	}

	if err := servercfg.LoadDefault(); err != nil {
		log.Fatalln("load config file error", err.Error())
	}
	cfg := servercfg.Current()
	geoIPPath, geoSitePath := runenv.GetPath(cfg.App.GeoIPPath), runenv.GetPath(cfg.App.GeoSitePath)
	// before any access list is compiled
	acl.SetGeoData(geoIPPath, geoSitePath)
	acl.SetMaxRules(cfg.App.AclMaxRules)
	applyRuntime(cfg)
	if cmd == "update-geo" {
		logs.Init("stdout", "info", "", 0, 0, 0, false, true)
		var name string
		if len(args) > 1 {
			name = args[1]
		}
		files, err := install.PickGeoFiles(install.GeoFiles(geoIPPath, geoSitePath), name)
		if err != nil {
			logs.Error("update geodata failed: %v", err)
			os.Exit(1)
		}
		res, err := install.UpdateGeoData(files)
		if err != nil {
			logs.Error("update geodata failed: %v", err)
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
		logs.Warn("Set timezone error %v", err)
	}
	netaddr.SetCustomDNS(cfg.App.DNSServer)
	logType := cfg.Log.Type
	logLevel = cfg.Log.Level
	logPath := cfg.Log.Path
	if logPath == "" || strings.EqualFold(logPath, "on") || strings.EqualFold(logPath, "true") {
		logPath = runenv.GetLogPath()
	}
	if !strings.EqualFold(logPath, "off") && !strings.EqualFold(logPath, "false") && !strings.EqualFold(logPath, "docker") && logPath != "/dev/null" {
		if !filepath.IsAbs(logPath) {
			logPath = filepath.Join(runenv.GetRunPath(), logPath)
		}
		if runenv.IsWindows() {
			logPath = strings.ReplaceAll(logPath, "\\", "\\\\")
		}
	}
	logMaxFiles := cfg.Log.MaxFiles
	logMaxDays := cfg.Log.MaxDays
	logMaxSize := cfg.Log.MaxSize
	logCompress := cfg.Log.Compress
	logColor := cfg.Log.Color

	if len(os.Args) > 1 && os.Args[1] == "service" && !strings.EqualFold(logType, "off") && !strings.EqualFold(logType, "both") {
		logType = "file"
	}
	logs.Init(logType, logLevel, logPath, logMaxSize, logMaxFiles, logMaxDays, logCompress, logColor)
	s, err := service.New(prg, svcConfig)
	if err != nil {
		logs.Error("service function disabled %v", err)
		// run without service
		srv := prg.mustRun()
		<-srv.Done()
		logs.Error("nps stopped: %v", srv.Close())
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
				logs.Error("install failed: %v", err)
				os.Exit(1)
			}
			svcConfig.Executable = binPath
			s, err := service.New(prg, svcConfig)
			if err != nil {
				logs.Error("%v", err)
				return
			}
			if err := service.Control(s, "install"); err != nil {
				logs.Error("install service failed: %v", err)
				os.Exit(1)
			}
			if service.Platform() == "unix-systemv" {
				logs.Info("unix-systemv service")
				confPath := "/etc/init.d/" + svcConfig.Name
				_ = os.Symlink(confPath, "/etc/rc.d/S90"+svcConfig.Name)
				_ = os.Symlink(confPath, "/etc/rc.d/K02"+svcConfig.Name)
			}
			return
		case "start", "restart", "stop":
			if err := install.ControlService(s, svcConfig.Name, cmd); err != nil {
				logs.Error("Valid actions: %q error: %v", service.ControlAction, err)
			}
			return
		case "uninstall":
			if err := install.UninstallService(s, svcConfig.Name); err != nil {
				logs.Error("Valid actions: %q error: %v", service.ControlAction, err)
			}
			if service.Platform() == "unix-systemv" {
				logs.Info("unix-systemv service")
				_ = os.Remove("/etc/rc.d/S90" + svcConfig.Name)
				_ = os.Remove("/etc/rc.d/K02" + svcConfig.Name)
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
	case "", "service", "install", "uninstall", "start", "stop", "restart", "update", "update-force", "update-geo":
		return true
	}
	return false
}

func printUsage(cmd string) {
	fmt.Fprintf(os.Stderr, "unknown command %q\n\nUsage: %s [flags] [command]\n\nWithout a command nps runs in the foreground.\n\nCommands:\n", cmd, filepath.Base(os.Args[0]))
	fmt.Fprint(os.Stderr, "  install       install the system service\n  uninstall     remove the system service\n  start         start the system service\n  stop          stop the system service\n  restart       restart the system service\n  update        update nps to the latest release\n  update-force  update nps even if it is up to date\n  update-geo    update geoip and the geodata files present, or the one named geoip or geosite\n  service       run as a service, used by the service manager\n\nFlags:\n")
	flag.PrintDefaults()
}

func (p *nps) reloadConfig() {
	old := servercfg.Current()
	if err := servercfg.Reload(); err != nil {
		logs.Warn("Config reload failed: %v", err)
		return
	}
	cfg := servercfg.Current()
	logs.SetLevel(cfg.Log.Level)
	applyRuntime(cfg)
	applyNtp(cfg)
	stats.StartSystemInfo()
	if srv := p.srv.Load(); srv != nil {
		srv.ApplyWebhook(cfg.Webhook)
		handler, err := routers.Init(srv.Store(), srv)
		if err != nil {
			logs.Warn("Config reload: the web manager keeps its previous settings: %v", err)
		} else {
			srv.SetWebHandler(handler)
		}
	}
	if names := servercfg.RestartRequired(old, cfg); len(names) > 0 {
		logs.Warn("Config reloaded, but these changes need a restart: %s", strings.Join(names, ", "))
	}
}

// applyRuntime hands the settings that apply without a restart to the
// packages that use them.
func applyRuntime(cfg *servercfg.Snapshot) {
	model.SetFlowLimitActual(cfg.Feature.FlowLimitActual)
	model.SetClientHistoryLimit(cfg.Runtime.ClientHistoryLimit)
}

func applyNtp(cfg *servercfg.Snapshot) {
	clock.SetNtpServer(cfg.App.NTPServer)
	clock.SetNtpInterval(time.Duration(cfg.App.NTPInterval) * time.Minute)
	go clock.Sync()
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

func normalizeLegacyLongFlags() {
	norm := func(s string) string {
		s = strings.ReplaceAll(s, "-", "_")
		s = strings.ReplaceAll(s, ".", "_")
		return s
	}
	defined := map[string]struct{}{}
	flag.CommandLine.VisitAll(func(f *flag.Flag) {
		defined[norm(f.Name)] = struct{}{}
	})
	if len(os.Args) <= 1 {
		return
	}
	out := make([]string, 0, len(os.Args))
	out = append(out, os.Args[0])
	for _, a := range os.Args[1:] {
		if strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && len(a) > 2 {
			s := a[1:]
			name, val := s, ""
			if i := strings.IndexByte(s, '='); i >= 0 {
				name, val = s[:i], s[i:]
			}
			if _, ok := defined[norm(name)]; ok {
				a = "--" + name + val
			}
		}
		out = append(out, a)
	}
	os.Args = out
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
		logs.Warn("nps did not stop within %v", stopTimeout)
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
			logs.Warn("nps: panic serving %v: %s", err, buf)
		}
	}()
	srv := p.mustRun()
	select {
	case <-p.exit:
	case <-srv.Done():
		logs.Error("nps stopped: %v", srv.Close())
		os.Exit(1)
	}
	logs.Warn("stop...")
	_ = srv.Close()
	close(p.stopped)
	return nil
}

// mustRun starts nps and ends the process with a non-zero code when that
// fails: nothing is served then.
func (p *nps) mustRun() *server.Server {
	srv, err := run()
	if err != nil {
		logs.Error("nps failed to start: %v", err)
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
	selectMode, err := bridge.ParseSelectMode(cfg.Bridge.SelectMode)
	if err != nil {
		logs.Warn("Invalid client select mode %v; fallback to %s(%d)", cfg.Bridge.SelectMode, selectMode, selectMode)
	} else {
		logs.Info("Client select mode set to %s(%d)", selectMode, selectMode)
	}
	srv := server.New(db, server.Config{
		DisconnectTimeout:   cfg.Runtime.DisconnectTimeout,
		SecureMode:          cfg.Security.SecureMode,
		SelectMode:          selectMode,
		DisableRemoteUpdate: *noUpdate,
		Transports: bridge.Transports{
			TCP:  cfg.Bridge.ServerTCPEnabled,
			TLS:  cfg.Bridge.ServerTLSEnabled,
			WS:   cfg.Bridge.ServerWSEnabled,
			WSS:  cfg.Bridge.ServerWSSEnabled,
			KCP:  cfg.Bridge.ServerKCPEnabled,
			QUIC: cfg.Bridge.ServerQUICEnabled,
		},
	})
	handler, err := routers.Init(db, srv)
	if err != nil {
		return nil, err
	}
	srv.SetWebHandler(handler)
	srv.SetIPRegisterHandler(routers.InitIPRegister(db, srv))
	if srv.CanUpdateSelf() {
		// query the latest release now, so that the dashboard knows it
		install.LatestVersion()
	}
	logs.Info("the config path is: %s", servercfg.Path())
	logs.Info("the version of server is %s, allow client core version to be %s", version.VERSION, srv.Bridge().MinClientVersion())
	applyNtp(cfg)
	if err := connection.InitConnectionService(); err != nil {
		return nil, err
	}
	legacy.SetPingInterval(time.Duration(cfg.Network.MuxPingInterval) * time.Second)
	//crypt.InitTls(filepath.Join(runenv.GetRunPath(), "conf", "server.pem"), filepath.Join(runenv.GetRunPath(), "conf", "server.key"))
	cert, err := crypt.LoadKeyPair(cfg.Bridge.CertFile, cfg.Bridge.KeyFile)
	if err != nil {
		logs.Info("Using randomly generated certificate.")
	}
	crypt.InitTls(cert)
	ports.InitAllowPort()
	stats.StartSystemInfo()
	go file.WatchAccessLists(context.Background(), time.Minute)
	if err := srv.Start(context.Background()); err != nil {
		_ = srv.Close()
		return nil, err
	}
	return srv, nil
}
