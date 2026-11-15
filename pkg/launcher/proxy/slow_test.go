package proxy_test

import (
	"errors"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/launcher/proxy"
)

// shortLimits are small limits for the tests of slow and closed peers.
func shortLimits() proxy.Limits {
	l := noLimits()
	l.HeaderTimeout = 100 * time.Millisecond
	l.HandshakeTimeout = 100 * time.Millisecond
	l.IdleTimeout = 200 * time.Millisecond
	l.DialTimeout = time.Second

	return l
}

// waitClosed waits until the peer closes c.
func waitClosed(t *testing.T, c net.Conn, what string) {
	t.Helper()

	bound(t, c, ioWait)

	_, err := io.Copy(io.Discard, c)

	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		t.Fatalf("%s: the connection stayed open", what)
	}
}

// TestSlowRequestLine checks that a client that sends its request line too
// slowly loses its connection.
func TestSlowRequestLine(t *testing.T) {
	t.Parallel()

	e := newEnv(t, withLimits(shortLimits()))
	c := dialRaw(t, e.addr)

	send(t, c, "CONNECT api.exa")
	waitClosed(t, c, "slow request line")
}

// TestNoTLSHandshake checks that a client that never starts the handshake
// loses its connection.
func TestNoTLSHandshake(t *testing.T) {
	t.Parallel()

	e := newEnv(t, withLimits(shortLimits()))
	c, _, status := e.connectRaw(t, testHost+":"+portOf(e))

	if status != http.StatusOK {
		t.Fatalf("status %d", status)
	}

	waitClosed(t, c, "no TLS handshake")
}

// TestIdleAfterHandshake checks that an idle inspected connection ends.
func TestIdleAfterHandshake(t *testing.T) {
	t.Parallel()

	e := newEnv(t, withLimits(shortLimits()))
	c, _ := e.rawTLS(t)

	waitClosed(t, c, "idle TLS end")
}

// TestIdleTunnel checks that an idle tunnel ends and leaves its record.
func TestIdleTunnel(t *testing.T) {
	t.Parallel()

	e := newEnv(t, withRules(tunnelRules), withLimits(shortLimits()))
	c, _, status := e.connectRaw(t, testHost+":"+portOf(e))

	if status != http.StatusOK {
		t.Fatalf("status %d", status)
	}

	waitClosed(t, c, "idle tunnel")

	if rec := e.sink.waitFor(t, 1)[0]; !rec.Allowed || rec.Mode != "tunnel" {
		t.Errorf("record %+v", rec)
	}
}

// TestSlowHost checks that a host that answers too slowly gives a 504.
func TestSlowHost(t *testing.T) {
	t.Parallel()

	limits := shortLimits()
	limits.ResponseHeaderTimeout = 100 * time.Millisecond

	e := newEnv(t, withLimits(limits), withHandler(func(http.ResponseWriter, *http.Request) {
		pause(t, time.Second)
	}))

	if rep := e.get(t, http1, "/v1/x"); rep.status != http.StatusGatewayTimeout {
		t.Errorf("status %d, want 504", rep.status)
	}

	if rec := e.sink.lastReq(t, 1); rec.Reason != proxy.ReasonTimeout {
		t.Errorf("record %+v, want timeout", rec)
	}
}

// TestClientClosesTunnel checks that a client that closes a tunnel leaves its
// record.
func TestClientClosesTunnel(t *testing.T) {
	t.Parallel()

	e := newEnv(t, withRules(tunnelRules))
	c, _, status := e.connectRaw(t, testHost+":"+portOf(e))

	if status != http.StatusOK {
		t.Fatalf("status %d", status)
	}

	closeNow(t, c)

	if rec := e.sink.waitFor(t, 1)[0]; !rec.Allowed || rec.Status != http.StatusOK {
		t.Errorf("record %+v", rec)
	}
}

// TestHostClosesDuringResponse checks that a host that closes early gives the
// client a failed transfer and not a short success.
func TestHostClosesDuringResponse(t *testing.T) {
	t.Parallel()

	e := newEnv(t, withHandler(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "100000")
		write(w, "partial")
		flush(w)

		panic(http.ErrAbortHandler)
	}))

	if _, err := tryGet(t, e.client(t, http1), e.base()+"/v1/x", ""); err == nil {
		t.Error("the client saw a complete response of a host that closed early")
	}
}
