package bridge

import (
	"net"

	"github.com/djylb/nps/lib/model"
)

// Store is what the bridge reads and changes of the stored clients, tunnels
// and hosts. The server passes its store; the bridge reaches no other.
type Store interface {
	// GetClient returns client id, or an error if there is none.
	GetClient(id int) (*model.Client, error)
	// IsPubClient reports whether id is the client of the public key.
	IsPubClient(id int) bool
	// GetClientIdByBlake2bVkey finds the client of a hashed verify key.
	GetClientIdByBlake2bVkey(digest string) (int, error)
	// GetIdByVerifyKey finds the client whose key hashFunc turns into
	// vKey, and records the address it connects from.
	GetIdByVerifyKey(vKey, addr, localAddr string, hashFunc func(string) string) (int, error)
	// NewClient stores c, assigning its id and verify key.
	NewClient(c *model.Client) error
	// ClientPresence reports that client id got or lost its session.
	ClientPresence(id int, c *model.Client, online bool)
	// GlobalDenied reports whether the global access lists deny addr.
	GlobalDenied(addr net.Addr, what string) bool
	// GetGlobal returns the global settings, nil if there are none.
	GetGlobal() *model.Glob

	RangeTunnels(fn func(*model.Tunnel) bool)
	RangeHosts(fn func(*model.Host) bool)
	NextTunnelID() int
	NextHostID() int

	// ClientTunnel finds the stored tunnel of c that matches t.
	ClientTunnel(c *model.Client, t *model.Tunnel) (*model.Tunnel, bool)
	NewTask(t *model.Tunnel) error
	DelTask(id int) error
	// ReserveTaskPassword checks that the password of t is free for it.
	ReserveTaskPassword(t *model.Tunnel) error
	// TunnelChanged reports that the stored t was changed in place.
	TunnelChanged(t *model.Tunnel)
	// GetTaskBySecretIndex finds the task whose password has index p
	// (crypt.SecretIndex).
	GetTaskBySecretIndex(p string) *model.Tunnel

	// ClientHost finds the stored host of c that matches h.
	ClientHost(c *model.Client, h *model.Host) (*model.Host, bool)
	IsHostExist(h *model.Host) bool
	NewHost(h *model.Host) error
	// HostChanged reports that the stored h was changed in place.
	HostChanged(h *model.Host)
}
