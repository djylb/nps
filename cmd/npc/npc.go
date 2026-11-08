//go:build !sdk

package main

import (
	"context"
	"errors"
	goflag "flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/djylb/nps/bridge/legacy"
	"github.com/djylb/nps/client"
	"github.com/djylb/nps/internal/cliargs"
	"github.com/djylb/nps/internal/logsetup"
	"github.com/djylb/nps/lib/clock"
	"github.com/djylb/nps/lib/config"
	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/install"
	"github.com/djylb/nps/lib/logevent"
	"github.com/djylb/nps/lib/model"
	"github.com/djylb/nps/lib/netaddr"
	"github.com/djylb/nps/lib/runenv"
	"github.com/djylb/nps/lib/textutil"
	"github.com/djylb/nps/lib/version"
	"github.com/djylb/slogx"
	"github.com/kardianos/service"
	flag "github.com/spf13/pflag"
)

var (
	log    = slogx.Module("client")
	cfgLog = slogx.Module("config")
)

// Config
// The log defaults: a file, except on OpenWrt, where npc logs nothing unless
// told to (runenv.LogDefaults).
var defaultLogType, defaultLogMaxSize, defaultLogMaxFiles = runenv.LogDefaults("file", 5, 10)

var (
	ver            = flag.BoolP("version", "v", false, "Show current version")
	serverAddr     = flag.StringP("server", "s", "", "Server addr (ip1:port1,ip2:port2)")
	verifyKey      = flag.StringP("vkey", "k", "", "Authentication key (eg: vkey1,vkey2)")
	connType       = flag.StringP("type", "t", "tcp", "Connection type with the server (tcp|tls|kcp|quic|ws|wss) (eg: tcp,tls)")
	configPath     = flag.StringP("config", "c", "", "Configuration file path (path1,path2)")
	proxyUrl       = flag.String("proxy", "", "Proxy URL: http, https, socks5, socks5h, socks4 or socks4a (eg: socks5://user:pass@127.0.0.1:9007)")
	localIP        = flag.String("local_ip", "", "Local source IP for outbound connections")
	localIPForward = flag.Bool("local_ip_forward", false, "Apply local_ip to tunnel forwarding egress for public IP/domain targets")
	localType      = flag.String("local_type", "p2p", "P2P target type")
	localPort      = flag.Int("local_port", 2000, "P2P local port")
	password       = flag.String("password", "", "P2P password flag")
	target         = flag.String("target", "", "P2P target")
	targetType     = flag.String("target_type", "all", "P2P target connection type (all|tcp|udp)")
	localProxy     = flag.Bool("local_proxy", false, "Secret enable proxy local (true or false)")
	fallbackSecret = flag.Bool("fallback_secret", true, "P2P fallback secret (true or false)")
	disableP2P     = flag.Bool("disable_p2p", false, "Disable P2P connection (true or false)")
	disableRemote  = flag.Bool("disable_remote_update", false, "Ignore update requests from the server (true or false)")
	registerTime   = flag.Int("time", 2, "Register time in hours")
	logType        = flag.String("log", defaultLogType, "Log output mode (stdout|file|both|off)")
	logLevel       = flag.String("log_level", "info", "Log level (trace|debug|info|warn|error|off), with per-module levels: info,p2p=debug")
	logPath        = flag.String("log_path", "", "NPC log path (empty to use default, 'off' to disable)")
	logMaxSize     = flag.Int("log_max_size", defaultLogMaxSize, "Maximum log file size in MB before rotation (0 to disable)")
	logMaxDays     = flag.Int("log_max_days", 7, "Number of days to retain old log files (0 to disable)")
	logMaxFiles    = flag.Int("log_max_files", defaultLogMaxFiles, "Maximum number of log files to retain (0 to disable)")
	logCompress    = flag.Bool("log_compress", false, "Compress rotated log files (true or false)")
	logFormat      = flag.String("log_format", "auto", "Log line format (auto|text|json); auto is coloured on a terminal, logfmt elsewhere")
	debug          = flag.Bool("debug", true, "Enable debug mode")
	pprofAddr      = flag.String("pprof", "", "PProf debug address (ip:port)")
	skipVerify     = flag.Bool("skip_verify", false, "Skip verification of server certificate")
	disconnectTime = flag.Int("disconnect_timeout", 30, "Disconnect timeout in seconds")
	keepAlive      = flag.Int("keepalive", 0, "KeepAlive Period in seconds")
	p2pTime        = flag.Int("p2p_timeout", 5, "P2P timeout in seconds")
	dnsServer      = flag.String("dns_server", "8.8.8.8", "DNS server for domain lookup")
	ntpServer      = flag.String("ntp_server", "", "NTP server for time synchronization")
	ntpInterval    = flag.Int("ntp_interval", 5, "interval between NTP synchronizations (minutes)")
	timezone       = flag.String("timezone", "", "Time zone to use for time(eg: Asia/Shanghai)")
	genTOTP        = flag.Bool("gen2fa", false, "Generate TOTP Secret")
	getTOTP        = flag.String("get2fa", "", "Get TOTP Code")
	autoReconnect  = flag.Bool("auto_reconnect", true, "Auto Reconnect")
	launchInputs   = flag.StringArray("launch", nil, "Quick start payload, repeatable: npc:// URL, http(s) URL, @file, base64, JSON, YAML or INI")
)

func main() {
	flag.CommandLine.SetNormalizeFunc(func(f *flag.FlagSet, name string) flag.NormalizedName {
		name = strings.ReplaceAll(name, "-", "_")
		name = strings.ReplaceAll(name, ".", "_")
		return flag.NormalizedName(name)
	})
	flag.CommandLine.SortFlags = false
	flag.CommandLine.SetInterspersed(true)
	flag.CommandLine.AddGoFlagSet(goflag.CommandLine)
	prepared, warnings := cliargs.Prepare(flag.CommandLine, os.Args[1:], cliargs.NPC, "install", "start", "stop", "uninstall", "restart", "status", "register", "update", "update-force", "version")
	os.Args = append(os.Args[:1], prepared...)
	argWarnings = warnings
	flag.Parse()

	args := flag.Args()
	var cmd string
	if len(args) > 0 {
		cmd = args[0]
	}

	// TOTP
	if *genTOTP {
		crypt.PrintTOTPSecret()
		return
	}
	if *getTOTP != "" {
		crypt.PrintTOTPCode(*getTOTP)
		return
	}
	// Version
	if *ver || cmd == "version" {
		version.PrintVersion(legacy.CoreVersion(legacy.LatestIndex))
		return
	}
	install.RecoverUpdate()
	client.SkipTLSVerify = *skipVerify
	client.DisableP2P = *disableP2P
	client.AutoReconnect = *autoReconnect
	client.LocalIPForward = *localIPForward
	crypt.SkipVerify = *skipVerify

	// Timezone
	if err := clock.SetTimezone(*timezone); err != nil {
		cfgLog.Warn("set timezone failed", "err", err)
	}

	// Log
	configureLogging()
	watchReopen()

	// DNS
	netaddr.SetCustomDNS(*dnsServer)

	// NTP
	clock.SetNtpServer(*ntpServer)
	clock.SetNtpInterval(time.Duration(*ntpInterval) * time.Minute)

	// KeepAlive
	if *keepAlive > 0 {
		interval := time.Duration(*keepAlive) * time.Second
		client.QuicConfig.KeepAlivePeriod = interval
		legacy.SetPingInterval(interval)
	}

	// Init
	options := make(service.KeyValue)
	svcConfig := &service.Config{
		Name:        "Npc",
		DisplayName: "nps内网穿透客户端",
		Description: "一款轻量级、功能强大的内网穿透代理服务器。支持tcp、udp流量转发，支持内网http代理、内网socks5代理，同时支持snappy压缩、站点保护、加密传输、多路复用、header修改等。支持web图形化管理，集成多用户模式。",
		Option:      options,
	}

	// 平台相关的服务设置
	install.ConfigureService(svcConfig)
	if !*disableRemote {
		client.RemoteUpdate = func() error {
			if !install.CanRestartSelf() {
				log.Info("remote update ignored: on Windows only npc installed as a service updates itself")
				return nil
			}
			u, err := install.UpdateSelf("npc", svcConfig.Name)
			if u == nil {
				if err == nil {
					log.Info("remote update: already the latest version")
				}
				return err
			}
			log.Info("remote update installed, restarting", "to", u.Version)
			return u.Restart()
		}
	}

	// 创建服务
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	prg := NewNpc(ctx)
	s, err := service.New(prg, svcConfig)
	if cmd == "update" || cmd == "update-force" {
		if err != nil {
			log.Warn("service manager unavailable", "err", err)
			s = nil
		}
		res, err := install.Update(context.Background(), "npc", svcConfig.Name, install.UpdateOptions{Force: cmd == "update-force"})
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
	if err != nil {
		log.Warn("service manager unavailable, running without it", "err", err)
		run(ctx, cancel)
		// run without service
		wg := sync.WaitGroup{}
		wg.Add(1)
		wg.Wait()
		return
	}

	// 处理服务命令
	switch cmd {
	case "status":
		if err := client.GetTaskStatus(*serverAddr, *verifyKey, *connType, *proxyUrl, *localIP); err != nil {
			log.Error("status failed", "err", err)
			os.Exit(1)
		}
		return
	case "register":
		if err := client.RegisterLocalIp(*serverAddr, *verifyKey, *connType, *proxyUrl, *localIP, *registerTime); err != nil {
			log.Error("register failed", "err", err)
			os.Exit(1)
		}
		return
	case "start", "stop", "restart":
		if err := install.ControlService(s, svcConfig.Name, cmd); err != nil {
			log.Error("service control failed", "action", cmd, "valid_actions", service.ControlAction, "err", err)
		}
		return
	case "install":
		_ = service.Control(s, "stop")
		_ = install.UninstallService(s, svcConfig.Name)
		binPath, err := install.NPC()
		if err != nil {
			log.Error("install failed", "err", err)
			os.Exit(1)
		}
		svcConfig.Executable = binPath
		confDir := filepath.Join(runenv.GetInstallPath(), "conf")
		svcArgs, files, err := serviceArguments(flag.CommandLine, confDir, os.Getenv("NPC_LAUNCH"))
		if err == nil {
			err = writeServiceFiles(confDir, files)
		}
		if err != nil {
			log.Error("install failed", "err", err)
			os.Exit(1)
		}
		svcConfig.Arguments = svcArgs
		if s, err = service.New(prg, svcConfig); err != nil {
			log.Error("create service failed", "err", err)
			return
		}
		if err := service.Control(s, "install"); err != nil {
			log.Error("install service failed", "err", err)
			os.Exit(1)
		}
		install.LinkServiceStart(svcConfig.Name)
		return
	case "uninstall":
		if err := install.UninstallService(s, svcConfig.Name); err != nil {
			log.Error("service control failed", "action", cmd, "err", err)
		}
		return
	}
	_ = s.Run()
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
		cfgLog.Warn(w)
	}
}

// Log configure
func configureLogging() {
	if strings.EqualFold(*logType, "false") {
		*logType = "off"
	}
	if *debug && *logType != "off" {
		if *logType != "both" {
			*logType = "stdout"
		}
		*logLevel = "trace"
	}
	if *logPath == "" || strings.EqualFold(*logPath, "on") || strings.EqualFold(*logPath, "true") {
		*logPath = runenv.GetNpcLogPath()
	}
	err := logsetup.Apply(logsetup.Options{
		Output: *logType, Format: *logFormat, Level: *logLevel, Path: *logPath, Dir: runenv.GetRunPath(),
		MaxSize: *logMaxSize, MaxFiles: *logMaxFiles, MaxDays: *logMaxDays, Compress: *logCompress,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "log:", err)
		os.Exit(1)
	}
	warnArgs(strings.EqualFold(*logType, "off"))
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
	go func() { _ = p.run() }()
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
			log.Error("panic", "err", err, "stack", string(buf))
		}
	}()
	run(p.ctx, p.cancel)
	<-p.exit
	slogx.Emit(context.Background(), log, &logevent.Stopping, "version", version.VERSION, "role", "npc")
	slogx.Default.Flush()
	return nil
}

// 主运行逻辑
func run(ctx context.Context, cancel context.CancelFunc) {
	install.StartUpdate()
	runenv.InitPProfByAddr(*pprofAddr)
	if inputs := launchPayloads(); len(inputs) > 0 {
		startLaunch(ctx, cancel, inputs)
		return
	}
	//p2p or secret command
	if *password != "" {
		slogx.Emit(context.Background(), log, &logevent.Started, "version", version.VERSION, "role", "npc", "core_version", legacy.CoreVersion(legacy.LatestIndex))
		clock.Sync()
		commonConfig := new(config.CommonConfig)
		commonConfig.Server = *serverAddr
		commonConfig.VKey = *verifyKey
		commonConfig.Tp = strings.ToLower(*connType)
		commonConfig.LocalIP = strings.TrimSpace(*localIP)
		localServer := new(config.LocalServer)
		localServer.Type = strings.ToLower(*localType)
		localServer.Password = *password
		localServer.Target = *target
		localServer.TargetType = strings.ToLower(*targetType)
		localServer.Port = *localPort
		localServer.Fallback = *fallbackSecret
		localServer.LocalProxy = *localProxy
		commonConfig.Client = new(model.Client)
		commonConfig.Client.Cnf = new(model.Config)
		commonConfig.DisconnectTime = *p2pTime
		// The manager cancels p2pCtx when it gives up, which only happens
		// with auto reconnection off.
		p2pCtx, p2pCancel := context.WithCancel(ctx)
		p2pm := client.NewP2PManager(p2pCtx, p2pCancel, commonConfig)
		go func() { _ = p2pm.StartLocalServer(localServer) }()
		go func() {
			<-p2pCtx.Done()
			if ctx.Err() == nil {
				fail(cancel, errors.New("the connection to the server ended and auto reconnection is off"))
			}
		}()
		return
	}
	env := runenv.GetEnvMap()
	if *serverAddr == "" {
		*serverAddr = env["NPC_SERVER_ADDR"]
	}
	if *verifyKey == "" {
		*verifyKey = env["NPC_SERVER_VKEY"]
	}
	if *configPath == "" {
		*configPath = env["NPC_CONFIG_PATH"]
	}
	if *localIP == "" {
		*localIP = env["NPC_LOCAL_IP"]
	}
	hasCommand, err := commandLineMode(*serverAddr, *verifyKey)
	if err != nil {
		cfgLog.Error("invalid command line", "err", err)
		os.Exit(1)
	}
	if hasCommand {
		slogx.Emit(context.Background(), log, &logevent.Started, "version", version.VERSION, "role", "npc", "core_version", legacy.CoreVersion(legacy.LatestIndex))
		clock.Sync()
		*serverAddr = strings.ReplaceAll(*serverAddr, "，", ",")
		*serverAddr = strings.ReplaceAll(*serverAddr, "：", ":")
		*verifyKey = strings.ReplaceAll(*verifyKey, "，", ",")
		*connType = strings.ReplaceAll(*connType, "，", ",")
		*localIP = strings.ReplaceAll(*localIP, "，", ",")

		serverAddrs := strings.Split(*serverAddr, ",")
		verifyKeys := strings.Split(*verifyKey, ",")
		connTypes := strings.Split(*connType, ",")
		localIPs := strings.Split(*localIP, ",")

		serverAddrs = textutil.HandleArrEmptyVal(serverAddrs)
		verifyKeys = textutil.HandleArrEmptyVal(verifyKeys)
		connTypes = textutil.HandleArrEmptyVal(connTypes)
		localIPs = textutil.HandleArrEmptyVal(localIPs)

		if len(connTypes) == 0 {
			connTypes = append(connTypes, "tcp")
		}

		if len(serverAddrs) == 0 || len(verifyKeys) == 0 || serverAddrs[0] == "" || verifyKeys[0] == "" {
			cfgLog.Error("server and vkey must not be empty")
			os.Exit(1)
		}

		maxLength := textutil.ExtendArrs(&serverAddrs, &verifyKeys, &connTypes, &localIPs)
		for i := 0; i < maxLength; i++ {
			serverAddr := serverAddrs[i]
			verifyKey := verifyKeys[i]
			connType := connTypes[i]
			localIP := localIPs[i]
			connType = strings.ToLower(connType)

			go func(serverAddr, verifyKey, connType, localIP string) {
				for {
					log.Info("connecting", "server", serverAddr, "vkey", slogx.Redacted(verifyKey), "carrier", connType, "local_ip", localIP)
					client.NewRPClient(serverAddr, verifyKey, connType, *proxyUrl, localIP, "", nil, *disconnectTime, nil).Start(ctx)
					if *autoReconnect {
						log.Info("client closed, reconnecting", "server", serverAddr)
						if !client.WaitReconnect(ctx) {
							return
						}
					} else {
						log.Info("client closed", "server", serverAddr)
						cancel()
						os.Exit(1)
						return
					}
				}
			}(serverAddr, verifyKey, connType, localIP)
		}
	}
	if *configPath != "" || !hasCommand {
		if *configPath == "" {
			*configPath = runenv.GetConfigPath()
		}

		configPaths := strings.Split(*configPath, ",")
		for i := range configPaths {
			configPaths[i] = strings.TrimSpace(configPaths[i])
		}

		for _, path := range configPaths {
			go func() {
				if err := client.StartFromFile(ctx, path); err != nil {
					fail(cancel, err)
				}
			}()
		}
	}
}

// fail stops every client of this process and exits with a non-zero code.
func fail(cancel context.CancelFunc, err error) {
	log.Error("npc stopped", "err", err)
	cancel()
	os.Exit(1)
}

// commandLineMode reports whether npc runs from -server and -vkey rather
// than a config file. Both or neither must be given: with only one, such as
// after a misspelt flag was ignored, npc stops instead of quietly reading a
// config file.
func commandLineMode(server, vkey string) (bool, error) {
	switch {
	case server != "" && vkey != "":
		return true, nil
	case server == "" && vkey == "":
		return false, nil
	case server == "":
		return false, errors.New("-vkey is set but -server is not; give both, or neither to use a config file")
	default:
		return false, errors.New("-server is set but -vkey is not; give both, or neither to use a config file")
	}
}
