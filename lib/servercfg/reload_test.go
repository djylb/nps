package servercfg

import (
	"errors"
	"os"
	"slices"
	"testing"
)

func TestSettingsListsLeafFields(t *testing.T) {
	s := Settings()
	for _, want := range []string{"Log.Level", "Bridge.Display.TCP.Port", "Auth.TrustedProxies", "Network.QUICALPNList"} {
		if !slices.Contains(s, want) {
			t.Errorf("Settings lacks %s", want)
		}
	}
	if slices.Contains(s, "Bridge.Display") || slices.Contains(s, "Webhook") {
		t.Error("Settings lists a struct field")
	}
}

func TestCheckSettings(t *testing.T) {
	if err := CheckSettings([]Applier{{Name: "a", Settings: []string{"Log.Nope"}}}); err == nil {
		t.Error("unknown setting accepted")
	}
	if err := CheckSettings([]Applier{{Name: "a", Settings: []string{"Bridge.Display"}}, {Name: "b", Settings: []string{"Bridge.Display.WS.IP"}}}); err == nil {
		t.Error("overlapping settings accepted")
	}
	if got := Unowned([]Applier{{Settings: []string{"Webhook"}}}); slices.Contains(got, "Webhook.URL") || !slices.Contains(got, "Log.Level") {
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
		l.applier("level", []string{"Log.Level"}, nil),
		Applier{Name: "listener", Settings: []string{"Web.Port"}, Apply: RestartRequired},
		Applier{Name: "live", Settings: []string{"App.Name"}},
		Applier{Name: "same", Settings: []string{"Log.Path"}, Apply: func(_, cur *Snapshot) error {
			seen = "called"
			return nil
		}},
	)
	if !r.OK() || !slices.Equal(r.Applied, []string{"Log.Level", "App.Name"}) || !slices.Equal(r.Restart, []string{"Web.Port"}) {
		t.Fatalf("report = %+v", r)
	}
	if !slices.Equal(l.calls, []string{"level:info>debug"}) || seen != "" {
		t.Fatalf("calls = %v, unchanged applier: %q", l.calls, seen)
	}
	if Current().Log.Level != "debug" || Current().Web.Port != 9090 || String("appname") != "b" {
		t.Fatal("the new snapshot is not current")
	}
	if r = Reload(l.applier("level", []string{"Log.Level"}, nil)); !r.OK() || len(r.Applied) != 0 || len(l.calls) != 1 {
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
		l.applier("first", []string{"Log.Level"}, nil),
		Applier{Name: "web", Settings: []string{"App.Name"}, Apply: func(_, _ *Snapshot) error {
			during = Current().App.Name
			return errors.New("boom")
		}},
		l.applier("never", []string{"Log.Type", "Log.Path"}, nil),
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
	r := Reload(l.applier("level", []string{"Log.Level"}, nil))
	if r.OK() || r.Error == "" || len(l.calls) != 0 || Current() != old {
		t.Fatalf("report = %+v, calls %v", r, l.calls)
	}
}
