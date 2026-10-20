package api

import (
	"encoding/json"
	"reflect"
	"strconv"
	"testing"

	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/servercfg"
	webservice "github.com/djylb/nps/web/service"
)

type rateInputContext struct {
	Context
	admin  bool
	values map[string]string
}

func (c rateInputContext) String(k string) string     { return c.values[k] }
func (c rateInputContext) Int(k string, _ ...int) int { v, _ := strconv.Atoi(c.values[k]); return v }
func (c rateInputContext) Bool(string, ...bool) bool  { return false }
func (c rateInputContext) SessionValue(k string) interface{} {
	if k == "isAdmin" {
		return c.admin
	}
	return nil
}

func TestIndexRateLimitInputsAndFeaturePolicy(t *testing.T) {
	// Like client rate_limit, allow_rate_limit controls presentation; it does
	// not strip submitted limits or grant administrator privileges.
	for _, enabled := range []bool{false, true} {
		for _, admin := range []bool{false, true} {
			cfg := &servercfg.Snapshot{Feature: servercfg.FeatureConfig{AllowRateLimit: enabled}}
			c := rateInputContext{admin: admin, values: map[string]string{"upload_limit": "123", "download_limit": "-45"}}
			inputs := []interface{}{addTunnelInput(c, cfg), editTunnelInput(c, cfg), addHostInput(c, cfg), editHostInput(c, cfg)}
			for _, input := range inputs {
				v := reflect.ValueOf(input)
				if v.FieldByName("UploadLimit").Int() != 123 || v.FieldByName("DownloadLimit").Int() != -45 {
					t.Fatalf("%T: limits not parsed", input)
				}
				if v.FieldByName("IsAdmin").Bool() != admin {
					t.Fatalf("%T: admin changed", input)
				}
			}
		}
	}
}

func assertRateJSON(t *testing.T, original, wrapped interface{}, upload, download int64) {
	t.Helper()
	decode := func(v interface{}) map[string]interface{} {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]interface{}
		if err = json.Unmarshal(b, &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	base, got := decode(original), decode(wrapped)
	if got["UploadRate"] != float64(upload) || got["DownloadRate"] != float64(download) {
		t.Fatalf("rates: %v", got)
	}
	delete(got, "UploadRate")
	delete(got, "DownloadRate")
	if !reflect.DeepEqual(base, got) {
		t.Fatalf("original JSON fields changed: base=%v wrapped=%v", base, got)
	}
	for _, key := range []string{"UploadRate", "DownloadRate"} {
		if _, exists := base[key]; exists {
			t.Fatalf("%s leaked into original JSON", key)
		}
	}
}

func TestIndexRateRowsPreserveJSON(t *testing.T) {
	tunnel := &file.Tunnel{Id: 7, Remark: "tunnel", Port: 1234, Flow: &file.Flow{}}
	host := &file.Host{Id: 8, Host: "example.com", Remark: "host", Flow: &file.Flow{}}
	tunnel.SetRateLimits(12, 34)
	host.SetRateLimits(56, 78)
	tr := tunnelRateRows([]*file.Tunnel{tunnel, nil})
	hr := hostRateRows([]*file.Host{host, nil})
	u, d := tunnel.CurrentRates()
	assertRateJSON(t, tunnel, tr[0], u, d)
	u, d = host.CurrentRates()
	assertRateJSON(t, host, hr[0], u, d)
	if tr[1] != nil || hr[1] != nil {
		t.Fatal("nil rows changed")
	}
	if tunnelRateRows(nil) != nil || hostRateRows(nil) != nil {
		t.Fatal("nil slice changed")
	}
	if len(tunnelRateRows([]*file.Tunnel{})) != 0 || len(hostRateRows([]*file.Host{})) != 0 {
		t.Fatal("empty slice changed")
	}
	// Nonzero values must serialize without unit conversion or truncation.
	assertRateJSON(t, tunnel, &tunnelRateRow{Tunnel: tunnel, UploadRate: 12345, DownloadRate: 67890}, 12345, 67890)
	assertRateJSON(t, host, &hostRateRow{Host: host, UploadRate: 98765, DownloadRate: 43210}, 98765, 43210)
}

func TestIndexRateLimitActionsRetainPermissions(t *testing.T) {
	expected := map[string]struct {
		permission string
		ownership  ActionOwnership
	}{
		"add":       {webservice.PermissionTunnelsCreate, ActionOwnershipNone},
		"edit":      {webservice.PermissionTunnelsUpdate, ActionOwnershipTunnel},
		"clear":     {webservice.PermissionTunnelsControl, ActionOwnershipTunnel},
		"addhost":   {webservice.PermissionHostsCreate, ActionOwnershipNone},
		"edithost":  {webservice.PermissionHostsUpdate, ActionOwnershipHost},
		"clearhost": {webservice.PermissionHostsControl, ActionOwnershipHost},
	}
	for _, spec := range ProtectedActionCatalog(&App{}) {
		if spec.Controller != "index" {
			continue
		}
		want, ok := expected[spec.Name]
		if !ok {
			continue
		}
		if !spec.Protected || spec.Permission != want.permission || spec.Ownership != want.ownership {
			t.Fatalf("authorization changed: %+v", spec)
		}
		delete(expected, spec.Name)
	}
	if len(expected) != 0 {
		t.Fatalf("missing actions: %v", expected)
	}
}

type rateResponseContext struct {
	rateInputContext
	response interface{}
}

func (c *rateResponseContext) RespondJSON(_ int, v interface{}) { c.response = v }

type rateListService struct {
	webservice.IndexService
	tunnel    *file.Tunnel
	host      *file.Host
	clearID   int
	clearMode string
}

func (s *rateListService) ListTunnels(webservice.TunnelListInput) ([]*file.Tunnel, int) {
	return []*file.Tunnel{s.tunnel}, 17
}
func (s *rateListService) ListHosts(webservice.HostListInput) ([]*file.Host, int) {
	return []*file.Host{s.host}, 18
}
func (s *rateListService) ClearTunnel(id int, mode string) error {
	s.clearID = id
	s.clearMode = mode
	return nil
}
func (s *rateListService) ClearHost(id int, mode string) error {
	s.clearID = id
	s.clearMode = mode
	return nil
}

func TestIndexRateListHandlers(t *testing.T) {
	s := &rateListService{tunnel: &file.Tunnel{Id: 7, Remark: "tunnel"}, host: &file.Host{Id: 8, Host: "example.com"}}
	app := &App{Services: webservice.Services{Index: s}}
	c := &rateResponseContext{rateInputContext: rateInputContext{values: map[string]string{}}}
	app.GetTunnel(c)
	table := c.response.(TableResponse)
	if table.Total != 17 {
		t.Fatalf("total=%d", table.Total)
	}
	rows := table.Rows.([]*tunnelRateRow)
	if len(rows) != 1 || rows[0].Tunnel != s.tunnel {
		t.Fatal("tunnel fields changed")
	}
	u, d := s.tunnel.CurrentRates()
	assertRateJSON(t, s.tunnel, rows[0], u, d)
	app.HostList(c)
	table = c.response.(TableResponse)
	if table.Total != 18 {
		t.Fatalf("total=%d", table.Total)
	}
	hosts := table.Rows.([]*hostRateRow)
	if len(hosts) != 1 || hosts[0].Host != s.host {
		t.Fatal("host fields changed")
	}
	u, d = s.host.CurrentRates()
	assertRateJSON(t, s.host, hosts[0], u, d)
}

func TestIndexRateClearHandlers(t *testing.T) {
	s := &rateListService{}
	app := &App{Services: webservice.Services{Index: s}}
	for _, mode := range []string{"upload_limit", "download_limit"} {
		for _, handler := range []func(Context){app.ClearTunnel, app.ClearHost} {
			c := &rateResponseContext{rateInputContext: rateInputContext{values: map[string]string{"id": "42", "mode": mode}}}
			handler(c)
			if s.clearID != 42 || s.clearMode != mode {
				t.Fatalf("clear=%d,%s", s.clearID, s.clearMode)
			}
			if c.response.(StatusMessageResponse).Status != 1 {
				t.Fatalf("response=%+v", c.response)
			}
		}
	}
}

// Existing consumers decode rows directly into file models. Extra speed fields
// must remain additive, including when the measured speed is zero.
func TestIndexRateTableLegacyJSONCompatibility(t *testing.T) {
	tunnel := &file.Tunnel{Id: 7, Mode: "tcp", Port: 1234, Remark: "legacy tunnel", Client: &file.Client{Id: 3}, Target: &file.Target{TargetStr: "127.0.0.1:80"}, Flow: &file.Flow{InletFlow: 123, ExportFlow: 456}}
	host := &file.Host{Id: 8, Host: "example.com", Location: "/api", Remark: "legacy host", Client: &file.Client{Id: 3}, Target: &file.Target{TargetStr: "127.0.0.1:81"}, Flow: &file.Flow{InletFlow: 789, ExportFlow: 321}}
	tunnel.SetRateLimits(12, 34)
	host.SetRateLimits(56, 78)
	for _, tc := range []struct {
		name     string
		rows     interface{}
		original interface{}
		legacy   interface{}
	}{
		{"tunnel", tunnelRateRows([]*file.Tunnel{tunnel}), tunnel, new(struct {
			Rows  []*file.Tunnel `json:"rows"`
			Total int            `json:"total"`
		})},
		{"host", hostRateRows([]*file.Host{host}), host, new(struct {
			Rows  []*file.Host `json:"rows"`
			Total int          `json:"total"`
		})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data, err := json.Marshal(TableResponse{Rows: tc.rows, Total: 19})
			if err != nil {
				t.Fatal(err)
			}
			var wire struct {
				Rows  []map[string]json.RawMessage `json:"rows"`
				Total int                          `json:"total"`
			}
			if err = json.Unmarshal(data, &wire); err != nil {
				t.Fatal(err)
			}
			if wire.Total != 19 || len(wire.Rows) != 1 {
				t.Fatalf("table changed: %s", data)
			}
			for _, key := range []string{"UploadRate", "DownloadRate"} {
				if string(wire.Rows[0][key]) != "0" {
					t.Fatalf("%s must be present as zero: %s", key, data)
				}
			}
			if err = json.Unmarshal(data, tc.legacy); err != nil {
				t.Fatalf("legacy decoder: %v", err)
			}
			legacyRow := reflect.ValueOf(tc.legacy).Elem().FieldByName("Rows").Index(0).Interface()
			before, err := json.Marshal(tc.original)
			if err != nil {
				t.Fatal(err)
			}
			after, err := json.Marshal(legacyRow)
			if err != nil {
				t.Fatal(err)
			}
			if string(before) != string(after) {
				t.Fatalf("legacy fields changed: before=%s after=%s", before, after)
			}
		})
	}
}
