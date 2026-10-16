package dustpan_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/dustpan"
)

// TestServerAnswers431PastTheHeaderCap proves that request headers above 64
// KiB never reach a handler: the server answers 431, so a client cannot grow
// an audit entry past the header cap. A small request on the same route
// still answers 200, so the cap only stops oversized headers.
func TestServerAnswers431PastTheHeaderCap(t *testing.T) {
	t.Parallel()

	st := openAuditStore(t)
	ln := openTestListener(t)

	srv, err := dustpan.NewServer(append(auditSinkServerOptions(t, st), dustpan.WithListener(ln))...)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	done := runServer(ctx, srv)
	waitForTCP(t, ln.Addr().String())

	client := &http.Client{Timeout: 10 * time.Second}
	url := "http://" + ln.Addr().String() + "/health/ready"

	small, err := client.Get(url) //nolint:gosec,noctx // A test request to the test server.
	if err != nil {
		t.Fatalf("small request: %v", err)
	}

	_, _ = io.Copy(io.Discard, small.Body)
	_ = small.Body.Close()

	if small.StatusCode != http.StatusOK {
		t.Fatalf("small request status: got %d, want %d", small.StatusCode, http.StatusOK)
	}

	big, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	big.Header.Set("X-Padding", strings.Repeat("p", 70<<10))

	// The oversized request uses a fresh connection: the server sizes its
	// header buffer from the cap when it first reads a connection.
	fresh := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}}

	resp, err := fresh.Do(big)
	if err != nil {
		t.Fatalf("oversized request: %v", err)
	}

	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()

	if resp.StatusCode != http.StatusRequestHeaderFieldsTooLarge {
		t.Fatalf("oversized request status: got %d, want %d", resp.StatusCode, http.StatusRequestHeaderFieldsTooLarge)
	}

	cancel()

	if runErr := <-done; runErr != nil {
		t.Fatalf("run: %v", runErr)
	}
}
