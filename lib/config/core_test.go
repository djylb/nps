package config

import "testing"

const coreKeys = "[common]\nserver_addr=tls://a.com:8025|wss://a.com:443/ws?tier=1\nvkey=k\nconn_type=tls\npipes=multi\nscheduler_up=spread_eta\n" +
	"session_max_lanes=3\nrelay_idle=20\np2p_visitor_keep_server=true\nsession_resume_timeout=-1\n"

func TestCommonCoreKeys(t *testing.T) {
	c, err := parseConfig(coreKeys)
	if err != nil {
		t.Fatal(err)
	}
	cc := c.CommonConfig
	want := CoreSettings{MaxLanes: 3, RelayIdle: 20, KeepServer: true, ResumeTimeout: -1}
	if cc.CoreSettings != want || cc.Pipes != "multi" || cc.SchedulerUp != "spread_eta" ||
		cc.Server != "tls://a.com:8025|wss://a.com:443/ws?tier=1" || cc.VKey != "k" || cc.Tp != "tls" {
		t.Fatalf("%+v", cc)
	}
}

// A flag is the default of a file that does not set the key; the file wins.
func TestCommonDefaultsFromCommandLine(t *testing.T) {
	old := CommonDefaults
	t.Cleanup(func() { CommonDefaults = old })
	CommonDefaults = Defaults{Pipes: "multi", SchedulerUp: "spread_round_robin",
		CoreSettings: CoreSettings{MaxLanes: 5, RelayIdle: 7, ResumeTimeout: 9}}

	c, err := parseConfig("[common]\nserver_addr=a:1\nvkey=k\nrelay_idle=40\nnew_protocol=false\n")
	if err != nil {
		t.Fatal(err)
	}
	cc := c.CommonConfig
	want := CoreSettings{MaxLanes: 5, RelayIdle: 40, ResumeTimeout: 9}
	if cc.CoreSettings != want || cc.Pipes != "multi" || cc.SchedulerUp != "spread_round_robin" {
		t.Fatalf("%+v", cc)
	}
	if n := NewCommonConfig(); n.Tp != "tcp" || !n.AutoReconnection || n.Client == nil || n.Client.Cnf == nil {
		t.Fatalf("%+v", n)
	}
}

func TestCommonCoreKeysInvalid(t *testing.T) {
	if _, err := parseConfig("[common]\nserver_addr=a:1\nvkey=k\nscheduler_up=nonsense\n"); err == nil {
		t.Fatal("bad scheduler_up accepted")
	}
	if _, err := parseConfig("[common]\nserver_addr=a:1\nvkey=k\npipes=nonsense\n"); err == nil {
		t.Fatal("bad pipes accepted")
	}
}
