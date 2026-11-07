package client

// The golden vectors in testdata/wire are the wire format released clients
// depend on. They were recorded from the code before the protocol moved to
// its library: never update them to match a change, unless that change is
// meant to alter the bytes on the wire, which breaks released clients.
//
//	WIRE_GOLDEN_UPDATE=1 go test ./client -run TestWireGolden

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"testing/cryptotest"
	"time"

	"github.com/djylb/nps/lib/model"

	"encoding/binary"
	"io"

	"github.com/djylb/nps/bridge"
	"github.com/djylb/nps/bridge/legacy"
	"github.com/djylb/nps/internal/tunnelwire"
	"github.com/djylb/nps/lib/config"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/version"
)

const (
	goldenKey       = "golden-vkey"
	goldenTimestamp = int64(1700000000)
	goldenUUID      = "01890000-0000-7000-8000-000000000001"
	goldenNode      = "01890000-0000-7000-8000-000000000002"
	goldenClientVer = "0.35.0"
)

func goldenPath(name string) string { return filepath.Join("testdata", "wire", name) }

// checkGolden compares got, as indented JSON, with the file name, or writes
// it when updating.
func checkGolden(t *testing.T, name string, got any) {
	t.Helper()
	b, err := json.MarshalIndent(got, "", " ")
	if err != nil {
		t.Fatal(err)
	}
	b = append(b, '\n')
	if os.Getenv("WIRE_GOLDEN_UPDATE") == "1" {
		if _, err := os.Stat(goldenPath(name)); err == nil && os.Getenv("WIRE_GOLDEN_UPDATE_FORCE") != "1" {
			t.Fatalf("%s exists: vectors are not rewritten", name)
		}
		if err := os.WriteFile(goldenPath(name), b, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(goldenPath(name))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(want, b) {
		t.Fatalf("%s differs from the recorded wire format\nwant %s\ngot  %s", name, want, b)
	}
}

// recordConn records what a pipe end reads and writes.
type recordConn struct {
	net.Conn
	mu     sync.Mutex
	in     bytes.Buffer
	out    bytes.Buffer
	remote net.Addr
}

func (c *recordConn) Read(b []byte) (int, error) {
	n, err := c.Conn.Read(b)
	c.mu.Lock()
	c.in.Write(b[:n])
	c.mu.Unlock()
	return n, err
}

func (c *recordConn) Write(b []byte) (int, error) {
	n, err := c.Conn.Write(b)
	c.mu.Lock()
	c.out.Write(b[:n])
	c.mu.Unlock()
	return n, err
}

func (c *recordConn) RemoteAddr() net.Addr { return c.remote }

func (c *recordConn) snapshot() (in, out string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return hex.EncodeToString(c.in.Bytes()), hex.EncodeToString(c.out.Bytes())
}

type handshakeVector struct {
	Index     int
	CoreVer   string
	ClientVer string
	Key       string
	Timestamp int64
	Seed      uint64
	ClientHex string // client to server: handshake, then a rgst work with one hour
	ServerHex string // server to client
	UUID      string // what the client read as the server UUID
	Work      map[string]string
}

func fixHandshake(t *testing.T) {
	t.Helper()
	hooks := HandshakeHooks
	mk := bridge.NewServerUUID
	t.Cleanup(func() { HandshakeHooks = hooks; bridge.NewServerUUID = mk })
	HandshakeHooks.Timestamp = func() int64 { return goldenTimestamp }
	HandshakeHooks.OutboundIP = func() net.IP { return net.ParseIP("192.0.2.1") }
	HandshakeHooks.PadLen = func() int { return 3 }
	bridge.NewServerUUID = func() string { return goldenUUID }
}

func goldenCert(t *testing.T) {
	t.Helper()
	// A throwaway certificate; the handshake sends its fingerprint.
	cert, err := tls.LoadX509KeyPair(goldenPath("cert.pem"), goldenPath("key.pem"))
	if err != nil {
		t.Fatal(err)
	}
	crypt.InitTls(cert)
	t.Cleanup(func() { crypt.InitTls(tls.Certificate{}) })
}

func goldenDb(t *testing.T) *file.DbUtils {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "conf"), 0o755); err != nil {
		t.Fatal(err)
	}
	db := file.New(root)
	cl := model.NewClient(goldenKey, false, false)
	cl.Id = 1
	if err := db.NewClient(cl); err != nil {
		t.Fatal(err)
	}
	return db
}

// recordHandshake runs the client of protocol index against the bridge over a
// pipe. Each side waits for the other at every step, so crypto/rand is read in
// a fixed order and the seed fixes every random byte.
func recordHandshake(t *testing.T, db *file.DbUtils, index int, seed uint64) handshakeVector {
	t.Helper()
	cryptotest.SetGlobalRandom(t, seed)
	oldVer := Ver
	Ver = index
	t.Cleanup(func() { Ver = oldVer })

	cs, ss := net.Pipe()
	defer func() { _ = cs.Close() }()
	rec := &recordConn{Conn: ss, remote: &net.TCPAddr{IP: net.IPv4(192, 0, 2, 2), Port: 40000}}
	b := bridge.NewTunnel(db, &sync.Map{}, bridge.Options{DisconnectTime: 30})
	go b.CliProcess(conn.NewConn(rec), "tcp")

	c, uuid, _, err := handshakeConn(cs, "tcp", goldenKey, false, false, nil, "")
	if err != nil {
		t.Fatalf("index %d: %v", index, err)
	}
	node := goldenNode
	if err := SendType(c, legacy.WorkRegister, node); err != nil {
		t.Fatalf("index %d: %v", index, err)
	}
	hour := []byte{1, 0, 0, 0}
	if _, err := c.Write(hour); err != nil {
		t.Fatalf("index %d: %v", index, err)
	}
	// rgst closes the connection once it has read the hours.
	var one [1]byte
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	_, _ = c.Read(one[:])

	in, out := rec.snapshot()
	v := handshakeVector{
		Index: index, CoreVer: legacy.CoreVersion(index), ClientVer: goldenClientVer, Key: goldenKey,
		Timestamp: goldenTimestamp, Seed: seed, ClientHex: in, ServerHex: out, UUID: uuid,
		Work: map[string]string{},
	}
	for _, flag := range []string{legacy.WorkMain, legacy.WorkChan, legacy.WorkConfig, legacy.WorkVisitor,
		legacy.WorkSecret, legacy.WorkFile, legacy.WorkRegister, legacy.WorkP2P, legacy.WorkP2PSession} {
		a, z := net.Pipe()
		rw := &recordConn{Conn: a, remote: a.RemoteAddr()}
		done := make(chan struct{})
		go func() {
			defer close(done)
			buf := make([]byte, 4096)
			for {
				if _, err := z.Read(buf); err != nil {
					return
				}
			}
		}()
		if err := SendType(conn.NewConn(rw), flag, node); err != nil {
			t.Fatal(err)
		}
		_ = a.Close()
		<-done
		_, wrote := rw.snapshot()
		v.Work[flag] = wrote
	}
	return v
}

func TestWireGoldenHandshake(t *testing.T) {
	fixHandshake(t)
	goldenCert(t)
	db := goldenDb(t)
	if version.VERSION != goldenClientVer {
		t.Skip("the vectors announce client " + goldenClientVer)
	}
	for index := range tunnelwire.IndexCount() {
		v := recordHandshake(t, db, index, uint64(1000+index))
		checkGolden(t, fmt.Sprintf("handshake_%d.json", index), v)
	}
}

const goldenConfig = `[common]
server_addr=192.0.2.1:8024
conn_type=tcp
vkey=golden-vkey
auto_reconnect=true
basic_username=user
basic_password=pass
compress=true
crypt=true
rate_limit=128
flow_limit=1024
max_conn=7
remark=golden-client
web_username=web
web_password=webpass
pprof_addr=
[web1]
host=a.example.com
target_addr=127.0.0.1:8080,127.0.0.1:8081
proxy_protocol=2
host_change=b.example.com
header_Foo=bar
response_Bar=foo
location=/x
scheme=https
local_proxy=false
multi_account=
[tcp1]
mode=tcp
target_addr=127.0.0.1:8080
server_port=9001
proxy_protocol=1
remark=t1
[udp1]
mode=udp
target_addr=127.0.0.1:53
server_port=9002
[many]
mode=tcp
target_addr=127.0.0.1:8000-8001
server_port=9003-9004
[sec]
mode=secret
password=s3cret
target_addr=127.0.0.1:22
[file1]
mode=file
server_port=9005
local_path=/srv
strip_pre=/web/
read_only=true
[socks]
mode=mixProxy
socks5=true
http=true
server_port=9006
[p2p1]
mode=p2p
password=p2ppass
target_addr=127.0.0.1:22
`

// richTask, richHost and richClient set every field that has a JSON name.
func richFlow() *model.Flow {
	return &model.Flow{ExportFlow: 11, InletFlow: 22, FlowLimit: 33, TimeLimit: time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)}
}

func richClient() *model.Client {
	c := &model.Client{
		Cnf: &model.Config{U: "u", P: "p", Compress: true, Crypt: true},
		Id:  5, VerifyKey: "vk", Mode: "tcp", Addr: "192.0.2.3", LocalAddr: "10.0.0.1", Remark: "r",
		Status: true, IsConnect: true, RateLimit: 9, Flow: richFlow(), NoStore: true, NoDisplay: true,
		MaxConn: 4, NowConn: 2, WebUserName: "wu", WebPassword: "wp", WebTotpSecret: "ts",
		ConfigConnAllow: true, MaxTunnelNum: 3, Version: "0.34.8",
		CreateTime: "2026-01-01 00:00:00", LastOnlineTime: "2026-01-02 00:00:00",
		RegisterKey: "rk", RegisterMaxHours: 6,
	}
	c.ExportFlow.Store(100)
	c.InletFlow.Store(200)
	c.IpListPriority = 1
	c.BlackIpList = []string{"192.0.2.0/24"}
	c.WhiteIpList = []string{"198.51.100.0/24"}
	c.RegisteredOnly = true
	return c
}

func richTask() *model.Tunnel {
	t := &model.Tunnel{
		MaxConn: 1, Id: 2, Port: 3, ServerIp: "0.0.0.0", Mode: "tcp", Status: true, RunStatus: true,
		Client: richClient(), Ports: "1-2", Flow: richFlow(), NowConn: 4, Password: "pw", Remark: "rm",
		TargetAddr: "ta", TargetType: "tt", DestAclMode: 1, DestAclRules: "a\nb", NoStore: true,
		IsHttp: true, HttpProxy: true, Socks5Proxy: true, LocalPath: "/l", StripPre: "/s", ReadOnly: true,
		Target:       &model.Target{TargetStr: "127.0.0.1:1\n127.0.0.1:2", LocalProxy: true, ProxyProtocol: 2},
		UserAuth:     &model.MultiAccount{Content: "a=b", AccountMap: map[string]string{"a": "b", "c": "d"}},
		MultiAccount: &model.MultiAccount{Content: "e=f", AccountMap: map[string]string{"e": "f"}},
	}
	t.UploadLimit, t.DownloadLimit = 7, 8
	t.IpListPriority = 1
	t.BlackIpList = []string{"192.0.2.0/24"}
	t.WhiteIpList = []string{"198.51.100.0/24"}
	t.RegisteredOnly = true
	t.HealthCheckTimeout, t.HealthMaxFail, t.HealthCheckInterval = 1, 2, 3
	t.HttpHealthUrl, t.HealthCheckType, t.HealthCheckTarget = "/h", "http", "ht"
	return t
}

func richHost() *model.Host {
	h := &model.Host{
		Id: 3, Host: "h.example.com", HeaderChange: "hc", RespHeaderChange: "rhc", HostChange: "hc2",
		Location: "/", PathRewrite: "/p", Remark: "rm", Scheme: "all", RedirectURL: "ru", HttpsJustProxy: true,
		TlsOffload: true, AutoSSL: true, CertType: "file", CertHash: "ch", CertFile: "cf", KeyFile: "kf",
		NoStore: true, IsClose: true, AutoHttps: true, AutoCORS: true, CompatMode: true, Flow: richFlow(),
		MaxConn: 2, NowConn: 1, Client: richClient(), TargetIsHttps: true,
		Target:       &model.Target{TargetStr: "127.0.0.1:1", ProxyProtocol: 1},
		UserAuth:     &model.MultiAccount{Content: "a=b", AccountMap: map[string]string{"a": "b"}},
		MultiAccount: &model.MultiAccount{Content: "c=d", AccountMap: map[string]string{"c": "d"}},
	}
	h.UploadLimit, h.DownloadLimit = 5, 6
	h.IpListPriority = 1
	h.BlackIpList = []string{"192.0.2.0/24"}
	h.WhiteIpList = []string{"198.51.100.0/24"}
	return h
}

type infoVector struct {
	Name    string
	Flag    string
	Wire    string // the frame as the client writes it
	Decoded string // what the server makes of it, as JSON
}

// send writes v as a client does.
func send(w io.Writer, v any, flag string) error {
	switch m := v.(type) {
	case *model.Tunnel:
		return legacy.WriteTask(w, m)
	case *model.Host:
		return legacy.WriteHost(w, m)
	case *model.Client:
		return legacy.WriteClient(w, m)
	case *conn.Link:
		return legacy.WriteLink(w, m)
	}
	return legacy.WriteSignal(w, v, flag)
}

// frameOf returns what a client writes for v.
func frameOf(t *testing.T, v any, flag string) []byte {
	t.Helper()
	a, z := net.Pipe()
	defer func() { _ = a.Close(); _ = z.Close() }()
	var out bytes.Buffer
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 64<<10)
		n, _ := z.Read(buf)
		out.Write(buf[:n])
	}()
	if err := send(a, v, flag); err != nil {
		t.Fatal(err)
	}
	<-done
	return out.Bytes()
}

func decodeFrame[T any](t *testing.T, frame []byte, read func(io.Reader) (*T, error), reset func(*T)) string {
	t.Helper()
	a, z := net.Pipe()
	defer func() { _ = a.Close(); _ = z.Close() }()
	go func() { _, _ = a.Write(frame) }()
	v, err := read(z)
	if err != nil {
		return "error: " + err.Error()
	}
	reset(v)
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func decodeTask(t *testing.T, frame []byte) string {
	return decodeFrame(t, frame, legacy.ReadTask, func(v *model.Tunnel) { v.Id = 0 })
}

func decodeHost(t *testing.T, frame []byte) string {
	return decodeFrame(t, frame, legacy.ReadHost, func(v *model.Host) { v.Id = 0 })
}

func decodeClient(t *testing.T, frame []byte) string {
	return decodeFrame(t, frame, legacy.ReadClient, func(*model.Client) {})
}

func rawFrame(s string) []byte {
	return tunnelwire.AppendFrame(nil, []byte(s))
}

func TestWireGoldenInfo(t *testing.T) {
	goldenDb(t)
	cnf, err := config.Parse(goldenConfig)
	if err != nil {
		t.Fatal(err)
	}
	var out []infoVector
	add := func(name, flag string, v any, decode func(*testing.T, []byte) string) {
		frame := frameOf(t, v, flag)
		out = append(out, infoVector{Name: name, Flag: flag, Wire: hex.EncodeToString(frame), Decoded: decode(t, frame[len(flag):])})
	}
	add("client/rich", legacy.FlagNewConf, richClient(), decodeClient)
	add("client/config", legacy.FlagNewConf, cnf.CommonConfig.Client, decodeClient)
	add("client/zero", legacy.FlagNewConf, &model.Client{}, decodeClient)
	add("host/rich", legacy.FlagNewHost, richHost(), decodeHost)
	add("host/zero", legacy.FlagNewHost, &model.Host{}, decodeHost)
	for i, h := range cnf.Hosts {
		add("host/config"+strconv.Itoa(i), legacy.FlagNewHost, h, decodeHost)
	}
	add("task/rich", legacy.FlagNewTask, richTask(), decodeTask)
	add("task/zero", legacy.FlagNewTask, &model.Tunnel{}, decodeTask)
	for i, task := range cnf.Tasks {
		add("task/config"+strconv.Itoa(i), legacy.FlagNewTask, task, decodeTask)
	}
	// What other versions send: fields unknown here, fields of the wrong type,
	// and no fields.
	for _, p := range []struct {
		name, frame string
		decode      func(*testing.T, []byte) string
	}{
		{"task/unknown-field", `{"Mode":"tcp","Port":1,"Ports":"1","Target":{"TargetStr":"a"},"NewField":{"x":1}}`, decodeTask},
		{"task/wrong-type", `{"Mode":"tcp","Port":"bad","Remark":"kept","Target":{"TargetStr":"a","ProxyProtocol":"x"}}`, decodeTask},
		{"task/case", `{"mode":"udp","PORTS":"5","target":{"targetstr":"b"}}`, decodeTask},
		{"task/null", `null`, decodeTask},
		{"task/bad-json", `{"Mode":`, decodeTask},
		{"host/wrong-type", `{"Host":"h","MaxConn":"x","Remark":"kept","Flow":{"FlowLimit":"x"}}`, decodeHost},
		{"host/null", `null`, decodeHost},
		{"client/wrong-type", `{"Cnf":{"U":"u","Crypt":"x"},"Remark":"kept","ExportFlow":"x","Rate":{"Limit":5}}`, decodeClient},
		{"client/flow", `{"Flow":{"FlowLimit":9,"TimeLimit":"2030-01-02T03:04:05Z"},"NoDisplay":true,"Status":false}`, decodeClient},
		{"client/history", `{"ConnectionHistory":[{"Online":true,"Time":"2030-01-02T03:04:05Z"}]}`, decodeClient},
		{"client/null", `null`, decodeClient},
	} {
		out = append(out, infoVector{Name: p.name, Wire: hex.EncodeToString(rawFrame(p.frame)), Decoded: p.decode(t, rawFrame(p.frame))})
	}
	checkGolden(t, "info.json", out)
}

type frameVector struct {
	Name string
	Hex  string
}

func TestWireGoldenFrames(t *testing.T) {
	var out []frameVector
	add := func(name string, b []byte) { out = append(out, frameVector{name, hex.EncodeToString(b)}) }
	rich := conn.NewLink("tcp", "127.0.0.1:80", true, true, "192.0.2.9:5", true, func(o *conn.Options) {
		o.Timeout = 3 * time.Second
		o.NeedAck = true
		o.Node = "not on the wire"
	})
	add("link/rich", frameOf(t, rich, ""))
	add("link/new", frameOf(t, conn.NewLink("udp5", "", false, false, "[2001:db8::1]:5", false), ""))
	add("link/zero", frameOf(t, &conn.Link{}, ""))
	add("signal/p2p-invite", frameOf(t, legacy.P2PInvite{SessionID: "s", Token: "t"}, legacy.P2PPunchStart))
	add("signal/update", frameOf(t, struct{}{}, legacy.UpdateRequest))
	add("p2p/request", rawFrame(mustJSON(legacy.P2PRequest{Password: "p", Background: true, ServerIP: "192.0.2.1"})))
	add("p2p/join", rawFrame(mustJSON(legacy.P2PJoin{SessionID: "s", Token: "t", ServerIP: "192.0.2.1", Decline: "d"})))
	add("p2p/reply", rawFrame(mustJSON(legacy.P2PReply{Code: legacy.P2PCodeBusy, Message: "m", SessionID: "s"})))
	add("p2p/empty", rawFrame(mustJSON(legacy.P2PReply{})))
	add("const/ack", []byte(legacy.Ack))
	add("const/close", []byte(legacy.ResClose))
	add("const/verify", []byte(tunnelwire.VerifyFailed+tunnelwire.VerifySuccess))
	a, z := net.Pipe()
	var hb bytes.Buffer
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 4096)
		n, _ := z.Read(buf)
		hb.Write(buf[:n])
	}()
	if err := legacy.WriteHealth(a, "127.0.0.1:1", "1"); err != nil {
		t.Fatal(err)
	}
	<-done
	_ = a.Close()
	_ = z.Close()
	add("health/up", hb.Bytes())
	add("key/blake2b", []byte(crypt.Blake2b(goldenKey)))
	add("key/md5", []byte(crypt.Md5(goldenKey)))
	add("key/file", []byte(legacy.FileKey("vk", &model.Tunnel{Mode: "file", ServerIp: "0.0.0.0", Port: 9005, LocalPath: "/srv", StripPre: "/web/", ReadOnly: true, MultiAccount: new(model.MultiAccount)})))
	add("key/node", []byte(tunnelwire.NameUUID("192.0.2.2:40000")))
	add("auth/hmac", goldenMAC())
	checkGolden(t, "frames.json", out)
}

// goldenMAC is the handshake MAC of the vectors by the standard library.
func goldenMAC() []byte {
	mac := hmac.New(sha256.New, []byte(goldenKey))
	mac.Write(binary.BigEndian.AppendUint64(nil, uint64(goldenTimestamp)))
	mac.Write([]byte("ab"))
	return mac.Sum(nil)
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}
