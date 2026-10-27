package servertest_test

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/servertest"
	"opendustpan.org/opendustpan/pkg/dustpan"
)

// drainTestPeriod is the drain of the drain test: long enough to send
// requests in it, short enough to keep the test fast.
const drainTestPeriod = 3 * time.Second

// TestReadinessDuringDrain proves the stop order: after the stop signal the
// readiness probe answers 503 while the process still serves other requests,
// and a request that began before the signal completes with its answer.
// Liveness stays 204, because the process runs.
func TestReadinessDuringDrain(t *testing.T) {
	t.Parallel()

	c := servertest.Start(t, 1, dustpan.WithDrainPeriod(drainTestPeriod))

	// The request is in flight: the server waits for the rest of the body.
	pipeReader, pipeWriter := io.Pipe()

	type result struct {
		status int
		body   string
		err    error
	}

	done := make(chan result, 1)

	go func() {
		status, body, err := c.DoStream(t.Context(), 0, http.MethodPost, "/api/server/namespaces", pipeReader)
		done <- result{status: status, body: body, err: err}
	}()

	if _, err := pipeWriter.Write([]byte(`{"name":`)); err != nil {
		t.Fatalf("write first part of the body: %v", err)
	}

	signaledAt := time.Now()
	wait := c.Signal(t, 0)

	waitForStatus(
		t,
		c,
		request{
			server: 0,
			method: http.MethodGet,
			path:   "/health/ready",
			body:   "",
			want:   http.StatusServiceUnavailable,
		},
	)

	for _, path := range []string{"/health/ready", "/health/ready/api", "/health/ready/worker"} {
		status, body := c.Do(t, 0, http.MethodGet, path, "")
		if status != http.StatusServiceUnavailable || !strings.Contains(body, "draining") {
			t.Errorf("GET %s in the drain = %d %s, want 503 that names draining", path, status, body)
		}
	}

	send(t, c,
		request{server: 0, method: http.MethodGet, path: "/health/live", body: "", want: http.StatusNoContent},
		request{server: 0, method: http.MethodGet, path: "/api/server/namespaces", body: "", want: http.StatusOK},
	)

	// The shutdown has not begun: the request in flight still completes.
	if _, err := pipeWriter.Write([]byte(`"drained"}`)); err != nil {
		t.Fatalf("write last part of the body: %v", err)
	}

	if err := pipeWriter.Close(); err != nil {
		t.Fatalf("close body: %v", err)
	}

	got := <-done
	if got.err != nil || got.status != http.StatusCreated {
		t.Fatalf("request in flight = %d %s, error %v; want 201", got.status, got.body, got.err)
	}

	wait()

	if took := time.Since(signaledAt); took < drainTestPeriod {
		t.Fatalf("the server stopped %v after the signal, want after the drain of %v", took, drainTestPeriod)
	}
}
