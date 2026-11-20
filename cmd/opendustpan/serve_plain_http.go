package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"

	"opendustpan.org/opendustpan/internal/cli"
)

// errListenNotTCP reports a listener whose address is not a TCP address.
var errListenNotTCP = errors.New("listen: the bound address is not a TCP address")

// listenSettings answers the settings that decide whether serve may listen
// with plain HTTP on its listen address.
func (f *serveFlags) listenSettings() cli.ListenSettings {
	return cli.ListenSettings{
		Address:        f.listen,
		TLSCert:        f.tlsCert,
		TLSKey:         f.tlsKey,
		PublicURL:      f.publicURL,
		AllowPlainHTTP: f.allowPlainHTTP,
	}
}

// checkPlainHTTP refuses a --listen address that is not loopback when the
// server has no TLS, --public-url is not an https URL and --allow-plain-http
// is not set. Serve runs it at start, before it opens the database. The
// error names the three fixes.
func (f *serveFlags) checkPlainHTTP() error {
	_, err := cli.CheckListen(f.listenSettings())

	return err
}

// listenChecked binds the listen address and checks the bound address with
// the rule of checkPlainHTTP. With --allow-plain-http on an address that is
// not loopback, it logs a warning that names the flag.
func (f *serveFlags) listenChecked(ctx context.Context, logger *slog.Logger) (net.Listener, error) {
	//nolint:exhaustruct_v5 // The server listens with defaults; only the network and address matter.
	lc := &net.ListenConfig{}

	ln, err := lc.Listen(ctx, "tcp", f.listen)
	if err != nil {
		return nil, fmt.Errorf("listen: %w", err)
	}

	if checkErr := checkBoundAddr(ctx, logger, ln.Addr(), f.listenSettings()); checkErr != nil {
		return nil, errors.Join(checkErr, ln.Close())
	}

	return ln, nil
}

// checkBoundAddr checks the address that the listener holds, not the text
// of --listen: a name such as localhost can resolve to an address that is
// not loopback. It refuses an address that is not a TCP address.
//
// Defense in depth: serve refuses the configured address with
// checkPlainHTTP before it opens the database. This check stays so that a
// name that resolves to another address, or a new caller that skips
// checkPlainHTTP, cannot serve plain HTTP off loopback.
func checkBoundAddr(ctx context.Context, logger *slog.Logger, addr net.Addr, s cli.ListenSettings) error {
	tcp, ok := addr.(*net.TCPAddr)
	if !ok || tcp == nil || tcp.IP == nil {
		return fmt.Errorf("%w: %q", errListenNotTCP, addrText(addr))
	}

	bound := s
	bound.Address = tcp.String()

	reason, err := cli.CheckListen(bound)
	if err != nil {
		return err
	}

	if reason == cli.ListenPlainHTTPAllowed {
		logger.WarnContext(ctx, "serve uses plain HTTP on an address that is not loopback: tokens and state "+
			"go over the network with no encryption",
			slog.String("addr", bound.Address), slog.String("flag", "--"+cli.AllowPlainHTTPFlag))
	}

	return nil
}

// addrText answers the text of addr, or the empty text for a nil address.
func addrText(addr net.Addr) string {
	if addr == nil {
		return ""
	}

	return addr.String()
}

// closeServeListener closes the listener of serve after the server stopped.
// The server closes it at its shutdown, so an error for a closed listener is
// expected and is not logged.
func closeServeListener(ctx context.Context, logger *slog.Logger, ln net.Listener) {
	if err := ln.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		logger.WarnContext(ctx, "close the listener", slog.String("error", err.Error()))
	}
}
