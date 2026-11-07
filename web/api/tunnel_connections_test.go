package api

import (
	"testing"

	"github.com/djylb/nps/lib/model"

	"github.com/djylb/nps/lib/file"
)

func TestTunnelMaxConnInputAndLiveCount(t *testing.T) {
	c := rateInputContext{values: map[string]string{"max_conn": "9"}}
	if tunnelInput(c, nil).Tunnel.MaxConn != 9 {
		t.Fatal("limit input ignored")
	}
	task := &model.Tunnel{MaxConn: 2}
	if !task.GetConn() {
		t.Fatal("reserve")
	}
	clone := task.Clone()
	db := &file.DbUtils{JsonDb: file.NewJsonDb(t.TempDir())}
	db.JsonDb.Tasks.Store(task.Id, task)
	if err := db.UpdateTask(clone); err != nil {
		t.Fatal(err)
	}
	task.CutConn()
	rows := tunnelRateRows([]*model.Tunnel{clone})
	if rows[0].NowConn != 0 {
		t.Fatal("API returned stale edited count")
	}
}
