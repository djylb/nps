package httpproxy

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"strings"

	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/logs"
)

// certMatch is how a certificate covers a server name.
type certMatch int

const (
	matchNone certMatch = iota
	matchWildcard
	matchExact
)

// matchCert reports how leaf covers name, by its DNS SANs, or by its common
// name if it has none, following RFC 6125: a wildcard stands for exactly the
// leftmost label, so "*.example.com" covers a.example.com but neither
// example.com nor x.a.example.com. Names compare case-insensitively,
// ignoring a trailing dot.
func matchCert(leaf *x509.Certificate, name string) certMatch {
	if leaf == nil {
		return matchNone
	}
	name = normalizeName(name)
	names := leaf.DNSNames
	if len(names) == 0 && leaf.Subject.CommonName != "" {
		names = []string{leaf.Subject.CommonName}
	}
	best := matchNone
	for _, n := range names {
		n = normalizeName(n)
		if n == name {
			return matchExact
		}
		if suffix, ok := strings.CutPrefix(n, "*."); ok && suffix != "" {
			if label, rest, ok := strings.Cut(name, "."); ok && label != "" && rest == suffix {
				best = matchWildcard
			}
		}
	}
	return best
}

func normalizeName(name string) string {
	return strings.TrimSuffix(strings.ToLower(name), ".")
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
//  4. otherwise host's own certificate, then the default one, as configured.
//
// It fails if none of them loads.
func (s *HttpsServer) selectCert(host *file.Host, sni string) (*tls.Certificate, error) {
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
		file.GetDb().RangeClientCertHosts(host.Client.Id, func(v *file.Host) bool {
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

	var deflt *tls.Certificate
	if s.hasDefaultCert {
		cert, err := s.loadCert(s.defaultCertFile, s.defaultKeyFile, "file")
		if err != nil {
			logs.Error("Failed to load certificate: %v", err)
		} else if certCovers(cert, sni) != matchNone || own == nil {
			return cert, nil
		}
		deflt = cert
	}
	if own != nil {
		return own, nil
	}
	if deflt != nil {
		return deflt, nil
	}
	return nil, errors.New("no certificate for " + sni)
}

// autoSSL reports whether TLS for host is terminated with certificates
// obtained by AutoSSL: the entry enables it and the server listens on the
// standard HTTP or HTTPS port, or AutoSSL is forced.
func (s *HttpProxy) autoSSL(host *file.Host) bool {
	return host.AutoSSL && (s.HttpPort == 80 || s.HttpsPort == 443 || s.ForceAutoSsl)
}

// serverTLSConfig returns the configuration to terminate TLS for sni with:
// that of AutoSSL if host enables it, a fresh one holding the certificate
// selectCert picks otherwise, or, if host is nil because no entry serves
// sni, one holding the default certificate, so the HTTP layer can answer
// that the host is not found. It returns nil if there is no certificate.
func (s *HttpsServer) serverTLSConfig(host *file.Host, sni string) (cfg *tls.Config, autoSSL bool) {
	var cert *tls.Certificate
	switch {
	case host == nil:
		if !s.hasDefaultCert {
			return nil, false
		}
		c, err := s.loadCert(s.defaultCertFile, s.defaultKeyFile, "file")
		if err != nil {
			logs.Error("Failed to load certificate: %v", err)
			return nil, false
		}
		cert = c
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
