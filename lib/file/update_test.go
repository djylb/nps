package file

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/djylb/nps/lib/model"
)

func TestRateLimitsAcceptedCloneRetainsBuckets(t *testing.T) {
	db := newTestDb(t)
	original := &model.Tunnel{Id: 1, Mode: "tcp"}
	original.SetRateLimits(10, 20)
	if err := db.NewTask(original); err != nil {
		t.Fatal(err)
	}
	buckets := original.RateLimiter()
	clone := original.Clone()
	clone.SetRateLimits(30, 40)
	if original.UploadLimit != 10 {
		t.Fatal("editing clone modified original")
	}
	if err := db.UpdateTask(clone); err != nil {
		t.Fatal(err)
	}
	if clone.RateLimiter() != buckets {
		t.Fatal("accepted edit disconnected active rate buckets")
	}
	clone.ClearDownloadLimit()
	if clone.UploadLimit != 30 || clone.DownloadLimit != 0 {
		t.Fatal("clear changed opposite limit")
	}
}

func TestTunnelConnectionLimitAcrossEdit(t *testing.T) {
	task := &model.Tunnel{MaxConn: 3}
	db := newTestDb(t)
	if err := db.NewTask(task); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if !task.GetConn() {
			t.Fatal("initial reservation")
		}
	}
	clone := task.Clone()
	clone.SetMaxConn(1)
	if err := db.UpdateTask(clone); err != nil {
		t.Fatal(err)
	}
	if clone.GetConn() {
		t.Fatal("edit forgot existing connections")
	}
	for range 2 {
		task.CutConn()
	}
	if clone.GetConn() {
		t.Fatal("lowering limit admitted while at capacity")
	}
	task.CutConn()
	if !clone.GetConn() {
		t.Fatal("old connection did not release shared slot")
	}
	if task.CurrentConnections() != 1 {
		t.Fatal("edited tunnel has independent count")
	}
	clone.CutConn()
	data, err := json.Marshal(clone)
	if err != nil {
		t.Fatal(err)
	}
	var loaded model.Tunnel
	if err = json.Unmarshal(data, &loaded); err != nil {
		t.Fatal(err)
	}
	if loaded.MaxConn != 1 || loaded.CurrentConnections() != 0 {
		t.Fatal("configuration/runtime persistence mismatch")
	}
}

func TestClientFlowsMarked(t *testing.T) {
	t.Cleanup(func() { model.SetFlowLimitActual(false) })
	model.SetFlowLimitActual(true)
	db := &DbUtils{JsonDb: NewJsonDb(t.TempDir())}
	c := &model.Client{Id: 3, VerifyKey: "vk-marked", Cnf: new(model.Config), Flow: &model.Flow{FlowLimit: 1, InletFlow: 600 << 10}}
	if err := db.NewClient(c); err != nil && !strings.Contains(err.Error(), "store") {
		t.Fatal(err)
	}
	if traffic, _ := c.Flow.Over(); !traffic {
		t.Fatal("a new client's flow not marked")
	}
	u := &model.Client{Id: 3, VerifyKey: "vk-marked", Cnf: new(model.Config), Flow: &model.Flow{FlowLimit: 1, InletFlow: 600 << 10}}
	_ = db.UpdateClient(u)
	if traffic, _ := u.Flow.Over(); !traffic {
		t.Fatal("an updated client's flow not marked")
	}
}

func TestUpdateTaskKeepsFlowOfActiveConnections(t *testing.T) {
	db := useFlowTestDb(t)
	stored := &model.Tunnel{Id: 5, Mode: "tcp", Flow: &model.Flow{}, Target: &model.Target{}, Client: &model.Client{Id: 1, Cnf: &model.Config{}, Flow: &model.Flow{}}}
	db.JsonDb.Tasks.Store(stored.Id, stored)
	active := stored.Flow
	edited := stored.Clone()
	edited.Flow.SetFlowLimit(7)
	if err := db.UpdateTask(edited); err != nil {
		t.Fatal(err)
	}
	if edited.Flow != active {
		t.Fatal("the edited task counts on another flow than its active connections")
	}
	if active.Values().FlowLimit != 7 {
		t.Fatalf("flow limit = %d", active.Values().FlowLimit)
	}
}

func useFlowTestDb(t *testing.T) *DbUtils {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "conf"), 0o755); err != nil {
		t.Fatal(err)
	}
	return New(root)
}
