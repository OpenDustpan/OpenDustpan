package ldapauth

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"syscall"
	"time"

	"github.com/go-ldap/ldap/v3"

	"opendustpan.org/opendustpan/internal/egress/egresshttp"
	"opendustpan.org/opendustpan/pkg/egress"
	"opendustpan.org/opendustpan/pkg/egress/plainconn"
	"opendustpan.org/opendustpan/pkg/session"
)

// ErrPlainRefused reports a connection with no TLS to a host that is not a
// loopback IP address, when the installation setting that allows plain
// connections is off. It matches session.ErrMethodUnavailable too.
var ErrPlainRefused = errors.New("a connection with no TLS to the directory is not allowed")

// plainRefused returns the error of a refused plain connection to address.
// It names the address, the setting key and the one call that changes the
// setting.
func plainRefused(address string) error {
	return fmt.Errorf(`%w: %w: %s: use ldaps:// or StartTLS, or send PATCH /api/dustpan/v1/settings `+
		`with {"%s": true}`, session.ErrMethodUnavailable, ErrPlainRefused, address,
		plainconn.SettingAllowPlainConnections)
}

// plainRule is the rule of the dial guard for a connection with no TLS.
type plainRule int

// Rules for a connection with no TLS.
const (
	// plainNotUsed is the rule of a TLS connection.
	plainNotUsed plainRule = iota + 1
	// plainLoopbackOnly allows a plain connection to a loopback address
	// only.
	plainLoopbackOnly
	// plainAllowed allows a plain connection: the installation opted in.
	plainAllowed
)

// dialControl returns the Control function of the dialer. It applies the
// egress rules to the resolved address of each connection, and refuses a
// plain connection to an address that is not loopback with the rule
// plainLoopbackOnly. It runs before the connection opens.
func dialControl(rules egress.Rules, rule plainRule) func(network, address string, c syscall.RawConn) error {
	guard := egresshttp.ControlWith(rules)

	return func(network, address string, c syscall.RawConn) error {
		if err := guard(network, address, c); err != nil {
			return err
		}

		// The egress guard runs first. It refuses a loopback address unless
		// the rules set LoopbackForTests. With the production rules, a plain
		// connection to a loopback address is refused by the guard, and the
		// plainLoopbackOnly rule has no effect. The rule has an effect only
		// when the egress rules allow loopback, which only tests do.
		//
		// Defense in depth: Verify refuses a plain URL whose host is not a
		// loopback IP address before it dials. This check stays so that a
		// name that resolves to another address cannot pass with no TLS.
		if rule != plainNotUsed && rule != plainAllowed {
			ap, err := netip.ParseAddrPort(address)
			if err != nil || !ap.Addr().Unmap().IsLoopback() {
				return plainRefused(address)
			}
		}

		return nil
	}
}

// open dials the directory, starts TLS, and binds with the service
// account. The caller closes the connection. The connection closes when
// ctx ends.
func (m *Method) open(ctx context.Context) (*ldap.Conn, func(), error) {
	rule := plainNotUsed

	if m.endpoint.transport == transportPlain {
		rule = plainLoopbackOnly
		if m.allowPlain(ctx) {
			rule = plainAllowed
		}

		if rule != plainAllowed && !plainHostAllowed(m.endpoint.host) {
			return nil, nil, plainRefused(m.endpoint.address)
		}
	}

	var dialer net.Dialer

	dialer.Timeout = m.connectTimeout
	dialer.Control = dialControl(m.egress, rule)

	dialCtx, cancel := context.WithTimeout(ctx, m.connectTimeout)
	defer cancel()

	raw, err := dialer.DialContext(dialCtx, "tcp", m.endpoint.address)
	if err != nil {
		return nil, nil, m.fail(ctx, session.ErrMethodUnavailable, "connect to the directory", err)
	}

	conn, err := m.secure(dialCtx, raw)
	if err != nil {
		if cerr := raw.Close(); cerr != nil {
			m.logDetail(ctx, "close the connection", cerr)
		}

		return nil, nil, err
	}

	conn.SetTimeout(m.operationTimeout)

	stop := context.AfterFunc(ctx, func() { m.closeConn(ctx, conn) })
	done := func() {
		stop()
		m.closeConn(ctx, conn)
	}

	if err := conn.Bind(m.bindDN, m.bindPassword.Reveal()); err != nil {
		done()

		return nil, nil, m.serviceBindError(ctx, err)
	}

	return conn, done, nil
}

// secure starts TLS on raw for an ldaps:// URL or StartTLS, and returns the
// LDAP connection.
func (m *Method) secure(ctx context.Context, raw net.Conn) (*ldap.Conn, error) {
	tlsConfig := &tls.Config{
		RootCAs:    m.rootCAs,
		ServerName: m.endpoint.host,
		MinVersion: tls.VersionTLS12,
	}

	switch m.endpoint.transport {
	case transportLDAPS:
		tc := tls.Client(raw, tlsConfig)
		if err := tc.HandshakeContext(ctx); err != nil {
			return nil, m.fail(ctx, session.ErrMethodUnavailable, "TLS handshake with the directory", err)
		}

		conn := ldap.NewConn(tc, true)
		conn.Start()

		return conn, nil
	case transportStartTLS:
		conn := ldap.NewConn(raw, false)
		conn.Start()
		conn.SetTimeout(m.connectTimeout)

		deadline, _ := ctx.Deadline()
		if err := raw.SetDeadline(deadline); err != nil {
			m.closeConn(ctx, conn)

			return nil, m.fail(ctx, session.ErrMethodUnavailable, "set the deadline of the handshake", err)
		}

		if err := conn.StartTLS(tlsConfig); err != nil {
			m.closeConn(ctx, conn)

			return nil, m.fail(ctx, session.ErrMethodUnavailable, "StartTLS with the directory", err)
		}

		if err := raw.SetDeadline(time.Time{}); err != nil {
			m.closeConn(ctx, conn)

			return nil, m.fail(ctx, session.ErrMethodUnavailable, "clear the deadline of the handshake", err)
		}

		return conn, nil
	case transportPlain:
		conn := ldap.NewConn(raw, false)
		conn.Start()

		return conn, nil
	default:
		// Fail closed: parseURL sets one of the kinds above.
		return nil, fmt.Errorf("%w: the transport is not known", session.ErrMethodUnavailable)
	}
}

// closeConn closes conn and logs an error of the close.
func (m *Method) closeConn(ctx context.Context, conn *ldap.Conn) {
	if err := conn.Close(); err != nil {
		m.logger.LogAttrs(ctx, slog.LevelDebug, "ldap connection close failed",
			slog.String("provider", m.providerID), slog.String("error", err.Error()))
	}
}

// allowPlain calls the AllowPlain hook of the config, which reads the
// installation setting that allows plain connections. An error counts as
// false.
func (m *Method) allowPlain(ctx context.Context) bool {
	on, err := m.allowPlainFn(ctx)
	if err != nil {
		m.logger.LogAttrs(ctx, slog.LevelWarn, "ldap plain-transport setting read failed, the setting counts as off",
			slog.String("provider", m.providerID), slog.String("error", err.Error()))

		return false
	}

	return on
}

// network reports whether err is an error of the client or the network,
// not an answer of the directory.
func network(err error) bool {
	var le *ldap.Error
	if !errors.As(err, &le) {
		return true
	}

	return le.ResultCode >= ldap.ErrorNetwork
}

// serviceBindError returns the error of a failed bind as the service
// account.
func (m *Method) serviceBindError(ctx context.Context, err error) error {
	if network(err) {
		return m.fail(ctx, session.ErrMethodUnavailable, "bind as the service account", err)
	}

	return m.fail(ctx, session.ErrMethodUnavailable, "the directory refused the service account", err)
}

// fail logs err, the error of the library, the network or the directory,
// with the step, and returns sentinel with the step only. So the text of the
// directory and of the network reaches the log of the operator, never an
// error that a caller can show to a person.
func (m *Method) fail(ctx context.Context, sentinel error, step string, err error) error {
	m.logDetail(ctx, step, err)

	return fmt.Errorf("%w: %s", sentinel, step)
}

// logDetail logs err with the step.
func (m *Method) logDetail(ctx context.Context, step string, err error) {
	m.logger.LogAttrs(ctx, slog.LevelInfo, "ldap directory error", slog.String("provider", m.providerID),
		slog.String("step", step), slog.String("error", err.Error()))
}
