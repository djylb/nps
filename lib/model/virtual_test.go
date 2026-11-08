package model

import "testing"

func file(remark string, port int, ports string) *Tunnel {
	return &Tunnel{Mode: "file", Port: port, Ports: ports, Remark: remark, LocalPath: "/srv", Flow: new(Flow), Client: new(Client), Target: new(Target)}
}

func TestConvertFileTunnel(t *testing.T) {
	f := file("a", 8000, "")
	cs, ok := ConvertFileTunnel(f)
	if !ok || len(cs) != 1 || !f.IsVirtualBackend() || f.ServiceKind != FileServiceKind {
		t.Fatalf("converted %v, %d tunnels, %+v", ok, len(cs), f)
	}
	c := cs[0]
	if c.Mode != "tcp" || c.Port != 8000 || c.LocalPath != "" || !c.Generated || c.Target.TargetStr != f.Target.TargetStr || c.TargetType != TargetTCP {
		t.Fatalf("tunnel %+v", c)
	}
	if _, ok := ParseServiceTarget(c.Target.TargetStr); !ok {
		t.Fatalf("target %q", c.Target.TargetStr)
	}
	if more, ok := ConvertFileTunnel(f); ok || more != nil {
		t.Fatal("converted twice")
	}
	g := file("renamed", 8000, "")
	_, _ = ConvertFileTunnel(g)
	if g.Target.TargetStr != f.Target.TargetStr {
		t.Fatalf("identifier changed: %q, %q", g.Target.TargetStr, f.Target.TargetStr)
	}
	if cs, _ := ConvertFileTunnel(file("", 0, "9000,9001")); len(cs) != 2 || cs[1].Port != 9001 {
		t.Fatalf("%d tunnels", len(cs))
	}
	n := file("docs", 0, "")
	if cs, ok := ConvertFileTunnel(n); !ok || len(cs) != 0 || n.ServiceName != "docs" {
		t.Fatalf("%d tunnels, name %q", len(cs), n.ServiceName)
	}
}
