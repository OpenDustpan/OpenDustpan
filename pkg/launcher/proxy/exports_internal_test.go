package proxy

import (
	"context"
	"io"
	"net"
	"net/netip"
	"syscall"
	"time"

	"opendustpan.org/opendustpan/pkg/egress"
)

// ProbeForms exposes the forms of a secret to the tests.
func ProbeForms(secret string) [][]byte { return formsOf(secret) }

// ProbeScanReader exposes the scan reader to the tests.
func ProbeScanReader(secrets []string, r io.Reader) io.Reader {
	return newScanner(secrets).reader(r)
}

// ProbeScanWindow exposes the window of a scanner to the tests.
func ProbeScanWindow(secrets []string) int { return newScanner(secrets).window }

// ProbeControl exposes the Control check of the outbound guard.
func ProbeControl(rules egress.Rules, expected netip.Addr) func(string, string, syscall.RawConn) error {
	return guardControl(rules, expected)
}

// Dialed is one connect that the guarded dial tried.
type Dialed struct {
	Addr    netip.AddrPort
	Control func(string, string, syscall.RawConn) error
}

// ProbeGuardedDial builds the guarded dial with a connect function that
// records the address and the Control check instead of opening a socket.
func ProbeGuardedDial(
	rules egress.Rules, res Resolver, timeout time.Duration, seen func(Dialed),
) func(ctx context.Context, host string, port int) (net.Conn, error) {
	g := &guardDialer{
		rules: rules, resolver: res, timeout: timeout,
		dial: func(_ context.Context, d *net.Dialer, addr netip.AddrPort) (net.Conn, error) {
			seen(Dialed{Addr: addr, Control: d.Control})

			return nil, ErrDialFailed
		},
	}

	return g.DialContext
}
