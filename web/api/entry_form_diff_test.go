package api

import (
	"encoding/json"
	"errors"
	"flag"
	"maps"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/djylb/nps/lib/model"
	"github.com/djylb/nps/lib/servercfg"
	"github.com/djylb/nps/lib/textutil"
	webservice "github.com/djylb/nps/web/service"
)

var updateGolden = flag.Bool("update", false, "rewrite the golden files of the differential tests")

// formContext reads form values as the gin context does.
type formContext struct {
	Context
	admin  bool
	values map[string]string
}

func (c formContext) String(k string) string { return c.values[k] }
func (c formContext) Int(k string, def ...int) int {
	v := strings.TrimSpace(c.values[k])
	if v == "" {
		if len(def) > 0 {
			return def[0]
		}
		return 0
	}
	n, _ := strconv.Atoi(v)
	return n
}
func (c formContext) Bool(k string, def ...bool) bool {
	v := strings.TrimSpace(c.values[k])
	if v == "" {
		if len(def) > 0 {
			return def[0]
		}
		return false
	}
	b, _ := textutil.ParseBool(v)
	return b
}
func (c formContext) SessionValue(k string) any {
	if k == "isAdmin" {
		return c.admin
	}
	return nil
}

type diffRepo struct {
	webservice.Repository
	client *model.Client
	tunnel *model.Tunnel
	host   *model.Host
}

func (r *diffRepo) NextTunnelID() int { return 5 }
func (r *diffRepo) NextHostID() int   { return 6 }
func (r *diffRepo) GetClient(id int) (*model.Client, error) {
	if id == r.client.Id {
		return r.client, nil
	}
	return nil, errors.New("no client")
}
func (r *diffRepo) CreateTunnel(t *model.Tunnel) error                     { r.tunnel = t; return nil }
func (r *diffRepo) GetTunnel(int) (*model.Tunnel, error)                   { return r.tunnel, nil }
func (r *diffRepo) SaveTunnel(t *model.Tunnel) error                       { r.tunnel = t; return nil }
func (r *diffRepo) CreateHost(h *model.Host) error                         { r.host = h; return nil }
func (r *diffRepo) GetHost(int) (*model.Host, error)                       { return r.host, nil }
func (r *diffRepo) SaveHost(h *model.Host, _ string) error                 { r.host = h; return nil }
func (r *diffRepo) HostExists(*model.Host) bool                            { return false }
func (r *diffRepo) DeleteTunnelRecord(int) error                           { return nil }
func (r *diffRepo) ClientOwnsTunnel(int, int) bool                         { return true }
func (r *diffRepo) ClientOwnsHost(int, int) bool                           { return true }
func (r *diffRepo) FindReusableCert(string, int, int) (*model.Host, error) { return nil, nil }

type diffRuntime struct{ webservice.Runtime }

func (diffRuntime) GenerateTunnelPort(*model.Tunnel) int   { return 18080 }
func (diffRuntime) TunnelPortAvailable(*model.Tunnel) bool { return true }
func (diffRuntime) AddTunnel(*model.Tunnel) error          { return nil }
func (diffRuntime) StopTunnel(int) error                   { return nil }
func (diffRuntime) StartTunnel(int) error                  { return nil }
func (diffRuntime) RemoveHostCache(int)                    {}

func diffClient() *model.Client {
	c := model.NewClient("vk", false, false)
	c.Id = 1
	return c
}

func storedTunnel(c *model.Client) *model.Tunnel {
	return &model.Tunnel{
		Id: 1, Port: 9000, Mode: "udp", ServerIp: "0.0.0.0", Client: c, Status: true, Ports: "9000",
		TargetAddr: "10.0.0.1", TargetType: "tcp", ReadOnly: true, Remark: "old", Password: "oldpw",
		LocalPath: "/old", StripPre: "/o", HttpProxy: true, DestAclMode: 2, DestAclRules: "9.9.9.9",
		Flow:         &model.Flow{FlowLimit: 1, ExportFlow: 10, InletFlow: 20},
		Target:       &model.Target{TargetStr: "old:1", ProxyProtocol: 1},
		MultiAccount: &model.MultiAccount{Content: "m"},
		UserAuth:     &model.MultiAccount{Content: "a=b"},
		MaxConn:      4,
		UploadLimit:  1, DownloadLimit: 2,
	}
}

func storedHost(c *model.Client) *model.Host {
	return &model.Host{
		Id: 1, Host: "old.example", Location: "/", Scheme: "https", Client: c, Remark: "old",
		HeaderChange: "a:b", RespHeaderChange: "c:d", HostChange: "h", PathRewrite: "/p", RedirectURL: "u",
		HttpsJustProxy: true, TlsOffload: true, AutoSSL: true, CertFile: "OLDCERT", KeyFile: "OLDKEY",
		AutoHttps: true, AutoCORS: true, CompatMode: true, TargetIsHttps: true, IsClose: true, NoStore: false,
		Flow:         &model.Flow{FlowLimit: 1, ExportFlow: 10, InletFlow: 20},
		Target:       &model.Target{TargetStr: "old:1", ProxyProtocol: 1, LocalProxy: true},
		MultiAccount: &model.MultiAccount{Content: "m"},
		UserAuth:     &model.MultiAccount{Content: "a=b"},
		MaxConn:      4,
		UploadLimit:  1, DownloadLimit: 2,
	}
}

var commonFormFull = map[string]string{
	"client_id": "1", "id": "1", "compress": "ZSTD ",
	"target": " 127.0.0.1:80\r\n127.0.0.1:81 ", "proxy_protocol": "2", "local_proxy": "true",
	"health_check": "true", "health_check_type": "http", "health_check_path": "/h&x<y>",
	"health_check_interval": "5", "health_check_timeout": "3", "health_check_max_fail": "2",
	"auth": "u=p\r\nv=<w>", "remark": "r<&>'\"", "max_conn": "7", "upload_limit": "100",
	"download_limit": "200", "flow_limit": "300", "time_limit": "1700000000", "flow_reset": "true",
	"iplistpriority": "1", "blackiplist": "1.2.3.4", "whiteiplist": "", "registered_only": "true",
}

var commonFormInvalid = map[string]string{
	"proxy_protocol": "abc", "local_proxy": "maybe", "health_check": "yes", "health_check_interval": "x",
	"max_conn": "-5", "upload_limit": "-1", "download_limit": " 12 ", "flow_limit": "1.5",
	"time_limit": "garbage", "flow_reset": "2", "target": "BRIDGE://x", "iplistpriority": "z",
}

var tunnelFormCases = map[string]map[string]string{
	"full": merge(commonFormFull, map[string]string{
		"port": "8080", "server_ip": "1.2.3.4<x>", "type": "tcp", "target_type": "all",
		"password": "pw'\"&", "local_path": "/srv/<a>", "strip_pre": "/s&", "enable_http": "1",
		"enable_socks5": "true", "dest_acl_mode": "1", "dest_acl_rules": "1.2.3.4\r\nexample.com",
	}),
	"empty":       {"client_id": "1"},
	"noclient":    {},
	"otherclient": {"client_id": "2", "type": "tcp"},
	"invalid": merge(commonFormInvalid, map[string]string{
		"client_id": "1", "port": "x", "type": "socks5", "enable_http": "maybe", "enable_socks5": "T",
		"dest_acl_mode": "9", "dest_acl_rules": "",
	}),
	"badacl":  {"client_id": "1", "type": "tcp", "dest_acl_mode": "1", "dest_acl_rules": "not a rule ::: !"},
	"badlist": {"client_id": "1", "type": "tcp", "blackiplist": "not-an-ip"},
}

var hostFormCases = map[string]map[string]string{
	"full": merge(commonFormFull, map[string]string{
		"host": "a.example<b>", "header": "X-A:1\r\nX-B:<2>", "resp_header": "X-C:3", "hostchange": "c.example",
		"location": "/loc", "path_rewrite": "/rw&", "redirect_url": "https://r.example/?a=1&b=2",
		"scheme": " HTTPS ", "https_just_proxy": "true", "tls_offload": "true", "auto_ssl": "1",
		"key_file": "KEY<1>", "cert_file": "CERT&1", "auto_https": "true", "auto_cors": "true",
		"compat_mode": "true", "target_is_https": "true",
	}),
	"empty":       {"client_id": "1"},
	"noclient":    {},
	"otherclient": {"client_id": "2", "host": "x.example"},
	"invalid": merge(commonFormInvalid, map[string]string{
		"client_id": "1", "host": "i.example", "scheme": "ftp", "https_just_proxy": "maybe", "auto_ssl": "0",
		"tls_offload": "x",
	}),
	"badlist": {"client_id": "1", "host": "x.example", "blackiplist": "not-an-ip"},
}

func merge(a, b map[string]string) map[string]string {
	m := make(map[string]string, len(a)+len(b))
	maps.Copy(m, a)
	maps.Copy(m, b)
	return m
}

func snapshotEntry(v any, flow *model.Flow) string {
	if flow != nil {
		flow.TimeLimit = flow.TimeLimit.UTC()
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "marshal: " + err.Error()
	}
	return string(b)
}

// TestEntryFormDifferential records what every form field, missing value and
// invalid value of the tunnel and host forms leads to in the stored entry,
// and compares it with the golden file written by the earlier implementation.
func TestEntryFormDifferential(t *testing.T) {
	got := map[string]string{}
	cfgs := map[string]*servercfg.Snapshot{
		"nocfg":   nil,
		"local":   {Feature: servercfg.FeatureConfig{AllowUserLocal: true}},
		"nolocal": {Feature: servercfg.FeatureConfig{}},
	}
	for name, values := range tunnelFormCases {
		for cfgName, cfg := range cfgs {
			for _, admin := range []bool{false, true} {
				for _, edit := range []bool{false, true} {
					client := diffClient()
					repo := &diffRepo{client: client}
					if edit {
						repo.tunnel = storedTunnel(client)
					}
					svc := webservice.DefaultIndexService{Backend: webservice.Backend{Repository: repo, Runtime: diffRuntime{}}}
					c := formContext{admin: admin, values: values}
					key := "tunnel/" + name + "/" + cfgName + "/admin=" + strconv.FormatBool(admin) + "/edit=" + strconv.FormatBool(edit)
					if err := submitTunnelForm(svc, c, cfg, edit); err != nil {
						got[key] = "error: " + err.Error()
						continue
					}
					got[key] = snapshotEntry(repo.tunnel, repo.tunnel.Flow)
				}
			}
		}
	}
	for name, values := range hostFormCases {
		for cfgName, cfg := range cfgs {
			for _, admin := range []bool{false, true} {
				for _, edit := range []bool{false, true} {
					client := diffClient()
					repo := &diffRepo{client: client}
					if edit {
						repo.host = storedHost(client)
					}
					svc := webservice.DefaultIndexService{Backend: webservice.Backend{Repository: repo, Runtime: diffRuntime{}}}
					c := formContext{admin: admin, values: values}
					key := "host/" + name + "/" + cfgName + "/admin=" + strconv.FormatBool(admin) + "/edit=" + strconv.FormatBool(edit)
					if err := submitHostForm(svc, c, cfg, edit); err != nil {
						got[key] = "error: " + err.Error()
						continue
					}
					got[key] = snapshotEntry(repo.host, repo.host.Flow)
				}
			}
		}
	}
	compareGolden(t, filepath.Join("testdata", "entry_form.golden"), got)
}

func compareGolden(t *testing.T, path string, got map[string]string) {
	t.Helper()
	if *updateGolden {
		b, err := json.MarshalIndent(got, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
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
	keys := make([]string, 0, len(want))
	for k := range want {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if got[k] != want[k] {
			t.Errorf("%s:\n got %s\nwant %s", k, got[k], want[k])
		}
	}
	if len(got) != len(want) {
		t.Errorf("got %d cases, want %d", len(got), len(want))
	}
}

func submitTunnelForm(svc webservice.IndexService, c Context, cfg *servercfg.Snapshot, edit bool) error {
	if edit {
		_, err := svc.EditTunnel(tunnelInput(c, cfg))
		return err
	}
	_, err := svc.AddTunnel(tunnelInput(c, cfg))
	return err
}

func submitHostForm(svc webservice.IndexService, c Context, cfg *servercfg.Snapshot, edit bool) error {
	if edit {
		_, err := svc.EditHost(hostInput(c, cfg))
		return err
	}
	_, err := svc.AddHost(hostInput(c, cfg))
	return err
}
