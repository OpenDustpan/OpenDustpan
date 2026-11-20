package dustpan_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/store/sqlite"
	"opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/dustpan"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/maintenance"
)

const webhookHook = "0123456789abcdef0123456789abcdef"

// gitHostMode says whether the server of a test has the Git host store.
type gitHostMode int

const (
	withoutGitHostStore gitHostMode = iota
	withGitHostStore
)

// webhookServer is a server over a fresh SQLite database, with or without the
// Git host store.
func webhookServer(t *testing.T, mode gitHostMode) (*sqlite.Store, *httptest.Server) {
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

	blobs, err := fs.Open(filepath.Join(t.TempDir(), "blobs"))
	if err != nil {
		t.Fatalf("open blob store: %v", err)
	}

	authn, err := identity.NewStaticToken("admin", adminToken)
	if err != nil {
		t.Fatalf("authenticator: %v", err)
	}

	opts := append(fullServerOptions(t, st, blobs, authn), dustpan.WithAllowedOrigins(origin))
	if mode == withGitHostStore {
		opts = append(opts, dustpan.WithGitHostStore(st.GitHost()))
	}

	srv, err := dustpan.NewServer(opts...)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	ts := httptest.NewServer(startedHandler(t, srv))
	t.Cleanup(ts.Close)

	return st, ts
}

// webhookPost sends a delivery with a signature that matches nothing.
func webhookPost(t *testing.T, ts *httptest.Server, path string) (int, http.Header, string) {
	t.Helper()

	req, err := http.NewRequestWithContext(
		t.Context(), http.MethodPost, ts.URL+path, strings.NewReader(`{"zen":"x"}`),
	)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	req.Header.Set("X-Hub-Signature-256", "sha256="+strings.Repeat("0", 64))
	req.Header.Set("X-Github-Event", "ping")
	req.Header.Set("X-Github-Delivery", "11111111-2222-3333-4444-555555555555")

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}

	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			t.Errorf("close body: %v", closeErr)
		}
	}()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	return resp.StatusCode, resp.Header, string(body)
}

// TestWebhookRouteIsMountedWithAGitHostStore proves that a server with a Git
// host store answers a delivery with 204 and no body, whatever the hook ID, and
// that a server with no store serves no such route.
func TestWebhookRouteIsMountedWithAGitHostStore(t *testing.T) {
	t.Parallel()

	_, with := webhookServer(t, withGitHostStore)

	for _, hook := range []string{webhookHook, strings.Repeat("f", 32), "short"} {
		status, _, body := webhookPost(t, with, "/webhooks/"+hook)
		if status != http.StatusNoContent || body != "" {
			t.Fatalf("POST /webhooks/%s = %d %q, want 204 and no body", hook, status, body)
		}
	}

	_, without := webhookServer(t, withoutGitHostStore)

	if status, _, _ := webhookPost(t, without, "/webhooks/"+webhookHook); status == http.StatusNoContent {
		t.Fatalf("a server with no Git host store answered the delivery with %d", status)
	}
}

// TestWebhookWaitsInMaintenanceMode proves that the intake writes nothing in
// maintenance mode: it answers 503 with Retry-After and no body, so the sender
// can deliver again, and it serves again after the installation leaves
// maintenance mode.
func TestWebhookWaitsInMaintenanceMode(t *testing.T) {
	t.Parallel()

	st, ts := webhookServer(t, withGitHostStore)

	if err := st.SetState(t.Context(), maintenance.StateMaintenance); err != nil {
		t.Fatalf("enter maintenance mode: %v", err)
	}

	waitForWebhookStatus(t, ts, http.StatusServiceUnavailable, func(header http.Header, body string) {
		if header.Get("Retry-After") == "" || body != "" {
			t.Fatalf("maintenance refusal: Retry-After %q, body %q; want a wait and no body",
				header.Get("Retry-After"), body)
		}
	})

	if err := st.SetState(t.Context(), maintenance.StateNormal); err != nil {
		t.Fatalf("leave maintenance mode: %v", err)
	}

	waitForWebhookStatus(t, ts, http.StatusNoContent, func(http.Header, string) {})
}

// waitForWebhookStatus polls the intake until it answers want. The watcher
// caches the state for a short time. check runs on the answer that matched.
func waitForWebhookStatus(t *testing.T, ts *httptest.Server, want int, check func(http.Header, string)) {
	t.Helper()

	// 600 tries of 50 ms are 30 seconds.
	for range 600 {
		status, header, body := webhookPost(t, ts, "/webhooks/"+webhookHook)
		if status == want {
			check(header, body)

			return
		}

		timer := time.NewTimer(50 * time.Millisecond)
		select {
		case <-timer.C:
		case <-t.Context().Done():
			timer.Stop()
			t.Fatalf("test ended while waiting for the status %d", want)
		}
	}

	t.Fatalf("the intake never answered %d", want)
}
