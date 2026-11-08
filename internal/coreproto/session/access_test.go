package session

import (
	"bytes"
	"testing"

	"github.com/djylb/nps/internal/coreproto/wire"
	"github.com/zmuxio/zmux-go"
)

// The proof of an access needs the session's exporter on every record mode,
// the plain carrier of a Trusted session included (§9.1).
func TestAccessExporterOnTrustedTCP(t *testing.T) {
	e := start(t, "tcp", rec(1, "vkey-exporter", false))
	d := e.dialer("vkey-exporter")
	first := e.dial(d)
	e.waitOnline()
	waitFor(t, "record 4", func() bool {
		d.mu.Lock()
		defer d.mu.Unlock()
		return d.record == wire.RecordTrusted
	})
	_ = first.Close()
	<-first.Done()
	c := e.dial(d)
	s := e.waitOnline()
	if s.mode != zmux.Trusted {
		t.Fatalf("second session in %v, want trusted", s.mode)
	}
	cm, err := c.zc.ExportKeyingMaterial(wire.AccessExportLabel, nil, wire.AccessExportLen)
	if err != nil {
		t.Fatalf("client exporter (mode %v): %v", s.mode, err)
	}
	sm, err := s.zc.ExportKeyingMaterial(wire.AccessExportLabel, nil, wire.AccessExportLen)
	if err != nil {
		t.Fatalf("server exporter (mode %v): %v", s.mode, err)
	}
	if len(cm) == 0 || !bytes.Equal(cm, sm) {
		t.Fatalf("exporters differ: %x / %x", cm, sm)
	}
}
