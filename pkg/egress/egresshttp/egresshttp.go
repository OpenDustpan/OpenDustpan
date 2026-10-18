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

// Client returns an HTTP client that applies the egress rules: the dial
// guard on every connection, no proxy from the environment, the redirect
// policy r for the schemes s, and egress.Timeout for each request. Read a
// body with ReadBody.
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
		Transport: transport,
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
