package httpproxy

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"slices"

	"github.com/djylb/nps/lib/model"

	"github.com/djylb/netx/tlsconn"
	"github.com/djylb/nps/lib/logs"
)

// certMatch is how a certificate covers a server name.
type certMatch int

const (
	matchNone certMatch = iota
	matchWildcard
	matchExact
)

// matchCert reports how leaf covers name, by the rules of tlsconn.Names:
// an exact name or IP address, or a wildcard for the leftmost label.
func matchCert(leaf *x509.Certificate, name string) certMatch {
	names := tlsconn.CertNames(leaf)
	if !names.Covers(name) {
		return matchNone
	}
	if c, err := tlsconn.CanonicalName(name); err == nil {
		if _, exact := slices.BinarySearch(names.DNS, c); !exact {
			return matchWildcard
		}
	}
	return matchExact
}

func certCovers(cert *tls.Certificate, name string) certMatch {
	if cert == nil {
		return matchNone
	}
	return matchCert(cert.Leaf, name)
}

// selectCert returns the certificate to terminate TLS for sni on host:
//
//  1. host's own certificate, if it covers sni;
//  2. the certificate of another entry of the same client that covers sni,
//     an exact name before a wildcard; certificates never cross clients;
//  3. the default certificate, if it covers sni;
//  4. otherwise host's own certificate, as configured.
//
// The default certificate is never served for a name it does not cover, so
// arbitrary server names cannot fetch it. It fails if no certificate fits.
func (s *HttpsServer) selectCert(host *model.Host, sni string) (*tls.Certificate, error) {
	var own *tls.Certificate
	if host.HasCert() {
		cert, err := s.loadCert(host.CertFile, host.KeyFile, host.CertType)
		if err != nil {
			logs.Debug("Failed to load certificate of %s: %v", host.Host, err)
		} else if certCovers(cert, sni) != matchNone {
			return cert, nil
		}
		own = cert
	}

	var best *tls.Certificate
	bestMatch := matchNone
	if host.Client != nil {
		s.store.RangeClientCertHosts(host.Client.Id, func(v *model.Host) bool {
			if v.Id == host.Id {
				return true
			}
			cert, err := s.loadCert(v.CertFile, v.KeyFile, v.CertType)
			if err != nil {
				return true
			}
			if m := certCovers(cert, sni); m > bestMatch {
				best, bestMatch = cert, m
			}
			return bestMatch != matchExact
		})
	}
	if best != nil {
		return best, nil
	}

	if cert := s.defaultCertFor(sni); cert != nil {
		return cert, nil
	}
	if own != nil {
		return own, nil
	}
	return nil, errors.New("no certificate for " + sni)
}

// defaultCertFor returns the default certificate if it covers sni.
func (s *HttpsServer) defaultCertFor(sni string) *tls.Certificate {
	if !s.hasDefaultCert {
		return nil
	}
	cert, err := s.loadCert(s.defaultCertFile, s.defaultKeyFile, "file")
	if err != nil {
		logs.Error("Failed to load certificate: %v", err)
		return nil
	}
	if certCovers(cert, sni) == matchNone {
		return nil
	}
	return cert
}

// autoSSL reports whether TLS for host is terminated with certificates
// obtained by AutoSSL: the entry enables it and the server listens on the
// standard HTTP or HTTPS port, or AutoSSL is forced.
func (s *HttpProxy) autoSSL(host *model.Host) bool {
	return host.AutoSSL && (s.HttpPort == 80 || s.HttpsPort == 443 || s.ForceAutoSsl)
}

// serverTLSConfig returns the configuration to terminate TLS for sni with:
// that of AutoSSL if host enables it, a fresh one holding the certificate
// selectCert picks otherwise. It returns nil if no enabled entry serves sni,
// so no certificate is ever handed out for an unconfigured name, or if there
// is no fitting certificate.
func (s *HttpsServer) serverTLSConfig(host *model.Host, sni string) (cfg *tls.Config, autoSSL bool) {
	var cert *tls.Certificate
	switch {
	case host == nil:
		return nil, false
	case s.autoSSL(host):
		return s.certMagicTls, true
	default:
		c, err := s.selectCert(host, sni)
		if err != nil {
			return nil, false
		}
		cert = c
	}
	cfg = &tls.Config{
		Certificates: []tls.Certificate{*cert},
		NextProtos:   s.tlsNextProtos,
	}
	cfg.SetSessionTicketKeys(s.ticketKeys)
	return cfg, false
}
