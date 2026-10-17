package conn

import (
	"bytes"
	"errors"
	"io"
	"net"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/djylb/nps/lib/file"
)

// referenceCopy is the per-direction copy that the relay replaces, without
// its HTTP detection, kept to compare traffic accounting against: every write adds its bytes to each
// flow as both inlet and export, and a flow over a limit stops the copy.
func referenceCopy(dst io.Writer, src io.Reader, flows []*file.Flow) (written int64, err error) {
	buf := make([]byte, 32<<10)
	for {
		nr, er := src.Read(buf)
		if er == nil || nr > 0 {
			nw, ew := dst.Write(buf[:nr])
			if nw > 0 {
				written += int64(nw)
				for _, f := range flows {
					if f == nil {
						continue
					}
					f.Add(int64(nw), int64(nw))
					if f.FlowLimit > 0 && (f.FlowLimit<<20) < (f.ExportFlow+f.InletFlow) {
						return written, errors.New("flow limit exceeded")
					}
					if !f.TimeLimit.IsZero() && f.TimeLimit.Before(time.Now()) {
						return written, errors.New("time limit exceeded")
					}
				}
			}
			if ew != nil {
				return written, ew
			}
			if nr != nw {
				return written, io.ErrShortWrite
			}
		}
		if er != nil {
			return written, er
		}
	}
}

// referenceCopyWaitGroup relays mux and outside as CopyWaitGroup did before
// it used netx.Relay, subtracting the opposite direction from the task flow.
func referenceCopyWaitGroup(mux, outside net.Conn, flows []*file.Flow, taskFlow *file.Flow) {
	var in, out int64
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		defer func() { _ = mux.Close(); _ = outside.Close() }()
		in, _ = referenceCopy(mux, outside, flows)
	}()
	go func() {
		defer wg.Done()
		defer func() { _ = mux.Close(); _ = outside.Close() }()
		out, _ = referenceCopy(outside, mux, flows)
	}()
	wg.Wait()
	taskFlow.Sub(out, in)
}

func referenceJoin(c1, c2 net.Conn, flows []*file.Flow) {
	var wg sync.WaitGroup
	wg.Add(2)
	closeBoth := sync.OnceFunc(func() { _ = c1.Close(); _ = c2.Close() })
	go func() { defer wg.Done(); defer closeBoth(); _, _ = referenceCopy(c1, c2, flows) }()
	go func() { defer wg.Done(); defer closeBoth(); _, _ = referenceCopy(c2, c1, flows) }()
	wg.Wait()
}

// tcpPair returns the two ends of a loopback TCP connection.
func tcpPair(t *testing.T) (net.Conn, net.Conn) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	accepted := make(chan net.Conn, 1)
	go func() {
		c, _ := ln.Accept()
		accepted <- c
	}()
	c, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	s := <-accepted
	if s == nil {
		t.Fatal("accept failed")
	}
	return c, s
}

type relayFunc func(mux, outside net.Conn)

// runExchange relays between a visitor and a backend: the visitor sends up
// bytes, the backend answers with down bytes once it has them all, and the
// visitor closes once it has the answer. It returns when the relay does.
func runExchange(t *testing.T, relay relayFunc, up, down int) {
	t.Helper()
	muxNear, backend := tcpPair(t)
	outside, visitor := tcpPair(t)
	defer func() { _ = backend.Close(); _ = visitor.Close() }()

	done := make(chan struct{})
	go func() {
		defer close(done)
		relay(muxNear, outside)
	}()

	errs := make(chan error, 2)
	go func() {
		if _, err := io.CopyN(io.Discard, backend, int64(up)); err != nil {
			errs <- err
			return
		}
		_, err := backend.Write(bytes.Repeat([]byte{'d'}, down))
		errs <- err
	}()
	go func() {
		if _, err := visitor.Write(bytes.Repeat([]byte{'u'}, up)); err != nil {
			errs <- err
			return
		}
		_, err := io.CopyN(io.Discard, visitor, int64(down))
		errs <- err
	}()
	for range 2 {
		select {
		case err := <-errs:
			if err != nil {
				t.Fatalf("exchange: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("exchange timed out")
		}
	}
	_ = visitor.Close()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("relay did not return after the visitor closed")
	}
}

type flowValues struct{ inlet, export int64 }

func valuesOf(f *file.Flow) flowValues {
	f.RLock()
	defer f.RUnlock()
	return flowValues{f.InletFlow, f.ExportFlow}
}

func newRelayTask() *file.Tunnel {
	return &file.Tunnel{
		Flow:   &file.Flow{InletFlow: 7, ExportFlow: 11},
		Client: &file.Client{Flow: &file.Flow{InletFlow: 3, ExportFlow: 5}},
		Target: &file.Target{TargetStr: "127.0.0.1:80"},
	}
}

func TestCopyWaitGroupAccountingMatchesReference(t *testing.T) {
	const up, down = 100_000, 250_000
	measure := func(useRelay bool) (task, client flowValues) {
		tk := newRelayTask()
		flows := []*file.Flow{tk.Flow, tk.Client.Flow}
		runExchange(t, func(mux, outside net.Conn) {
			if useRelay {
				CopyWaitGroup(mux, outside, false, false, nil, flows, true, 0, nil, tk, true, false)
			} else {
				referenceCopyWaitGroup(mux, outside, flows, tk.Flow)
			}
		}, up, down)
		return valuesOf(tk.Flow), valuesOf(tk.Client.Flow)
	}
	refTask, refClient := measure(false)
	gotTask, gotClient := measure(true)
	if gotTask != refTask || gotClient != refClient {
		t.Fatalf("task %+v client %+v, reference task %+v client %+v", gotTask, gotClient, refTask, refClient)
	}
	// The task keeps the visitor's bytes as inlet and the backend's as
	// export; the client counts all of them both ways.
	if want := (flowValues{7 + up, 11 + down}); gotTask != want {
		t.Fatalf("task flow = %+v, want %+v", gotTask, want)
	}
	if want := (flowValues{3 + up + down, 5 + up + down}); gotClient != want {
		t.Fatalf("client flow = %+v, want %+v", gotClient, want)
	}
}

func TestJoinAccountingMatchesReference(t *testing.T) {
	const up, down = 70_000, 130_000
	measure := func(useRelay bool) (host, client flowValues) {
		tk := newRelayTask()
		hostFlow := &file.Flow{InletFlow: 1, ExportFlow: 2}
		flows := []*file.Flow{hostFlow, tk.Client.Flow}
		runExchange(t, func(mux, outside net.Conn) {
			if useRelay {
				Join(outside, mux, flows, tk, "192.0.2.1:1234")
			} else {
				referenceJoin(outside, mux, flows)
			}
		}, up, down)
		return valuesOf(hostFlow), valuesOf(tk.Client.Flow)
	}
	refHost, refClient := measure(false)
	gotHost, gotClient := measure(true)
	if gotHost != refHost || gotClient != refClient {
		t.Fatalf("host %+v client %+v, reference host %+v client %+v", gotHost, gotClient, refHost, refClient)
	}
	if want := (flowValues{1 + up + down, 2 + up + down}); gotHost != want {
		t.Fatalf("host flow = %+v, want %+v", gotHost, want)
	}
}

// relayUntilStopped streams data from a visitor into a relay that the flows
// are expected to stop, and returns once it has.
func relayUntilStopped(t *testing.T, tk *file.Tunnel, flows []*file.Flow) {
	t.Helper()
	muxNear, backend := tcpPair(t)
	outside, visitor := tcpPair(t)
	defer func() { _ = backend.Close(); _ = visitor.Close() }()
	go func() { _, _ = io.Copy(io.Discard, backend) }()
	go func() {
		chunk := make([]byte, 16<<10)
		for {
			if _, err := visitor.Write(chunk); err != nil {
				return
			}
		}
	}()
	done := make(chan struct{})
	go func() {
		defer close(done)
		CopyWaitGroup(muxNear, outside, false, false, nil, flows, true, 0, nil, tk, true, false)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("relay was not stopped by the flow limit")
	}
}

func TestCopyWaitGroupStopsAtFlowLimit(t *testing.T) {
	tk := newRelayTask()
	tk.Flow = &file.Flow{FlowLimit: 1}
	relayUntilStopped(t, tk, []*file.Flow{tk.Flow, tk.Client.Flow})
	// Every byte passed visitor to backend: the task keeps them as inlet,
	// which went just over the 1 MiB limit together with the export.
	got := valuesOf(tk.Flow)
	if got.export != 0 || 2*got.inlet <= 1<<20 || 2*got.inlet > 1<<20+2*32<<10 {
		t.Fatalf("task flow = %+v, want inlet just over 512 KiB", got)
	}
}

func TestCopyWaitGroupStopsAtTimeLimit(t *testing.T) {
	tk := newRelayTask()
	tk.Flow = &file.Flow{TimeLimit: time.Now().Add(-time.Second)}
	before := valuesOf(tk.Client.Flow)
	relayUntilStopped(t, tk, []*file.Flow{tk.Flow, tk.Client.Flow})
	// The first write stops the relay at the task flow, before the client
	// flow counts it.
	if got := valuesOf(tk.Client.Flow); got != before {
		t.Fatalf("client flow = %+v, want %+v", got, before)
	}
}

func TestCopyWaitGroupDetectsHTTP(t *testing.T) {
	for _, tc := range []struct {
		first string
		want  bool
	}{
		{"GET /index.html HTTP/1.1\r\nHost: example.com\r\n\r\n", true},
		{"POST /api HTTP/1.1\r\nHost: example.com\r\n\r\n", true},
		{"HTTP/1.1 200 OK\r\n\r\n", true},
		{"SSH-2.0-OpenSSH_9.9\r\n", false},
	} {
		tk := newRelayTask()
		tk.IsHttp = !tc.want
		runExchange(t, func(mux, outside net.Conn) {
			CopyWaitGroup(mux, outside, false, false, nil, nil, true, 0, nil, tk, true, false)
		}, 0, 0)
		// No bytes: nothing to detect.
		if tk.IsHttp != !tc.want {
			t.Fatalf("IsHttp changed without data")
		}
		muxNear, backend := tcpPair(t)
		outside, visitor := tcpPair(t)
		done := make(chan struct{})
		go func() {
			defer close(done)
			CopyWaitGroup(muxNear, outside, false, false, nil, nil, true, 0, nil, tk, true, false)
		}()
		if _, err := visitor.Write([]byte(tc.first)); err != nil {
			t.Fatal(err)
		}
		got := make([]byte, len(tc.first))
		if _, err := io.ReadFull(backend, got); err != nil || string(got) != tc.first {
			t.Fatalf("backend got %q, %v", got, err)
		}
		_ = visitor.Close()
		<-done
		_ = backend.Close()
		tk.RLock()
		isHTTP := tk.IsHttp
		tk.RUnlock()
		if isHTTP != tc.want {
			t.Fatalf("%q: IsHttp = %v, want %v", strings.SplitN(tc.first, "\r\n", 2)[0], isHTTP, tc.want)
		}
	}
}

func TestRelayFramedUDPKeepsDatagrams(t *testing.T) {
	target, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = target.Close() }()
	udpConn, err := net.DialUDP("udp", nil, target.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatal(err)
	}
	tunnel, peer := net.Pipe()
	defer func() { _ = peer.Close() }()
	tk := newRelayTask()
	done := make(chan struct{})
	go func() {
		defer close(done)
		CopyWaitGroup(tunnel, udpConn, false, false, nil, []*file.Flow{tk.Flow, tk.Client.Flow}, false, 0, nil, tk, true, true)
	}()
	_ = peer.SetDeadline(time.Now().Add(2 * time.Second))
	_ = target.SetDeadline(time.Now().Add(2 * time.Second))

	// Two datagrams, one larger than a single read of the tunnel stream
	// would take in one piece, arrive whole and apart.
	sizes := []int{9000, 3}
	var frames []byte
	for i, n := range sizes {
		frames = append(frames, byte(n>>8), byte(n))
		frames = append(frames, bytes.Repeat([]byte{byte('a' + i)}, n)...)
	}
	go func() { _, _ = peer.Write(frames) }()
	buf := make([]byte, 64<<10)
	var from *net.UDPAddr
	for i, n := range sizes {
		m, addr, err := target.ReadFromUDP(buf)
		if err != nil || m != n || buf[0] != byte('a'+i) {
			t.Fatalf("datagram %d: %d bytes, %v; want %d", i, m, err, n)
		}
		from = addr
	}
	// Each datagram back becomes one frame.
	for i, n := range []int{5000, 0, 7} {
		if _, err := target.WriteToUDP(bytes.Repeat([]byte{byte('x' + i)}, n), from); err != nil {
			t.Fatal(err)
		}
		hdr := make([]byte, 2)
		if _, err := io.ReadFull(peer, hdr); err != nil || int(hdr[0])<<8|int(hdr[1]) != n {
			t.Fatalf("frame %d header % x, %v; want length %d", i, hdr, err, n)
		}
		if _, err := io.ReadFull(peer, buf[:n]); err != nil {
			t.Fatal(err)
		}
	}
	_ = peer.Close()
	<-done
	// Bytes counted are the datagrams' payloads, without the frame headers.
	const total = 9000 + 3 + 5000 + 0 + 7
	if got := valuesOf(tk.Client.Flow); got != (flowValues{3 + total, 5 + total}) {
		t.Fatalf("client flow = %+v, want %d more each way", got, total)
	}
}

func TestRelayLeavesNoGoroutines(t *testing.T) {
	// Warm up the listener helpers and pools before counting.
	runExchange(t, func(mux, outside net.Conn) {
		CopyWaitGroup(mux, outside, false, false, nil, nil, true, 0, nil, nil, true, false)
	}, 10, 10)
	before := runtime.NumGoroutine()
	for i := range 20 {
		tk := newRelayTask()
		flows := []*file.Flow{tk.Flow, tk.Client.Flow}
		if i%2 == 0 {
			runExchange(t, func(mux, outside net.Conn) {
				CopyWaitGroup(mux, outside, false, false, nil, flows, true, 0, nil, tk, true, false)
			}, 1000, 2000)
		} else {
			runExchange(t, func(mux, outside net.Conn) {
				Join(outside, mux, flows, tk, "")
			}, 1000, 2000)
		}
	}
	tk := newRelayTask()
	tk.Flow.FlowLimit = 1
	relayUntilStopped(t, tk, []*file.Flow{tk.Flow})
	deadline := time.Now().Add(3 * time.Second)
	for {
		n := runtime.NumGoroutine()
		if n <= before {
			return
		}
		if time.Now().After(deadline) {
			buf := make([]byte, 1<<20)
			t.Fatalf("goroutines: %d before, %d after\n%s", before, n, buf[:runtime.Stack(buf, true)])
		}
		time.Sleep(20 * time.Millisecond)
	}
}
