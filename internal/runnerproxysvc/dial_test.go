package runnerproxysvc_test

import (
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"

	"opendustpan.org/opendustpan/pkg/egress"
	"opendustpan.org/opendustpan/pkg/jobproxy"
)

// TestBoundDialChecksEachAddress checks that the tunnel dial uses only an
// address that the outbound guard checked. A mixed answer with one denied
// address fails closed, and a checked address is the one that carries the
// bytes.
func TestBoundDialChecksEachAddress(t *testing.T) {
	t.Parallel()

	t.Run("mixed answer refused", func(t *testing.T) {
		t.Parallel()

		checkMixedAnswer(t)
	})

	t.Run("checked address dialed", func(t *testing.T) {
		t.Parallel()

		checkCheckedAddress(t)
	})

	t.Run("server host tunnel", func(t *testing.T) {
		t.Parallel()

		checkServerTunnel(t)
	})
}

// checkMixedAnswer checks that a mixed DNS answer is refused before a dial.
func checkMixedAnswer(t *testing.T) {
	t.Helper()

	opts := defaultOptions()
	opts.rules = tunnelRules(testHost, jobproxy.DefaultPort)
	opts.hasRules = true
	opts.egress = egress.Rules{Allow: nil, LoopbackForTests: false}
	opts.resolver = &testResolver{
		answers: map[string][]netip.Addr{
			testHost: {netip.MustParseAddr("93.184.216.34"), netip.MustParseAddr("10.0.0.1")},
		},
		calls: atomic.Int32{},
	}

	srv, log := newServer(t, opts)

	result := connectTo(t, srv.Addr(), testHost+":443")
	defer closeConn(t, result.conn)

	if result.status != http.StatusForbidden {
		t.Fatalf("CONNECT with a mixed answer got %d, want 403", result.status)
	}

	if err := srv.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	line := log.String()
	if !strings.Contains(line, "reason=guard-refused") {
		t.Errorf("the blocked line %q does not hold the guard reason", line)
	}
}

// checkCheckedAddress checks that the checked address carries the bytes.
func checkCheckedAddress(t *testing.T) {
	t.Helper()

	port := echoListener(t)
	resolver := &testResolver{
		answers: map[string][]netip.Addr{testHost: {netip.MustParseAddr("127.0.0.1")}},
		calls:   atomic.Int32{},
	}

	opts := defaultOptions()
	opts.rules = tunnelRules(testHost, port)
	opts.hasRules = true
	opts.egress = egress.Rules{Allow: nil, LoopbackForTests: true}
	opts.resolver = resolver

	srv, _ := newServer(t, opts)

	result := connectTo(t, srv.Addr(), fmt.Sprintf("%s:%d", testHost, port))
	defer closeConn(t, result.conn)

	if result.status != http.StatusOK {
		t.Fatalf("CONNECT to a checked address got %d, want 200", result.status)
	}

	if _, err := result.conn.Write([]byte("ping")); err != nil {
		t.Fatalf("write through the tunnel: %v", err)
	}

	got := make([]byte, 4)
	if _, err := io.ReadFull(result.reader, got); err != nil {
		t.Fatalf("read through the tunnel: %v", err)
	}

	if string(got) != "ping" {
		t.Errorf("the tunnel returned %q, want %q", got, "ping")
	}

	if calls := resolver.calls.Load(); calls != 1 {
		t.Errorf("the resolver ran %d times, want 1", calls)
	}
}

// checkServerTunnel checks that the built-in tunnel rule of the server host
// carries the launcher traffic without a proxy rule for that host.
func checkServerTunnel(t *testing.T) {
	t.Helper()

	port := echoListener(t)
	resolver := &testResolver{
		answers: map[string][]netip.Addr{serverHost: {netip.MustParseAddr("127.0.0.1")}},
		calls:   atomic.Int32{},
	}

	opts := defaultOptions()
	opts.rules = ""
	opts.hasRules = true
	opts.serverHost = serverHost
	opts.serverPort = port
	opts.egress = egress.Rules{Allow: nil, LoopbackForTests: true}
	opts.resolver = resolver

	srv, _ := newServer(t, opts)

	result := connectTo(t, srv.Addr(), fmt.Sprintf("%s:%d", serverHost, port))
	defer closeConn(t, result.conn)

	if result.status != http.StatusOK {
		t.Fatalf("CONNECT to the server host got %d, want 200", result.status)
	}

	if _, err := result.conn.Write([]byte("poll")); err != nil {
		t.Fatalf("write through the tunnel: %v", err)
	}

	got := make([]byte, 4)
	if _, err := io.ReadFull(result.reader, got); err != nil {
		t.Fatalf("read through the tunnel: %v", err)
	}

	if string(got) != "poll" {
		t.Errorf("the tunnel returned %q, want %q", got, "poll")
	}
}
