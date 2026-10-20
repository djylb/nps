package service

import (
	"bytes"
	"io"
	"testing"
	"testing/synctest"
	"time"

	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/rate"
)

type visibleRateConn struct{ *bytes.Reader }

func (visibleRateConn) Write(p []byte) (int, error) { return len(p), nil }
func (visibleRateConn) Close() error                { return nil }

func TestVisibleClientsRateSortBeforePaging(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		old := file.GetDb()
		file.Db = &file.DbUtils{JsonDb: &file.JsonDb{}}
		t.Cleanup(func() { file.Db = old })
		for id := 1; id <= 4; id++ {
			client := &file.Client{Id: id, Remark: "visible", Rate: rate.NewRate(0), NoDisplay: id == 4}
			file.Db.JsonDb.Clients.Store(id, client)
			conn := rate.NewRateConn(visibleRateConn{bytes.NewReader(make([]byte, id*100))}, client.Rate)
			if _, err := conn.Write(make([]byte, id*200)); err != nil {
				t.Fatal(err)
			}
			if _, err := io.ReadFull(conn, make([]byte, id*100)); err != nil {
				t.Fatal(err)
			}
			if err := conn.Close(); err != nil {
				t.Fatal(err)
			}
		}
		time.Sleep(time.Second)
		for _, field := range []string{"speed", "upload_rate", "download_rate"} {
			for _, order := range []string{"asc", "desc"} {
				want := []int{1, 3}
				if order == "desc" {
					want = []int{3, 1}
				}
				for offset := 0; offset < 2; offset++ {
					rows, total := (defaultRepository{}).ListVisibleClients(ListClientsInput{Offset: offset, Limit: 1, Sort: field, Order: order, Search: "visible", Visibility: ClientVisibility{PrimaryClientID: 1, ClientIDs: []int{1, 3, 4}}})
					if total != 2 || len(rows) != 1 || rows[0].Id != want[offset] {
						t.Fatalf("%s %s page %d: total=%d rows=%+v want=%d", field, order, offset, total, rows, want[offset])
					}
				}
			}
		}
	})
}
