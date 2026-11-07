package proxy

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"strconv"
	"syscall"
	"time"

	"opendustpan.org/opendustpan/pkg/egress"
)

// The errors of the outbound guard dial. Their text is fixed: it holds no
// address, no host and no text of a library error.
var (
	// ErrGuardRefused is the error for an address that the outbound guard
	// refuses.
	ErrGuardRefused = errors.New("the outbound guard refused the address")
	// ErrResolveFailed is the error for a host that did not resolve.
	ErrResolveFailed = errors.New("the host did not resolve")
	// ErrDialFailed is the error for a connection that did not open.
	ErrDialFailed = errors.New("the connection did not open")
)

// maxDialAddrs is the largest number of addresses that one dial tries.
const maxDialAddrs = 4

// Resolver is the part of net.Resolver that the job proxy uses. A test gives
// its own.
type Resolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

// dialFunc opens one connection to one address that the guard checked.
type dialFunc func(ctx context.Context, d *net.Dialer, addr netip.AddrPort) (net.Conn, error)

// guardDialer resolves a host, checks each address with the rules of
// pkg/egress and dials the address that it checked.
type guardDialer struct {
	rules    egress.Rules
	resolver Resolver
	timeout  time.Duration
	dial     dialFunc
}

// plainDial is the dialFunc of a real connection.
func plainDial(ctx context.Context, d *net.Dialer, addr netip.AddrPort) (net.Conn, error) {
	c, err := d.DialContext(ctx, "tcp", addr.String())
	if err != nil {
		// The text of a network error holds the address, so it is not kept.
		if errors.Is(err, ErrGuardRefused) {
			return nil, ErrGuardRefused
		}

		return nil, ErrDialFailed
	}

	return c, nil
}

// DialContext opens a connection to host and port. Layer 1: every address of
// the answer must pass CheckAddr, or the dial fails and nothing connects.
// Layer 2: the dial uses the IP address that layer 1 checked, so a second DNS
// answer cannot change the target. Layer 3: net.Dialer.Control checks the
// address of the socket again, at the moment of the connect.
func (g *guardDialer) DialContext(ctx context.Context, host string, port int) (net.Conn, error) {
	addrs, err := g.resolve(ctx, host)
	if err != nil {
		return nil, err
	}

	last := ErrDialFailed

	for _, a := range addrs {
		var d net.Dialer

		d.Timeout = g.timeout
		d.Control = guardControl(g.rules, a)
		d.KeepAlive = -1

		ap, perr := netip.ParseAddrPort(hostPort(a.String(), port))
		if perr != nil {
			return nil, ErrDialFailed
		}

		c, derr := g.dial(ctx, &d, ap)
		if derr == nil {
			return c, nil
		}

		if errors.Is(derr, ErrGuardRefused) {
			last = ErrGuardRefused
		}
	}

	return nil, last
}

// resolve returns the checked addresses of host. It refuses the whole host
// when any address of the answer fails the check, so a mixed answer cannot
// pick an address after the check.
func (g *guardDialer) resolve(ctx context.Context, host string) ([]netip.Addr, error) {
	var addrs []netip.Addr

	if a, err := netip.ParseAddr(host); err == nil {
		addrs = []netip.Addr{a}
	} else {
		rctx, cancel := context.WithTimeout(ctx, g.timeout)
		defer cancel()

		got, lerr := g.resolver.LookupNetIP(rctx, "ip", host)
		if lerr != nil || len(got) == 0 {
			return nil, ErrResolveFailed
		}

		addrs = got
	}

	// Check every address of the answer first, then keep the first few, so an
	// address late in the answer cannot pass unchecked.
	out := make([]netip.Addr, 0, min(len(addrs), maxDialAddrs))

	for _, a := range addrs {
		if g.rules.CheckAddr(a) != nil {
			return nil, ErrGuardRefused
		}

		if len(out) < maxDialAddrs {
			out = append(out, a.Unmap())
		}
	}

	return out, nil
}

// guardControl returns the Control function of a dial to expected. It refuses
// a socket address that is not expected, and an address that the rules refuse.
// Defense in depth: resolve checked the address already; this check stays so
// that a change in the dial path cannot reach an address that nobody checked.
func guardControl(rules egress.Rules, expected netip.Addr) func(network, address string, _ syscall.RawConn) error {
	return func(_, address string, _ syscall.RawConn) error {
		ap, err := netip.ParseAddrPort(address)
		if err != nil {
			return ErrGuardRefused
		}

		got := ap.Addr().Unmap()
		if got != expected.Unmap() || rules.CheckAddr(got) != nil {
			return ErrGuardRefused
		}

		return nil
	}
}

// hostPort joins a host and a port.
func hostPort(host string, port int) string {
	return net.JoinHostPort(host, strconv.Itoa(port))
}
