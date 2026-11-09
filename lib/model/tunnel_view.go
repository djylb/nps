package model

import "github.com/djylb/nps/lib/acl"

// SourceView is an immutable view of an entry's source lists: the compiled
// lists and whether the visitor address has to be registered. The zero value
// stands for no entry and allows everything.
type SourceView struct {
	acl            *acl.Source
	registeredOnly bool
}

// View returns the view of l, compiling the lists as user lists if that has
// not happened yet. The caller has to keep l from being rewritten meanwhile.
func (l *SourceLists) View() SourceView {
	if l == nil {
		return SourceView{}
	}
	return SourceView{acl: l.SourceACL(), registeredOnly: l.RegisteredOnly}
}

// ACL returns the compiled lists.
func (v SourceView) ACL() *acl.Source { return v.acl }

// RegisteredOnly reports whether only registered visitor addresses are let in.
func (v SourceView) RegisteredOnly() bool { return v.registeredOnly }

// TunnelView is an immutable copy of the settings of a tunnel that an update
// from the client's configuration can replace while the tunnel is serving.
// Target and MultiAccount are replaced by an update, never modified, so the
// view may keep reading through them.
//
// Code that reads these fields on a tunnel other than one it is still
// building takes a view and reads the view; the fields themselves are only
// touched under the tunnel's lock, here and in Update.
type TunnelView struct {
	ServerIp     string
	Mode         string
	Password     string
	Remark       string
	TargetType   string
	HttpProxy    bool
	Socks5Proxy  bool
	ReadOnly     bool
	DestAclMode  int
	DestAclRules string
	LocalPath    string
	StripPre     string
	Target       *Target
	MultiAccount *MultiAccount
	Source       SourceView
}

// View returns the current settings of t.
func (t *Tunnel) View() TunnelView {
	t.RLock()
	defer t.RUnlock()
	return t.viewLocked()
}

func (t *Tunnel) viewLocked() TunnelView {
	return TunnelView{
		ServerIp:     t.ServerIp,
		Mode:         t.Mode,
		Password:     t.Password,
		Remark:       t.Remark,
		TargetType:   t.TargetType,
		HttpProxy:    t.HttpProxy,
		Socks5Proxy:  t.Socks5Proxy,
		DestAclMode:  t.DestAclMode,
		DestAclRules: t.DestAclRules,
		LocalPath:    t.LocalPath,
		StripPre:     t.StripPre,
		ReadOnly:     t.ReadOnly,
		Target:       t.Target,
		MultiAccount: t.MultiAccount,
		Source:       t.SourceLists.View(),
	}
}

// HostView is an immutable copy of the settings of a host that an update from
// the client's configuration can replace while the host is serving. Target
// and MultiAccount are replaced by an update, never modified, so the view may
// keep reading through them.
//
// Code that reads these fields on a host other than one it is still building
// takes a view and reads the view; the fields themselves are only touched
// under the host's lock, here and in Update.
type HostView struct {
	HeaderChange     string
	RespHeaderChange string
	HostChange       string
	PathRewrite      string
	Remark           string
	RedirectURL      string
	CertType         string
	CertHash         string
	CertFile         string
	KeyFile          string
	HttpsJustProxy   bool
	AutoSSL          bool
	AutoHttps        bool
	AutoCORS         bool
	CompatMode       bool
	TargetIsHttps    bool
	Target           *Target
	MultiAccount     *MultiAccount
	Source           SourceView
}

// View returns the current settings of s.
func (s *Host) View() HostView {
	s.RLock()
	defer s.RUnlock()
	return s.viewLocked()
}

func (s *Host) viewLocked() HostView {
	return HostView{
		HeaderChange:     s.HeaderChange,
		RespHeaderChange: s.RespHeaderChange,
		HostChange:       s.HostChange,
		PathRewrite:      s.PathRewrite,
		Remark:           s.Remark,
		RedirectURL:      s.RedirectURL,
		HttpsJustProxy:   s.HttpsJustProxy,
		AutoSSL:          s.AutoSSL,
		CertType:         s.CertType,
		CertHash:         s.CertHash,
		CertFile:         s.CertFile,
		KeyFile:          s.KeyFile,
		AutoHttps:        s.AutoHttps,
		AutoCORS:         s.AutoCORS,
		CompatMode:       s.CompatMode,
		TargetIsHttps:    s.TargetIsHttps,
		Target:           s.Target,
		MultiAccount:     s.MultiAccount,
		Source:           s.SourceLists.View(),
	}
}

// HasCert reports whether the host has a certificate configured.
func (v HostView) HasCert() bool { return v.CertFile != "" && v.KeyFile != "" }

// ListenIP returns the address t listens on, setting the wildcard address if
// none is set.
func (t *Tunnel) ListenIP() string {
	t.Lock()
	defer t.Unlock()
	if t.ServerIp == "" {
		t.ServerIp = "0.0.0.0"
	}
	return t.ServerIp
}

// Modify runs fn with t locked for writing, for an edit of the settings of a
// tunnel that is serving. fn must not call methods of t that take its lock.
func (t *Tunnel) Modify(fn func(t *Tunnel)) {
	t.Lock()
	defer t.Unlock()
	fn(t)
}

// Modify runs fn with s locked for writing, for an edit of the settings of a
// host that is serving. fn must not call methods of s that take its lock.
func (s *Host) Modify(fn func(s *Host)) {
	s.Lock()
	defer s.Unlock()
	fn(s)
}
