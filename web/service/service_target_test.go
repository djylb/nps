package service

import (
	"errors"
	"testing"

	npsbridge "github.com/djylb/nps/bridge"
	"github.com/djylb/nps/lib/model"
)

type servicesRuntime struct {
	stubRuntime
	services []npsbridge.VirtualService
}

func (r servicesRuntime) VirtualServices() []npsbridge.VirtualService { return r.services }

func TestServiceTargetIsJudgedPerClientAndPerLine(t *testing.T) {
	a := model.ServiceTarget("file", "a")
	b := model.ServiceTarget("file", "b")
	svc := DefaultIndexService{Backend: Backend{Runtime: servicesRuntime{services: []npsbridge.VirtualService{
		{Client: 1, Kind: "file", Name: "a", Network: model.TargetTCP, Target: a},
		{Client: 2, Kind: "file", Name: "b", Network: model.TargetTCP, Target: b},
	}}}}

	tcp := &model.Tunnel{Mode: "udp", Target: &model.Target{TargetStr: a}}
	if err := svc.checkServiceTarget(tcp, 1); !errors.Is(err, ErrTunnelModeInvalid) {
		t.Fatalf("udp tunnel to the tcp service of its client: %v", err)
	}
	// another client's service of the same target is not this client's
	if err := svc.checkServiceTarget(tcp, 3); err != nil {
		t.Fatalf("other client judged: %v", err)
	}

	if !svc.ServiceAvailable(1, a+"\n127.0.0.1:80") {
		t.Fatal("reported service with a plain target not available")
	}
	if svc.ServiceAvailable(1, a+"\n"+b) {
		t.Fatal("a line naming another client's service must be unavailable")
	}
	if svc.ServiceAvailable(1, " "+b+" ") {
		t.Fatal("service of another client available")
	}
	if !svc.ServiceAvailable(1, "127.0.0.1:80\n127.0.0.1:81") {
		t.Fatal("plain targets must be available")
	}
}
