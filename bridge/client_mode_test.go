package bridge

import (
	"sync/atomic"
	"testing"
	"time"
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

	atomic.StoreInt64(&client.lastConnectNano, time.Now().Add(-10*time.Second).UnixNano())
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
	atomic.StoreInt64(&client.lastConnectNano, time.Now().Add(-10*time.Second).UnixNano())
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
