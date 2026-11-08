// Package tunnelwire implements the wire protocol between a tunnel client and
// its server. The bytes it puts on the wire are frozen for released peers.
package tunnelwire

import (
	"cmp"
	"strconv"
	"strings"
)

// versions lists the protocol versions a client may announce; a peer's index
// into it decides what it speaks. A server closes the connection on any other
// version, so this table cannot grow without breaking older servers.
var versions = [...]string{
	"0.26.0", // 0
	"0.27.0", // 1
	"0.28.0", // 2
	"0.29.0", // 3
	"0.30.0", // 4
	"0.31.0", // 5
	"0.32.0", // 6
	"0.33.0", // 7
	"0.34.0", // 8
}

// MinSecureIndex is the lowest index a server in secure mode accepts.
const MinSecureIndex = 7

// LatestIndex is the highest protocol index.
const LatestIndex = len(versions) - 1

// Client releases features depend on, as opposed to protocol indexes.
const (
	// P2PMinVersion is the oldest client release that speaks the current P2P
	// protocol.
	P2PMinVersion = "0.35.0"
	// P2PMinIndex is the lowest protocol index a P2P client may use: the
	// provider opens its P2P control streams on the tunnel.
	P2PMinIndex = 5
	// RemoteUpdateMinVersion is the oldest client release that acts on an
	// update request. Older clients treat an unknown message on the signal
	// connection as a broken stream and reconnect.
	RemoteUpdateMinVersion = "0.34.9"
	// ServerHealthMinVersion is the oldest client release that acts on health
	// groups. Older clients treat them as a broken stream and reconnect.
	ServerHealthMinVersion = "0.35.0"
)

// Version returns the version string of index, the latest for one out of range.
func Version(index int) string {
	if index < 0 || index >= len(versions) {
		return versions[LatestIndex]
	}
	return versions[index]
}

// Index returns the index of version, or -1 if it is not in the table.
func Index(version string) int {
	for i, v := range versions {
		if v == version {
			return i
		}
	}
	return -1
}

// IndexCount returns how many protocol indexes there are.
func IndexCount() int { return len(versions) }

// MinVersion returns the oldest version a server accepts.
func MinVersion(secure bool) string {
	if secure {
		return Version(MinSecureIndex)
	}
	return Version(0)
}

// Capabilities are what a peer speaks. Every decision that depends on a
// peer's version reads one of these; no code compares indexes.
type Capabilities struct {
	Index         int
	ClientVersion string
	// TimestampAuth authenticates with a timestamp, key digest and MAC rather
	// than an MD5 of the key alone.
	TimestampAuth bool
	// CertFingerprint: the server sends its certificate's fingerprint.
	CertFingerprint bool
	// TransportInInfo: the client's info holds its transport after its address.
	TransportInInfo bool
	// RandomPadding: handshakes and work requests end in a random frame.
	RandomPadding bool
	// NodeID: the server sends a UUID and work requests carry the node's
	// UUID, the same across the connections of a client process.
	NodeID bool
	// NodeIDHasPort: without NodeID a node is named after its address with
	// the port; before this, after the address alone.
	NodeIDHasPort bool
	// TunnelStreams: the signal connection and control streams are streams on
	// the tunnel, so that the tunnel alone decides whether a node is online.
	TunnelStreams bool
	// NativeQUIC: over QUIC the connection itself carries the streams, rather
	// than a mux on its first stream.
	NativeQUIC bool
	// LinkAck: the client acknowledges links that ask for it.
	LinkAck bool
	// VisitorTunnel: visitors keep a tunnel for their secret and P2P streams.
	VisitorTunnel bool
	// FramedUDP: UDP links carry one datagram per frame.
	FramedUDP bool
	// P2P: the client can take part in a P2P session.
	P2P bool
	// RemoteUpdate: the client acts on update requests.
	RemoteUpdate bool
	// ServerHealth: the client runs the health checks the server gives it.
	ServerHealth bool
}

// Caps returns what a peer announcing protocol index and client version speaks.
func Caps(index int, clientVersion string) Capabilities {
	return Capabilities{
		Index:           index,
		ClientVersion:   clientVersion,
		TimestampAuth:   index >= 1,
		CertFingerprint: index >= 2,
		TransportInInfo: index >= 3,
		RandomPadding:   index >= 4,
		NodeIDHasPort:   index >= 5,
		TunnelStreams:   index >= 5,
		NativeQUIC:      index >= 5,
		NodeID:          index >= 6,
		LinkAck:         index >= 6,
		VisitorTunnel:   index >= 6,
		FramedUDP:       index >= 7,
		P2P:             index >= P2PMinIndex && CompareVersions(clientVersion, P2PMinVersion) >= 0,
		RemoteUpdate:    CompareVersions(clientVersion, RemoteUpdateMinVersion) >= 0,
		ServerHealth:    CompareVersions(clientVersion, ServerHealthMinVersion) >= 0,
	}
}

// CompareVersions compares two dotted versions numerically segment by
// segment. A leading "v" and build metadata are ignored; a pre-release suffix
// sorts before the release with the same numbers.
func CompareVersions(a, b string) int {
	an, ap := splitVersion(a)
	bn, bp := splitVersion(b)
	for i := 0; i < max(len(an), len(bn)); i++ {
		var x, y int
		if i < len(an) {
			x = an[i]
		}
		if i < len(bn) {
			y = bn[i]
		}
		if x != y {
			return cmp.Compare(x, y)
		}
	}
	switch {
	case ap == bp:
		return 0
	case ap == "":
		return 1
	case bp == "":
		return -1
	}
	return strings.Compare(ap, bp)
}

func splitVersion(v string) ([]int, string) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	v, _, _ = strings.Cut(v, "+")
	core, pre, _ := strings.Cut(v, "-")
	parts := strings.Split(core, ".")
	nums := make([]int, len(parts))
	for i, p := range parts {
		nums[i], _ = strconv.Atoi(p)
	}
	return nums, pre
}
