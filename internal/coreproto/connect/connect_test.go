package connect

import (
	"testing"
	"time"

	"github.com/djylb/lifecycle/lifecycletest"
	"github.com/djylb/nps/client"
	"github.com/djylb/nps/lib/config"
	"github.com/djylb/nps/lib/connprofile"
)

func TestMain(m *testing.M) { lifecycletest.VerifyMain(m) }

// The settings of a profile reach the dial parameters in one place, from
// whichever source they came.
func TestDialConfig(t *testing.T) {
	c, err := config.Parse("[common]\nserver_addr=tls://a.com:8025|wss://a.com:443/ws?tier=1\nvkey=k\nconn_type=tcp\npipes=multi\n" +
		"scheduler_up=spread_eta\nsession_max_lanes=3\nsession_resume_timeout=-1\nlocal_ip=127.0.0.1\nproxy_url=socks5://127.0.0.1:1\n")
	if err != nil {
		t.Fatal(err)
	}
	cc := c.CommonConfig
	dc, err := DialConfig(cc, cc.Server)
	if err != nil {
		t.Fatal(err)
	}
	if len(dc.Candidates) != 2 || dc.Candidates[0].Carrier != "tls" || dc.Candidates[1].Carrier != "wss" ||
		dc.VKey != "k" || dc.Pipes != connprofile.PipesMulti || dc.Scheduler != "spread_eta" ||
		dc.MaxLanes != 3 || dc.ResumeTimeout != -time.Second || dc.Exit == nil || dc.PeerExit == nil || dc.Puncher == nil {
		t.Fatalf("%+v", dc)
	}
	if dc.CanUpdate != client.UpdateEnabled() || dc.Update == nil {
		t.Fatalf("update: CanUpdate = %v, enabled = %v", dc.CanUpdate, client.UpdateEnabled())
	}
	moved := *cc
	moved.ResumeTimeout = 12
	if dc, err = DialConfig(&moved, moved.Server); err != nil || dc.ResumeTimeout != 12*time.Second {
		t.Fatalf("%v %v", dc.ResumeTimeout, err)
	}
	moved.Pin = "zz"
	if _, err = DialConfig(&moved, moved.Server); err == nil {
		t.Fatal("bad pin accepted")
	}
	moved.Pin = ""
	moved.Server = "tls://"
	if _, err = DialConfig(&moved, moved.Server); err == nil {
		t.Fatal("bad server accepted")
	}
}
