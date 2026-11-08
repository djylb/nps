package tunnelwire

import (
	"errors"
	"net"
	"testing"
	"time"
)

func TestHandshakeEveryIndex(t *testing.T) {
	const key = "test-key"
	fp := []byte("fingerprint")
	lookup := func(want string) func(string) (Credential, error) {
		return func(got string) (Credential, error) {
			if got != want {
				return Credential{}, errors.New("unknown")
			}
			return Credential{ID: 9, Key: key, Enabled: true}, nil
		}
	}
	for _, secure := range []bool{false, true} {
		for index := range IndexCount() {
			if secure && index < MinSecureIndex {
				continue
			}
			a, b := net.Pipe()
			_ = a.SetDeadline(time.Now().Add(5 * time.Second))
			_ = b.SetDeadline(time.Now().Add(5 * time.Second))
			type out struct {
				res *ServerResult
				err error
			}
			done := make(chan out, 1)
			go func() {
				res, err := ServerHandshake(b, ServerConfig{
					Secure: secure, LookupDigest: lookup(KeyDigest(key)), LookupMD5: lookup(MD5Hex(key)),
					Fingerprint: func() []byte { return fp }, NewUUID: func() string { return "server-uuid" },
				})
				done <- out{res, err}
			}()
			res, err := ClientHandshake(a, ClientConfig{Index: index, ClientVersion: "9.9.9", Key: key, Transport: "tcp", LocalIP: net.IPv4(192, 0, 2, 1)})
			if err != nil {
				t.Fatalf("secure %v index %d client: %v", secure, index, err)
			}
			got := <-done
			if got.err != nil {
				t.Fatalf("secure %v index %d server: %v", secure, index, got.err)
			}
			if got.res.ClientID != 9 || got.res.Caps.ClientVersion != "9.9.9" {
				t.Fatalf("index %d: %+v", index, got.res)
			}
			if res.Caps.NodeID && res.ServerUUID != "server-uuid" {
				t.Fatalf("index %d uuid %q", index, res.ServerUUID)
			}
			if res.Caps.CertFingerprint && string(res.Fingerprint) != string(fp) {
				t.Fatalf("index %d fingerprint %q", index, res.Fingerprint)
			}
			if res.Caps.TransportInInfo && got.res.Transport != "tcp" {
				t.Fatalf("index %d transport %q", index, got.res.Transport)
			}
			_ = a.Close()
			_ = b.Close()
		}
	}
}

func TestBlake2bVectors(t *testing.T) {
	for in, want := range map[string]string{
		"":    "0e5751c026e543b2e8ab2eb06099daa1d1e5df47778f7787faab45cdf12fe3a8",
		"abc": "bddd813c634239723171ef3fee98579b94964e3bb1cb3e427262c8c068d52319",
	} {
		if got := KeyDigest(in); got != want {
			t.Errorf("KeyDigest(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestCompareVersions(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want int
	}{{"0.34.8", "0.34.8", 0}, {"v0.34.10", "0.34.9", 1}, {"0.34.0-rc1", "0.34.0", -1}, {"0.35", "0.35.0", 0}} {
		if got := CompareVersions(c.a, c.b); got != c.want {
			t.Errorf("%s %s = %d", c.a, c.b, got)
		}
	}
}

func TestCapsThresholds(t *testing.T) {
	if c := Caps(5, "0.35.0"); !c.P2P || c.NodeID || c.LinkAck {
		t.Fatalf("%+v", c)
	}
	if c := Caps(8, "0.34.10"); c.P2P || !c.RemoteUpdate || !c.FramedUDP {
		t.Fatalf("%+v", c)
	}
	if c := Caps(4, "0.35.0"); c.P2P || c.TunnelStreams || !c.RemoteUpdate {
		t.Fatalf("%+v", c)
	}
}

func TestServerHealthCapability(t *testing.T) {
	for v, want := range map[string]bool{"0.34.10": false, "0.35.0": true, "0.36.1": true, "": false} {
		if got := Caps(LatestIndex, v).ServerHealth; got != want {
			t.Errorf("ServerHealth of %q = %v, want %v", v, got, want)
		}
	}
}
