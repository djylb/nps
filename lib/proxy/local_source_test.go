package proxy

import (
	"net"
	"os"
	"os/exec"
	"testing"

	"github.com/djylb/nps/lib/model"

	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/file"
)

// A proxy running inside npc has no server data and must not open it.
func TestLocalProxyLeavesServerDataAlone(t *testing.T) {
	const child = "NPS_TEST_LOCAL_SOURCE"
	if os.Getenv(child) == "" {
		cmd := exec.Command(os.Args[0], "-test.run=^TestLocalProxyLeavesServerDataAlone$")
		cmd.Env = append(os.Environ(), child+"=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		return
	}
	a, b := net.Pipe()
	defer func() { _ = a.Close(); _ = b.Close() }()
	s := &BaseServer{Bridge: failBridge{}}
	task := &model.Tunnel{Mode: "p2pt", Client: &model.Client{}}
	func() {
		defer func() { _ = recover() }() // only the source check matters here
		_, _, _, _ = s.openClientLink(conn.NewConn(a), task.Client, "127.0.0.1:1", "tcp", false, "", task)
	}()
	if file.Db != nil {
		t.Fatal("the local proxy opened the server data")
	}
}
