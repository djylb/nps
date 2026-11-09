package model

import (
	"reflect"
	"slices"
	"testing"

	"github.com/djylb/nps/internal/fieldtest"
)

// The struct pointer fields whose fields are settings of their own.
var settingStructs = []string{"Target", "Flow", "UserAuth", "MultiAccount", "HealthConfig"}

// Fields of tunnels and hosts that no setting sets, and why.
var (
	serverState = []string{"Id", "Client", "NowConn", "NoStore", "Routes", "Flow.ExportFlow", "Flow.InletFlow", "Flow.DirectIn", "Flow.DirectOut"}
	// Read as access lists, the same way for clients (web/api requestAccessLists).
	accessLists = []string{"IpListPriority", "BlackIpList", "WhiteIpList", "RegisteredOnly"}
	// Derived from the Content beside them; npc.conf multi_account is a file
	// read by lib/config.
	accounts = []string{"UserAuth.AccountMap", "MultiAccount.Content", "MultiAccount.AccountMap"}

	tunnelUnset = slices.Concat(serverState, accessLists, accounts, []string{"Status", "RunStatus", "IsHttp", "ServiceKind", "ServiceName", "Generated", "LaunchForm"})
	hostUnset   = slices.Concat(serverState, accessLists, accounts, []string{"IsClose", "CertType", "CertHash"})
)

// TestEntrySettingsCoverModel fails for a field added to Tunnel or Host
// until it is given a setting, which the web form, the API and npc.conf then
// read, or listed among the fields no setting sets.
func TestEntrySettingsCoverModel(t *testing.T) {
	checkSettings(t, "Tunnel", TunnelSettings, reflect.TypeFor[Tunnel](), tunnelUnset, func() any { return new(Tunnel) })
	checkSettings(t, "Host", HostSettings, reflect.TypeFor[Host](), hostUnset, func() any { return new(Host) })
}

func checkSettings(t *testing.T, kind string, settings []EntrySetting, typ reflect.Type, unset []string, newEntry func() any) {
	t.Helper()
	set := map[string]bool{}
	forms, confs := map[string]bool{}, map[string]bool{}
	for _, s := range settings {
		if s.Form == "" && s.Conf == "" {
			t.Errorf("%s %s: neither a form parameter nor an npc.conf key", kind, s.Field)
		}
		if s.Form != "" {
			if forms[s.Form] {
				t.Errorf("%s: form parameter %q twice", kind, s.Form)
			}
			forms[s.Form] = true
		}
		if s.Conf != "" {
			if confs[s.Conf] {
				t.Errorf("%s: npc.conf key %q twice", kind, s.Conf)
			}
			confs[s.Conf] = true
		}
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("%s %s: %v", kind, s.Field, r)
				}
			}()
			_ = s.Set(newEntry(), "1")
		}()
		set[s.Field] = true
	}
	for _, name := range unset {
		if set[name] {
			t.Errorf("%s %s has a setting and is listed as unset", kind, name)
		}
		set[name] = true
	}
	fields := fieldtest.Fields(typ, settingStructs...)
	for _, name := range fields {
		if !set[name] {
			t.Errorf("%s %s: no setting; add it to %sSettings or list it as unset", kind, name, kind)
		}
	}
	for name := range set {
		if !slices.Contains(fields, name) {
			t.Errorf("%s has no field %s", kind, name)
		}
	}
}

func TestEntrySettingSet(t *testing.T) {
	var tunnel Tunnel
	for _, c := range []struct {
		field, raw string
		want       any
		bad        bool
	}{
		{"MaxConn", " -3 ", 0, false},
		{"DestAclMode", "-3", -3, false},
		{"Flow.FlowLimit", "12", int64(12), false},
		{"Target.ProxyProtocol", "x", 0, false},
		{"HttpProxy", " T ", true, false},
		{"Socks5Proxy", "maybe", false, true},
		{"Remark", " a ", " a ", false},
	} {
		i := slices.IndexFunc(TunnelSettings, func(s EntrySetting) bool { return s.Field == c.field })
		err := TunnelSettings[i].Set(&tunnel, c.raw)
		if got := fieldtest.Value(reflect.ValueOf(&tunnel).Elem(), c.field).Interface(); got != c.want || (err != nil) != c.bad {
			t.Errorf("%s = %q: got %v, %v", c.field, c.raw, got, err)
		}
	}
}

// TestCloneCopiesFields fails for a field added to Tunnel that Clone does not
// copy.
func TestCloneCopiesFields(t *testing.T) {
	src := new(Tunnel)
	fieldtest.Fill(src, 0)
	c := src.Clone()
	sv, cv := reflect.ValueOf(src).Elem(), reflect.ValueOf(c).Elem()
	for _, name := range fieldtest.Fields(reflect.TypeFor[Tunnel](), "Flow") {
		if !reflect.DeepEqual(fieldtest.Value(cv, name).Interface(), fieldtest.Value(sv, name).Interface()) {
			t.Errorf("Clone does not copy %s", name)
		}
	}
}

// TestHostCloneCopiesFields fails for a field added to Host that Clone does
// not copy.
func TestHostCloneCopiesFields(t *testing.T) {
	src := new(Host)
	fieldtest.Fill(src, 0)
	c := src.Clone()
	sv, cv := reflect.ValueOf(src).Elem(), reflect.ValueOf(c).Elem()
	for _, name := range fieldtest.Fields(reflect.TypeFor[Host](), "Flow") {
		if !reflect.DeepEqual(fieldtest.Value(cv, name).Interface(), fieldtest.Value(sv, name).Interface()) {
			t.Errorf("Clone does not copy %s", name)
		}
	}
}

// TestUpdateCopiesFields fails for a field added to Tunnel or Host that
// Update, which takes the settings a client sends again, does not copy and
// that is not listed here.
func TestUpdateCopiesFields(t *testing.T) {
	kept := map[string][]string{
		"Tunnel": {"Id", "Port", "Status", "RunStatus", "Client", "Ports", "Flow", "NowConn", "TargetAddr",
			"NoStore", "IsHttp", "Routes", "UserAuth", "HealthConfig", "Compress", "ServiceKind", "ServiceName", "Generated", "LaunchForm"},
		"Host": {"Id", "Host", "Location", "Scheme", "TlsOffload", "CertType", "CertHash", "NoStore", "IsClose",
			"Flow", "NowConn", "Client", "Routes", "UserAuth", "HealthConfig", "Compress"},
	}
	for kind, pair := range map[string][2]any{"Tunnel": {new(Tunnel), new(Tunnel)}, "Host": {new(Host), new(Host)}} {
		dst, src := pair[0], pair[1]
		fieldtest.Fill(dst, 0)
		fieldtest.Fill(src, 1)
		switch d := dst.(type) {
		case *Tunnel:
			d.Update(src.(*Tunnel))
		case *Host:
			d.Update(src.(*Host))
		}
		dv, sv := reflect.ValueOf(dst).Elem(), reflect.ValueOf(src).Elem()
		for _, name := range fieldtest.Fields(dv.Type()) {
			if slices.Contains(kept[kind], name) {
				continue
			}
			if !reflect.DeepEqual(fieldtest.Value(dv, name).Interface(), fieldtest.Value(sv, name).Interface()) {
				t.Errorf("%s.Update does not copy %s", kind, name)
			}
		}
	}
}
