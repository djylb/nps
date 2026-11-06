package legacy

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"strings"
	"time"

	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/rate"
	"github.com/djylb/tunnelwire"
	"github.com/djylb/tunnelwire/control"
)

// ReadTask reads a task sent with FlagNewTask. It holds no state of the sender:
// a new id, flow and no-store mark.
func ReadTask(r io.Reader) (*file.Tunnel, error) {
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
	t.Id = int(file.GetDb().JsonDb.GetTaskId())
	t.NoStore = true
	t.Flow = new(file.Flow)
	return t, nil
}

// ReadHost reads a host sent with FlagNewHost.
func ReadHost(r io.Reader) (*file.Host, error) {
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
	h.Id = int(file.GetDb().JsonDb.GetHostId())
	h.Flow = new(file.Flow)
	h.NoStore = true
	return h, nil
}

// ReadClient reads a client sent with FlagNewConf.
func ReadClient(r io.Reader) (*file.Client, error) {
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
		c.Flow = new(file.Flow)
	}
	c.NoDisplay = false
	return c, nil
}

// WriteTask sends t with FlagNewTask.
func WriteTask(w io.Writer, t *file.Tunnel) error {
	return control.Write(w, taskToWire(t), FlagNewTask)
}

// WriteHost sends h with FlagNewHost.
func WriteHost(w io.Writer, h *file.Host) error {
	return control.Write(w, hostToWire(h), FlagNewHost)
}

// WriteClient sends c with FlagNewConf.
func WriteClient(w io.Writer, c *file.Client) error {
	return control.Write(w, clientToWire(c), FlagNewConf)
}

func flowFromWire(m *control.Flow) *file.Flow {
	if m == nil {
		return nil
	}
	return &file.Flow{ExportFlow: m.ExportFlow, InletFlow: m.InletFlow, FlowLimit: m.FlowLimit, TimeLimit: m.TimeLimit}
}

func flowToWire(f *file.Flow) *control.Flow {
	if f == nil {
		return nil
	}
	v := f.Values()
	return &control.Flow{ExportFlow: v.ExportFlow, InletFlow: v.InletFlow, FlowLimit: v.FlowLimit, TimeLimit: v.TimeLimit}
}

func listsFromWire(m control.SourceLists) file.SourceLists {
	return file.SourceLists{IpListPriority: m.IpListPriority, BlackIpList: m.BlackIpList, WhiteIpList: m.WhiteIpList, RegisteredOnly: m.RegisteredOnly}
}

func listsToWire(l *file.SourceLists) control.SourceLists {
	return control.SourceLists{IpListPriority: l.IpListPriority, BlackIpList: l.BlackIpList, WhiteIpList: l.WhiteIpList, RegisteredOnly: l.RegisteredOnly}
}

func targetFromWire(m *control.Target) *file.Target {
	if m == nil {
		return nil
	}
	return &file.Target{TargetStr: m.TargetStr, LocalProxy: m.LocalProxy, ProxyProtocol: m.ProxyProtocol}
}

func targetToWire(t *file.Target) *control.Target {
	if t == nil {
		return nil
	}
	return &control.Target{TargetStr: t.TargetStr, LocalProxy: t.LocalProxy, ProxyProtocol: t.ProxyProtocol}
}

func accountFromWire(m *control.MultiAccount) *file.MultiAccount {
	if m == nil {
		return nil
	}
	return &file.MultiAccount{Content: m.Content, AccountMap: m.AccountMap}
}

func accountToWire(a *file.MultiAccount) *control.MultiAccount {
	if a == nil {
		return nil
	}
	return &control.MultiAccount{Content: a.Content, AccountMap: a.AccountMap}
}

func clientFromWire(m *control.Client) (*file.Client, error) {
	if m == nil {
		return nil, nil
	}
	c := &file.Client{
		Id: m.Id, VerifyKey: m.VerifyKey, Mode: m.Mode, Addr: m.Addr, LocalAddr: m.LocalAddr, Remark: m.Remark,
		Status: m.Status, IsConnect: m.IsConnect, RateLimit: m.RateLimit, Flow: flowFromWire(m.Flow),
		NoStore: m.NoStore, NoDisplay: m.NoDisplay, MaxConn: m.MaxConn, NowConn: m.NowConn,
		WebUserName: m.WebUserName, WebPassword: m.WebPassword, WebTotpSecret: m.WebTotpSecret,
		ConfigConnAllow: m.ConfigConnAllow, MaxTunnelNum: m.MaxTunnelNum, Version: m.Version,
		CreateTime: m.CreateTime, LastOnlineTime: m.LastOnlineTime,
		RegisterKey: m.RegisterKey, RegisterMaxHours: m.RegisterMaxHours,
	}
	if m.Cnf != nil {
		c.Cnf = &file.Config{U: m.Cnf.U, P: m.Cnf.P, Compress: m.Cnf.Compress, Crypt: m.Cnf.Crypt}
	}
	c.ExportFlow.Store(m.ExportFlow)
	c.InletFlow.Store(m.InletFlow)
	if m.Rate != nil {
		c.Rate = new(rate.Rate)
	}
	lists := listsFromWire(m.SourceLists)
	c.IpListPriority, c.BlackIpList, c.WhiteIpList, c.RegisteredOnly = lists.IpListPriority, lists.BlackIpList, lists.WhiteIpList, lists.RegisteredOnly
	if len(m.ConnectionHistory) > 0 {
		c.ConnectionHistory = new(file.ConnectionHistory)
		if err := json.Unmarshal(m.ConnectionHistory, c.ConnectionHistory); err != nil {
			var typeErr *json.UnmarshalTypeError
			if !errors.As(err, &typeErr) {
				return nil, err
			}
		}
	}
	return c, nil
}

func clientToWire(c *file.Client) *control.Client {
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

func taskFromWire(m *control.Task) (*file.Tunnel, error) {
	client, err := clientFromWire(m.Client)
	if err != nil {
		return nil, err
	}
	t := &file.Tunnel{
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
	t.HealthCheckTimeout, t.HealthMaxFail, t.HealthCheckInterval = m.HealthCheckTimeout, m.HealthMaxFail, m.HealthCheckInterval
	t.HttpHealthUrl, t.HealthCheckType, t.HealthCheckTarget = m.HttpHealthUrl, m.HealthCheckType, m.HealthCheckTarget
	return t, nil
}

func taskToWire(t *file.Tunnel) *control.Task {
	return &control.Task{
		RateLimits: control.RateLimits{UploadLimit: t.UploadLimit, DownloadLimit: t.DownloadLimit},
		MaxConn:    t.MaxConn, Id: t.Id, Port: t.Port, ServerIp: t.ServerIp, Mode: t.Mode, Status: t.Status,
		RunStatus: t.RunStatus, Client: clientToWire(t.Client), Ports: t.Ports, Flow: flowToWire(t.Flow), NowConn: t.NowConn,
		Password: t.Password, Remark: t.Remark, TargetAddr: t.TargetAddr, TargetType: t.TargetType,
		DestAclMode: t.DestAclMode, DestAclRules: t.DestAclRules, SourceLists: listsToWire(&t.SourceLists),
		NoStore: t.NoStore, IsHttp: t.IsHttp, HttpProxy: t.HttpProxy, Socks5Proxy: t.Socks5Proxy,
		LocalPath: t.LocalPath, StripPre: t.StripPre, ReadOnly: t.ReadOnly, Target: targetToWire(t.Target),
		UserAuth: accountToWire(t.UserAuth), MultiAccount: accountToWire(t.MultiAccount),
		Health: control.Health{
			HealthCheckTimeout: t.HealthCheckTimeout, HealthMaxFail: t.HealthMaxFail, HealthCheckInterval: t.HealthCheckInterval,
			HttpHealthUrl: t.HttpHealthUrl, HealthCheckType: t.HealthCheckType, HealthCheckTarget: t.HealthCheckTarget,
		},
	}
}

func hostFromWire(m *control.Host) (*file.Host, error) {
	client, err := clientFromWire(m.Client)
	if err != nil {
		return nil, err
	}
	h := &file.Host{
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

func hostToWire(h *file.Host) *control.Host {
	return &control.Host{
		RateLimits: control.RateLimits{UploadLimit: h.UploadLimit, DownloadLimit: h.DownloadLimit},
		Id:         h.Id, Host: h.Host, HeaderChange: h.HeaderChange, RespHeaderChange: h.RespHeaderChange,
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

// WriteHealth reports that addr is up ("1") or not ("0").
func WriteHealth(w io.Writer, addr, status string) error {
	_, err := w.Write(control.EncodeHealth(addr, status))
	return err
}

// ReadHealth reads what WriteHealth wrote.
func ReadHealth(r io.Reader) (string, bool, error) { return control.ReadHealth(r) }

// FileKey names a client's file server on both sides of a file task.
func FileKey(key string, t *file.Tunnel) string {
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
