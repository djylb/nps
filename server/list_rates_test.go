package server

import (
	"bytes"
	"io"
	"reflect"
	"testing"
	"testing/synctest"
	"time"

	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/rate"
)

type speedTestConn struct{ *bytes.Reader }

func (speedTestConn) Write(p []byte) (int, error) { return len(p), nil }
func (speedTestConn) Close() error                { return nil }

func TestListsSortCurrentRatesBeforePaging(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		loadTestData(t,
			`[{"Id":1,"VerifyKey":"a"},{"Id":2,"VerifyKey":"b"},{"Id":3,"VerifyKey":"c"}]`,
			`[{"Id":1,"Port":1,"Mode":"tcp","Client":{"Id":1}},{"Id":2,"Port":2,"Mode":"tcp","Client":{"Id":2}},{"Id":3,"Port":3,"Mode":"tcp","Client":{"Id":3}}]`,
			`[{"Id":1,"Host":"a.test","Client":{"Id":1}},{"Id":2,"Host":"b.test","Client":{"Id":2}},{"Id":3,"Host":"c.test","Client":{"Id":3}}]`)
		for i, profile := range []struct{ upload, download int }{{300, 10}, {100, 400}, {200, 50}} {
			c, _ := file.GetDb().GetClient(i + 1)
			c.Rate = rate.NewRate(0)
			tunnel, _ := file.GetDb().GetTask(i + 1)
			host, _ := file.GetDb().GetHostById(i + 1)
			conn := rate.NewRateConn(speedTestConn{bytes.NewReader(make([]byte, profile.download))}, c.Rate, tunnel.RateLimiter(), host.RateLimiter())
			if _, err := conn.Write(make([]byte, profile.upload)); err != nil {
				t.Fatal(err)
			}
			if _, err := io.ReadFull(conn, make([]byte, profile.download)); err != nil {
				t.Fatal(err)
			}
			if err := conn.Close(); err != nil {
				t.Fatal(err)
			}
		}
		time.Sleep(time.Second)
		for _, tc := range []struct {
			field string
			ids   []int
		}{
			{"speed", []int{3, 1, 2}}, {"upload_rate", []int{2, 3, 1}}, {"download_rate", []int{1, 3, 2}},
			{"UploadRate", []int{2, 3, 1}}, {"DownloadRate", []int{1, 3, 2}},
		} {
			for _, order := range []string{"asc", "desc"} {
				want := append([]int(nil), tc.ids...)
				if order == "desc" {
					want[0], want[2] = want[2], want[0]
				}
				for offset := 0; offset < 3; offset++ {
					tunnels, n := GetTunnel(offset, 1, "tcp", 0, "", tc.field, order)
					if n != 3 || len(tunnels) != 1 || tunnels[0].Id != want[offset] {
						t.Fatalf("tunnels %s %s offset %d: %+v want %d", tc.field, order, offset, tunnels, want[offset])
					}
					hosts, n := GetHostList(offset, 1, 0, "", tc.field, order)
					if n != 3 || len(hosts) != 1 || hosts[0].Id != want[offset] {
						t.Fatalf("hosts %s %s offset %d want %d", tc.field, order, offset, want[offset])
					}
					clients, n := GetClientList(offset, 1, "", tc.field, order, 0)
					if n != 3 || len(clients) != 1 || clients[0].Id != want[offset] {
						t.Fatalf("clients %s %s offset %d want %d; got %+v", tc.field, order, offset, want[offset], clients)
					}
					clients, n = file.GetDb().GetClientList(offset, 1, "", tc.field, order, 0)
					if n != 3 || len(clients) != 1 || clients[0].Id != want[offset] {
						t.Fatalf("db clients %s %s offset %d want %d", tc.field, order, offset, want[offset])
					}
				}
				keys := file.GetMapKeys(&file.GetDb().JsonDb.Clients, true, tc.field, order)
				if !reflect.DeepEqual(keys, want) {
					t.Fatalf("visibility sort %s %s: %v want %v", tc.field, order, keys, want)
				}
			}
		}
		// Client scope and search still filter before pagination.
		hosts, n := GetHostList(0, 1, 2, "b.test", "upload_rate", "desc")
		if n != 1 || len(hosts) != 1 || hosts[0].Id != 2 {
			t.Fatal("host scope/search changed")
		}
		tunnels, n := GetTunnel(0, 1, "tcp", 2, "", "speed", "desc")
		if n != 1 || len(tunnels) != 1 || tunnels[0].Id != 2 {
			t.Fatal("tunnel scope changed")
		}
	})
}

type sampledSpeed struct {
	id               int
	upload, download int64
	reads            int
}

func (s *sampledSpeed) CurrentRates() (int64, int64) { s.reads++; return s.upload, s.download }
func TestSortCurrentRatesSamplesOnceAndKeepsTies(t *testing.T) {
	rows := []*sampledSpeed{{id: 1, upload: 20}, {id: 2, upload: 10}, {id: 3, upload: 20}}
	sortCurrentRates(rows, "upload_rate", "desc")
	if rows[0].id != 1 || rows[1].id != 3 || rows[2].id != 2 {
		t.Fatal("unstable rate sort")
	}
	for _, row := range rows {
		if row.reads != 1 {
			t.Fatalf("sampled %d times", row.reads)
		}
	}
}
