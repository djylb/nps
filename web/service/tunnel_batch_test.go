package service

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/djylb/nps/lib/model"
)

// batchRepo and batchRuntime record what a batch stores and starts.
type batchRepo struct {
	stubRepository
	maxTunnels int
	existing   int
	next       *int
	created    *[]*model.Tunnel
	records    *[]int
}

func (r batchRepo) NextTunnelID() int { *r.next++; return *r.next }
func (r batchRepo) GetClient(id int) (*model.Client, error) {
	return &model.Client{Id: id, Status: true, Flow: &model.Flow{}, MaxTunnelNum: r.maxTunnels}, nil
}
func (r batchRepo) ClientEntryCount(*model.Client) int { return r.existing }
func (r batchRepo) CreateTunnel(t *model.Tunnel) error {
	*r.created = append(*r.created, t)
	*r.records = append(*r.records, t.Id)
	return nil
}
func (r batchRepo) DeleteTunnelRecord(id int) error {
	*r.records = slices.DeleteFunc(*r.records, func(v int) bool { return v == id })
	return nil
}

type batchRuntime struct {
	stubRuntime
	repo    batchRepo
	taken   map[int]bool
	failAdd int // port whose start fails
	started *[]int
}

func (r batchRuntime) TunnelPortAvailable(t *model.Tunnel) bool { return !r.taken[t.Port] }
func (r batchRuntime) AddTunnel(t *model.Tunnel) error {
	if t.Port == r.failAdd {
		return errors.New("listen failed")
	}
	*r.started = append(*r.started, t.Port)
	return nil
}
func (r batchRuntime) DeleteTunnel(id int) error {
	return r.repo.DeleteTunnelRecord(id)
}

type batchEnv struct {
	svc     DefaultIndexService
	created []*model.Tunnel
	records []int
	started []int
}

func newBatchEnv(maxTunnels, existing int, taken map[int]bool, failAdd int) *batchEnv {
	e := new(batchEnv)
	next := 0
	repo := batchRepo{maxTunnels: maxTunnels, existing: existing, next: &next, created: &e.created, records: &e.records}
	rt := batchRuntime{repo: repo, taken: taken, failAdd: failAdd, started: &e.started}
	e.svc = DefaultIndexService{Backend: Backend{Repository: repo, Runtime: rt}}
	return e
}

func batchInput(mode, port, target, remark string) TunnelInput {
	return TunnelInput{ClientID: 1, PortSpec: port, Tunnel: &model.Tunnel{Mode: mode, Remark: remark, Target: &model.Target{TargetStr: target}}}
}

func portsOf(ts []*model.Tunnel) (out []int) {
	for _, t := range ts {
		out = append(out, t.Port)
	}
	return
}

func TestAddTunnelsRangeOneToOne(t *testing.T) {
	e := newBatchEnv(0, 0, nil, 0)
	res, err := e.svc.AddTunnels(batchInput("tcp", "8000-8002,9000", "127.0.0.1:80-82,90", "web"))
	if err != nil || len(res) != 4 {
		t.Fatalf("AddTunnels = %v, %v", res, err)
	}
	wantPorts, wantTargets, wantRemarks := []int{8000, 8001, 8002, 9000}, []string{"127.0.0.1:80", "127.0.0.1:81", "127.0.0.1:82", "127.0.0.1:90"}, []string{"web-8000", "web-8001", "web-8002", "web-9000"}
	for i, tn := range e.created {
		if tn.Port != wantPorts[i] || tn.Target.TargetStr != wantTargets[i] || tn.Remark != wantRemarks[i] {
			t.Errorf("tunnel %d = %d %q %q", i, tn.Port, tn.Target.TargetStr, tn.Remark)
		}
	}
	if !slices.Equal(e.started, wantPorts) {
		t.Errorf("started %v", e.started)
	}
}

func TestAddTunnelsSingleTargetForAllPorts(t *testing.T) {
	e := newBatchEnv(0, 0, nil, 0)
	if _, err := e.svc.AddTunnels(batchInput("udp", "8000-8001", "10.0.0.5:53", "")); err != nil {
		t.Fatal(err)
	}
	for i, tn := range e.created {
		if tn.Target.TargetStr != "10.0.0.5:53" || tn.Remark != []string{"8000", "8001"}[i] {
			t.Errorf("tunnel %d = %q %q", i, tn.Target.TargetStr, tn.Remark)
		}
	}
	// A bare target port is kept bare.
	e = newBatchEnv(0, 0, nil, 0)
	if _, err := e.svc.AddTunnels(batchInput("tcp", "8000,8001", "80", "")); err != nil || e.created[1].Target.TargetStr != "80" {
		t.Fatalf("bare target: %v", err)
	}
}

func TestAddTunnelsTargetCountMismatch(t *testing.T) {
	e := newBatchEnv(0, 0, nil, 0)
	_, err := e.svc.AddTunnels(batchInput("tcp", "8000-8003", "127.0.0.1:80-81", ""))
	if !errors.Is(err, ErrPortRange) || !strings.Contains(err.Error(), "host:port") {
		t.Fatalf("err = %v", err)
	}
	if len(e.created) != 0 {
		t.Fatal("created on error")
	}
	_, err = e.svc.AddTunnels(batchInput("tcp", "8000-8003", "127.0.0.1:80\n127.0.0.1:81", ""))
	if !errors.Is(err, ErrPortRange) || !strings.Contains(err.Error(), "one target line") {
		t.Fatalf("multi-line err = %v", err)
	}
}

func TestAddTunnelsModeAndLimit(t *testing.T) {
	e := newBatchEnv(0, 0, nil, 0)
	if _, err := e.svc.AddTunnels(batchInput("mixProxy", "8000-8001", "", "")); !errors.Is(err, ErrPortRange) {
		t.Fatalf("mixProxy err = %v", err)
	}
	in := batchInput("tcp", "8000-8010", "80", "")
	in.PortRangeMax = 10
	if _, err := e.svc.AddTunnels(in); !errors.Is(err, ErrPortRange) || !strings.Contains(err.Error(), "11 ports") {
		t.Fatalf("limit err = %v", err)
	}
	in.PortRangeMax = 11
	if res, err := e.svc.AddTunnels(in); err != nil || len(res) != 11 {
		t.Fatalf("at limit: %v %v", len(res), err)
	}
	e = newBatchEnv(0, 0, nil, 0)
	in = batchInput("tcp", "8000-8010", "80", "")
	in.PortRangeMax = 0
	if res, err := e.svc.AddTunnels(in); err != nil || len(res) != 11 {
		t.Fatalf("unlimited: %v %v", len(res), err)
	}
	if _, err := e.svc.AddTunnels(batchInput("tcp", "a-b", "80", "")); !errors.Is(err, ErrPortRange) {
		t.Fatalf("garbage err = %v", err)
	}
}

func TestAddTunnelsPortConflictCreatesNothing(t *testing.T) {
	e := newBatchEnv(0, 0, map[int]bool{8002: true}, 0)
	_, err := e.svc.AddTunnels(batchInput("tcp", "8000-8003", "80", ""))
	var pe *PortError
	if !errors.As(err, &pe) || pe.Port != 8002 || !errors.Is(err, ErrPortUnavailable) {
		t.Fatalf("err = %v", err)
	}
	if len(e.created) != 0 || len(e.started) != 0 {
		t.Fatalf("created %v started %v", portsOf(e.created), e.started)
	}
}

func TestAddTunnelsClientLimit(t *testing.T) {
	e := newBatchEnv(5, 3, nil, 0)
	if _, err := e.svc.AddTunnels(batchInput("tcp", "8000-8002", "80", "")); !errors.Is(err, ErrTunnelLimitExceeded) {
		t.Fatalf("err = %v", err)
	}
	if len(e.created) != 0 {
		t.Fatal("created over the limit")
	}
	if res, err := e.svc.AddTunnels(batchInput("tcp", "8000-8001", "80", "")); err != nil || len(res) != 2 {
		t.Fatalf("within limit: %v %v", res, err)
	}
}

func TestAddTunnelsStartFailureRollsBack(t *testing.T) {
	e := newBatchEnv(0, 0, nil, 8002)
	_, err := e.svc.AddTunnels(batchInput("tcp", "8000-8003", "80", ""))
	var pe *PortError
	if !errors.As(err, &pe) || pe.Port != 8002 {
		t.Fatalf("err = %v", err)
	}
	if len(e.records) != 0 {
		t.Fatalf("records left: %v", e.records)
	}
}

func TestAddTunnelsSinglePortUnchanged(t *testing.T) {
	e := newBatchEnv(0, 0, nil, 0)
	in := batchInput("tcp", "8000", "127.0.0.1:80", "x")
	in.Tunnel.Port = 8000
	res, err := e.svc.AddTunnels(in)
	if err != nil || len(res) != 1 || e.created[0].Remark != "x" || e.created[0].Target.TargetStr != "127.0.0.1:80" {
		t.Fatalf("%v %v", res, err)
	}
	// A list that holds one port is a single port too.
	e = newBatchEnv(0, 0, nil, 0)
	if _, err := e.svc.AddTunnels(batchInput("tcp", "8005,8005", "127.0.0.1:80", "x")); err != nil || e.created[0].Port != 8005 || e.created[0].Remark != "x" {
		t.Fatalf("one-port list: %v", err)
	}
}
