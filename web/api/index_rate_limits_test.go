package api

import (
	"encoding/json"
	"reflect"
	"strconv"
	"testing"

	"github.com/djylb/nps/lib/model"

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
func (c rateInputContext) SessionValue(k string) any {
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
			ti, hi := tunnelInput(c, cfg), hostInput(c, cfg)
			// Negative limits are stored as zero.
			if ti.Tunnel.UploadLimit != 123 || ti.Tunnel.DownloadLimit != 0 || hi.Host.UploadLimit != 123 || hi.Host.DownloadLimit != 0 {
				t.Fatal("limits not parsed")
			}
			if ti.IsAdmin != admin || hi.IsAdmin != admin {
				t.Fatal("admin changed")
			}
		}
	}
}

func assertRateJSON(t *testing.T, original, wrapped any, upload, download int64) {
	t.Helper()
	decode := func(v any) map[string]any {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
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
	tunnel := &model.Tunnel{Id: 7, Remark: "tunnel", Port: 1234, Flow: &model.Flow{}}
	host := &model.Host{Id: 8, Host: "example.com", Remark: "host", Flow: &model.Flow{}}
	tunnel.SetRateLimits(12, 34)
	host.SetRateLimits(56, 78)
	tr := tunnelRateRows([]*model.Tunnel{tunnel, nil})
	hr := hostRateRows([]*model.Host{host, nil})
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
	if len(tunnelRateRows([]*model.Tunnel{})) != 0 || len(hostRateRows([]*model.Host{})) != 0 {
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
	response any
}

func (c *rateResponseContext) RespondJSON(_ int, v any) { c.response = v }

type rateListService struct {
	webservice.IndexService
	tunnel    *model.Tunnel
	host      *model.Host
	clearID   int
	clearMode string
}

func (s *rateListService) ListTunnels(webservice.TunnelListInput) ([]*model.Tunnel, int) {
	return []*model.Tunnel{s.tunnel}, 17
}
func (s *rateListService) ListHosts(webservice.HostListInput) ([]*model.Host, int) {
	return []*model.Host{s.host}, 18
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
	s := &rateListService{tunnel: &model.Tunnel{Id: 7, Remark: "tunnel"}, host: &model.Host{Id: 8, Host: "example.com"}}
	app := &App{Services: webservice.Services{Index: s}}
	c := &rateResponseContext{values: map[string]string{}}
	app.GetTunnel(c)
	table := c.response.(TableResponse)
	if table.Total != 17 {
		t.Fatalf("total=%d", table.Total)
	}
	rows := table.Rows.([]*tunnelRateRow)
	if len(rows) != 1 || rows[0].Id != s.tunnel.Id || rows[0].Remark != s.tunnel.Remark {
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
	if len(hosts) != 1 || hosts[0].Id != s.host.Id || hosts[0].Host.Host != s.host.Host {
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
			c := &rateResponseContext{values: map[string]string{"id": "42", "mode": mode}}
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
	tunnel := &model.Tunnel{Id: 7, Mode: "tcp", Port: 1234, Remark: "legacy tunnel", Client: &model.Client{Id: 3}, Target: &model.Target{TargetStr: "127.0.0.1:80"}, Flow: &model.Flow{InletFlow: 123, ExportFlow: 456}}
	host := &model.Host{Id: 8, Host: "example.com", Location: "/api", Remark: "legacy host", Client: &model.Client{Id: 3}, Target: &model.Target{TargetStr: "127.0.0.1:81"}, Flow: &model.Flow{InletFlow: 789, ExportFlow: 321}}
	tunnel.SetRateLimits(12, 34)
	host.SetRateLimits(56, 78)
	for _, tc := range []struct {
		name     string
		rows     any
		original any
		legacy   any
	}{
		{"tunnel", tunnelRateRows([]*model.Tunnel{tunnel}), tunnel, new(struct {
			Rows  []*model.Tunnel `json:"rows"`
			Total int             `json:"total"`
		})},
		{"host", hostRateRows([]*model.Host{host}), host, new(struct {
			Rows  []*model.Host `json:"rows"`
			Total int           `json:"total"`
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
