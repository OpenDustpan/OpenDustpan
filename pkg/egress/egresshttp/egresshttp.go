// Package egresshttp applies the egress rules to the outbound HTTP requests
// of the server. Every sender of a notification and every reader of an
// iCal feed makes its requests with a client from this package.
package egresshttp

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"syscall"

	"opendustpan.org/opendustpan/pkg/egress"
)

// Control is a net.Dialer Control function. It returns egress.ErrRefused
// for a connection to an address that egress.CheckAddr refuses. It runs
// after name resolution, on the address of each connection, so a DNS answer
// that changes after a URL check cannot pass it.
func Control(_, address string, _ syscall.RawConn) error {
	ap, err := netip.ParseAddrPort(address)
	if err != nil {
		return fmt.Errorf("%w: the address does not parse", egress.ErrRefused)
	}

	return egress.CheckAddr(ap.Addr())
}

// maxIdleConns bounds the idle connections that a client keeps.
const maxIdleConns = 16

// Client returns an HTTP client that applies the egress rules:
// egress.CheckURL with the schemes s on the URL of every request, the
// first one and each redirect, the dial guard on every connection, no
// proxy from the environment, the redirect policy r, and egress.Timeout
// for each request. Read a body with ReadBody.
func Client(r egress.Redirects, s egress.Scheme) *http.Client {
	dialer := &net.Dialer{ //nolint:exhaustruct_v5 // the other fields keep their zero defaults; two of them are deprecated
		Timeout: egress.Timeout,
		Control: Control,
	}

	transport := &http.Transport{
		Proxy:               nil,
		DialContext:         dialer.DialContext,
		TLSHandshakeTimeout: egress.Timeout,
		MaxIdleConns:        maxIdleConns,
		ForceAttemptHTTP2:   true,
	}

	return &http.Client{
		Transport: checkURL{next: transport, scheme: s},
		Timeout:   egress.Timeout,
		Jar:       nil,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > egress.MaxRedirects {
				return fmt.Errorf("%w: more than %d redirects", egress.ErrRefused, egress.MaxRedirects)
			}

			return egress.CheckRedirect(via[len(via)-1].URL, req.URL, r, s)
		},
	}
}

// checkURL is a RoundTripper that runs egress.CheckURL on the URL of each
// request before next sends it.
type checkURL struct {
	next   http.RoundTripper
	scheme egress.Scheme
}

func (c checkURL) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL == nil {
		return nil, fmt.Errorf("%w: the request has no URL", egress.ErrRefused)
	}

	err := egress.CheckURL(req.URL.String(), c.scheme)
	if err != nil {
		if req.Body != nil {
			// A RoundTripper closes the body also on an error. The error
			// of the check is the one that the caller needs.
			if cerr := req.Body.Close(); cerr != nil {
				return nil, errors.Join(err, cerr)
			}
		}

		return nil, err
	}

	resp, err := c.next.RoundTrip(req)
	if err != nil {
		return nil, fmt.Errorf("round trip: %w", err)
	}

	return resp, nil
}

// CloseIdleConnections closes the idle connections of next, for
// http.Client.CloseIdleConnections.
func (c checkURL) CloseIdleConnections() {
	if ci, ok := c.next.(interface{ CloseIdleConnections() }); ok {
		ci.CloseIdleConnections()
	}
}

// ErrTooLarge is the error of a body larger than egress.MaxBodyBytes.
var ErrTooLarge = errors.New("the response body is larger than the limit")

// ReadBody reads at most egress.MaxBodyBytes of the body of resp, after
// the transport decompressed it, and returns ErrTooLarge for a larger
// body.
func ReadBody(resp *http.Response) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(resp.Body, egress.MaxBodyBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	if len(b) > egress.MaxBodyBytes {
		return nil, fmt.Errorf("read body: %w", ErrTooLarge)
	}

	return b, nil
}
