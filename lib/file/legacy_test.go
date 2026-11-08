package file

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/djylb/nps/lib/model"
)

func TestLoadConvertsLegacyTaskModes(t *testing.T) {
	dir := t.TempDir()
	writeConf(t, dir, "clients.json", `[{"Id":1,"VerifyKey":"k1","Status":true,"Cnf":{},"Flow":{}}]`)
	writeConf(t, dir, "tasks.json", `[
  {"Id":1,"Mode":"p2p","Password":"pw","Client":{"Id":1},"Flow":{},"Target":{"TargetStr":"127.0.0.1:22"}},
  {"Id":2,"Port":1080,"Mode":"socks5","Client":{"Id":1},"Flow":{},"Target":{}}
]`)
	writeConf(t, dir, "hosts.json", `[]`)
	writeConf(t, dir, "global.json", `{}`)
	db := loadAll(dir)
	v, _ := db.Tasks.Load(1)
	if task := v.(*model.Tunnel); task.Mode != "secret" || task.Password != "pw" {
		t.Fatalf("p2p task = %s %q", task.Mode, task.Password)
	}
	v, _ = db.Tasks.Load(2)
	if task := v.(*model.Tunnel); task.Mode != "mixProxy" || !task.Socks5Proxy {
		t.Fatalf("socks5 task = %+v", task)
	}
	b, err := os.ReadFile(filepath.Join(dir, "conf", "tasks.json"))
	if err != nil {
		b, err = os.ReadFile(filepath.Join(dir, "tasks.json"))
	}
	if err != nil || strings.Contains(string(b), `"p2p"`) || strings.Contains(string(b), `"socks5"`) {
		t.Fatalf("converted tunnels not written back (%v): %s", err, b)
	}
}
