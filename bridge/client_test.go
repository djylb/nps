package bridge

import (
	"net"
	"testing"
	"time"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/file"
)

func TestSetClientSelectMode(t *testing.T) {
	tests := []struct {
		name    string
		in      any
		want    SelectMode
		wantErr bool
	}{
		{name: "enum", in: RoundRobin, want: RoundRobin},
		{name: "int", in: 2, want: Random},
		{name: "string alias", in: "rr", want: RoundRobin},
		{name: "string number", in: "0", want: Primary},
		{name: "invalid string", in: "bad", want: Primary, wantErr: true},
		{name: "out of range", in: 10, want: Primary, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := SetClientSelectMode(tt.in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("SetClientSelectMode(%v) err=%v, wantErr=%v", tt.in, err, tt.wantErr)
			}
			if ClientSelectMode != tt.want {
				t.Fatalf("SetClientSelectMode(%v) mode=%v, want=%v", tt.in, ClientSelectMode, tt.want)
			}
		})
	}
}

func TestClientGetNodeByFileRespectsGraceThenPrunesOfflineNode(t *testing.T) {
	node := NewNode("n1", "", 6)
	client := NewClient(1, node)

	if err := client.AddFile("file-key", "n1"); err != nil {
		t.Fatalf("AddFile returned error: %v", err)
	}

	if got, ok := client.GetNodeByFile("file-key"); ok || got != nil {
		t.Fatalf("expected no node during grace window, got ok=%v node=%v", ok, got)
	}
	if count := client.NodeCount(); count != 1 {
		t.Fatalf("node should be kept during grace window, count=%d", count)
	}

	client.lastConnectNano.Store(time.Now().Add(-10 * time.Second).UnixNano())
	if got, ok := client.GetNodeByFile("file-key"); ok || got != nil {
		t.Fatalf("expected no node after grace window, got ok=%v node=%v", ok, got)
	}
	if count := client.NodeCount(); count != 0 {
		t.Fatalf("offline node should be pruned after grace window, count=%d", count)
	}
}

func TestClientGetNodeByFileReturnsOnlineNodeWithoutExternalDependency(t *testing.T) {
	node := NewNode("n2", "", 6)
	client := NewClient(-1, node)

	if err := client.AddFile("f2", "n2"); err != nil {
		t.Fatalf("AddFile returned error: %v", err)
	}
	got, ok := client.GetNodeByFile("f2")
	if !ok || got != node {
		t.Fatalf("expected online node by file mapping, ok=%v got=%v", ok, got)
	}
}

func TestRemoveOfflineNodesRetriesBeforeRemoval(t *testing.T) {
	node := NewNode("n3", "", 6)
	client := NewClient(2, node)
	client.lastConnectNano.Store(time.Now().Add(-10 * time.Second).UnixNano())
	node.joinNano = time.Now().Add(-10 * time.Second).UnixNano()

	for i := 0; i < retryTimeMax; i++ {
		if removed := client.RemoveOfflineNodes(true); removed != 0 {
			t.Fatalf("attempt %d removed=%d, want 0 before retry limit", i+1, removed)
		}
	}

	if removed := client.RemoveOfflineNodes(true); removed != 1 {
		t.Fatalf("expected node removal after retries exhausted, removed=%d", removed)
	}
	if count := client.NodeCount(); count != 0 {
		t.Fatalf("expected no nodes left, count=%d", count)
	}
}

func TestNodeCloseIfSignalCurrentKeepsReplacedNode(t *testing.T) {
	newPipe := func() *conn.Conn {
		a, b := net.Pipe()
		t.Cleanup(func() { _ = a.Close(); _ = b.Close() })
		return conn.NewConn(a)
	}
	oldSignal, newSignal := newPipe(), newPipe()

	node := NewNode("n4", "", 6)
	node.AddSignal(oldSignal)
	node.AddSignal(newSignal)

	if node.CloseIfSignalCurrent(oldSignal) {
		t.Fatal("outdated signal closed the node")
	}
	if node.GetSignal() != newSignal || newSignal.IsClosed() {
		t.Fatal("outdated signal tore down the current signal")
	}
	if !node.CloseIfSignalCurrent(newSignal) {
		t.Fatal("current signal did not close the node")
	}
	if node.GetSignal() != nil || !newSignal.IsClosed() {
		t.Fatal("node not closed by its current signal")
	}
}

func TestClientNodeSelectModes(t *testing.T) {
	defer func() { ClientSelectMode = Primary }()
	ClientSelectMode = Primary
	client := NewClient(-1, NewNode("a", "", 8))
	client.AddNode(NewNode("b", "", 8))
	for i := 0; i < 3; i++ {
		if n := client.GetNode(); n == nil || n.UUID != "b" {
			t.Fatalf("Primary must stick to the last node, got %v", n)
		}
	}

	ClientSelectMode = RoundRobin
	seen := map[string]int{}
	for i := 0; i < 4; i++ {
		seen[client.GetNode().UUID]++
	}
	if seen["a"] != 2 || seen["b"] != 2 {
		t.Fatalf("RoundRobin rotation = %v", seen)
	}
	if client.LastUUID == "" {
		t.Fatalf("the chosen node must become the last node")
	}
}

func TestClientCheckNodeSkipsOfflineNodesInGrace(t *testing.T) {
	defer func() { ClientSelectMode = Primary }()
	ClientSelectMode = Primary
	node := NewNode("n4", "", 6)
	client := NewClient(3, node)
	if got := client.CheckNode(); got != nil {
		t.Fatalf("an offline node in its grace window must not be chosen, got %v", got)
	}
	if client.NodeCount() != 1 {
		t.Fatalf("an offline node in its grace window must be kept")
	}
	client.lastConnectNano.Store(time.Now().Add(-10 * time.Second).UnixNano())
	node.joinNano = time.Now().Add(-10 * time.Second).UnixNano()
	if got := client.CheckNode(); got != nil || client.NodeCount() != 0 {
		t.Fatalf("an offline node past its grace window must be removed, got %v count %d", got, client.NodeCount())
	}
}

func TestAllowsFlag(t *testing.T) {
	const (
		ctl = 1 << file.KeyControl
		pub = 1 << file.KeyPublic
		vis = 1 << file.KeyVisitor
	)
	want := map[string]int{
		common.WorkMain:       ctl,
		common.WorkChan:       ctl | pub,
		common.WorkFile:       ctl | pub,
		common.WorkP2PSession: ctl | pub,
		common.WorkConfig:     ctl | pub,
		common.WorkRegister:   ctl | pub,
		common.WorkVisitor:    ctl | pub | vis,
		common.WorkSecret:     ctl | pub | vis,
		common.WorkP2P:        ctl | pub | vis,
		"none":                0,
	}
	for flag, mask := range want {
		for _, kind := range []file.KeyKind{file.KeyControl, file.KeyPublic, file.KeyVisitor} {
			if got := allowsFlag(kind, flag); got != (mask&(1<<kind) != 0) {
				t.Errorf("allowsFlag(%d, %q) = %v", kind, flag, got)
			}
		}
	}
}
