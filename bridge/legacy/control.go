package legacy

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"strings"
	"time"

	"github.com/djylb/nps/lib/model"

	"github.com/djylb/nps/internal/tunnelwire"
	"github.com/djylb/nps/internal/tunnelwire/control"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/healthcheck"
	"github.com/djylb/nps/lib/rate"
)

// ReadTask reads a task sent with FlagNewTask. It holds no state of the sender:
// a fresh flow and the no-store mark; the receiver gives it an id.
func ReadTask(r io.Reader) (*model.Tunnel, error) {
	var m *control.Task
	if err := control.Read(r, &m); err != nil {
		return nil, err
	}
	if m == nil {
		return nil, control.ErrEmptyInfo
	}
	t, err := taskFromWire(m)
	if err != nil {
		return nil, err
	}
	t.NoStore = true
	t.Flow = new(model.Flow)
	return t, nil
}

// ReadHost reads a host sent with FlagNewHost; the receiver gives it an id.
func ReadHost(r io.Reader) (*model.Host, error) {
	var m *control.Host
	if err := control.Read(r, &m); err != nil {
		return nil, err
	}
	if m == nil {
		return nil, control.ErrEmptyInfo
	}
	h, err := hostFromWire(m)
	if err != nil {
		return nil, err
	}
	h.Flow = new(model.Flow)
	h.NoStore = true
	return h, nil
}

// ReadClient reads a client sent with FlagNewConf.
func ReadClient(r io.Reader) (*model.Client, error) {
	var m *control.Client
	if err := control.Read(r, &m); err != nil {
		return nil, err
	}
	if m == nil {
		return nil, control.ErrEmptyInfo
	}
	c, err := clientFromWire(m)
	if err != nil {
		return nil, err
	}
	c.NoStore = true
	c.Status = true
	if c.Flow == nil {
		c.Flow = new(model.Flow)
	}
	c.NoDisplay = false
	return c, nil
}

// WriteTask sends t with FlagNewTask.
func WriteTask(w io.Writer, t *model.Tunnel) error {
	return control.Write(w, taskToWire(t), FlagNewTask)
}

// WriteHost sends h with FlagNewHost.
func WriteHost(w io.Writer, h *model.Host) error {
	return control.Write(w, hostToWire(h), FlagNewHost)
}

// WriteClient sends c with FlagNewConf.
func WriteClient(w io.Writer, c *model.Client) error {
	return control.Write(w, clientToWire(c), FlagNewConf)
}

func flowFromWire(m *control.Flow) *model.Flow {
	if m == nil {
		return nil
	}
	return &model.Flow{ExportFlow: m.ExportFlow, InletFlow: m.InletFlow, FlowLimit: m.FlowLimit, TimeLimit: m.TimeLimit}
}

func flowToWire(f *model.Flow) *control.Flow {
	if f == nil {
		return nil
	}
	v := f.Values()
	return &control.Flow{ExportFlow: v.ExportFlow, InletFlow: v.InletFlow, FlowLimit: v.FlowLimit, TimeLimit: v.TimeLimit}
}

func listsFromWire(m control.SourceLists) model.SourceLists {
	return model.SourceLists{IpListPriority: m.IpListPriority, BlackIpList: m.BlackIpList, WhiteIpList: m.WhiteIpList, RegisteredOnly: m.RegisteredOnly}
}

func listsToWire(l *model.SourceLists) control.SourceLists {
	return control.SourceLists{IpListPriority: l.IpListPriority, BlackIpList: l.BlackIpList, WhiteIpList: l.WhiteIpList, RegisteredOnly: l.RegisteredOnly}
}

func targetFromWire(m *control.Target) *model.Target {
	if m == nil {
		return nil
	}
	return &model.Target{TargetStr: m.TargetStr, LocalProxy: m.LocalProxy, ProxyProtocol: m.ProxyProtocol}
}

func targetToWire(t *model.Target) *control.Target {
	if t == nil {
		return nil
	}
	return &control.Target{TargetStr: t.TargetStr, LocalProxy: t.LocalProxy, ProxyProtocol: t.ProxyProtocol}
}

func accountFromWire(m *control.MultiAccount) *model.MultiAccount {
	if m == nil {
		return nil
	}
	return &model.MultiAccount{Content: m.Content, AccountMap: m.AccountMap}
}

func accountToWire(a *model.MultiAccount) *control.MultiAccount {
	if a == nil {
		return nil
	}
	return &control.MultiAccount{Content: a.Content, AccountMap: a.AccountMap}
}

func clientFromWire(m *control.Client) (*model.Client, error) {
	if m == nil {
		return nil, nil
	}
	c := &model.Client{
		Id: m.Id, VerifyKey: m.VerifyKey, Mode: m.Mode, Addr: m.Addr, LocalAddr: m.LocalAddr, Remark: m.Remark,
		Status: m.Status, IsConnect: m.IsConnect, RateLimit: m.RateLimit, Flow: flowFromWire(m.Flow),
		NoStore: m.NoStore, NoDisplay: m.NoDisplay, MaxConn: m.MaxConn, NowConn: m.NowConn,
		WebUserName: m.WebUserName, WebPassword: m.WebPassword, WebTotpSecret: m.WebTotpSecret,
		ConfigConnAllow: m.ConfigConnAllow, MaxTunnelNum: m.MaxTunnelNum, Version: m.Version,
		CreateTime: m.CreateTime, LastOnlineTime: m.LastOnlineTime,
		RegisterKey: m.RegisterKey, RegisterMaxHours: m.RegisterMaxHours,
	}
	if m.Cnf != nil {
		c.Cnf = &model.Config{U: m.Cnf.U, P: m.Cnf.P, Compress: m.Cnf.Compress, Crypt: m.Cnf.Crypt}
	}
	c.ExportFlow.Store(m.ExportFlow)
	c.InletFlow.Store(m.InletFlow)
	if m.Rate != nil {
		c.Rate = new(rate.Rate)
	}
	lists := listsFromWire(m.SourceLists)
	c.IpListPriority, c.BlackIpList, c.WhiteIpList, c.RegisteredOnly = lists.IpListPriority, lists.BlackIpList, lists.WhiteIpList, lists.RegisteredOnly
	if len(m.ConnectionHistory) > 0 {
		c.ConnectionHistory = new(model.ConnectionHistory)
		if err := json.Unmarshal(m.ConnectionHistory, c.ConnectionHistory); err != nil {
			if _, ok := errors.AsType[*json.UnmarshalTypeError](err); !ok {
				return nil, err
			}
		}
	}
	return c, nil
}

func clientToWire(c *model.Client) *control.Client {
	if c == nil {
		return nil
	}
	m := &control.Client{
		Id: c.Id, VerifyKey: c.VerifyKey, Mode: c.Mode, Addr: c.Addr, LocalAddr: c.LocalAddr, Remark: c.Remark,
		Status: c.Status, IsConnect: c.IsConnect, RateLimit: c.RateLimit, Flow: flowToWire(c.Flow),
		ExportFlow: c.ExportFlow.Load(), InletFlow: c.InletFlow.Load(),
		NoStore: c.NoStore, NoDisplay: c.NoDisplay, MaxConn: c.MaxConn, NowConn: c.NowConn,
		WebUserName: c.WebUserName, WebPassword: c.WebPassword, WebTotpSecret: c.WebTotpSecret,
		ConfigConnAllow: c.ConfigConnAllow, MaxTunnelNum: c.MaxTunnelNum, Version: c.Version,
		SourceLists: listsToWire(&c.SourceLists),
		CreateTime:  c.CreateTime, LastOnlineTime: c.LastOnlineTime,
		RegisterKey: c.RegisterKey, RegisterMaxHours: c.RegisterMaxHours,
	}
	if c.Cnf != nil {
		m.Cnf = &control.Config{U: c.Cnf.U, P: c.Cnf.P, Compress: c.Cnf.Compress, Crypt: c.Cnf.Crypt}
	}
	if c.Rate != nil {
		m.Rate = &control.Rate{NowRate: c.Rate.Now(), Limit: c.Rate.Limit()}
	}
	if c.ConnectionHistory != nil {
		if b, err := json.Marshal(c.ConnectionHistory); err == nil {
			m.ConnectionHistory = b
		}
	}
	return m
}

func taskFromWire(m *control.Task) (*model.Tunnel, error) {
	client, err := clientFromWire(m.Client)
	if err != nil {
		return nil, err
	}
	t := &model.Tunnel{
		MaxConn: m.MaxConn, Id: m.Id, Port: m.Port, ServerIp: m.ServerIp, Mode: m.Mode, Status: m.Status,
		RunStatus: m.RunStatus, Client: client, Ports: m.Ports, Flow: flowFromWire(m.Flow), NowConn: m.NowConn,
		Password: m.Password, Remark: m.Remark, TargetAddr: m.TargetAddr, TargetType: m.TargetType,
		DestAclMode: m.DestAclMode, DestAclRules: m.DestAclRules, NoStore: m.NoStore, IsHttp: m.IsHttp,
		HttpProxy: m.HttpProxy, Socks5Proxy: m.Socks5Proxy, LocalPath: m.LocalPath, StripPre: m.StripPre,
		ReadOnly: m.ReadOnly, Target: targetFromWire(m.Target), UserAuth: accountFromWire(m.UserAuth),
		MultiAccount: accountFromWire(m.MultiAccount),
	}
	t.UploadLimit, t.DownloadLimit = m.UploadLimit, m.DownloadLimit
	lists := listsFromWire(m.SourceLists)
	t.IpListPriority, t.BlackIpList, t.WhiteIpList, t.RegisteredOnly = lists.IpListPriority, lists.BlackIpList, lists.WhiteIpList, lists.RegisteredOnly
	return t, nil
}

func taskToWire(t *model.Tunnel) *control.Task {
	return &control.Task{
		UploadLimit: t.UploadLimit, DownloadLimit: t.DownloadLimit,
		MaxConn: t.MaxConn, Id: t.Id, Port: t.Port, ServerIp: t.ServerIp, Mode: t.Mode, Status: t.Status,
		RunStatus: t.RunStatus, Client: clientToWire(t.Client), Ports: t.Ports, Flow: flowToWire(t.Flow), NowConn: t.NowConn,
		Password: t.Password, Remark: t.Remark, TargetAddr: t.TargetAddr, TargetType: t.TargetType,
		DestAclMode: t.DestAclMode, DestAclRules: t.DestAclRules, SourceLists: listsToWire(&t.SourceLists),
		NoStore: t.NoStore, IsHttp: t.IsHttp, HttpProxy: t.HttpProxy, Socks5Proxy: t.Socks5Proxy,
		LocalPath: t.LocalPath, StripPre: t.StripPre, ReadOnly: t.ReadOnly, Target: targetToWire(t.Target),
		UserAuth: accountToWire(t.UserAuth), MultiAccount: accountToWire(t.MultiAccount),
	}
}

func hostFromWire(m *control.Host) (*model.Host, error) {
	client, err := clientFromWire(m.Client)
	if err != nil {
		return nil, err
	}
	h := &model.Host{
		Id: m.Id, Host: m.Host, HeaderChange: m.HeaderChange, RespHeaderChange: m.RespHeaderChange,
		HostChange: m.HostChange, Location: m.Location, PathRewrite: m.PathRewrite, Remark: m.Remark, Scheme: m.Scheme,
		RedirectURL: m.RedirectURL, HttpsJustProxy: m.HttpsJustProxy, TlsOffload: m.TlsOffload, AutoSSL: m.AutoSSL,
		CertType: m.CertType, CertHash: m.CertHash, CertFile: m.CertFile, KeyFile: m.KeyFile, NoStore: m.NoStore,
		IsClose: m.IsClose, AutoHttps: m.AutoHttps, AutoCORS: m.AutoCORS, CompatMode: m.CompatMode,
		Flow: flowFromWire(m.Flow), MaxConn: m.MaxConn, NowConn: m.NowConn, Client: client,
		TargetIsHttps: m.TargetIsHttps, Target: targetFromWire(m.Target), UserAuth: accountFromWire(m.UserAuth),
		MultiAccount: accountFromWire(m.MultiAccount),
	}
	h.UploadLimit, h.DownloadLimit = m.UploadLimit, m.DownloadLimit
	lists := listsFromWire(m.SourceLists)
	h.IpListPriority, h.BlackIpList, h.WhiteIpList, h.RegisteredOnly = lists.IpListPriority, lists.BlackIpList, lists.WhiteIpList, lists.RegisteredOnly
	return h, nil
}

func hostToWire(h *model.Host) *control.Host {
	return &control.Host{
		UploadLimit: h.UploadLimit, DownloadLimit: h.DownloadLimit,
		Id: h.Id, Host: h.Host, HeaderChange: h.HeaderChange, RespHeaderChange: h.RespHeaderChange,
		HostChange: h.HostChange, Location: h.Location, PathRewrite: h.PathRewrite, Remark: h.Remark, Scheme: h.Scheme,
		RedirectURL: h.RedirectURL, HttpsJustProxy: h.HttpsJustProxy, TlsOffload: h.TlsOffload, AutoSSL: h.AutoSSL,
		CertType: h.CertType, CertHash: h.CertHash, CertFile: h.CertFile, KeyFile: h.KeyFile, NoStore: h.NoStore,
		IsClose: h.IsClose, AutoHttps: h.AutoHttps, AutoCORS: h.AutoCORS, CompatMode: h.CompatMode,
		Flow: flowToWire(h.Flow), MaxConn: h.MaxConn, NowConn: h.NowConn, Client: clientToWire(h.Client),
		TargetIsHttps: h.TargetIsHttps, Target: targetToWire(h.Target), UserAuth: accountToWire(h.UserAuth),
		MultiAccount: accountToWire(h.MultiAccount), SourceLists: listsToWire(&h.SourceLists),
	}
}

// ReadLink reads the Link a connection starts with.
func ReadLink(r io.Reader) (*conn.Link, error) {
	var m *control.Link
	if err := control.Read(r, &m); err != nil {
		return nil, err
	}
	if m == nil {
		return nil, nil
	}
	return &conn.Link{
		ConnType: m.ConnType, Host: m.Host, Crypt: m.Crypt, Compress: m.Compress, LocalProxy: m.LocalProxy,
		RemoteAddr: m.RemoteAddr, Option: conn.Options{Timeout: m.Option.Timeout, NeedAck: m.Option.NeedAck},
	}, nil
}

// WriteLink sends link at the start of a connection.
func WriteLink(w io.Writer, link *conn.Link) error {
	return control.Write(w, &control.Link{
		ConnType: link.ConnType, Host: link.Host, Crypt: link.Crypt, Compress: link.Compress, LocalProxy: link.LocalProxy,
		RemoteAddr: link.RemoteAddr, Option: control.LinkOptions{Timeout: link.Option.Timeout, NeedAck: link.Option.NeedAck},
	}, "")
}

// WriteSignal sends a message with flag on a signal connection.
func WriteSignal(w io.Writer, v any, flag string) error { return control.Write(w, v, flag) }

// WriteStatus reports whether an entry was added.
func WriteStatus(w io.Writer, ok bool) error { return control.WriteStatus(w, ok) }

// ReadStatus reads what WriteStatus wrote.
func ReadStatus(r io.Reader) (bool, error) { return control.ReadStatus(r) }

// healthGroupWire is the body of HealthSet: targets added to a group. A large
// group takes several messages.
type healthGroupWire struct {
	Group    string   `json:"group"`
	Type     string   `json:"type"`
	Path     string   `json:"path,omitempty"`
	Interval int      `json:"interval"`
	Timeout  int      `json:"timeout"`
	Fall     int      `json:"fall"`
	Targets  []string `json:"targets"`
}

// healthDelWire is the body of HealthDel.
type healthDelWire struct {
	Group string `json:"group"`
}

// healthChunk bounds the bytes of addresses in one HealthSet message, well below
// the limit of a frame.
const healthChunk = 32 << 10

// HealthSetBodies returns the bodies of the HealthSet messages that add g.
func HealthSetBodies(g healthcheck.Group) []any {
	m := healthGroupWire{Group: g.Name, Type: g.Type, Path: g.Path, Interval: g.Interval, Timeout: g.Timeout, Fall: g.Fall}
	var bodies []any
	rest, size := g.Targets, 0
	for i := 0; i < len(rest); i++ {
		size += len(rest[i]) + 3
		if size > healthChunk && i > 0 {
			c := m
			c.Targets = rest[:i]
			bodies = append(bodies, c)
			rest, i, size = rest[i:], -1, 0
		}
	}
	m.Targets = rest
	return append(bodies, m)
}

// HealthDelBody returns the body of the HealthDel message that removes a group.
func HealthDelBody(group string) any { return healthDelWire{Group: group} }

// ReadHealthSet reads the body of a HealthSet message.
func ReadHealthSet(r io.Reader) (healthcheck.Group, error) {
	var m healthGroupWire
	err := control.Read(r, &m)
	return healthcheck.Group{Name: m.Group, Type: m.Type, Path: m.Path, Interval: m.Interval, Timeout: m.Timeout, Fall: m.Fall, Targets: m.Targets}, err
}

// WriteGroupHealth writes a report of a group: a health report with the group as
// a third field, which only a server that sent the group receives.
func WriteGroupHealth(w io.Writer, rep healthcheck.Report) error {
	status := "0"
	if rep.Up {
		status = "1"
	}
	sep := tunnelwire.DataSeparator
	_, err := w.Write(tunnelwire.AppendFrame(nil, []byte(rep.Addr+sep+status+sep+rep.Group+sep)))
	return err
}

// ReadHealth reads a health report. A released npc reports its own checks
// without a group.
func ReadHealth(r io.Reader) (healthcheck.Report, error) {
	b, err := tunnelwire.ReadInfoFrame(r)
	if err != nil {
		return healthcheck.Report{}, err
	}
	arr := strings.Split(string(b), tunnelwire.DataSeparator)
	if len(arr) < 2 {
		return healthcheck.Report{}, errors.New("receive health info error")
	}
	rep := healthcheck.Report{Addr: arr[0], Up: arr[1] == "1" || arr[1] == "true"}
	if len(arr) > 2 {
		rep.Group = arr[2]
	}
	return rep, nil
}

// FileKey names a client's file server on both sides of a file task.
func FileKey(key string, t *model.Tunnel) string {
	return control.FileKey(key, t.Mode, t.ServerIp, t.Port, t.LocalPath, t.StripPre, t.ReadOnly, t.MultiAccount.Content)
}

// Ack is the answer to a link that asks for one.
const Ack = tunnelwire.Ack

// WriteAck acknowledges a link within timeout.
func WriteAck(c net.Conn, timeout time.Duration) error {
	_ = c.SetWriteDeadline(time.Now().Add(timeout))
	_, err := c.Write([]byte(Ack))
	_ = c.SetWriteDeadline(time.Time{})
	return err
}

// ReadAck waits for the answer to a link within timeout.
func ReadAck(c net.Conn, timeout time.Duration) error {
	_ = c.SetReadDeadline(time.Now().Add(timeout))
	buf, err := tunnelwire.ReadExact(c, len(Ack))
	_ = c.SetReadDeadline(time.Time{})
	if err != nil {
		return err
	}
	if string(buf) != Ack {
		return io.ErrUnexpectedEOF
	}
	return nil
}

// WriteStatusList answers a WorkStatus request.
func WriteStatusList(w io.Writer, remarks []string) error {
	var b []byte
	for _, r := range remarks {
		b = append(b, r...)
		b = append(b, tunnelwire.DataSeparator...)
	}
	return tunnelwire.WriteFrame(w, b)
}

// ReadStatusList reads what WriteStatusList wrote.
func ReadStatusList(r io.Reader) ([]string, error) {
	b, err := tunnelwire.ReadFrame(r)
	if err != nil {
		return nil, err
	}
	parts := strings.Split(string(b), tunnelwire.DataSeparator)
	if parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	return parts, nil
}
