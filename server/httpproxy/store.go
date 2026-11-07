package httpproxy

import (
	"net/http"

	"github.com/djylb/nps/lib/model"
)

// Store is what the host proxy reads of the stored hosts.
type Store interface {
	// GetInfoByHost finds the host entry that serves request r for host.
	GetInfoByHost(host string, r *http.Request) (*model.Host, error)
	// FindCertByHost finds the host entry whose certificate serves name.
	FindCertByHost(name string) (*model.Host, error)
	// RangeClientCertHosts calls fn for the hosts of client clientID that
	// carry a certificate, until fn returns false.
	RangeClientCertHosts(clientID int, fn func(*model.Host) bool)
}
