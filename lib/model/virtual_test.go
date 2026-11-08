package model

import "testing"

func TestConvertFileTunnel(t *testing.T) {
	f := &Tunnel{Mode: "file", Port: 8000, Remark: "a", LocalPath: "/srv", Flow: new(Flow), Client: new(Client), Target: new(Target)}
	cs, ok := ConvertFileTunnel(f)
	if !ok || len(cs) != 1 || f.Port != 0 || !f.IsVirtualBackend() {
		t.Fatalf("converted %v, %d companions, port %d", ok, len(cs), f.Port)
	}
	c := cs[0]
	if c.Mode != "tcp" || c.Port != 8000 || c.LocalPath != "" || c.Target.TargetStr != f.Target.TargetStr {
		t.Fatalf("companion %+v", c)
	}
	if _, ok := ParseServiceTarget(c.Target.TargetStr); !ok {
		t.Fatalf("target %q", c.Target.TargetStr)
	}
	// Idempotent.
	if more, ok := ConvertFileTunnel(f); ok || more != nil {
		t.Fatal("converted twice")
	}
	// The identifier ignores what a rename or edit changes.
	g := &Tunnel{Mode: "file", Port: 8000, Remark: "renamed", LocalPath: "/other", Flow: new(Flow), Client: new(Client), Target: new(Target)}
	_, _ = ConvertFileTunnel(g)
	if g.Target.TargetStr != f.Target.TargetStr {
		t.Fatalf("identifier changed: %q, %q", g.Target.TargetStr, f.Target.TargetStr)
	}
	// Several ports, one backend.
	m := &Tunnel{Mode: "file", Ports: "9000,9001", Flow: new(Flow), Client: new(Client), Target: new(Target)}
	if cs, _ := ConvertFileTunnel(m); len(cs) != 2 || cs[1].Port != 9001 {
		t.Fatalf("%d companions", len(cs))
	}
	// A declaration without a port is only the backend.
	n := &Tunnel{Mode: "file", Remark: "docs", Flow: new(Flow), Client: new(Client), Target: new(Target)}
	if cs, ok := ConvertFileTunnel(n); !ok || len(cs) != 0 || n.ServiceName != "docs" || !n.IsVirtualBackend() {
		t.Fatalf("%d companions, name %q", len(cs), n.ServiceName)
	}
}
