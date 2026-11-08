package model

import (
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/djylb/nps/lib/textutil"
)

// EntrySetting is one setting of a tunnel or host: the model field it sets,
// the web form and API parameter and the npc.conf key that set it.
// TunnelSettings and HostSettings are the only lists of them; the web form,
// the API and npc.conf are all read through these lists.
type EntrySetting struct {
	// Field is the model field, with the fields of pointer fields reached
	// through dots, as "Target.ProxyProtocol". It is a string, int, int64,
	// bool or time.Time field.
	Field string
	Form  string // web form and API parameter; "" if the web does not set it
	Conf  string // npc.conf key; "" if npc.conf does not set it

	// FormRaw keeps the form value as entered; other form values are
	// HTML-escaped.
	FormRaw bool
	// ConfList takes commas in the npc.conf value for line breaks.
	ConfList bool
	// ConfPEM is the PEM block type of an npc.conf value that is PEM text or
	// the path of a PEM file.
	ConfPEM string
	// ConfDefault is the value an npc.conf entry has without the key.
	ConfDefault string
	// Min0 stores a negative number as zero.
	Min0 bool
	// Custom marks a form value the web service checks or derives the stored
	// value from itself; it copies the other form values as they are.
	Custom bool
}

// Settings shared by tunnels and hosts.
var entrySettings = []EntrySetting{
	{Field: "Target.TargetStr", Form: "target", Conf: "target_addr", ConfList: true, Custom: true},
	{Field: "Target.ProxyProtocol", Form: "proxy_protocol", Conf: "proxy_protocol", Custom: true},
	{Field: "Target.LocalProxy", Form: "local_proxy", Custom: true},
	{Field: "UserAuth.Content", Form: "auth", Custom: true},
	{Field: "Remark", Form: "remark"},
	{Field: "MaxConn", Form: "max_conn", Conf: "max_conn", Min0: true, Custom: true},
	{Field: "UploadLimit", Form: "upload_limit", Conf: "upload_limit", Min0: true, Custom: true},
	{Field: "DownloadLimit", Form: "download_limit", Conf: "download_limit", Min0: true, Custom: true},
	{Field: "Flow.FlowLimit", Form: "flow_limit", Custom: true},
	{Field: "Flow.TimeLimit", Form: "time_limit", Custom: true},
	{Field: "HealthConfig.Enabled", Form: "health_check", Custom: true},
	{Field: "HealthConfig.Type", Form: "health_check_type", FormRaw: true, Custom: true},
	{Field: "HealthConfig.Path", Form: "health_check_path", FormRaw: true, Custom: true},
	{Field: "HealthConfig.Interval", Form: "health_check_interval", Custom: true},
	{Field: "HealthConfig.Timeout", Form: "health_check_timeout", Custom: true},
	{Field: "HealthConfig.MaxFail", Form: "health_check_max_fail", Custom: true},
}

// TunnelSettings are the settings of a Tunnel.
var TunnelSettings = append([]EntrySetting{
	{Field: "Port", Form: "port", Custom: true},
	{Field: "Ports", Conf: "server_port"},
	{Field: "ServerIp", Form: "server_ip", Conf: "server_ip"},
	{Field: "Mode", Form: "type", Conf: "mode", Custom: true},
	{Field: "TargetType", Form: "target_type"},
	{Field: "Target.TargetStr", Conf: "target_port"},
	{Field: "TargetAddr", Conf: "target_ip"},
	{Field: "Password", Form: "password", Conf: "password"},
	{Field: "LocalPath", Form: "local_path", Conf: "local_path"},
	{Field: "StripPre", Form: "strip_pre", Conf: "strip_pre"},
	{Field: "HttpProxy", Form: "enable_http", Conf: "http_proxy"},
	{Field: "Socks5Proxy", Form: "enable_socks5", Conf: "socks5_proxy"},
	{Field: "DestAclMode", Form: "dest_acl_mode", Conf: "dest_acl_mode", Custom: true},
	{Field: "DestAclRules", Form: "dest_acl_rules", FormRaw: true, Conf: "dest_acl_rules", ConfList: true, Custom: true},
	{Field: "ReadOnly", Conf: "read_only"},
}, entrySettings...)

// HostSettings are the settings of a Host.
var HostSettings = append([]EntrySetting{
	{Field: "Host", Form: "host", Conf: "host"},
	{Field: "HeaderChange", Form: "header"},
	{Field: "RespHeaderChange", Form: "resp_header"},
	{Field: "HostChange", Form: "hostchange", Conf: "host_change"},
	{Field: "Location", Form: "location", Conf: "location"},
	{Field: "PathRewrite", Form: "path_rewrite", Conf: "path_rewrite"},
	{Field: "RedirectURL", Form: "redirect_url", Conf: "redirect_url"},
	{Field: "Scheme", Form: "scheme", Conf: "scheme", ConfDefault: "all", Custom: true},
	{Field: "HttpsJustProxy", Form: "https_just_proxy", Conf: "https_just_proxy"},
	{Field: "TlsOffload", Form: "tls_offload"},
	{Field: "AutoSSL", Form: "auto_ssl", Conf: "auto_ssl"},
	{Field: "KeyFile", Form: "key_file", Conf: "key_file", ConfPEM: "PRIVATE"},
	{Field: "CertFile", Form: "cert_file", Conf: "cert_file", ConfPEM: "CERTIFICATE"},
	{Field: "AutoHttps", Form: "auto_https", Conf: "auto_https"},
	{Field: "AutoCORS", Form: "auto_cors", Conf: "auto_cors"},
	{Field: "CompatMode", Form: "compat_mode", Conf: "compat_mode"},
	{Field: "TargetIsHttps", Form: "target_is_https", Conf: "target_is_https"},
}, entrySettings...)

var timeType = reflect.TypeFor[time.Time]()

// field returns the field of entry, a *Tunnel or *Host, allocating the
// structs on its path that are nil.
func (s EntrySetting) field(entry any) reflect.Value {
	v := reflect.ValueOf(entry).Elem()
	for name := range strings.SplitSeq(s.Field, ".") {
		if v.Kind() == reflect.Pointer {
			if v.IsNil() {
				v.Set(reflect.New(v.Type().Elem()))
			}
			v = v.Elem()
		}
		v = v.FieldByName(name)
		if !v.IsValid() {
			panic(fmt.Sprintf("model: %T has no field %s", entry, s.Field))
		}
	}
	return v
}

// Set stores raw in the field of entry, a *Tunnel or *Host, parsing it for
// the type of the field. A number that cannot be parsed is stored as zero;
// a boolean that cannot be parsed is stored as false and reported.
func (s EntrySetting) Set(entry any, raw string) error {
	v := s.field(entry)
	switch {
	case v.Type() == timeType:
		v.Set(reflect.ValueOf(textutil.GetTimeNoErrByStr(raw)))
	case v.Kind() == reflect.String:
		v.SetString(raw)
	case v.Kind() == reflect.Int || v.Kind() == reflect.Int64:
		n := textutil.GetIntNoErrByStr(raw)
		if s.Min0 {
			n = max(n, 0)
		}
		v.SetInt(int64(n))
	case v.Kind() == reflect.Bool:
		b, err := textutil.ParseBool(raw)
		v.SetBool(b)
		if err != nil {
			return err
		}
	default:
		panic("model: unsupported setting type " + v.Type().String() + " of " + s.Field)
	}
	return nil
}

// CopyFormSettings copies the form settings that are not Custom from src to
// dst, both a *Tunnel or both a *Host.
func CopyFormSettings(settings []EntrySetting, dst, src any) {
	for _, s := range settings {
		if s.Form != "" && !s.Custom {
			s.field(dst).Set(s.field(src))
		}
	}
}
