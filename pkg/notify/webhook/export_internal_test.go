package webhook

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"time"

	"opendustpan.org/opendustpan/internal/egress/egresshttp"
	"opendustpan.org/opendustpan/pkg/egress"
)

// errNotTransport reports a test server whose client has no *http.Transport.
var errNotTransport = errors.New("the test server client has no *http.Transport")

// NewForTest returns a Sender that reaches srv, a TLS test server on
// loopback, for any host name. It keeps the redirect policy, the timeout and
// the URL check of the production client, and replaces the dial guard and
// the trust roots only. This file is part of the tests, so no production
// build holds the replacement.
func NewForTest(srv *httptest.Server, now func() time.Time) (*Sender, error) {
	c := egresshttp.Client(egress.NoRedirect, egress.HTTPSOnly)

	base, ok := srv.Client().Transport.(*http.Transport)
	if !ok {
		return nil, errNotTransport
	}

	tr := base.Clone()
	addr := srv.Listener.Addr().String()
	dialer := new(net.Dialer)

	tr.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return dialer.DialContext(ctx, network, addr)
	}

	c.Transport = urlChecked{next: tr}

	return newSender(c, now), nil
}

// urlChecked runs egress.CheckURL on each request, as the production
// transport does.
type urlChecked struct{ next http.RoundTripper }

func (u urlChecked) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := egress.CheckURL(req.URL.String(), egress.HTTPSOnly); err != nil {
		return nil, err
	}

	return u.next.RoundTrip(req) //nolint:wrapcheck // A test double passes the result on.
}

func (u urlChecked) CloseIdleConnections() {
	if ci, ok := u.next.(interface{ CloseIdleConnections() }); ok {
		ci.CloseIdleConnections()
	}
}

// NewDialGuardedForTest returns a Sender whose connections run the production
// dial guard, egresshttp.Control, and all go to a loopback address. So every
// connection stands for a name that resolves to a refused address, and the
// test needs no DNS. This file is part of the tests.
func NewDialGuardedForTest(now func() time.Time) *Sender {
	c := egresshttp.Client(egress.NoRedirect, egress.HTTPSOnly)

	dialer := new(net.Dialer)
	dialer.Control = egresshttp.Control

	tr := new(http.Transport)
	tr.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return dialer.DialContext(ctx, network, "127.0.0.1:9")
	}

	c.Transport = urlChecked{next: tr}

	return newSender(c, now)
}

// SetPortsForTest sets the ports of s, for a Sender that NewForTest made.
func (s *Sender) SetPortsForTest(p egress.Ports) { s.ports = p }

// SetTimeoutForTest sets the deadline of one send.
func (s *Sender) SetTimeoutForTest(d time.Duration) { s.timeout = d }
