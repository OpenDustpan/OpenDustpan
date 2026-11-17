package runnerproxysvc_test

import (
	"net/http"
	"net/netip"
	"sync/atomic"
	"testing"

	"opendustpan.org/opendustpan/internal/runnerproxysvc"
	"opendustpan.org/opendustpan/pkg/jobproxy"
)

// TestRequestIdentityChecks checks the layers that bind a request to the
// host of its proxy rule: an unnamed host, a mismatched SNI or Host header,
// and a path that is not canonical each get a refusal before any dial.
func TestRequestIdentityChecks(t *testing.T) {
	t.Parallel()

	resolver := &testResolver{answers: map[string][]netip.Addr{}, calls: atomic.Int32{}}

	opts := defaultOptions()
	opts.rules = inspectRules(jobproxy.DefaultPort, http.MethodPost)
	opts.hasRules = true
	opts.resolver = resolver

	srv, _ := newServer(t, opts)
	addr := srv.Addr()

	t.Run("unnamed host", func(t *testing.T) {
		t.Parallel()

		checkUnnamedHost(t, addr)
	})

	t.Run("SNI mismatch", func(t *testing.T) {
		t.Parallel()

		checkSNIMismatch(t, addr, srv)
	})

	t.Run("Host mismatch", func(t *testing.T) {
		t.Parallel()

		checkHostMismatch(t, addr, srv)
	})

	t.Run("non-canonical path", func(t *testing.T) {
		t.Parallel()

		checkNonCanonicalPath(t, addr, srv)
	})

	if got := resolver.calls.Load(); got != 0 {
		t.Errorf("the proxy resolved a host %d times for refusals that must not dial", got)
	}
}

// checkUnnamedHost checks that a CONNECT to a host that no proxy rule names
// gets 403.
func checkUnnamedHost(t *testing.T, addr string) {
	t.Helper()

	result := connectTo(t, addr, "unknown.example.test:443")
	defer closeConn(t, result.conn)

	if result.status != http.StatusForbidden {
		t.Fatalf("CONNECT to an unnamed host got %d, want 403", result.status)
	}
}

// checkSNIMismatch checks that a TLS handshake with another server name
// fails.
func checkSNIMismatch(t *testing.T, addr string, srv *runnerproxysvc.Server) {
	t.Helper()

	result := connectTo(t, addr, testHost+":443")
	defer closeConn(t, result.conn)

	if result.status != http.StatusOK {
		t.Fatalf("CONNECT got %d, want 200", result.status)
	}

	if _, _, err := startTLS(t, result, "other.example.test", rootsFor(t, srv)); err == nil {
		t.Fatal("the TLS handshake with a mismatched SNI passed")
	}
}

// checkHostMismatch checks that a request with another Host header gets 421.
func checkHostMismatch(t *testing.T, addr string, srv *runnerproxysvc.Server) {
	t.Helper()

	result := connectTo(t, addr, testHost+":443")
	defer closeConn(t, result.conn)

	if result.status != http.StatusOK {
		t.Fatalf("CONNECT got %d, want 200", result.status)
	}

	conn, reader, err := startTLS(t, result, testHost, rootsFor(t, srv))
	if err != nil {
		t.Fatalf("start TLS: %v", err)
	}
	defer closeConn(t, conn)

	reply := rawRequest(t, conn, reader, "GET /v1/team HTTP/1.1\r\nHost: other.example.test\r\n\r\n")
	if reply.status != http.StatusMisdirectedRequest {
		t.Fatalf("a request with a mismatched Host got %d, want 421", reply.status)
	}
}

// checkNonCanonicalPath checks that a path with a dot segment gets 400.
func checkNonCanonicalPath(t *testing.T, addr string, srv *runnerproxysvc.Server) {
	t.Helper()

	result := connectTo(t, addr, testHost+":443")
	defer closeConn(t, result.conn)

	if result.status != http.StatusOK {
		t.Fatalf("CONNECT got %d, want 200", result.status)
	}

	conn, reader, err := startTLS(t, result, testHost, rootsFor(t, srv))
	if err != nil {
		t.Fatalf("start TLS: %v", err)
	}
	defer closeConn(t, conn)

	reply := rawRequest(t, conn, reader, "GET /v1/../admin HTTP/1.1\r\nHost: "+testHost+"\r\n\r\n")
	if reply.status != http.StatusBadRequest {
		t.Fatalf("a non-canonical path got %d, want 400", reply.status)
	}
}
