package client

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/djylb/nps/internal/coreproto/coreapi"
	"github.com/djylb/nps/lib/config"
	"github.com/djylb/nps/lib/conn"
)

// The config upload on the new protocol is a 0.35 transition: the tunnels and
// domains of npc.conf are sent with the byte exchange of the old protocol's
// config upload (exchangeConfig), on a stream of the session, so that a config
// file keeps working when only the binary is upgraded. It is removed in the
// next major version, with this file and wire.OpConfigUpload.

// runCoreConfig hosts cnf's profile on the new protocol and, when the file
// defines tunnels or hosts, uploads them on every session it makes; a failed
// upload ends the session and is the error, as a failed exchange is on the
// old protocol. Like RunConfigOnce it returns when the session ends.
func runCoreConfig(ctx context.Context, cnf *config.Config) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	fsm := NewFileServerManager(ctx)
	defer fsm.CloseAll()
	var after func(context.Context, coreapi.ClientSession) error
	if len(cnf.Tasks)+len(cnf.Hosts) > 0 {
		after = func(ctx context.Context, s coreapi.ClientSession) error { return uploadConfig(ctx, s, cnf, fsm) }
	}
	return runCoreProfile(ctx, cnf.CommonConfig, false, after)
}

// uploadConfig sends the tunnels and hosts of cnf on a new stream of s, and
// reports the file servers it started as the session's virtual services.
func uploadConfig(ctx context.Context, s coreapi.ClientSession, cnf *config.Config, fsm *FileServerManager) error {
	up, ok := s.(coreapi.ConfigUploader)
	if !ok {
		return errors.New("config upload is not available on this session")
	}
	nc, err := up.OpenConfigUpload(ctx)
	if err != nil {
		return fmt.Errorf("config upload failed: %w", err)
	}
	c := conn.NewConn(nc)
	defer c.Close()
	vkey, err := exchangeConfig(c, cnf, fsm, time.Now().Add(configExchangeTimeout(cnf)))
	if err != nil {
		return fmt.Errorf("config exchange failed: %w", err)
	}
	logWebAccess(cnf, vkey)
	services := fsm.Services()
	if len(services) == 0 {
		return nil
	}
	host, ok := s.(coreapi.ServiceHost)
	if !ok {
		return errors.New("virtual services are not available on this session")
	}
	if err := host.PublishServices(ctx, services); err != nil {
		return fmt.Errorf("reporting virtual services failed: %w", err)
	}
	return nil
}
