package install

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/djylb/netx"
	"github.com/djylb/netx/tlsconn"
)

// httpClient sends requests with certificate verification against the system
// roots and retries a request without verification only when the certificate
// check itself failed, so devices with a broken CA store can still update.
// Release manifests are signed and every download is checked against a
// digest, so the connection only needs to deliver them.
type httpClient struct {
	verified   *http.Client
	unverified *http.Client
}

func newHTTPClient() *httpClient {
	return &httpClient{
		verified:   newTransportClient(false),
		unverified: newTransportClient(true),
	}
}

func newTransportClient(insecure bool) *http.Client {
	const timeout = 5 * time.Second
	const idleTimeout = 10 * time.Second
	const keepAliveTime = 30 * time.Second

	dial := func(ctx context.Context, network, addr string) (net.Conn, error) {
		d := &net.Dialer{Timeout: timeout, KeepAlive: keepAliveTime}
		return d.DialContext(ctx, network, addr)
	}
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			raw, err := dial(ctx, network, addr)
			if err != nil {
				return nil, err
			}
			return netx.NewTimeoutConn(raw, idleTimeout), nil
		},
		DialTLSContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			raw, err := dial(ctx, network, addr)
			if err != nil {
				return nil, err
			}
			host, _, err := net.SplitHostPort(addr)
			if err != nil {
				_ = raw.Close()
				return nil, err
			}
			cfg := &tls.Config{ServerName: host, InsecureSkipVerify: insecure}
			hsCtx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			tlsConn, err := tlsconn.Client(hsCtx, raw, cfg)
			if err != nil {
				return nil, err
			}
			return netx.NewTimeoutConn(tlsConn, idleTimeout), nil
		},
		TLSHandshakeTimeout:   timeout,
		ResponseHeaderTimeout: timeout,
	}
	if insecure {
		// requests through an HTTPS proxy use TLSClientConfig instead of DialTLSContext
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	return &http.Client{Transport: transport}
}

// send runs req through send with the verifying client, and again with the
// other one when the certificate check failed. It reports whether the
// certificate was verified.
func (c *httpClient) send(req *http.Request, send func(*http.Client) (*http.Response, error)) (*http.Response, bool, error) {
	// The response is returned to the caller, which closes its body.
	//noinspection GoResourceLeak
	resp, err := send(c.verified)
	if err == nil || !isCertificateError(err) {
		return resp, err == nil, err
	}
	if resp != nil {
		_ = resp.Body.Close()
	}
	fmt.Printf("WARNING: certificate verification for %s failed: %v\n", req.URL.Host, err)
	fmt.Println("WARNING: the system certificate store may be broken or outdated; retrying WITHOUT certificate verification, the download is still checked against its digest")
	resp, err = send(c.unverified)
	return resp, false, err
}

// do sends req, following redirects, and reports whether the server
// certificate was verified.
func (c *httpClient) do(req *http.Request) (*http.Response, bool, error) {
	return c.send(req, func(cl *http.Client) (*http.Response, error) { return cl.Do(req) })
}

// RoundTrip implements http.RoundTripper with the same fallback, for the
// HTTP sources of selfupdate.
func (c *httpClient) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, _, err := c.send(req, func(cl *http.Client) (*http.Response, error) { return cl.Transport.RoundTrip(req) })
	return resp, err
}

var _ http.RoundTripper = (*httpClient)(nil)

func isCertificateError(err error) bool {
	var verifyErr *tls.CertificateVerificationError
	var authErr x509.UnknownAuthorityError
	var invalidErr x509.CertificateInvalidError
	var hostErr x509.HostnameError
	return errors.As(err, &verifyErr) || errors.As(err, &authErr) ||
		errors.As(err, &invalidErr) || errors.As(err, &hostErr)
}
