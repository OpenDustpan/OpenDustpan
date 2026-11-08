package dustpan_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/dustpan"
	"opendustpan.org/opendustpan/pkg/secret"
)

// startRefusalText is the text of every refusal before Start has succeeded.
const startRefusalText = "The server has not started. The program that embeds it must call Start before it serves requests."

// startGateCase is one request to a handler that was not started.
type startGateCase struct {
	name   string
	method string
	path   string
	// want is a text that the 503 body holds, in the error shape of the adapter.
	want string
}

// serveOnce sends one request to handler and answers the recorder.
func serveOnce(t *testing.T, handler http.Handler, method, path string) *httptest.ResponseRecorder {
	t.Helper()

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), method, path, http.NoBody))

	return rec
}

// checkProbePasses proves that GET of one live-probe target answers 204
// before Start.
func checkProbePasses(t *testing.T, handler http.Handler, path string) {
	t.Helper()

	if rec := serveOnce(t, handler, http.MethodGet, path); rec.Code != http.StatusNoContent {
		t.Fatalf("GET %s = %d, want 204 before Start\n%s", path, rec.Code, rec.Body.String())
	}
}

// TestHandlerRefusesBeforeStart proves that a handler that was not started
// answers 503 on each route in the error shape of its adapter, with
// Retry-After, and that only GET of /health/live passes. The readiness
// probe, the metrics route and the console files answer 503 too. A path that
// only looks like the probe does not pass. An absolute-form target, which an
// HTTP/1.1 proxy sends, gets the same answer.
func TestHandlerRefusesBeforeStart(t *testing.T) {
	t.Parallel()

	// The console directory makes the static files reachable, so a request that
	// slips past the gate reaches index.html and the test sees a 200.
	consoleDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(consoleDir, "index.html"), []byte("<!doctype html>"), 0o600); err != nil {
		t.Fatalf("write the console file: %v", err)
	}

	handler := newModuleServer(t, dustpan.WithConsoleDir(consoleDir)).Handler()

	cases := []startGateCase{
		{"Pulumi API", http.MethodGet, "/api/stacks/acme", `"code":503`},
		{"installation API", http.MethodGet, "/api/server/namespaces", `"code":503`},
		{"capabilities", http.MethodGet, "/api/dustpan/v1/capabilities", `"code":503`},
		{"inventory API", http.MethodGet, "/api/inventory/units", `"code":503`},
		{"OpenTofu cloud API", http.MethodGet, "/api/v2/ping", `"errors"`},
		{"OpenTofu discovery", http.MethodGet, "/.well-known/terraform.json", `"errors"`},
		{"OpenTofu http backend", http.MethodGet, "/tofu/acme/ws", startRefusalText},
		{"runner protocol", http.MethodGet, "/runner/v1/claim", `"unavailable"`},
		{"readiness probe", http.MethodGet, "/health/ready", `"code":503`},
		{"readiness of a component", http.MethodGet, "/health/ready/api", `"code":503`},
		{"metrics", http.MethodGet, "/api/server/metrics", `"code":503`},
		{"console file", http.MethodGet, "/", `"code":503`},
		{"a write", http.MethodPost, "/api/server/namespaces", `"code":503`},
		{"probe with a method that is not GET", http.MethodPost, "/health/live", `"code":503`},
		{"probe with a slash", http.MethodGet, "/health/live/", `"code":503`},
		{"probe with a double slash", http.MethodGet, "//health/live", `"code":503`},
		{"probe with a longer path", http.MethodGet, "/health/live/x", `"code":503`},
		{"probe with another case", http.MethodGet, "/HEALTH/LIVE", `"code":503`},
		{"probe with an encoded slash", http.MethodGet, "/health%2Flive", `"code":503`},
		{"probe with a lowercase encoded slash", http.MethodGet, "/health%2flive", `"code":503`},
		{"probe with a double encoded slash", http.MethodGet, "/health%252Flive", `"code":503`},
		{"probe with a dot segment", http.MethodGet, "/./health/live", `"code":503`},
		{"probe with a parameter", http.MethodGet, "/health/live;a=b", `"code":503`},
		{"probe with HEAD", http.MethodHead, "/health/live", `"code":503`},
		{"probe with OPTIONS", http.MethodOptions, "/health/live", `"code":503`},
		{"absolute URI of an API route", http.MethodGet, "http://example.com/api/server/namespaces", `"code":503`},
		{
			"absolute URI of the probe with a method that is not GET",
			http.MethodPost,
			"http://example.com/health/live",
			`"code":503`,
		},
		{
			"absolute URI of the probe with a longer path",
			http.MethodGet,
			"http://example.com/health/live/x",
			`"code":503`,
		},
		{
			"absolute URI of the probe with a trailing slash",
			http.MethodGet,
			"http://example.com/health/live/",
			`"code":503`,
		},
		{
			"absolute URI of the probe with an encoded slash",
			http.MethodGet,
			"http://example.com/health%2Flive",
			`"code":503`,
		},
		{"absolute URI of the probe with another case", http.MethodGet, "http://example.com/HEALTH/LIVE", `"code":503`},
		{
			"absolute URI of an API route with a query",
			http.MethodGet,
			"http://example.com/api/server/namespaces?per_page=1",
			`"code":503`,
		},
		{
			"absolute URI of an API route over https",
			http.MethodGet,
			"https://example.com/api/server/namespaces",
			`"code":503`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec := serveOnce(t, handler, tc.method, tc.path)

			if rec.Code != http.StatusServiceUnavailable {
				t.Fatalf("%s %s = %d, want 503\n%s", tc.method, tc.path, rec.Code, rec.Body.String())
			}

			if rec.Header().Get("Retry-After") == "" {
				t.Fatalf("%s %s: the refusal has no Retry-After", tc.method, tc.path)
			}

			if !strings.Contains(rec.Body.String(), tc.want) ||
				!strings.Contains(rec.Body.String(), "has not started") {
				t.Fatalf(
					"%s %s body = %q, want %q and the text of the refusal",
					tc.method,
					tc.path,
					rec.Body.String(),
					tc.want,
				)
			}
		})
	}

	if rec := serveOnce(t, handler, http.MethodGet, "/health/live"); rec.Code != http.StatusNoContent {
		t.Fatalf("GET /health/live = %d, want 204 before Start", rec.Code)
	}

	// The gate compares the method and the escaped path only, so the scheme,
	// the host and the query of an absolute-form target do not change the
	// answer: each form of the probe passes.
	passCases := []struct {
		name string
		path string
	}{
		{"absolute URI of the probe", "http://example.com/health/live"},
		// The scheme is not part of the escaped path, so the https form passes.
		{"absolute URI of the probe over https", "https://example.com/health/live"},
		// The query is not part of the escaped path, so the probe with a query passes.
		{"absolute URI of the probe with a query", "http://example.com/health/live?wait=1"},
	}

	for _, tc := range passCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			checkProbePasses(t, handler, tc.path)
		})
	}
}

// TestHandlerServesAfterStart proves that the same handler serves after Start
// has succeeded, with no new handler: the gate reads the state on each
// request.
func TestHandlerServesAfterStart(t *testing.T) {
	t.Parallel()

	srv := newEveryModuleServer(t)
	handler := srv.Handler()

	rec := serveOnce(t, handler, http.MethodGet, "/api/dustpan/v1/capabilities")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("capabilities before Start = %d, want 503", rec.Code)
	}

	if err := srv.Start(t.Context()); err != nil {
		t.Fatalf("start: %v", err)
	}

	for _, path := range []string{"/health/live", "/health/ready", "/api/dustpan/v1/capabilities"} {
		if rec := serveOnce(t, handler, http.MethodGet, path); rec.Code >= http.StatusInternalServerError {
			t.Fatalf("GET %s after Start = %d, want a success\n%s", path, rec.Code, rec.Body.String())
		}
	}
}

// countingKeys counts the reads of the wrapper IDs, which the key check of the
// start-up steps makes once.
type countingKeys struct {
	secret.KeyStore

	lists atomic.Int32
}

// ListWrapperIDs counts the call.
func (c *countingKeys) ListWrapperIDs(ctx context.Context) ([]string, error) {
	c.lists.Add(1)

	ids, err := c.KeyStore.ListWrapperIDs(ctx)
	if err != nil {
		return nil, fmt.Errorf("list wrapper IDs: %w", err)
	}

	return ids, nil
}

// TestStartRunsTheStepsOnce proves that a second Start and a Run after a Start
// that succeeded do not run the steps again.
func TestStartRunsTheStepsOnce(t *testing.T) {
	t.Parallel()

	p := newPorts(t)
	keys := &countingKeys{KeyStore: p.st, lists: atomic.Int32{}}

	ln := openTestListener(t)

	opts := append(fullServerOptions(t, p.st, p.blobs, p.authn),
		dustpan.WithSecretKeyStore(keys), dustpan.WithListener(ln))

	srv, err := dustpan.NewServer(opts...)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	if err := srv.Start(t.Context()); err != nil {
		t.Fatalf("start: %v", err)
	}

	if err := srv.Start(t.Context()); err != nil {
		t.Fatalf("second start: %v", err)
	}

	if got := keys.lists.Load(); got != 1 {
		t.Fatalf("the key check ran %d times after two Start calls, want 1", got)
	}

	runCtx, stop := context.WithCancel(t.Context())
	done := make(chan error, 1)

	go func() { done <- srv.Run(runCtx) }()

	waitForReady(t, "http://"+ln.Addr().String()+"/health/ready")
	stop()

	if err := <-done; err != nil {
		t.Fatalf("run after start: %v", err)
	}

	if got := keys.lists.Load(); got != 1 {
		t.Fatalf("the key check ran %d times after Run, want 1", got)
	}
}

// waitForReady polls the readiness probe at url until it answers 200.
func waitForReady(t *testing.T, url string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
		if err != nil {
			t.Fatalf("new request: %v", err)
		}

		if resp, err := http.DefaultClient.Do(req); err == nil {
			code := resp.StatusCode

			if closeErr := resp.Body.Close(); closeErr != nil {
				t.Fatalf("close the body: %v", closeErr)
			}

			if code == http.StatusOK {
				return
			}
		}

		select {
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatalf("%s did not answer 200 in time", url)
		}
	}
}

// TestStartFailsClosed proves that a Start that fails keeps the gate shut and
// runs the steps again at the next call, and that Run stops with the same error.
func TestStartFailsClosed(t *testing.T) {
	t.Parallel()

	srv := newRunCheckServer(t, seedRunCheckStore(t))
	handler := srv.Handler()

	for range 2 {
		err := srv.Start(t.Context())
		if err == nil || !errors.Is(err, secret.ErrWrapperMismatch) {
			t.Fatalf("start with the wrong key = %v, want ErrWrapperMismatch", err)
		}
	}

	rec := serveOnce(t, handler, http.MethodGet, "/api/server/namespaces")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("a route after a failed Start = %d, want 503", rec.Code)
	}

	if err := srv.Run(t.Context()); err == nil || !errors.Is(err, secret.ErrWrapperMismatch) {
		t.Fatalf("run with the wrong key = %v, want ErrWrapperMismatch", err)
	}
}
