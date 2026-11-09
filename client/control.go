package client

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/djylb/slogx"

	"github.com/djylb/nps/bridge/legacy"
	"github.com/djylb/nps/lib/clock"
	"github.com/djylb/nps/lib/config"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/model"
	"github.com/djylb/nps/lib/netaddr"
	"github.com/djylb/timeshim"
)

var AutoReconnect = true
var LocalIPForward = false

var errAdd = errors.New("the server returned an error, which port or host may have been occupied or not allowed to open")

// WaitReconnect waits for the time before the next connection attempt. It
// returns false if ctx ended first.
func WaitReconnect(ctx context.Context) bool {
	return timeshim.Sleep(ctx, timeshim.Real, 5*time.Second) == nil
}

// errSessionEnded is returned once the connection to the server ended and
// reconnecting is turned off.
var errSessionEnded = errors.New("the connection to the server ended and auto reconnection is off")

// StartFromFile runs the client configured in path until pCtx ends, and then
// returns nil. It returns an error when the config cannot be loaded or when
// the session ended and reconnecting is turned off.
func StartFromFile(pCtx context.Context, path string) error {
	cnf, err := config.NewConfig(path)
	if err == nil && cnf.CommonConfig == nil {
		err = errors.New("no common section")
	}
	if err != nil {
		return fmt.Errorf("config file %s: %w", path, err)
	}
	log.Info("config loaded", "file", path)

	netaddr.SetCustomDNS(cnf.CommonConfig.DnsServer)

	clock.SetNtpServer(cnf.CommonConfig.NtpServer)
	if cnf.CommonConfig.NtpInterval > 0 {
		clock.SetNtpInterval(time.Duration(cnf.CommonConfig.NtpInterval) * time.Minute)
	}
	clock.Sync()

	// The key the server issued for a public key, kept across runs.
	var issued string

	first := true
	for {
		select {
		case <-pCtx.Done():
			return nil
		default:
		}
		if !first && (!cnf.CommonConfig.AutoReconnection || !AutoReconnect) {
			return errSessionEnded
		}
		if !first {
			log.Info("reconnecting", "server", cnf.CommonConfig.Server)
			if !WaitReconnect(pCtx) {
				return nil
			}
		}
		first = false

		issued, err = RunConfigOnce(pCtx, cnf, issued)
		if err != nil {
			log.Warn("session ended", "server", cnf.CommonConfig.Server, "err", err)
		}
	}
}

// RunConfigOnce runs cnf until the session closes or ctx ends and returns the
// issued key to pass to the next run, for a public key once the server gave one. Local servers run until ctx ends or one
// of them fails to start.
func RunConfigOnce(ctx context.Context, cnf *config.Config, issued string) (string, error) {
	if len(cnf.LocalServer) > 0 {
		return issued, runLocalServers(ctx, cnf)
	}
	return runCoreConfig(ctx, cnf, issued)
}

// logWebAccess logs the web login of the client cnf describes, whose key is
// vkey.
func logWebAccess(cnf *config.Config, vkey string) {
	if cnf.CommonConfig.Client.WebUserName == "" || cnf.CommonConfig.Client.WebPassword == "" {
		log.Info("web access login", "user", "user", "password", slogx.Redacted(vkey))
	} else {
		log.Info("web access login", "user", cnf.CommonConfig.Client.WebUserName, "password", slogx.Redacted(cnf.CommonConfig.Client.WebPassword))
	}
}

func runLocalServers(ctx context.Context, cnf *config.Config) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	mgr := NewSecretManager(ctx, cancel, cnf.CommonConfig)
	defer mgr.Close()
	if err := mgr.UseCore(); err != nil {
		return err
	}
	for _, l := range cnf.LocalServer {
		if err := mgr.StartLocalServer(l); err != nil {
			return err
		}
	}
	<-ctx.Done()
	return nil
}

// configExchangeTimeout bounds a whole config exchange; the server handles
// the entries one at a time, so it grows with their number.
func configExchangeTimeout(cnf *config.Config) time.Duration {
	return 30*time.Second + time.Duration(len(cnf.Hosts)+len(cnf.Tasks))*5*time.Second
}

// exchangeConfig sends the configuration on c, finished by deadline, and
// returns the vkey to run with. A refused host or task is logged and skipped;
// any error means the connection is unusable and the client must reconnect.
func exchangeConfig(c *conn.Conn, cnf *config.Config, deadline time.Time) (string, error) {
	if err := c.SetDeadline(deadline); err != nil {
		return "", err
	}
	var isPub bool
	if err := binary.Read(c, binary.LittleEndian, &isPub); err != nil {
		return "", err
	}

	// get tmp password
	vkey := cnf.CommonConfig.VKey
	if isPub {
		// send global configuration to server and get status of config setting
		if err := legacy.WriteClient(c, cnf.CommonConfig.Client); err != nil {
			return "", err
		}
		ok, err := legacy.ReadStatus(c)
		if err != nil {
			return "", err
		}
		if !ok {
			return "", errors.New("the web_user may have been occupied")
		}
		b, err := legacy.ReadExact(c, 16)
		if err != nil {
			return "", err
		}
		vkey = string(b)
	}

	var accepted []string // the routes the server took
	//send hosts to server
	for _, v := range cnf.Hosts {
		if err := legacy.WriteHost(c, v); err != nil {
			return "", err
		}
		ok, err := legacy.ReadStatus(c)
		if err != nil {
			return "", err
		}
		if !ok {
			log.Info("server refused host", "domain", v.Host, "err", errAdd)
			continue
		}
		accepted = append(accepted, v.Host)
	}

	//send task to server
	for _, v := range cnf.Tasks {
		if err := legacy.WriteTask(c, v); err != nil {
			return "", err
		}
		ok, err := readTaskAddStatus(c, v)
		if err != nil {
			return "", err
		}
		if !ok {
			log.Info("server refused tunnel", "mode", v.Mode, "ports", v.Ports, "remark", v.Remark, "err", errAdd)
			continue
		}
		accepted = append(accepted, v.Mode+":"+v.Ports)
	}
	log.Info("server accepted config", "server", cnf.CommonConfig.Server, "count", len(accepted), "routes", strings.Join(accepted, ","))
	return vkey, nil
}

// readTaskAddStatus reads the add statuses the server reports for a FlagNewTask:
// one per port the task expands to, ending at the first failure.
func readTaskAddStatus(c *conn.Conn, t *model.Tunnel) (bool, error) {
	n := 1
	if ports, _, ok := model.ConfigTaskPorts(t); ok {
		n = len(ports)
	}
	for i := 0; i < n; i++ {
		ok, err := legacy.ReadStatus(c)
		if err != nil || !ok {
			return false, err
		}
	}
	return true, nil
}
