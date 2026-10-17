package client

import (
	"net"
	"testing"

	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/file"
)

func TestReadTaskAddStatusConsumesEveryPort(t *testing.T) {
	a, b := net.Pipe()
	defer func() { _ = a.Close() }()
	defer func() { _ = b.Close() }()
	server, client := conn.NewConn(a), conn.NewConn(b)

	multi := &file.Tunnel{Mode: "tcp", Ports: "8000-8002", Target: &file.Target{TargetStr: "80-82"}}
	next := &file.Tunnel{Mode: "secret", Ports: "9000", Target: &file.Target{TargetStr: "127.0.0.1:22"}}
	go func() {
		// three statuses for the multi-port task, then two for the secret
		// task (its port plus the password entry), the last one a failure.
		for i := 0; i < 4; i++ {
			_ = server.WriteAddOk()
		}
		_ = server.WriteAddFail()
	}()

	if !readTaskAddStatus(client, multi) {
		t.Fatalf("multi-port task must succeed")
	}
	if readTaskAddStatus(client, next) {
		t.Fatalf("second task must see its own failure status")
	}
}
