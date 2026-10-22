//go:build e2e

package chat

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net"
	"net/http"
	"time"

	"opendustpan.org/opendustpan/internal/egress/egresshttp"
	"opendustpan.org/opendustpan/pkg/egress"
)

// NewLoopback returns a Sender that sends every request to the TLS receiver at
// addr, whatever host the URL names, and trusts roots with the name serverName
// for that receiver. It keeps the URL check, the host checks, the port check,
// the redirect policy, the timeout and the limits of the production sender,
// and replaces the dial guard and the trust of the certificate only.
//
// This seam is for the end-to-end suite, which has a receiver on the machine
// of the test. The file has the build tag e2e, so no production build holds
// it, and no flag, no environment variable and no setting can reach it: only
// code that is compiled with the tag can call it.
func NewLoopback(addr string, roots *x509.CertPool, serverName string, renderers []Renderer, opts ...Option) *Sender {
	c := egresshttp.Client(egress.NoRedirect, egress.HTTPSOnly)
	dialer := new(net.Dialer)

	tr := &http.Transport{
		Proxy:               nil,
		TLSClientConfig:     &tls.Config{RootCAs: roots, ServerName: serverName, MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout: egress.Timeout,
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, network, addr)
		},
	}

	c.Transport = loopbackChecked{next: tr}

	s := assemble(c, time.Now, renderers)

	for _, o := range opts {
		o(s)
	}

	return s
}

// loopbackChecked runs egress.CheckURL on each request, as the production
// transport does.
type loopbackChecked struct{ next http.RoundTripper }

func (l loopbackChecked) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := egress.CheckURL(req.URL.String(), egress.HTTPSOnly); err != nil {
		return nil, err
	}

	return l.next.RoundTrip(req) //nolint:wrapcheck // The seam passes the result on.
}

func (l loopbackChecked) CloseIdleConnections() {
	if ci, ok := l.next.(interface{ CloseIdleConnections() }); ok {
		ci.CloseIdleConnections()
	}
}
