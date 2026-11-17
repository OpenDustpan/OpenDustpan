package runnerproxysvc_test

import (
	"bytes"
	"net/http"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/runnerproxysvc"
	"opendustpan.org/opendustpan/pkg/jobproxy"
)

// TestBlockedLineHasHostAndPath checks that a refused request writes one
// blocked line with the host, the canonical path and the fixed reason, and
// that no header, secret or raw path reaches the line.
func TestBlockedLineHasHostAndPath(t *testing.T) {
	t.Parallel()

	t.Run("value of a refusal", func(t *testing.T) {
		t.Parallel()

		checkBlockedValue(t)
	})

	t.Run("hostile values", func(t *testing.T) {
		t.Parallel()

		checkHostileLogValue(t)
	})

	t.Run("through the proxy", func(t *testing.T) {
		t.Parallel()

		checkBlockedThroughProxy(t)
	})
}

// checkBlockedValue checks the line of a refused record with a path.
func checkBlockedValue(t *testing.T) {
	t.Helper()

	var log bytes.Buffer

	sink := runnerproxysvc.NewLogSink(&log)
	if err := sink.Write(testRecord()); err != nil {
		t.Fatalf("Write: %v", err)
	}

	line := log.String()

	wantParts := []string{
		"blocked by the job proxy", "host=" + testHost, "path=/v1/team", "reason=no-rule-matched",
	}
	for _, want := range wantParts {
		if !strings.Contains(line, want) {
			t.Errorf("the blocked line %q does not hold %q", line, want)
		}
	}

	if strings.Contains(line, "\r") {
		t.Errorf("the blocked line holds a carriage return: %q", line)
	}
}

// checkHostileLogValue checks that a hostile record cannot add a line, a raw
// path or a value that the sink does not log.
func checkHostileLogValue(t *testing.T) {
	t.Helper()

	var log bytes.Buffer

	sink := runnerproxysvc.NewLogSink(&log)

	rec := testRecord()
	rec.Host = "evil.example.test\r\nX-Test: 1"
	rec.Path = "/v1/../secret"
	rec.Reason = "bad\r\nreason"
	rec.Method = "hunter2"

	if err := sink.Write(rec); err != nil {
		t.Fatalf("Write: %v", err)
	}

	line := log.String()
	if strings.ContainsAny(line, "\r") {
		t.Errorf("the blocked line holds a carriage return: %q", line)
	}

	if strings.Count(line, "\n") != 1 {
		t.Errorf("the blocked line is not one line: %q", line)
	}

	if strings.Contains(line, "\nX-Test") {
		t.Errorf("the blocked line holds a header line: %q", line)
	}

	if strings.Contains(line, "..") {
		t.Errorf("the blocked line holds the raw path: %q", line)
	}

	if strings.Contains(line, "hunter2") {
		t.Errorf("the blocked line holds a value that it does not log: %q", line)
	}
}

// checkBlockedThroughProxy checks the blocked lines of three refusals at the
// real proxy.
func checkBlockedThroughProxy(t *testing.T) {
	t.Helper()

	opts := defaultOptions()
	opts.rules = inspectRules(jobproxy.DefaultPort, http.MethodPost)
	opts.hasRules = true

	srv, log := newServer(t, opts)
	addr := srv.Addr()

	checkRefusedMethod(t, addr, srv)
	checkRefusedHost(t, addr)
	checkRefusedPath(t, addr, srv)

	if err := srv.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	checkJobLog(t, log.String())
}

// checkRefusedMethod sends an allowed host with a method that no rule allows.
func checkRefusedMethod(t *testing.T, addr string, srv *runnerproxysvc.Server) {
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

	reply := rawRequest(t, conn, reader, "GET /v1/team HTTP/1.1\r\nHost: "+testHost+"\r\n\r\n")
	if reply.status != http.StatusForbidden {
		t.Fatalf("a request that no rule allows got %d, want 403", reply.status)
	}
}

// checkRefusedHost sends a CONNECT for a host that no rule names, with a
// header value that the log must not hold.
func checkRefusedHost(t *testing.T, addr string) {
	t.Helper()

	result := connectRaw(t, addr,
		"CONNECT unknown.example.test:443 HTTP/1.1\r\n"+
			"Host: unknown.example.test:443\r\nAuthorization: Bearer hunter2\r\n\r\n")
	defer closeConn(t, result.conn)

	if result.status != http.StatusForbidden {
		t.Fatalf("CONNECT to an unnamed host got %d, want 403", result.status)
	}
}

// checkRefusedPath sends a path that is not canonical.
func checkRefusedPath(t *testing.T, addr string, srv *runnerproxysvc.Server) {
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

	reply := rawRequest(t, conn, reader,
		"GET /v1/../secret HTTP/1.1\r\nHost: "+testHost+"\r\n\r\n")
	if reply.status != http.StatusBadRequest {
		t.Fatalf("a non-canonical path got %d, want 400", reply.status)
	}
}

// checkJobLog checks the three blocked lines of the job log.
func checkJobLog(t *testing.T, lines string) {
	t.Helper()

	if !strings.Contains(lines, "host="+testHost) || !strings.Contains(lines, "path=/v1/team") {
		t.Errorf("the job log %q does not hold the host and the canonical path", lines)
	}

	if !strings.Contains(lines, "host=unknown.example.test") {
		t.Errorf("the job log %q does not hold the unnamed host", lines)
	}

	if strings.Contains(lines, "/v1/../secret") {
		t.Errorf("the job log %q holds the raw path", lines)
	}

	if strings.Contains(lines, "hunter2") {
		t.Errorf("the job log %q holds the header value", lines)
	}
}
