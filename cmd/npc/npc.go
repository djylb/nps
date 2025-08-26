//go:build !sdk
// +build !sdk

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/ccding/go-stun/stun"
	"github.com/djylb/nps/client"
	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/config"
	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/install"
	"github.com/djylb/nps/lib/logs"
	"github.com/djylb/nps/lib/version"
	"github.com/kardianos/service"
)

// 全局配置变量
var (
	serverAddr     = flag.String("server", "", "Server addr (ip1:port1,ip2:port2)")
	configPath     = flag.String("config", "", "Configuration file path (path1,path2)")
	verifyKey      = flag.String("vkey", "", "Authentication key (eg: vkey1,vkey2)")
	logType        = flag.String("log", "file", "Log output mode (stdout|file|both|off)")
	connType       = flag.String("type", "tcp", "Connection type with the server (kcp|tcp|tls) (eg: tcp,tls)")
	proxyUrl       = flag.String("proxy", "", "Proxy socks5 URL (eg: socks5://user:pass@127.0.0.1:9007)")
	logLevel       = flag.String("log_level", "trace", "Log level (trace|debug|info|warn|error|fatal|panic|off)")
	registerTime   = flag.Int("time", 2, "Register time in hours")
	localPort      = flag.Int("local_port", 2000, "P2P local port")
	password       = flag.String("password", "", "P2P password flag")
	target         = flag.String("target", "", "P2P target")
	targetType     = flag.String("target_type", "all", "P2P target connection type (all|tcp|udp)")
	localProxy     = flag.Bool("local_proxy", false, "Secret enable proxy local (true or false)")
	localType      = flag.String("local_type", "p2p", "P2P target type")
	fallbackSecret = flag.Bool("fallback_secret", true, "P2P fallback secret (true or false)")
	disableP2P     = flag.Bool("disable_p2p", false, "Disable P2P connection (true or false)")
	logPath        = flag.String("log_path", "", "NPC log path (empty to use default, 'off' to disable)")
	logMaxSize     = flag.Int("log_max_size", 5, "Maximum log file size in MB before rotation (0 to disable)")
	logMaxDays     = flag.Int("log_max_days", 7, "Number of days to retain old log files (0 to disable)")
	logMaxFiles    = flag.Int("log_max_files", 10, "Maximum number of log files to retain (0 to disable)")
	logCompress    = flag.Bool("log_compress", false, "Compress rotated log files (true or false)")
	logColor       = flag.Bool("log_color", true, "Enable ANSI color codes in console output (true or false)")
	debug          = flag.Bool("debug", true, "Enable debug mode")
	pprofAddr      = flag.String("pprof", "", "PProf debug address (ip:port)")
	stunAddr       = flag.String("stun_addr", "stun.miwifi.com:3478", "STUN server address")
	ver            = flag.Bool("version", false, "Show current version")
	protoVer       = flag.Int("proto_version", version.GetLatestIndex(), fmt.Sprintf("Protocol version (0-%d)", version.GetLatestIndex()))
	skipVerify     = flag.Bool("skip_verify", false, "Skip verification of server certificate")
	disconnectTime = flag.Int("disconnect_timeout", 60, "Disconnect timeout in seconds")
	p2pTime        = flag.Int("p2p_timeout", 5, "P2P timeout in seconds")
	dnsServer      = flag.String("dns_server", "8.8.8.8", "DNS server for domain lookup")
	ntpServer      = flag.String("ntp_server", "", "NTP server for time synchronization")
	ntpInterval    = flag.Int("ntp_interval", 5, "interval between NTP synchronizations (minutes)")
	tlsEnable      = flag.Bool("tls_enable", false, "Enable TLS (Deprecated)")
	timezone       = flag.String("timezone", "", "Time zone to use for time(eg: Asia/Shanghai)")
	genTOTP        = flag.Bool("gen2fa", false, "Generate TOTP Secret")
	getTOTP        = flag.String("get2fa", "", "Get TOTP Code")
)

func main() {
	flag.Parse()
	// TOTP
	if *genTOTP {
		crypt.PrintTOTPSecret()
		return
	}
	if *getTOTP != "" {
		crypt.PrintTOTPCode(*getTOTP)
		return
	}
	// 显示版本并退出
	if *ver {
		version.PrintVersion(*protoVer)
		return
	}
	client.Ver = *protoVer
	client.SkipTLSVerify = *skipVerify
	client.DisableP2P = *disableP2P
	crypt.SkipVerify = *skipVerify
	if *protoVer < 2 {
		crypt.SkipVerify = true
	}

	// 配置时区
	err := common.SetTimezone(*timezone)
	if err != nil {
		logs.Warn("Set timezone error %v", err)
	}

	// 配置日志
	configureLogging()

	// 配置DNS
	common.SetCustomDNS(*dnsServer)

	// 配置NTP
	common.SetNtpServer(*ntpServer)
	common.SetNtpInterval(time.Duration(*ntpInterval) * time.Minute)

	// 初始化服务
	options := make(service.KeyValue)
	svcConfig := &service.Config{
		Name:        "Npc",
		DisplayName: "nps内网穿透客户端",
		Description: "一款轻量级、功能强大的内网穿透代理服务器。支持tcp、udp流量转发，支持内网http代理、内网socks5代理，同时支持snappy压缩、站点保护、加密传输、多路复用、header修改等。支持web图形化管理，集成多用户模式。",
		Option:      options,
	}

	// 非 Windows 系统添加服务依赖
	if !common.IsWindows() {
		svcConfig.Dependencies = []string{
			"Requires=network.target",
			"After=network-online.target syslog.target"}
		svcConfig.Option["SystemdScript"] = install.SystemdScript
		svcConfig.Option["SysvScript"] = install.SysvScript
	}

	// 配置服务启动参数
	for _, v := range os.Args[1:] {
		switch v {
		case "install", "start", "stop", "uninstall", "restart":
			continue
		}
		if !strings.Contains(v, "-service=") && !strings.Contains(v, "-debug=") {
			svcConfig.Arguments = append(svcConfig.Arguments, v)
		}
	}
	svcConfig.Arguments = append(svcConfig.Arguments, "-debug=false")

	// 创建服务
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	prg := NewNpc(ctx)
	s, err := service.New(prg, svcConfig)
	if err != nil {
		logs.Error("service function disabled %v", err)
		run(ctx)
		// run without service
		wg := sync.WaitGroup{}
		wg.Add(1)
		wg.Wait()
		return
	}

	// 处理服务命令
	if len(os.Args) >= 2 {
		switch os.Args[1] {
		case "status":
			//	if len(os.Args) > 2 {
			//		path := strings.Replace(os.Args[2], "-config=", "", -1)
			//		client.GetTaskStatus(path)
			//	}
			_ = flag.CommandLine.Parse(os.Args[2:])
			client.GetTaskStatus(*serverAddr, *verifyKey, *connType, *proxyUrl)
		case "register":
			_ = flag.CommandLine.Parse(os.Args[2:])
			client.RegisterLocalIp(*serverAddr, *verifyKey, *connType, *proxyUrl, *registerTime)
		case "update":
			install.UpdateNpc()
			return
		case "nat":
			c := stun.NewClient()
			_ = flag.CommandLine.Parse(os.Args[2:])
			c.SetServerAddr(*stunAddr)
			//logs.Info(*stunAddr)
			fmt.Println("STUN Server:", *stunAddr)
			nat, host, err := c.Discover()
			if err != nil {
				logs.Error("Error: %v", err)
				return
			}
			fmt.Println("NAT Type:", nat)
			if host != nil {
				fmt.Println("External IP Family:", host.Family())
				fmt.Println("External IP:", host.IP())
				fmt.Println("External Port:", host.Port())
			}
			os.Exit(0)
		case "start", "stop", "restart":
			// support busyBox and sysV, for openWrt
			if service.Platform() == "unix-systemv" {
				logs.Info("unix-systemv service")
				cmd := exec.Command("/etc/init.d/"+svcConfig.Name, os.Args[1])
				err := cmd.Run()
				if err != nil {
					logs.Error("%v", err)
				}
				return
			}
			err := service.Control(s, os.Args[1])
			if err != nil {
				logs.Error("Valid actions: %q error: %v", service.ControlAction, err)
			}
			return
		case "install":
			_ = service.Control(s, "stop")
			_ = service.Control(s, "uninstall")
			install.InstallNpc()
			err := service.Control(s, os.Args[1])
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
		case "uninstall":
			err := service.Control(s, os.Args[1])
			if err != nil {
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

// 配置日志记录
func configureLogging() {
	// 关闭日志输出
	if strings.EqualFold(*logType, "false") {
		*logType = "off"
	}

	// 控制台日志
	if *debug && *logType != "off" {
		if *logType != "both" {
			*logType = "stdout"
		}
		*logLevel = "trace"
	}

	// 处理日志路径默认值
	if *logPath == "" || strings.EqualFold(*logPath, "on") || strings.EqualFold(*logPath, "true") {
		*logPath = common.GetNpcLogPath() // 使用默认路径
	}

	// 相对路径使用配置文件路径
	if !filepath.IsAbs(*logPath) {
		*logPath = filepath.Join(common.GetRunPath(), *logPath)
	}

	// 针对 Windows 系统调整日志路径中的反斜杠
	if common.IsWindows() {
		*logPath = strings.Replace(*logPath, "\\", "\\\\", -1)
	}

	logs.Init(*logType, *logLevel, *logPath, *logMaxSize, *logMaxFiles, *logMaxDays, *logCompress, *logColor)
}

type Npc struct {
	ctx    context.Context
	cancel context.CancelFunc
	exit   chan struct{}
}

func NewNpc(pCtx context.Context) *Npc {
	ctx, cancel := context.WithCancel(pCtx)
	return &Npc{
		ctx:    ctx,
		exit:   make(chan struct{}),
		cancel: cancel,
	}
}

func (p *Npc) Start(_ service.Service) error {
	go p.run()
	return nil
}

func (p *Npc) Stop(_ service.Service) error {
	close(p.exit)
	p.cancel()
	if service.Interactive() {
		os.Exit(0)
	}
	return nil
}

func (p *Npc) run() error {
	defer func() {
		if err := recover(); err != nil {
			const size = 64 << 10
			buf := make([]byte, size)
			buf = buf[:runtime.Stack(buf, false)]
			logs.Warn("npc: panic serving %v: %s", err, buf)
		}
	}()
	run(p.ctx)
	select {
	case <-p.exit:
		logs.Warn("stop...")
	}
	return nil
}

// 主运行逻辑
func run(ctx context.Context) {
	common.InitPProfByAddr(*pprofAddr)
	if *tlsEnable {
		*connType = "tls"
	}
	//p2p or secret command
	if *password != "" {
		logs.Info("the version of client is %s, the core version of client is %s", version.VERSION, version.GetVersion(*protoVer))
		common.SyncTime()
		commonConfig := new(config.CommonConfig)
		commonConfig.Server = *serverAddr
		commonConfig.VKey = *verifyKey
		commonConfig.Tp = strings.ToLower(*connType)
		localServer := new(config.LocalServer)
		localServer.Type = strings.ToLower(*localType)
		localServer.Password = *password
		localServer.Target = *target
		localServer.TargetType = strings.ToLower(*targetType)
		localServer.Port = *localPort
		localServer.Fallback = *fallbackSecret
		localServer.LocalProxy = *localProxy
		commonConfig.Client = new(file.Client)
		commonConfig.Client.Cnf = new(file.Config)
		commonConfig.DisconnectTime = *p2pTime
		p2pm := client.NewP2PManager(ctx, commonConfig)
		go p2pm.StartLocalServer(localServer)
		return
	}
	env := common.GetEnvMap()
	if *serverAddr == "" {
		*serverAddr, _ = env["NPC_SERVER_ADDR"]
	}
	if *verifyKey == "" {
		*verifyKey, _ = env["NPC_SERVER_VKEY"]
	}
	if *configPath == "" {
		*configPath, _ = env["NPC_CONFIG_PATH"]
	}
	hasCommand := *verifyKey != "" && *serverAddr != ""
	if hasCommand {
		logs.Info("the version of client is %s, the core version of client is %s", version.VERSION, version.GetVersion(*protoVer))
		common.SyncTime()
		*serverAddr = strings.ReplaceAll(*serverAddr, "，", ",")
		*serverAddr = strings.ReplaceAll(*serverAddr, "：", ":")
		*verifyKey = strings.ReplaceAll(*verifyKey, "，", ",")
		*connType = strings.ReplaceAll(*connType, "，", ",")

		serverAddrs := strings.Split(*serverAddr, ",")
		verifyKeys := strings.Split(*verifyKey, ",")
		connTypes := strings.Split(*connType, ",")

		serverAddrs = common.HandleArrEmptyVal(serverAddrs)
		verifyKeys = common.HandleArrEmptyVal(verifyKeys)
		connTypes = common.HandleArrEmptyVal(connTypes)

		if len(connTypes) == 0 {
			connTypes = append(connTypes, "tcp")
			//logs.Info(connTypes)
		}
		//logs.Debug(connTypes)

		if len(serverAddrs) == 0 || len(verifyKeys) == 0 || serverAddrs[0] == "" || verifyKeys[0] == "" {
			logs.Error("serverAddr or verifyKey cannot be empty")
			os.Exit(1)
		}

		maxLength := common.ExtendArrs(&serverAddrs, &verifyKeys, &connTypes)
		//logs.Debug("max length: %d", maxLength)
		//logs.Debug(serverAddrs, verifyKeys, connTypes)
		for i := 0; i < maxLength; i++ {
			serverAddr := serverAddrs[i]
			verifyKey := verifyKeys[i]
			connType := connTypes[i]
			connType = strings.ToLower(connType)

			go func() {
				for {
					logs.Info("Start server: %s vkey: %s type: %s", serverAddr, verifyKey, connType)
					client.NewRPClient(serverAddr, verifyKey, connType, *proxyUrl, "", nil, *disconnectTime, nil).Start()
					logs.Info("Client closed! It will be reconnected in five seconds")
					time.Sleep(time.Second * 5)
				}
			}()
		}
	}
	if *configPath != "" || !hasCommand {
		if *configPath == "" {
			*configPath = common.GetConfigPath()
		}

		configPaths := strings.Split(*configPath, ",")
		for i := range configPaths {
			configPaths[i] = strings.TrimSpace(configPaths[i])
		}

		for _, path := range configPaths {
			go client.StartFromFile(path)
		}
	}
}
