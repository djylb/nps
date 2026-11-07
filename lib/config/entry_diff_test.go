package config

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

var updateGolden = flag.Bool("update", false, "rewrite the golden files of the differential tests")

// entryDiffConfig sets every tunnel and host key, with valid, invalid,
// repeated and missing values.
const entryDiffConfig = `[common]
server_addr=127.0.0.1:8024
vkey=k

[tcp_full]
mode=tcp
server_port=10000-10002,10005
server_ip=1.2.3.4
target_addr=127.0.0.1:80,127.0.0.1:81
proxy_protocol=2
target_ip=10.0.0.1
password=pw<&>
socks5_proxy=true
http_proxy=1
dest_acl_mode=1
dest_acl_rules=1.2.3.4,example.com
local_path=/srv
strip_pre=/s
read_only=true
max_conn=7
upload_limit=100
download_limit=200
multi_account=testdata/accounts.txt
unknown_key=x

[tcp_invalid]
mode=udp
server_port=abc
proxy_protocol=x
socks5_proxy=maybe
http_proxy=
dest_acl_mode=-3
max_conn=-5
upload_limit=-1
download_limit= 12x
read_only=yes
multi_account=testdata/missing.txt

[tcp_port_order]
target_addr=a:1,b:2
target_port=8080
mode=tcp
mode=udp

[tcp_port_order2]
target_port=8080
target_addr=a:1,b:2

[tcp_empty]
remark=ignored

[host_full]
host=a.example
target_addr=127.0.0.1:80,127.0.0.1:81
proxy_protocol=1
host_change=b.example
scheme=HTTPS
location=/loc
path_rewrite=/rw
cert_file=-----BEGIN CERTIFICATE-----x
key_file=-----BEGIN PRIVATE KEY-----y
https_just_proxy=true
auto_ssl=true
auto_https=true
auto_cors=1
compat_mode=true
redirect_url=https://r.example/?a=1&b=2
target_is_https=true
max_conn=3
upload_limit=10
download_limit=-20
header_X-A=1
response_X-B=2
header_X-C=3
multi_account=testdata/accounts.txt
server_ip=ignored
tls_offload=true
password=ignored

[host_invalid]
host=b.example
cert_file=testdata/missing.pem
https_just_proxy=nope
max_conn=x

[host_min]
host=c.example
`

// TestEntryConfigDifferential compares what every tunnel and host key of
// npc.conf leads to with the golden file written by the earlier
// implementation.
func TestEntryConfigDifferential(t *testing.T) {
	c, err := Parse(entryDiffConfig)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, task := range c.Tasks {
		b, err := json.Marshal(task)
		if err != nil {
			t.Fatal(err)
		}
		got["tunnel/"+task.Remark] = string(b)
	}
	for _, host := range c.Hosts {
		b, err := json.Marshal(host)
		if err != nil {
			t.Fatal(err)
		}
		got["host/"+host.Remark] = string(b)
	}
	path := filepath.Join("testdata", "entry_config.golden")
	if *updateGolden {
		b, _ := json.MarshalIndent(got, "", "  ")
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
		t.Errorf("got %d entries, want %d", len(got), len(want))
	}
}
