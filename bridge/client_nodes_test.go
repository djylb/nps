package bridge

import (
	"testing"
	"time"
)

// pastGrace moves the client and its nodes out of their grace windows.
func pastGrace(c *Client, nodes ...*Node) {
	c.lastConnectNano.Store(time.Now().Add(-10 * time.Second).UnixNano())
	for _, n := range nodes {
		n.mu.Lock()
		n.joinNano = time.Now().Add(-10 * time.Second).UnixNano()
		n.mu.Unlock()
	}
}

func TestClientAddNodeSameUUID(t *testing.T) {
	// Without node IDs a second connection of an online node is refused.
	first := NewNode("a", "0.26.0", 5)
	client := NewClient(-1, first, Primary)
	dup := NewNode("a", "0.26.1", 5)
	client.AddNode(dup)
	if n, ok := client.GetNodeByUUID("a"); !ok || n != first || client.NodeCount() != 1 {
		t.Fatalf("duplicate replaced the online node: %v %v count %d", n, ok, client.NodeCount())
	}
	if _, v := first.versions(); v != "0.26.0" {
		t.Fatalf("refused duplicate changed the node version to %q", v)
	}

	// With node IDs it is the same process: the node takes the new details.
	first = NewNode("b", "0.30.0", 6)
	client = NewClient(-1, first, Primary)
	client.AddNode(NewNode("b", "0.31.0", 7))
	n, ok := client.GetNodeByUUID("b")
	if !ok || n != first || client.NodeCount() != 1 {
		t.Fatalf("rejoin must keep the node entry: %v %v count %d", n, ok, client.NodeCount())
	}
	if bv, v := n.versions(); bv != 7 || v != "0.31.0" {
		t.Fatalf("rejoin versions = %d %q", bv, v)
	}
	if client.LastUUID != "b" {
		t.Fatalf("LastUUID = %q", client.LastUUID)
	}
}

func TestClientRemoveNodeUpdatesLastAndFiles(t *testing.T) {
	a, b := NewNode("a", "", 6), NewNode("b", "", 6)
	client := NewClient(4, a, Primary)
	client.AddNode(b)
	if err := client.setNodeServices("b", []string{"f"}); err != nil {
		t.Fatal(err)
	}
	if client.LastUUID != "b" {
		t.Fatalf("LastUUID = %q", client.LastUUID)
	}
	if !client.closeAndRemoveNodeIfCurrent("b", b, (*Node).IsOffline) {
		t.Fatal("offline node not removed")
	}
	if client.LastUUID != "a" {
		t.Fatalf("LastUUID after removal = %q, want a", client.LastUUID)
	}
	if _, ok := client.GetNodeByUUID("b"); ok || client.NodeCount() != 1 {
		t.Fatalf("removed node still listed, count %d", client.NodeCount())
	}
	if _, ok := client.services["f"]; ok {
		t.Fatal("file of removed node kept")
	}
	if err := client.setNodeServices("b", []string{"g"}); err == nil {
		t.Fatal("setNodeServices accepted a removed node")
	}
	if b.Retry() {
		t.Fatal("removed node not closed")
	}
	// A stale node value does not remove the current entry.
	if client.closeAndRemoveNodeIfCurrent("a", b, (*Node).IsOffline) {
		t.Fatal("removed an entry that maps to another node")
	}
}

func TestClientAwaitingTunnelKeepsNode(t *testing.T) {
	node := NewNode("a", "", 6)
	client := NewClient(5, node, RoundRobin)
	pastGrace(client, node)
	node.awaitTunnel()
	if !client.awaitingTunnel() {
		t.Fatal("client does not report the node awaiting its tunnel")
	}
	for range retryTimeMax + 2 {
		if got := client.GetNode(); got != nil {
			t.Fatalf("offline node chosen: %v", got)
		}
		if removed := client.RemoveOfflineNodes(true); removed != 0 {
			t.Fatalf("node awaiting its tunnel removed")
		}
	}
	if client.NodeCount() != 1 {
		t.Fatalf("count = %d", client.NodeCount())
	}
}

func TestClientRandomChoosesOnlyOnlineNodes(t *testing.T) {
	client := NewClient(-1, NewNode("a", "", 8), Random)
	client.AddNode(NewNode("b", "", 8))
	seen := map[string]bool{}
	for range 64 {
		n := client.GetNode()
		if n == nil {
			t.Fatal("no node chosen")
		}
		seen[n.UUID] = true
	}
	if !seen["a"] || !seen["b"] {
		t.Fatalf("Random never chose some node: %v", seen)
	}
}

func TestClientClose(t *testing.T) {
	a, b := NewNode("a", "", 6), NewNode("b", "", 6)
	client := NewClient(6, a, Primary)
	client.AddNode(b)
	if client.IsClosed() {
		t.Fatal("new client closed")
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	if !client.IsClosed() {
		t.Fatal("closed client not reported closed")
	}
	if _, ok := client.GetNodeByUUID("a"); ok {
		t.Fatal("closed client still lists nodes")
	}
	if a.Retry() || b.Retry() {
		t.Fatal("Close left nodes open")
	}
	if client.GetNode() != nil {
		t.Fatal("closed client chose a node")
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
}
