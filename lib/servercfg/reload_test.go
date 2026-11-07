package servercfg

import (
	"errors"
	"os"
	"slices"
	"testing"
)

func TestKeysAreTheKeysRead(t *testing.T) {
	keys := Keys()
	for _, want := range []string{"log_level", "web_port", "bridge_tcp_ip", "bridge_ip", "tls_bridge_port", "bridge_quic_show_alpn"} {
		if !slices.Contains(keys, want) {
			t.Errorf("Keys lacks %s", want)
		}
	}
	if !slices.IsSorted(keys) {
		t.Error("Keys is not sorted")
	}
}

func TestCheckSettings(t *testing.T) {
	if err := CheckSettings([]Applier{{Name: "a", Settings: []string{"log_nope"}}}); err == nil {
		t.Error("unknown key accepted")
	}
	if err := CheckSettings([]Applier{{Name: "a", Settings: []string{"web_port"}}, {Name: "b", Settings: []string{"web_port"}}}); err == nil {
		t.Error("key owned twice accepted")
	}
	if got := Unowned([]Applier{{Settings: []string{"webhook_url"}}}); slices.Contains(got, "webhook_url") || !slices.Contains(got, "log_level") {
		t.Errorf("Unowned = %v", got)
	}
}

type applyLog struct{ calls []string }

func (l *applyLog) applier(name string, settings []string, err error) Applier {
	return Applier{Name: name, Settings: settings, Apply: func(old, cur *Snapshot) error {
		l.calls = append(l.calls, name+":"+old.Log.Level+">"+cur.Log.Level)
		return err
	}}
}

func TestReloadAppliesChanges(t *testing.T) {
	resetTestState(t)
	path := writeConfig(t, "nps.conf", "log_level=info\nweb_port=8081\nappname=a\n")
	if err := Load(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("log_level=debug\nweb_port=9090\nappname=b\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var l applyLog
	var seen string
	r := Reload(
		l.applier("level", []string{"log_level"}, nil),
		Applier{Name: "listener", Settings: []string{"web_port", "web_ip"}, Apply: RestartRequired},
		Applier{Name: "live", Settings: []string{"appname", "http_proxy_access_port"}},
		Applier{Name: "same", Settings: []string{"log_path"}, Apply: func(_, cur *Snapshot) error {
			seen = "called"
			return nil
		}},
	)
	if !r.OK() || !slices.Equal(r.Applied, []string{"appname", "log_level"}) || !slices.Equal(r.Restart, []string{"web_port"}) {
		t.Fatalf("report = %+v", r)
	}
	if !slices.Equal(l.calls, []string{"level:info>debug"}) || seen != "" {
		t.Fatalf("calls = %v, unchanged applier: %q", l.calls, seen)
	}
	if Current().Log.Level != "debug" || Current().Web.Port != 9090 || String("appname") != "b" {
		t.Fatal("the new snapshot is not current")
	}
	if r = Reload(l.applier("level", []string{"log_level"}, nil)); !r.OK() || len(r.Applied) != 0 || len(l.calls) != 1 {
		t.Fatalf("reload without changes: %+v, calls %v", r, l.calls)
	}
}

func TestReloadFailureRestoresPrevious(t *testing.T) {
	resetTestState(t)
	path := writeConfig(t, "nps.conf", "log_level=info\nappname=a\n")
	if err := Load(path); err != nil {
		t.Fatal(err)
	}
	old := Current()
	if err := os.WriteFile(path, []byte("log_level=debug\nappname=b\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var l applyLog
	var during string
	r := Reload(
		l.applier("first", []string{"log_level"}, nil),
		Applier{Name: "web", Settings: []string{"appname"}, Apply: func(_, _ *Snapshot) error {
			during = Current().App.Name
			return errors.New("boom")
		}},
		l.applier("never", []string{"log", "log_path"}, nil),
	)
	if r.OK() || len(r.Failed) != 1 || r.Failed[0].Name != "web" || r.Failed[0].Error != "boom" || len(r.Applied) != 0 {
		t.Fatalf("report = %+v", r)
	}
	if during != "b" {
		t.Fatalf("the applier ran before the new snapshot was current: %q", during)
	}
	if !slices.Equal(l.calls, []string{"first:info>debug", "first:debug>info"}) {
		t.Fatalf("calls = %v", l.calls)
	}
	if Current() != old || String("appname") != "a" {
		t.Fatal("the previous snapshot is not current again")
	}
}

func TestReloadUnreadableKeepsPrevious(t *testing.T) {
	resetTestState(t)
	path := writeConfig(t, "nps.yaml", "log_level: info\n")
	if err := Load(path); err != nil {
		t.Fatal(err)
	}
	old := Current()
	if err := os.WriteFile(path, []byte("log_level: [\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var l applyLog
	r := Reload(l.applier("level", []string{"log_level"}, nil))
	if r.OK() || r.Error == "" || len(l.calls) != 0 || Current() != old {
		t.Fatalf("report = %+v, calls %v", r, l.calls)
	}
}

func TestReloadKeyChangesWithItsDefault(t *testing.T) {
	resetTestState(t)
	path := writeConfig(t, "nps.conf", "http_proxy_port=80\n")
	if err := Load(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("http_proxy_port=8080\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := Reload(
		Applier{Name: "listener", Settings: []string{"http_proxy_port"}, Apply: RestartRequired},
		Applier{Name: "links", Settings: []string{"http_proxy_access_port"}},
	)
	if !r.OK() || !slices.Equal(r.Applied, []string{"http_proxy_access_port"}) || !slices.Equal(r.Restart, []string{"http_proxy_port"}) {
		t.Fatalf("report = %+v", r)
	}
}
