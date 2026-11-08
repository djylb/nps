package client

import (
	"io"
	"net"
	"testing"
	"time"

	"github.com/djylb/nps/lib/config"
)

// BenchmarkSecretRelay measures the relay of a secret task on loopback:
// "link" opens a relayed connection per iteration and exchanges one small
// message (link setup cost), "bulk" streams through one relayed connection
// (throughput).
func BenchmarkSecretRelay(b *testing.B) {
	env := startP2PTestEnv(b, false, serveTCP(b, echoConn))
	addr, _ := env.visitor(b, &config.LocalServer{Type: config.LocalTCP, ConnMode: config.ConnRelay, Ip: "127.0.0.1"})
	b.Run("link", func(b *testing.B) {
		buf := make([]byte, 64)
		for b.Loop() {
			c, err := net.Dial("tcp", addr)
			if err != nil {
				b.Fatal(err)
			}
			_ = c.SetDeadline(time.Now().Add(10 * time.Second))
			if _, err := c.Write(buf); err != nil {
				b.Fatal(err)
			}
			if _, err := io.ReadFull(c, buf); err != nil {
				b.Fatal(err)
			}
			_ = c.Close()
		}
	})
	b.Run("bulk", func(b *testing.B) {
		c, err := net.Dial("tcp", addr)
		if err != nil {
			b.Fatal(err)
		}
		defer func() { _ = c.Close() }()
		chunk := make([]byte, 256<<10)
		b.SetBytes(int64(len(chunk)))
		done := make(chan error, 1)
		n := 0
		b.ResetTimer()
		go func() {
			buf := make([]byte, 256<<10)
			var total int64
			want := int64(b.N) * int64(len(chunk))
			for total < want {
				m, err := c.Read(buf)
				total += int64(m)
				if err != nil {
					done <- err
					return
				}
			}
			done <- nil
		}()
		for n = 0; n < b.N; n++ {
			if _, err := c.Write(chunk); err != nil {
				b.Fatal(err)
			}
		}
		if err := <-done; err != nil {
			b.Fatal(err)
		}
	})
}
