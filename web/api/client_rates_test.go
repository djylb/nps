package api

import (
	"bytes"
	"encoding/json"
	"io"
	"testing"
	"testing/synctest"
	"time"

	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/rate"
	webservice "github.com/djylb/nps/web/service"
)

type clientSpeedConn struct{ *bytes.Reader }

func (clientSpeedConn) Write(p []byte) (int, error) { return len(p), nil }
func (clientSpeedConn) Close() error                { return nil }

type clientSpeedService struct {
	webservice.ClientService
	client *file.Client
}

func (s clientSpeedService) List(webservice.ListClientsInput) webservice.ListClientsResult {
	return webservice.ListClientsResult{Rows: []*file.Client{s.client}, Total: 1, BridgeIP: "127.0.0.1", BridgeAddr: "127.0.0.1:8024", BridgeType: "tcp", BridgePort: 8024}
}

type clientSpeedContext struct{ rateResponseContext }

func (*clientSpeedContext) Actor() *Actor { return UserActor("user", []int{3}) }
func (*clientSpeedContext) Host() string  { return "localhost" }

func TestClientListDirectionalRatesAndLegacyJSON(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		client := &file.Client{Id: 3, Remark: "legacy", RateLimit: 64, Rate: rate.NewRate(64 * 1024), Flow: &file.Flow{}, Cnf: &file.Config{}}
		conn := rate.NewRateConn(clientSpeedConn{bytes.NewReader(make([]byte, 1234))}, client.Rate)
		if _, err := conn.Write(make([]byte, 5678)); err != nil {
			t.Fatal(err)
		}
		if _, err := io.ReadFull(conn, make([]byte, 1234)); err != nil {
			t.Fatal(err)
		}
		if err := conn.Close(); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Second)
		app := &App{Services: webservice.Services{Clients: clientSpeedService{client: client}, Authz: webservice.DefaultAuthorizationService{}}}
		c := &clientSpeedContext{rateResponseContext: rateResponseContext{rateInputContext: rateInputContext{values: map[string]string{}}}}
		app.ListClients(c)
		response := c.response.(ClientListResponse)
		rows := response.Rows.([]*clientRateRow)
		if len(rows) != 1 || rows[0].UploadRate != 5678 || rows[0].DownloadRate != 1234 {
			t.Fatalf("client speeds: %+v", rows)
		}
		if response.Total != 1 || response.BridgePort != 8024 || response.Traffic != nil {
			t.Fatalf("response metadata: %+v", response)
		}
		assertRateJSON(t, client, rows[0], 5678, 1234)
		data, err := json.Marshal(response)
		if err != nil {
			t.Fatal(err)
		}
		var legacy struct {
			Rows  []*file.Client `json:"rows"`
			Total int            `json:"total"`
		}
		if err = json.Unmarshal(data, &legacy); err != nil {
			t.Fatal(err)
		}
		if legacy.Total != 1 || len(legacy.Rows) != 1 || legacy.Rows[0].RateLimit != 64 || legacy.Rows[0].Remark != "legacy" {
			t.Fatalf("legacy JSON: %s", data)
		}
		var raw struct {
			Rows []map[string]json.RawMessage `json:"rows"`
		}
		if err = json.Unmarshal(data, &raw); err != nil {
			t.Fatal(err)
		}
		var oldRate struct{ NowRate, Limit int64 }
		if err = json.Unmarshal(raw.Rows[0]["Rate"], &oldRate); err != nil {
			t.Fatal(err)
		}
		if oldRate.NowRate != 6912 || oldRate.Limit != 64*1024 || client.Rate.Limit() != 64*1024 {
			t.Fatalf("combined rate changed: %+v", oldRate)
		}
	})
}

func TestClientRateRowsEmptyAndNil(t *testing.T) {
	if clientRateRows(nil) != nil {
		t.Fatal("nil rows changed")
	}
	if rows := clientRateRows([]*file.Client{}); rows == nil || len(rows) != 0 {
		t.Fatal("empty rows changed")
	}
	rows := clientRateRows([]*file.Client{nil, {Id: 1}})
	if rows[0] != nil || rows[1].UploadRate != 0 || rows[1].DownloadRate != 0 {
		t.Fatal("nil client/rate not handled")
	}
}
