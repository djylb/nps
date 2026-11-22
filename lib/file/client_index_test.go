package file

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/djylb/nps/lib/model"
)

func clientTunnelIDs(d *DbUtils, id int) (got []int) {
	d.RangeClientTunnels(id, func(t *model.Tunnel) bool { got = append(got, t.Id); return true })
	return
}

func clientHostIDs(d *DbUtils, id int) (got []int) {
	d.RangeClientHosts(id, func(h *model.Host) bool { got = append(got, h.Id); return true })
	return
}

// scanned returns what a full scan finds, which the index must match.
func scanned(d *DbUtils, id int) (tunnels, hosts []int) {
	d.RangeTunnels(func(t *model.Tunnel) bool {
		if t.Client != nil && t.Client.Id == id {
			tunnels = append(tunnels, t.Id)
		}
		return true
	})
	d.RangeHosts(func(h *model.Host) bool {
		if h.Client != nil && h.Client.Id == id {
			hosts = append(hosts, h.Id)
		}
		return true
	})
	slices.Sort(tunnels)
	slices.Sort(hosts)
	return
}

func checkIndex(t *testing.T, d *DbUtils, ids ...int) {
	t.Helper()
	for _, id := range ids {
		wt, wh := scanned(d, id)
		if gt := clientTunnelIDs(d, id); !slices.Equal(gt, wt) {
			t.Fatalf("client %d tunnels %v, want %v", id, gt, wt)
		}
		if gh := clientHostIDs(d, id); !slices.Equal(gh, wh) {
			t.Fatalf("client %d hosts %v, want %v", id, gh, wh)
		}
		if n := len(d.JsonDb.tunnelClients.byClient[id]); n != len(wt) {
			t.Fatalf("client %d: %d tunnel ids indexed, want %d", id, n, len(wt))
		}
		if n := len(d.JsonDb.hostClients.byClient[id]); n != len(wh) {
			t.Fatalf("client %d: %d host ids indexed, want %d", id, n, len(wh))
		}
	}
}

func TestClientEntryIndex(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "conf"), 0o755); err != nil {
		t.Fatal(err)
	}
	d := New(root)
	a := &model.Client{Id: 1, VerifyKey: "a", Status: true, Cnf: &model.Config{}}
	b := &model.Client{Id: 2, VerifyKey: "b", Status: true, Cnf: &model.Config{}}
	for _, c := range []*model.Client{a, b} {
		if err := d.NewClient(c); err != nil {
			t.Fatal(err)
		}
	}
	for i := 1; i <= 3; i++ {
		if err := d.NewTask(&model.Tunnel{Id: i, Mode: "tcp", Port: 10000 + i, Client: a, Target: &model.Target{}}); err != nil {
			t.Fatal(err)
		}
		if err := d.NewHost(&model.Host{Id: i, Host: "h" + string(rune('0'+i)) + ".test", Client: a, Target: &model.Target{}}); err != nil {
			t.Fatal(err)
		}
	}
	checkIndex(t, d, 1, 2)

	// a copy stored in place of the tunnel moves it
	moved := &model.Tunnel{Id: 2, Mode: "tcp", Port: 10002, Client: b, Target: &model.Target{}}
	if err := d.UpdateTask(moved); err != nil {
		t.Fatal(err)
	}
	// a change in place, announced
	tn, _ := d.GetTask(3)
	tn.Lock()
	tn.Client = b
	tn.Unlock()
	d.TunnelChanged(tn)
	h, _ := d.GetHostById(1)
	h.Lock()
	h.Client = b
	h.Unlock()
	if err := d.SaveHost(h, ""); err != nil {
		t.Fatal(err)
	}
	checkIndex(t, d, 1, 2)
	if got := clientTunnelIDs(d, 2); !slices.Equal(got, []int{2, 3}) {
		t.Fatalf("moved tunnels %v", got)
	}

	if err := d.DelTask(2); err != nil {
		t.Fatal(err)
	}
	if err := d.DelHost(1); err != nil {
		t.Fatal(err)
	}
	if err := d.NewTask(&model.Tunnel{Id: 7, Mode: "tcp", Port: 10007, Client: b, Target: &model.Target{}}); err != nil {
		t.Fatal(err)
	}
	checkIndex(t, d, 1, 2)

	// a change not yet announced is filtered out by the owner check
	tn.Lock()
	tn.Client = a
	tn.Unlock()
	if got := clientTunnelIDs(d, 2); !slices.Equal(got, []int{7}) {
		t.Fatalf("unannounced move still listed: %v", got)
	}
	d.TunnelChanged(tn)
	checkIndex(t, d, 1, 2)

	// the index is rebuilt from the files
	re, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	checkIndex(t, re, 1, 2)
	if got := clientTunnelIDs(re, 1); len(got) == 0 {
		t.Fatal("no tunnels indexed after the load")
	}
}
