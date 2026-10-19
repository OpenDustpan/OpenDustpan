package dustpan_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/dustpan"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// errStoreCanary and errBlobCanary are the Ping failures the tests use. A
// probe response must never hold their text.
var (
	errStoreCanary = errors.New("store-canary-must-not-leak")
	errBlobCanary  = errors.New("blob-canary-must-not-leak")
	// errPingBlocked ends a Ping that waits for the context, so the probe's
	// own timeout ends the check.
	errPingBlocked = errors.New("ping blocked until the context ended")
)

// pingStore wraps a SQLite store with a Ping behavior for readiness tests.
// Every other port passes through to the embedded store.
type pingStore struct {
	*sqlite.Store

	ping func(ctx context.Context) error
}

// Ping answers with the test behavior.
func (s pingStore) Ping(ctx context.Context) error {
	return s.ping(ctx)
}

// pingBlobs wraps a blob store with a Ping behavior for readiness tests.
// Every other port passes through to the embedded store.
type pingBlobs struct {
	*fs.Store

	ping func(ctx context.Context) error
}

// Ping answers with the test behavior.
func (b pingBlobs) Ping(ctx context.Context) error {
	return b.ping(ctx)
}

// failPing returns a Ping that fails with err.
func failPing(err error) func(context.Context) error {
	return func(context.Context) error {
		return err
	}
}

// blockPing is a Ping that waits for the context to end, so the probe's own
// timeout ends the check.
func blockPing(ctx context.Context) error {
	<-ctx.Done()

	return errPingBlocked
}

// okPing is a Ping that always answers.
func okPing(context.Context) error {
	return nil
}

// newReadinessServer builds a server on a fresh SQLite database and serves
// it. A nil ping func keeps the real store; otherwise the server pings
// through the wrapper with that behavior.
func newReadinessServer(
	t *testing.T,
	storePing, blobPing func(context.Context) error,
) *httptest.Server {
	t.Helper()

	st, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "dustpan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := st.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	disk, err := fs.Open(filepath.Join(t.TempDir(), "blobs"))
	if err != nil {
		t.Fatalf("open blob store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := disk.Close(); closeErr != nil {
			t.Errorf("close blob store: %v", closeErr)
		}
	})

	authn, err := identity.NewStaticToken("admin", adminToken)
	if err != nil {
		t.Fatalf("authenticator: %v", err)
	}

	srv, err := dustpan.NewServer(
		dustpan.WithRegion(region),
		dustpan.WithNamespaceStore(pingStore{Store: st, ping: orPing(storePing)}),
		dustpan.WithStackStore(st),
		dustpan.WithUpdateStore(st),
		dustpan.WithCheckpointStore(st),
		dustpan.WithEventStore(st),
		dustpan.WithSecretKeyStore(st),
		dustpan.WithSecretWrapper(testWrapper(t)),
		dustpan.WithWorkspaceStore(st),
		dustpan.WithStateStore(st),
		dustpan.WithBlobStore(pingBlobs{Store: disk, ping: orPing(blobPing)}),
		dustpan.WithInventoryReader(st),
		dustpan.WithAuthenticator(authn),
	)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	return ts
}

// orPing returns ping, or a Ping that always answers when ping is nil.
func orPing(ping func(context.Context) error) func(context.Context) error {
	if ping != nil {
		return ping
	}

	return okPing
}

// getProbe sends an unsigned GET to path and returns its status, headers
// and body. The probes stay public, so no token is sent.
func getProbe(t *testing.T, ts *httptest.Server, path string) (int, http.Header, string) {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, ts.URL+path, http.NoBody)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}

	data, err := io.ReadAll(resp.Body)
	if closeErr := resp.Body.Close(); closeErr != nil {
		t.Fatalf("close body: %v", closeErr)
	}

	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	return resp.StatusCode, resp.Header, string(data)
}

// TestHealthLiveStillChecksNothing proves GET /health/live still answers 204
// with no body and no sign-in after the readiness probe lands.
func TestHealthLiveStillChecksNothing(t *testing.T) {
	t.Parallel()

	ts := newReadinessServer(t, nil, nil)

	status, _, body := getProbe(t, ts, "/health/live")
	if status != http.StatusNoContent {
		t.Fatalf("GET /health/live status %d, want %d (body %q)", status, http.StatusNoContent, body)
	}

	if body != "" {
		t.Fatalf("GET /health/live body %q, want empty", body)
	}
}

// TestHealthReadyOK proves GET /health/ready answers 200 with {"status":"ok"}
// and no sign-in when the store and the blob store answer.
func TestHealthReadyOK(t *testing.T) {
	t.Parallel()

	ts := newReadinessServer(t, nil, nil)

	status, header, body := getProbe(t, ts, "/health/ready")
	if status != http.StatusOK {
		t.Fatalf("GET /health/ready status %d, want %d (body %q)", status, http.StatusOK, body)
	}

	if got := header.Get("Content-Type"); got != "application/json" {
		t.Fatalf("media type = %q, want application/json", got)
	}

	want := `{"status":"ok","components":[{"name":"api","status":"ok"},{"name":"worker","status":"ok"}]}`
	if strings.TrimSpace(body) != want {
		t.Fatalf("GET /health/ready body %q, want %q", body, want)
	}
}

// readyFailureCase is one failing-check case of TestHealthReadyFailures.
type readyFailureCase struct {
	name       string
	storePing  func(context.Context) error
	blobPing   func(context.Context) error
	wantBody   string
	wantAbsent []string
}

// TestHealthReadyFailures proves a failing check answers 503 with the names
// of the failed checks and no error text.
func TestHealthReadyFailures(t *testing.T) {
	t.Parallel()

	cases := []readyFailureCase{
		{
			name:       "store fails",
			storePing:  failPing(errStoreCanary),
			blobPing:   nil,
			wantBody:   `{"status":"unavailable","components":[{"name":"api","status":"unavailable","failed":["store"]},{"name":"worker","status":"unavailable","failed":["store"]}]}`,
			wantAbsent: []string{"blob", errStoreCanary.Error(), errBlobCanary.Error()},
		},
		{
			name:       "blob fails",
			storePing:  nil,
			blobPing:   failPing(errBlobCanary),
			wantBody:   `{"status":"unavailable","components":[{"name":"api","status":"unavailable","failed":["blob"]},{"name":"worker","status":"ok"}]}`,
			wantAbsent: []string{"store", errStoreCanary.Error(), errBlobCanary.Error()},
		},
		{
			name:       "both fail",
			storePing:  failPing(errStoreCanary),
			blobPing:   failPing(errBlobCanary),
			wantBody:   `{"status":"unavailable","components":[{"name":"api","status":"unavailable","failed":["store","blob"]},{"name":"worker","status":"unavailable","failed":["store"]}]}`,
			wantAbsent: []string{errStoreCanary.Error(), errBlobCanary.Error()},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkReadyFailure(t, tc)
		})
	}
}

// checkReadyFailure serves one failing-check case and checks its status,
// body and absent text.
func checkReadyFailure(t *testing.T, tc readyFailureCase) {
	t.Helper()

	ts := newReadinessServer(t, tc.storePing, tc.blobPing)

	status, header, body := getProbe(t, ts, "/health/ready")
	if status != http.StatusServiceUnavailable {
		t.Fatalf("GET /health/ready status %d, want %d (body %q)", status, http.StatusServiceUnavailable, body)
	}

	if got := header.Get("Content-Type"); got != "application/json" {
		t.Fatalf("media type = %q, want application/json", got)
	}

	if strings.TrimSpace(body) != tc.wantBody {
		t.Fatalf("GET /health/ready body %q, want %q", body, tc.wantBody)
	}

	for _, absent := range tc.wantAbsent {
		if strings.Contains(body, absent) {
			t.Fatalf("GET /health/ready body %q holds %q", body, absent)
		}
	}
}

// TestHealthReadySlowCheck proves a check that never answers still ends: the
// probe's 2-second limit turns it into a 503 that names the slow check. The
// request must finish well before the test would time out.
func TestHealthReadySlowCheck(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name      string
		storeSlow bool
		wantBody  string
	}{
		{name: "slow store", storeSlow: true, wantBody: `{"status":"unavailable","components":[{"name":"api","status":"unavailable","failed":["store"]},{"name":"worker","status":"unavailable","failed":["store"]}]}`},
		{name: "slow blob", storeSlow: false, wantBody: `{"status":"unavailable","components":[{"name":"api","status":"unavailable","failed":["blob"]},{"name":"worker","status":"ok"}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var storePing, blobPing func(context.Context) error

			if tc.storeSlow {
				storePing = blockPing
			} else {
				blobPing = blockPing
			}

			ts := newReadinessServer(t, storePing, blobPing)

			start := time.Now()
			status, _, body := getProbe(t, ts, "/health/ready")
			elapsed := time.Since(start)

			if status != http.StatusServiceUnavailable {
				t.Fatalf("GET /health/ready status %d, want %d (body %q)", status, http.StatusServiceUnavailable, body)
			}

			if strings.TrimSpace(body) != tc.wantBody {
				t.Fatalf("GET /health/ready body %q, want %q", body, tc.wantBody)
			}

			if elapsed > 30*time.Second {
				t.Fatalf("GET /health/ready took %s, want the 2-second limit to end it", elapsed)
			}
		})
	}
}
