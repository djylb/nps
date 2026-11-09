package model

// ClientInfo describes a client in events. Like TunnelInfo and HostInfo it
// holds no keys, passwords or certificates.
type ClientInfo struct {
	ID      int    `json:"id"`
	Remark  string `json:"remark,omitempty"`
	Addr    string `json:"addr,omitempty"`
	Version string `json:"version,omitempty"`
}

// TunnelInfo describes a tunnel in events.
type TunnelInfo struct {
	ID       int    `json:"id"`
	ClientID int    `json:"client_id"`
	Mode     string `json:"mode"`
	ServerIP string `json:"server_ip,omitempty"`
	Port     int    `json:"port"`
	Target   string `json:"target,omitempty"`
	Remark   string `json:"remark,omitempty"`
	Enabled  bool   `json:"enabled"`
	// FromClient marks an entry delivered by the client's configuration, which
	// goes when the client does.
	FromClient bool `json:"from_client"`
}

// HostInfo describes a domain entry in events.
type HostInfo struct {
	ID         int    `json:"id"`
	ClientID   int    `json:"client_id"`
	Host       string `json:"host"`
	Location   string `json:"location,omitempty"`
	Scheme     string `json:"scheme,omitempty"`
	Target     string `json:"target,omitempty"`
	Remark     string `json:"remark,omitempty"`
	Enabled    bool   `json:"enabled"`
	FromClient bool   `json:"from_client"`
}

// NewTunnelInfo snapshots t.
func NewTunnelInfo(t *Tunnel) TunnelInfo {
	v := t.View()
	info := TunnelInfo{ID: t.Id, Mode: v.Mode, ServerIP: v.ServerIp, Port: t.Port, Remark: v.Remark, Enabled: t.Status, FromClient: t.NoStore}
	if t.Client != nil {
		info.ClientID = t.Client.Id
	}
	if v.Target != nil {
		info.Target = v.Target.TargetStr
	}
	return info
}

// NewHostInfo snapshots h.
func NewHostInfo(h *Host) HostInfo {
	info := HostInfo{ID: h.Id, Host: h.Host, Location: h.Location, Scheme: h.Scheme, Remark: h.Remark, Enabled: !h.IsClose, FromClient: h.NoStore}
	if h.Client != nil {
		info.ClientID = h.Client.Id
	}
	if h.Target != nil {
		info.Target = h.Target.TargetStr
	}
	return info
}
