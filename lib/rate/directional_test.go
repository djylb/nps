package rate

import (
	"io"
	"net"
	"testing"
	"time"
)

func TestDirectionalSharedLimitsAndLiveClear(t *testing.T) {
	entry := new(Directional)
	entry.SetLimits(1000, 0)
	// Exhaust the shared two-second burst before either connection is wrapped.
	if err := entry.upload.Charge(2000); err != nil {
		t.Fatal(err)
	}
	var conns []net.Conn
	for range 2 {
		a, b := net.Pipe()
		t.Cleanup(func() { _ = b.Close() })
		go func() { _, _ = io.Copy(io.Discard, b) }()
		c := NewRateConn(a, nil, entry).(net.Conn)
		t.Cleanup(func() { _ = c.Close() })
		conns = append(conns, c)
	}
	done := make(chan error, 2)
	for _, c := range conns {
		go func() { _, err := c.Write(make([]byte, 1000)); done <- err }()
	}
	select {
	case err := <-done:
		t.Fatalf("shared upload limit bypassed: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	entry.SetLimits(0, 0)
	for range 2 {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(time.Second):
			t.Fatal("clearing did not release existing connection")
		}
	}
}

func TestDirectionalAccountingAndIndependentDownload(t *testing.T) {
	ResetGlobalTraffic()
	t.Cleanup(ResetGlobalTraffic)
	client := NewRate(0)
	entry := new(Directional)
	entry.SetLimits(1, 0)
	if err := entry.upload.Charge(1000); err != nil {
		t.Fatal(err)
	}
	a, b := net.Pipe()
	defer func() { _ = b.Close() }()
	c := NewRateConn(a, client, entry).(net.Conn)
	defer func() { _ = c.Close() }()
	_ = c.SetDeadline(time.Now().Add(time.Second))
	go func() { _, _ = b.Write(make([]byte, 73)) }()
	if _, err := io.ReadFull(c, make([]byte, 73)); err != nil {
		t.Fatalf("upload limit blocked download: %v", err)
	}
	entry.SetLimits(0, 0)
	go func() { _, _ = io.CopyN(io.Discard, b, 31) }()
	if _, err := c.Write(make([]byte, 31)); err != nil {
		t.Fatal(err)
	}
	if in, out := GlobalTraffic(); in != 31 || out != 73 {
		t.Fatalf("global charged more than once: %d/%d", in, out)
	}
	if out, in := client.traffic.Total(); in != 31 || out != 73 {
		t.Fatalf("client direction/accounting: %d/%d", in, out)
	}
	// The tunnel's read meter must receive download bytes only.
	time.Sleep(1100 * time.Millisecond)
	upload, download := entry.Throughput()
	if upload <= 0 || download <= upload {
		t.Fatalf("entry speeds have wrong directions: %d/%d", upload, download)
	}
	clientUpload, clientDownload := client.CurrentRates()
	if clientUpload <= 0 || clientDownload <= clientUpload {
		t.Fatalf("client speeds have wrong directions: %d/%d", clientUpload, clientDownload)
	}
}

func TestDirectionalKeepsEveryHierarchyLimit(t *testing.T) {
	for _, level := range []string{"global", "client", "entry"} {
		t.Run(level, func(t *testing.T) {
			SetGlobalLimit(0)
			t.Cleanup(func() { SetGlobalLimit(0) })
			client := NewRate(0)
			entry := new(Directional)
			switch level {
			case "global":
				SetGlobalLimit(1000)
				if err := globalRate.Charge(2000); err != nil {
					t.Fatal(err)
				}
			case "client":
				client.ResetLimit(1000)
				if err := client.l.Charge(2000); err != nil {
					t.Fatal(err)
				}
			case "entry":
				entry.SetLimits(1000, 0)
				if err := entry.upload.Charge(2000); err != nil {
					t.Fatal(err)
				}
			}
			a, b := net.Pipe()
			defer func() { _ = b.Close() }()
			go func() { _, _ = io.Copy(io.Discard, b) }()
			c := NewRateConn(a, client, entry).(net.Conn)
			defer func() { _ = c.Close() }()
			_ = c.SetWriteDeadline(time.Now().Add(100 * time.Millisecond))
			n, err := c.Write(make([]byte, 1000))
			if err == nil || n == 1000 {
				t.Fatalf("%s limit bypassed: %d %v", level, n, err)
			}
		})
	}
}
