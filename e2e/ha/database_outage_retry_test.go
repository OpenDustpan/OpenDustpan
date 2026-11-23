package ha_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/e2e/ha"
)

// TestDatabaseOutageRetryReport proves that a write to the state version route
// with an access token feeds a per-version retry report and the short window.
func TestDatabaseOutageRetryReport(t *testing.T) {
	t.Parallel()

	seen := make(chan string, 1)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "5")
		w.WriteHeader(http.StatusServiceUnavailable)

		seen <- fmt.Sprintf("%s %s %s", r.Method, r.URL.Path, r.Header.Get("Authorization"))
	}))
	t.Cleanup(server.Close)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost,
		server.URL+"/api/v2/state-versions", strings.NewReader(`{"data":{}}`))
	if err != nil {
		t.Fatalf("build the state version write: %v", err)
	}

	req.Header.Set("Authorization", "Bearer access-token")

	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatalf("write a state version: %v", err)
	}

	if err := resp.Body.Close(); err != nil {
		t.Fatalf("close the response: %v", err)
	}

	if resp.StatusCode != http.StatusServiceUnavailable || resp.Header.Get("Retry-After") != "5" {
		t.Fatalf("state version write answers %d with Retry-After %q, want 503 and 5",
			resp.StatusCode, resp.Header.Get("Retry-After"))
	}

	if got, want := <-seen, "POST /api/v2/state-versions Bearer access-token"; got != want {
		t.Fatalf("the server saw %q, want %q", got, want)
	}

	matrix := []ha.CLIVersion{
		{Tool: "pulumi", Version: "3.262.0"},
		{Tool: "tofu", Version: "1.12.12"},
	}
	report := ha.NewRetryReport(matrix)
	base := time.Date(2026, time.November, 23, 12, 0, 0, 0, time.UTC)

	attempts := []ha.WriteAttempt{
		{
			Version: "pulumi 3.262.0",
			Method:  http.MethodPost,
			Path:    "/api/v2/state-versions",
			Status:  http.StatusServiceUnavailable,
			At:      base,
		},
		{
			Version: "pulumi 3.262.0",
			Method:  http.MethodPost,
			Path:    "/api/v2/state-versions",
			Status:  http.StatusServiceUnavailable,
			At:      base.Add(8 * time.Second),
		},
		{
			Version: "tofu 1.12.12",
			Method:  http.MethodPost,
			Path:    "/api/v2/state-versions",
			Status:  http.StatusServiceUnavailable,
			At:      base,
		},
		{
			Version: "tofu 1.12.12",
			Method:  http.MethodPost,
			Path:    "/api/v2/state-versions",
			Status:  http.StatusServiceUnavailable,
			At:      base.Add(6 * time.Second),
		},
	}

	for _, attempt := range attempts {
		if err := report.Add(attempt); err != nil {
			t.Fatalf("record %s: %v", attempt.Version, err)
		}
	}

	if got := report.Shortest(); got != 6*time.Second {
		t.Errorf("smallest retry window is %s, want 6s", got)
	}

	if got, want := report.String(), "pulumi 3.262.0: 8s, tofu 1.12.12: 6s"; got != want {
		t.Errorf("retry report is %q, want %q", got, want)
	}
}

// TestDatabaseOutageRetryReportOneAttempt proves that a version with one
// refused attempt has a window of zero, and that the zero is the smallest
// window: the short window then fails instead of using another version.
func TestDatabaseOutageRetryReportOneAttempt(t *testing.T) {
	t.Parallel()

	matrix := []ha.CLIVersion{
		{Tool: "pulumi", Version: "3.262.0"},
		{Tool: "tofu", Version: "1.12.12"},
	}
	report := ha.NewRetryReport(matrix)
	base := time.Date(2026, time.November, 23, 12, 0, 0, 0, time.UTC)

	attempts := []ha.WriteAttempt{
		{
			Version: "pulumi 3.262.0",
			Method:  http.MethodPost,
			Path:    "/api/v2/state-versions",
			Status:  http.StatusServiceUnavailable,
			At:      base,
		},
		{
			Version: "pulumi 3.262.0",
			Method:  http.MethodPost,
			Path:    "/api/v2/state-versions",
			Status:  http.StatusServiceUnavailable,
			At:      base.Add(8 * time.Second),
		},
		{
			Version: "tofu 1.12.12",
			Method:  http.MethodPost,
			Path:    "/api/v2/state-versions",
			Status:  http.StatusServiceUnavailable,
			At:      base,
		},
	}

	for _, attempt := range attempts {
		if err := report.Add(attempt); err != nil {
			t.Fatalf("record %s: %v", attempt.Version, err)
		}
	}

	if got := report.Shortest(); got != 0 {
		t.Errorf("smallest retry window is %s, want 0s for a version with one attempt", got)
	}
}

// TestDatabaseOutageRetryReportNotMeasured proves that the report names a
// matrix version with no attempt as not measured, not as a window of 0s.
func TestDatabaseOutageRetryReportNotMeasured(t *testing.T) {
	t.Parallel()

	matrix := []ha.CLIVersion{
		{Tool: "pulumi", Version: "3.262.0"},
		{Tool: "pulumi", Version: "3.200.0"},
	}
	report := ha.NewRetryReport(matrix)
	base := time.Date(2026, time.November, 23, 12, 0, 0, 0, time.UTC)

	for _, at := range []time.Time{base, base.Add(8 * time.Second)} {
		attempt := ha.WriteAttempt{
			Version: "pulumi 3.262.0",
			Method:  http.MethodPost,
			Path:    "/api/stacks/acme/web/dev/update",
			Status:  http.StatusServiceUnavailable,
			At:      at,
		}

		if err := report.Add(attempt); err != nil {
			t.Fatalf("record %s: %v", attempt.Version, err)
		}
	}

	if got, want := report.String(), "pulumi 3.262.0: 8s, pulumi 3.200.0: not measured"; got != want {
		t.Errorf("retry report is %q, want %q", got, want)
	}
}
