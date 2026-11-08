package client

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/djylb/nps/internal/coreproto/coreapi"
	"github.com/djylb/nps/lib/config"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/model"
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
	cnf.ConvertFileTunnels()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	fsm := NewFileServerManager(ctx)
	defer fsm.CloseAll()
	if len(cnf.Tasks)+len(cnf.Hosts) == 0 {
		return runCoreProfile(ctx, cnf.CommonConfig, false, nil)
	}
	var issued string
	err := runCoreProfile(ctx, cnf.CommonConfig, false, func(ctx context.Context, s coreapi.ClientSession) error {
		vkey, err := uploadConfig(ctx, s, cnf, fsm)
		if err != nil {
			return err
		}
		if vkey != cnf.CommonConfig.VKey {
			// The public key: the server made a client with this key, and
			// the session of the public key has done its work.
			issued = vkey
			_ = s.Close()
			return nil
		}
		return publishFiles(ctx, s, fsm)
	})
	if err != nil || issued == "" || ctx.Err() != nil {
		return err
	}
	run := *cnf.CommonConfig
	run.VKey = issued
	return runCoreProfile(ctx, &run, false, func(ctx context.Context, s coreapi.ClientSession) error {
		return publishFiles(ctx, s, fsm)
	})
}

// uploadConfig sends the tunnels and hosts of cnf on a new stream of s, and
// starts the file servers it declares. It returns the key to run with.
func uploadConfig(ctx context.Context, s coreapi.ClientSession, cnf *config.Config, fsm *FileServerManager) (string, error) {
	up, ok := s.(coreapi.ConfigUploader)
	if !ok {
		return "", errors.New("config upload is not available on this session")
	}
	// A virtual backend is local: the session reports it, it is not uploaded.
	send := *cnf
	send.Tasks = nil
	for _, t := range cnf.Tasks {
		if !t.IsVirtualBackend() {
			send.Tasks = append(send.Tasks, t)
		} else if t.ServiceKind == model.FileServiceKind {
			fsm.StartFileServer(t, cnf.CommonConfig.VKey)
		}
	}
	nc, err := up.OpenConfigUpload(ctx)
	if err != nil {
		return "", fmt.Errorf("config upload failed: %w", err)
	}
	c := conn.NewConn(nc)
	defer c.Close()
	vkey, err := exchangeConfig(c, &send, fsm, time.Now().Add(configExchangeTimeout(&send)))
	if err != nil {
		return "", fmt.Errorf("config exchange failed: %w", err)
	}
	logWebAccess(cnf, vkey)
	return vkey, nil
}

// publishFiles reports the file servers as the session's virtual services.
func publishFiles(ctx context.Context, s coreapi.ClientSession, fsm *FileServerManager) error {
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
