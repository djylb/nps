package legacy

import (
	"bytes"
	"errors"
	"github.com/djylb/nps/lib/healthcheck"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"encoding/hex"
	"github.com/djylb/netx"
	"github.com/djylb/tunnelwire"
	"golang.org/x/crypto/blake2b"
)

// The library's BLAKE2b, without a dependency, against the reference.
func TestKeyDigestMatchesBlake2b(t *testing.T) {
	buf := make([]byte, 600)
	for i := range buf {
		buf[i] = byte(i * 7)
	}
	for n := 0; n <= len(buf); n++ {
		want := blake2b.Sum256(buf[:n])
		if got := tunnelwire.KeyDigest(string(buf[:n])); got != hex.EncodeToString(want[:]) {
			t.Fatalf("length %d: %s", n, got)
		}
	}
}

func TestReadAckTimeout(t *testing.T) {
	server, client := net.Pipe()
	defer func() { _ = server.Close() }()
	defer func() { _ = client.Close() }()

	err := ReadAck(client, 50*time.Millisecond)
	if err == nil {
		t.Fatal("ReadAck() error = nil, want timeout error")
	}
	if !netx.IsTimeout(err) {
		t.Fatalf("ReadAck() error = %v, want timeout-compatible error", err)
	}
}

func TestWriteAckAndReadAck(t *testing.T) {
	server, client := net.Pipe()
	defer func() { _ = server.Close() }()
	defer func() { _ = client.Close() }()
	errCh := make(chan error, 1)
	go func() {
		errCh <- WriteAck(server, 2*time.Second)
	}()

	if err := ReadAck(client, 2*time.Second); err != nil {
		t.Fatalf("ReadAck() error = %v", err)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("WriteAck() error = %v", err)
	}
}

func TestReadAckUnexpectedValue(t *testing.T) {
	server, client := net.Pipe()
	defer func() { _ = server.Close() }()
	defer func() { _ = client.Close() }()
	errCh := make(chan error, 1)
	go func() {
		_ = server.SetWriteDeadline(time.Now().Add(2 * time.Second))
		_, err := server.Write([]byte("NAK"))
		errCh <- err
	}()

	err := ReadAck(client, 2*time.Second)
	if err == nil {
		t.Fatal("ReadAck() error = nil, want non-nil")
	}
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("ReadAck() error = %v, want %v", err, io.ErrUnexpectedEOF)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("server write error = %v", err)
	}
}

// testdata/real holds what released npc binaries really sent on their first
// connection to a server, with the key "interopkey": the handshake, and the
// work request behind it.
func TestRealClientHandshakes(t *testing.T) {
	const key = "interopkey"
	for _, c := range []struct {
		file      string
		index     int
		version   string
		transport string
		macCheck  bool
	}{
		{"v0.26.56", 0, "0.26.56", "", false},
		{"v0.27.0", 1, "0.27.0", "", false},
		{"v0.28.3", 2, "0.28.3", "", false},
		{"v0.29.38", 3, "0.29.38", "tcp", false},
		{"v0.30.6", 4, "0.30.6", "tcp", false},
		{"v0.31.1", 5, "0.31.1", "tcp", false},
		{"v0.32.10", 6, "0.32.10", "tcp", false},
		{"v0.33.14", 7, "0.33.14", "tcp", true},
		{"v0.34.8", 8, "0.34.8", "tcp", true},
		{"v0.34.10", 8, "0.34.10", "tcp", true},
	} {
		t.Run(c.file, func(t *testing.T) {
			b, err := os.ReadFile(filepath.Join("testdata", "real", c.file+".bin"))
			if err != nil {
				t.Fatal(err)
			}
			r := bytes.NewReader(b)
			rw := struct {
				io.Reader
				io.Writer
			}{r, io.Discard}
			lookup := func(digest string) (tunnelwire.Credential, error) {
				if digest != tunnelwire.KeyDigest(key) && digest != tunnelwire.MD5Hex(key) {
					return tunnelwire.Credential{}, errors.New("unknown key")
				}
				return tunnelwire.Credential{ID: 1, Key: key, Enabled: true}, nil
			}
			res, err := tunnelwire.ServerHandshake(rw, tunnelwire.ServerConfig{
				Secure: c.macCheck, LookupDigest: lookup, LookupMD5: lookup,
				Fingerprint: func() []byte { return []byte("fingerprint") },
				NewUUID:     func() string { return "uuid" },
			})
			if err != nil {
				t.Fatalf("handshake: %v", err)
			}
			if res.Caps.Index != c.index || res.Caps.ClientVersion != c.version || res.ClientID != 1 || res.Transport != c.transport {
				t.Fatalf("%+v", res)
			}
			flag, uuid, err := ReadWork(r, res.Caps, "192.0.2.1:5")
			if err != nil {
				t.Fatalf("work: %v", err)
			}
			if flag != WorkMain && flag != WorkChan {
				t.Fatalf("first work %q", flag)
			}
			if uuid == "" {
				t.Fatal("no node id")
			}
		})
	}
}

func TestHealthSetBodiesFitFrames(t *testing.T) {
	var targets []string
	for range 5000 {
		targets = append(targets, "192.168.100.100:65535")
	}
	total, bodies := 0, HealthSetBodies(healthcheck.Group{Name: "t1", Targets: targets})
	for _, b := range bodies {
		n := len(b.(healthGroupWire).Targets)
		if n*24 > healthChunk+24 {
			t.Fatalf("body of %d targets", n)
		}
		total += n
	}
	if len(bodies) < 2 || total != len(targets) {
		t.Fatalf("%d bodies, %d targets", len(bodies), total)
	}
}
