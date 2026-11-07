package bridge

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/djylb/nps/internal/fieldtest"
	"github.com/djylb/nps/lib/model"
)

var updateGolden = flag.Bool("update", false, "rewrite the golden files of the differential tests")

// sentTask is a task as a client could send it, with every field set.
func sentTask() *model.Tunnel {
	t := new(model.Tunnel)
	fieldtest.Fill(t)
	t.Routes = nil
	return t
}

// TestConfigPortTaskDifferential compares the entries made of a task with
// every field set with the golden file written by the earlier implementation.
func TestConfigPortTaskDifferential(t *testing.T) {
	client := model.NewClient("vk", false, false)
	client.Id = 3
	got := map[string]string{}
	for name, ports := range map[string][2][]int{
		"single": {{8000}, {80}},
		"multi":  {{8000, 8001}, {80, 81}},
	} {
		for _, addr := range []string{"", "10.0.0.1"} {
			for i := range ports[0] {
				task := sentTask()
				task.TargetAddr = addr
				tl := configPortTask(task, client, 42, ports[0], ports[1], i)
				b, err := json.Marshal(tl)
				if err != nil {
					t.Fatal(err)
				}
				got[name+"/"+addr+"/"+string(rune('0'+i))] = string(b)
			}
		}
	}
	path := filepath.Join("testdata", "config_task.golden")
	if *updateGolden {
		b, _ := json.MarshalIndent(got, "", "  ")
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var want map[string]string
	if err := json.Unmarshal(b, &want); err != nil {
		t.Fatal(err)
	}
	for k, w := range want {
		if got[k] != w {
			t.Errorf("%s:\n got %s\nwant %s", k, got[k], w)
		}
	}
	if len(got) != len(want) {
		t.Errorf("got %d entries, want %d", len(got), len(want))
	}
}
