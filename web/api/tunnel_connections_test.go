package api

import (
	"testing"

	"github.com/djylb/nps/lib/file"
)

func TestTunnelMaxConnInputAndLiveCount(t *testing.T) {
	c := rateInputContext{values: map[string]string{"max_conn": "9"}}
	if addTunnelInput(c, nil).MaxConn != 9 || editTunnelInput(c, nil).MaxConn != 9 {
		t.Fatal("limit input ignored")
	}
	task := &file.Tunnel{MaxConn: 2}
	if !task.GetConn() {
		t.Fatal("reserve")
	}
	clone := task.Clone()
	db := &file.DbUtils{JsonDb: &file.JsonDb{}}
	db.JsonDb.Tasks.Store(task.Id, task)
	if err := db.UpdateTask(clone); err != nil {
		t.Fatal(err)
	}
	task.CutConn()
	rows := tunnelRateRows([]*file.Tunnel{clone})
	if rows[0].NowConn != 0 {
		t.Fatal("API returned stale edited count")
	}
}
