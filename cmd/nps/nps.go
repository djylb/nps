package main

import (
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/beego/beego"
	"github.com/djylb/nps/bridge"
	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/daemon"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/install"
	"github.com/djylb/nps/lib/logs"
	"github.com/djylb/nps/lib/version"
	"github.com/djylb/nps/server"
	"github.com/djylb/nps/server/connection"
	"github.com/djylb/nps/server/tool"
	"github.com/djylb/nps/web/routers"
	"github.com/kardianos/service"

	goflag "flag"
	flag "github.com/spf13/pflag"
)

var (
	logLevel string
	confPath = flag.StringP("conf_path", "c", "", "Set Conf Path")
	ver      = flag.BoolP("version", "v", false, "Show Current Version")
	genTOTP  = flag.Bool("gen2fa", false, "Generate TOTP Secret")
	getTOTP  = flag.String("get2fa", "", "Get TOTP Code")
)

func main() {
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
		version.PrintVersion(version.GetLatestIndex())
		return
	}

	// set config path
	if cp := strings.TrimSpace(*confPath); cp != "" {
		common.ConfPath = cp
	}

	if err := beego.LoadAppConfig("ini", filepath.Join(common.GetRunPath(), "conf", "nps.conf")); err != nil {
		log.Println("load config file error", err.Error())
		if err := beego.LoadAppConfig("ini", filepath.Join(common.GetAppPath(), "conf", "nps.conf")); err != nil {
			log.Fatalln("load config file error", err.Error())
		}
	}

	pprofIp := beego.AppConfig.String("pprof_ip")
	pprofPort := beego.AppConfig.DefaultString("pprof_port", "0")
	if pprofPort != "0" {
		pprofAddr := common.BuildAddress(pprofIp, pprofPort)
		common.InitPProfByAddr(pprofAddr)
	}

	err := common.SetTimezone(beego.AppConfig.String("timezone"))
	if err != nil {
		logs.Warn("Set timezone error %v", err)
	}
	common.SetCustomDNS(beego.AppConfig.String("dns_server"))
	logType := beego.AppConfig.DefaultString("log", "stdout")
	logLevel = beego.AppConfig.DefaultString("log_level", "trace")
	logPath := beego.AppConfig.String("log_path")
	if logPath == "" || strings.EqualFold(logPath, "on") || strings.EqualFold(logPath, "true") {
		logPath = common.GetLogPath()
	}
	if !strings.EqualFold(logPath, "off") && !strings.EqualFold(logPath, "false") && !strings.EqualFold(logPath, "docker") && logPath != "/dev/null" {
		if !filepath.IsAbs(logPath) {
			logPath = filepath.Join(common.GetRunPath(), logPath)
		}
		if common.IsWindows() {
			logPath = strings.Replace(logPath, "\\", "\\\\", -1)
		}
	}
	logMaxFiles := beego.AppConfig.DefaultInt("log_max_files", 30)
	logMaxDays := beego.AppConfig.DefaultInt("log_max_days", 30)
	logMaxSize := beego.AppConfig.DefaultInt("log_max_size", 5)
	logCompress := beego.AppConfig.DefaultBool("log_compress", false)
	logColor := beego.AppConfig.DefaultBool("log_color", true)

	// init service
	options := make(service.KeyValue)
	svcConfig := &service.Config{
		Name:        "Nps",
		DisplayName: "nps内网穿透代理服务器",
		Description: "一款轻量级、功能强大的内网穿透代理服务器。支持tcp、udp流量转发，支持内网http代理、内网socks5代理，同时支持snappy压缩、站点保护、加密传输、多路复用、header修改等。支持web图形化管理，集成多用户模式。",
		Option:      options,
	}

	for _, v := range os.Args[1:] {
		switch v {
		case "install", "start", "stop", "uninstall", "restart":
			continue
		}
		svcConfig.Arguments = append(svcConfig.Arguments, v)
	}

	svcConfig.Arguments = append(svcConfig.Arguments, "service")
	if len(os.Args) > 1 && os.Args[1] == "service" && !strings.EqualFold(logType, "off") && !strings.EqualFold(logType, "both") {
		logType = "file"
	}
	logs.Init(logType, logLevel, logPath, logMaxSize, logMaxFiles, logMaxDays, logCompress, logColor)
	if !common.IsWindows() {
		svcConfig.Dependencies = []string{
			"Requires=network.target",
			"After=network-online.target syslog.target"}
		svcConfig.Option["SystemdScript"] = install.SystemdScript
		svcConfig.Option["SysvScript"] = install.SysvScript
	}
	prg := &nps{}
	prg.exit = make(chan struct{})
	s, err := service.New(prg, svcConfig)
	if err != nil {
		logs.Error("service function disabled %v", err)
		run()
		// run without service
		wg := sync.WaitGroup{}
		wg.Add(1)
		wg.Wait()
		return
	}

	if cmd != "" && cmd != "service" {
		switch cmd {
		case "reload":
			daemon.InitDaemon("nps", common.GetRunPath(), common.GetTmpPath())
			return
		case "install":
			// uninstall before
			_ = service.Control(s, "stop")
			_ = service.Control(s, "uninstall")

			binPath := install.InstallNps()
			svcConfig.Executable = binPath
			s, err := service.New(prg, svcConfig)
			if err != nil {
				logs.Error("%v", err)
				return
			}
			err = service.Control(s, cmd)
			if err != nil {
				logs.Error("Valid actions: %q error: %v", service.ControlAction, err)
			}
			if service.Platform() == "unix-systemv" {
				logs.Info("unix-systemv service")
				confPath := "/etc/init.d/" + svcConfig.Name
				_ = os.Symlink(confPath, "/etc/rc.d/S90"+svcConfig.Name)
				_ = os.Symlink(confPath, "/etc/rc.d/K02"+svcConfig.Name)
			}
			return
		case "start", "restart", "stop":
			if service.Platform() == "unix-systemv" {
				logs.Info("unix-systemv service")
				c := exec.Command("/etc/init.d/"+svcConfig.Name, cmd)
				if err := c.Run(); err != nil {
					logs.Error("%v", err)
				}
				return
			}
			err := service.Control(s, cmd)
			if err != nil {
				logs.Error("Valid actions: %q error: %v", service.ControlAction, err)
			}
			return
		case "uninstall":
			err := service.Control(s, cmd)
			if err != nil {
				logs.Error("Valid actions: %q error: %v", service.ControlAction, err)
			}
			if service.Platform() == "unix-systemv" {
				logs.Info("unix-systemv service")
				_ = os.Remove("/etc/rc.d/S90" + svcConfig.Name)
				_ = os.Remove("/etc/rc.d/K02" + svcConfig.Name)
			}
			return
		case "update":
			install.UpdateNps()
			return
			//default:
			//	logs.Error("command is not support")
			//	return
		}
	}
	_ = s.Run()
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
	exit chan struct{}
}

func (p *nps) Start(s service.Service) error {
	_, _ = s.Status()
	go p.run()
	return nil
}
func (p *nps) Stop(s service.Service) error {
	_, _ = s.Status()
	close(p.exit)
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
	run()
	select {
	case <-p.exit:
		logs.Warn("stop...")
	}
	return nil
}

func run() {
	routers.Init()
	task := &file.Tunnel{
		Mode: "webServer",
	}
	if beego.AppConfig.DefaultBool("secure_mode", false) {
		bridge.ServerSecureMode = true
	}
	logs.Info("the config path is: %s", common.GetRunPath())
	logs.Info("the version of server is %s, allow client core version to be %s", version.VERSION, version.GetMinVersion(bridge.ServerSecureMode))
	_ = bridge.SetClientSelectMode(beego.AppConfig.DefaultString("bridge_select_mode", ""))
	ntpServer := beego.AppConfig.DefaultString("ntp_server", "")
	ntpInterval := beego.AppConfig.DefaultInt("ntp_interval", 5)
	common.SetNtpServer(ntpServer)
	common.SetNtpInterval(time.Duration(ntpInterval) * time.Minute)
	go common.SyncTime()
	connection.InitConnectionService()
	//crypt.InitTls(filepath.Join(common.GetRunPath(), "conf", "server.pem"), filepath.Join(common.GetRunPath(), "conf", "server.key"))
	cert, ok := common.LoadCert(beego.AppConfig.String("bridge_cert_file"), beego.AppConfig.String("bridge_key_file"))
	if !ok {
		logs.Info("Using randomly generated certificate.")
	}
	crypt.InitTls(cert)
	tool.InitAllowPort()
	tool.StartSystemInfo()
	timeout := beego.AppConfig.DefaultInt("disconnect_timeout", 60)
	bridgePort := connection.BridgePort
	bridgeType := beego.AppConfig.DefaultString("bridge_type", "both")
	bridge.ServerKcpEnable = beego.AppConfig.DefaultBool("kcp_enable", true) && connection.BridgeKcpPort != 0 && (bridgeType == "kcp" || bridgeType == "udp" || bridgeType == "both")
	bridge.ServerQuicEnable = beego.AppConfig.DefaultBool("quic_enable", true) && connection.BridgeQuicPort != 0 && (bridgeType == "quic" || bridgeType == "udp" || bridgeType == "both")
	if bridgeType == "both" {
		bridgeType = "tcp"
	}
	bridge.ServerTcpEnable = beego.AppConfig.DefaultBool("tcp_enable", true) && connection.BridgeTcpPort != 0 && bridgeType == "tcp"
	bridge.ServerTlsEnable = beego.AppConfig.DefaultBool("tls_enable", true) && connection.BridgeTlsPort != 0 && bridgeType == "tcp"
	bridge.ServerWsEnable = beego.AppConfig.DefaultBool("ws_enable", true) && connection.BridgeWsPort != 0 && connection.BridgePath != "" && bridgeType == "tcp"
	bridge.ServerWssEnable = beego.AppConfig.DefaultBool("wss_enable", true) && connection.BridgeWssPort != 0 && connection.BridgePath != "" && bridgeType == "tcp"
	go server.StartNewServer(bridgePort, task, bridgeType, timeout)
}
