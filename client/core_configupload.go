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

// errKeyIssued ends the session of the public key: the server made a client,
// and the session of that client's own key does the rest.
var errKeyIssued = errors.New("client key issued")

// runCoreConfig hosts cnf's profile on the new protocol and, when the file
// defines tunnels or hosts, uploads them on every session it makes; a failed
// upload ends the session and is the error, as a failed exchange is on the
// old protocol. Like RunConfigOnce it returns when the session ends. issued
// is the key the server gave for the public key in an earlier run: it is used
// instead of that key, which would make another client. The key to pass to
// the next run is returned.
func runCoreConfig(ctx context.Context, cnf *config.Config, issued string) (string, error) {
	cnf.ConvertFileTunnels()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	fsm := NewFileServerManager(ctx)
	defer fsm.CloseAll()
	if len(cnf.Tasks)+len(cnf.Hosts) == 0 {
		return issued, runCoreProfile(ctx, cnf.CommonConfig, false, nil)
	}
	withKey := func(key string) *config.Config {
		run, common := *cnf, *cnf.CommonConfig
		common.VKey, run.CommonConfig = key, &common
		return &run
	}
	if issued != "" {
		cnf = withKey(issued)
	}
	publish := func(ctx context.Context, s coreapi.ClientSession, h *coreHost) error {
		return h.publish(ctx, s, fsm.Services())
	}
	var got string
	err := runCoreProfile(ctx, cnf.CommonConfig, false, func(ctx context.Context, s coreapi.ClientSession, h *coreHost) error {
		vkey, err := uploadConfig(ctx, s, cnf, fsm)
		if err != nil {
			return err
		}
		if vkey != cnf.CommonConfig.VKey {
			got = vkey
			return errKeyIssued
		}
		return publish(ctx, s, h)
	})
	if got == "" {
		return issued, err
	}
	if ctx.Err() != nil {
		return got, nil
	}
	return got, runCoreProfile(ctx, withKey(got).CommonConfig, false, publish)
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
