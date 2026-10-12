//go:build e2e

package e2e

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

// proxyTestTimeout bounds each read and dial of the proxy test.
const proxyTestTimeout = 5 * time.Second

// startEchoServer listens on loopback and echoes each line back on every
// connection. It answers the address and stops when the test ends.
func startEchoServer(t *testing.T) string {
	t.Helper()

	//nolint:exhaustruct_v5 // The test listens with defaults; only the network and address matter.
	lc := &net.ListenConfig{}

	listener, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen echo server: %v", err)
	}

	var wg sync.WaitGroup

	wg.Go(func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}

			wg.Go(func() {
				defer conn.Close() //nolint:errcheck // The echo ends; the result is not checked.

				_, _ = io.Copy(conn, conn) //nolint:errcheck // A copy error only ends the echo.
			})
		}
	})

	t.Cleanup(func() {
		_ = listener.Close() //nolint:errcheck // The test ends; the result is not checked.
		wg.Wait()
	})

	return listener.Addr().String()
}

// dialProxy dials the proxy on port.
func dialProxy(t *testing.T, port string) net.Conn {
	t.Helper()

	//nolint:exhaustruct_v5 // The dialer needs only a timeout; the rest stays zero.
	dialer := &net.Dialer{Timeout: proxyTestTimeout}

	conn, err := dialer.DialContext(t.Context(), "tcp", net.JoinHostPort("127.0.0.1", port))
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}

	t.Cleanup(func() { _ = conn.Close() }) //nolint:errcheck // The test ends; the result is not checked.

	return conn
}

// echoes reports whether conn answers one line with the same line.
func echoes(t *testing.T, conn net.Conn, line string) bool {
	t.Helper()

	if err := conn.SetDeadline(time.Now().Add(proxyTestTimeout)); err != nil {
		t.Fatalf("set deadline: %v", err)
	}

	if _, err := io.WriteString(conn, line+"\n"); err != nil {
		return false
	}

	got, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		return false
	}

	return got == line+"\n"
}

// closedByPeer reports whether a read on conn ends because the peer closed
// it, and not because the deadline passed.
func closedByPeer(t *testing.T, conn net.Conn) bool {
	t.Helper()

	if err := conn.SetReadDeadline(time.Now().Add(proxyTestTimeout)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}

	buf := make([]byte, 1)

	_, err := conn.Read(buf)

	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return false
	}

	return err != nil
}

// portIsBound reports whether another listener cannot bind port, that is,
// whether the proxy still holds it.
func portIsBound(ctx context.Context, t *testing.T, port string) bool {
	t.Helper()

	//nolint:exhaustruct_v5 // The probe listens with defaults; only the network and address matter.
	lc := &net.ListenConfig{}

	listener, err := lc.Listen(ctx, "tcp", net.JoinHostPort("127.0.0.1", port))
	if err != nil {
		return true
	}

	_ = listener.Close() //nolint:errcheck // The probe only tests the bind.

	return false
}

// TestDBProxyPauseKeepsThePort proves that a pause drops the live
// connections and closes new ones at once, that the proxy keeps its port
// while paused, and that a resume forwards again on the same port (issue
// 391). No listener is closed and bound again.
func TestDBProxyPauseKeepsThePort(t *testing.T) {
	t.Parallel()

	target := startEchoServer(t)
	proxy, _ := startDBProxy(t.Context(), t, "postgres://user@"+target+"/db")
	port := proxy.port

	live := dialProxy(t, port)
	if !echoes(t, live, "before") {
		t.Fatal("proxy does not forward before the pause")
	}

	proxy.Pause()

	if !closedByPeer(t, live) {
		t.Fatal("pause left a live connection open")
	}

	if !closedByPeer(t, dialProxy(t, port)) {
		t.Fatal("paused proxy did not close a new connection")
	}

	if !portIsBound(t.Context(), t, port) {
		t.Fatalf("paused proxy released port %s", port)
	}

	proxy.Resume()

	if proxy.port != port {
		t.Fatalf("resume moved the proxy from port %s to %s", port, proxy.port)
	}

	if !echoes(t, dialProxy(t, port), "after") {
		t.Fatal("proxy does not forward after the resume")
	}
}
