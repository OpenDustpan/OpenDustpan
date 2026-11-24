package ha_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/e2e/ha"
)

const (
	retryCheckpoint   = "/api/stacks/acme/web/dev/update/u1/checkpoint"
	retryEvents       = "/api/stacks/acme/web/dev/update/u1/events/batch"
	retryStateVersion = "/api/v2/workspaces/ws-1/state-versions"
)

// TestDatabaseOutageRetryReport proves that a write to the state version route
// with an access token feeds a retry report for each version and write kind,
// and the short window.
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
			Method:  http.MethodPatch,
			Path:    retryCheckpoint,
			Status:  http.StatusServiceUnavailable,
			Round:   0,
			At:      base,
		},
		{
			Version: "pulumi 3.262.0",
			Method:  http.MethodPatch,
			Path:    retryCheckpoint,
			Status:  http.StatusServiceUnavailable,
			Round:   0,
			At:      base.Add(8 * time.Second),
		},
		{
			Version: "tofu 1.12.12",
			Method:  http.MethodPost,
			Path:    retryStateVersion,
			Status:  http.StatusServiceUnavailable,
			Round:   0,
			At:      base,
		},
		{
			Version: "tofu 1.12.12",
			Method:  http.MethodPost,
			Path:    retryStateVersion,
			Status:  http.StatusServiceUnavailable,
			Round:   0,
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

	if got, want := report.String(), "pulumi 3.262.0 checkpoint: 8s, tofu 1.12.12 state version: 6s"; got != want {
		t.Errorf("retry report is %q, want %q", got, want)
	}
}

// TestDatabaseOutageRetryReportOneAttempt proves that a write kind with one
// refused attempt is not retried: its window is 0 s, the report names it, and
// the smallest window above 0 s skips it.
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
			Method:  http.MethodPatch,
			Path:    retryCheckpoint,
			Status:  http.StatusServiceUnavailable,
			Round:   0,
			At:      base,
		},
		{
			Version: "pulumi 3.262.0",
			Method:  http.MethodPost,
			Path:    retryEvents,
			Status:  http.StatusServiceUnavailable,
			Round:   0,
			At:      base,
		},
		{
			Version: "pulumi 3.262.0",
			Method:  http.MethodPost,
			Path:    retryEvents,
			Status:  http.StatusServiceUnavailable,
			Round:   0,
			At:      base.Add(28 * time.Second),
		},
		{
			Version: "tofu 1.12.12",
			Method:  http.MethodPost,
			Path:    retryStateVersion,
			Status:  http.StatusServiceUnavailable,
			Round:   0,
			At:      base,
		},
	}

	for _, attempt := range attempts {
		if err := report.Add(attempt); err != nil {
			t.Fatalf("record %s: %v", attempt.Version, err)
		}
	}

	if got := report.Shortest(); got != 28*time.Second {
		t.Errorf("smallest retry window above 0s is %s, want 28s", got)
	}

	want := "pulumi 3.262.0 checkpoint: 0s, pulumi 3.262.0 events: 28s, tofu 1.12.12 state version: 0s"
	if got := report.String(); got != want {
		t.Errorf("retry report is %q, want %q", got, want)
	}

	if got, want := report.NotRetried("pulumi 3.262.0"), []ha.WriteKind{ha.WriteCheckpoint}; !slices.Equal(got, want) {
		t.Errorf("write kinds of pulumi 3.262.0 with no retry are %v, want %v", got, want)
	}

	if got, want := report.NotRetried("tofu 1.12.12"), []ha.WriteKind{ha.WriteStateVersion}; !slices.Equal(got, want) {
		t.Errorf("write kinds of tofu 1.12.12 with no retry are %v, want %v", got, want)
	}
}

// TestDatabaseOutageRetryReportOnlyZero proves that a report with only windows
// of 0 s has no smallest window above 0 s.
func TestDatabaseOutageRetryReportOnlyZero(t *testing.T) {
	t.Parallel()

	report := ha.NewRetryReport([]ha.CLIVersion{{Tool: "pulumi", Version: "3.262.0"}})

	attempt := ha.WriteAttempt{
		Version: "pulumi 3.262.0",
		Method:  http.MethodPatch,
		Path:    retryCheckpoint,
		Status:  http.StatusServiceUnavailable,
		Round:   0,
		At:      time.Date(2026, time.November, 23, 12, 0, 0, 0, time.UTC),
	}

	if err := report.Add(attempt); err != nil {
		t.Fatalf("record %s: %v", attempt.Version, err)
	}

	if got := report.Shortest(); got != 0 {
		t.Errorf("smallest retry window above 0s is %s, want 0 (none)", got)
	}
}

// TestDatabaseOutageRetryReportTwoKinds proves that each write kind of a
// version has its own window, from its earliest to its latest attempt, also
// when the attempts arrive out of order, and that the checkpoint routes are
// one kind.
func TestDatabaseOutageRetryReportTwoKinds(t *testing.T) {
	t.Parallel()

	matrix := []ha.CLIVersion{{Tool: "pulumi", Version: "3.262.0"}}
	report := ha.NewRetryReport(matrix)
	base := time.Date(2026, time.November, 23, 12, 0, 0, 0, time.UTC)

	attempts := []struct {
		method string
		path   string
		at     time.Time
	}{
		{http.MethodPost, retryEvents, base.Add(2 * time.Second)},
		{http.MethodPatch, retryCheckpoint, base.Add(5 * time.Second)},
		{http.MethodPatch, "/api/stacks/acme/web/dev/update/u1/checkpointdelta", base.Add(9 * time.Second)},
		{http.MethodPost, retryEvents, base},
	}

	for _, a := range attempts {
		attempt := ha.WriteAttempt{
			Version: "pulumi 3.262.0",
			Method:  a.method,
			Path:    a.path,
			Status:  http.StatusServiceUnavailable,
			Round:   0,
			At:      a.at,
		}

		if err := report.Add(attempt); err != nil {
			t.Fatalf("record %s %s: %v", a.method, a.path, err)
		}
	}

	if got := report.Shortest(); got != 2*time.Second {
		t.Errorf("smallest retry window is %s, want 2s", got)
	}

	if got, want := report.String(), "pulumi 3.262.0 checkpoint: 4s, pulumi 3.262.0 events: 2s"; got != want {
		t.Errorf("retry report is %q, want %q", got, want)
	}
}

// TestDatabaseOutageRetryReportPerRound proves that a window never spans two
// rounds: a version that retries in two outages gets the window of each
// outage, and the report keeps the smallest of them.
func TestDatabaseOutageRetryReportPerRound(t *testing.T) {
	t.Parallel()

	matrix := []ha.CLIVersion{{Tool: "pulumi", Version: "3.262.0"}}
	report := ha.NewRetryReport(matrix)
	base := time.Date(2026, time.November, 23, 12, 0, 0, 0, time.UTC)

	attempts := []struct {
		round int
		at    time.Time
	}{
		{0, base},
		{0, base.Add(8 * time.Second)},
		{1, base.Add(100 * time.Second)},
		{1, base.Add(106 * time.Second)},
	}

	for _, a := range attempts {
		attempt := ha.WriteAttempt{
			Version: "pulumi 3.262.0",
			Method:  http.MethodPost,
			Path:    retryEvents,
			Status:  http.StatusServiceUnavailable,
			Round:   a.round,
			At:      a.at,
		}

		if err := report.Add(attempt); err != nil {
			t.Fatalf("record round %d: %v", a.round, err)
		}
	}

	if got := report.Shortest(); got != 6*time.Second {
		t.Errorf("smallest retry window is %s, want 6s (round 1), not a window across rounds", got)
	}

	if got, want := report.String(), "pulumi 3.262.0 events: 6s"; got != want {
		t.Errorf("retry report is %q, want %q", got, want)
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
			Path:    retryEvents,
			Status:  http.StatusServiceUnavailable,
			Round:   0,
			At:      at,
		}

		if err := report.Add(attempt); err != nil {
			t.Fatalf("record %s: %v", attempt.Version, err)
		}
	}

	if got, want := report.String(), "pulumi 3.262.0 events: 8s, pulumi 3.200.0: not measured"; got != want {
		t.Errorf("retry report is %q, want %q", got, want)
	}

	if got, want := report.Unmeasured(), []string{"pulumi 3.200.0"}; !slices.Equal(got, want) {
		t.Errorf("unmeasured versions are %v, want %v", got, want)
	}

	if got := report.NotRetried("pulumi 3.200.0"); len(got) != 0 {
		t.Errorf("a version with no attempt has write kinds with no retry: %v", got)
	}
}

// TestDatabaseOutageWriteKindOf proves which requests are a write kind of the
// retry report. Any other request is not a write kind, and the report refuses
// it.
func TestDatabaseOutageWriteKindOf(t *testing.T) {
	t.Parallel()

	update := "/api/stacks/acme/web/dev/update/u1"

	tests := []struct {
		name   string
		method string
		path   string
		want   ha.WriteKind
		found  bool
	}{
		{"checkpoint", http.MethodPatch, update + "/checkpoint", ha.WriteCheckpoint, true},
		{"checkpoint verbatim", http.MethodPatch, update + "/checkpointverbatim", ha.WriteCheckpoint, true},
		{"checkpoint delta", http.MethodPatch, update + "/checkpointdelta", ha.WriteCheckpoint, true},
		{"journal entries", http.MethodPatch, update + "/journalentries", ha.WriteCheckpoint, true},
		{"events", http.MethodPost, update + "/events/batch", ha.WriteEvents, true},
		{"complete", http.MethodPost, update + "/complete", ha.WriteComplete, true},
		{"state version", http.MethodPost, retryStateVersion, ha.WriteStateVersion, true},
		{"checkpoint with POST", http.MethodPost, update + "/checkpoint", "", false},
		{"complete with PATCH", http.MethodPatch, update + "/complete", "", false},
		{"state version read", http.MethodGet, retryStateVersion, "", false},
		{"renew lease", http.MethodPost, update + "/renew_lease", "", false},
		{"cancel", http.MethodPost, update + "/cancel", "", false},
		{"start update", http.MethodPost, update, "", false},
		{"create update", http.MethodPost, "/api/stacks/acme/web/dev/update", "", false},
		{"other stacks route", http.MethodPost, "/api/stacks/acme/web/dev/other/u1/complete", "", false},
		{"state version list route", http.MethodPost, "/api/v2/state-versions", "", false},
		{"workspace route", http.MethodPost, "/api/v2/workspaces/ws-1/actions/lock", "", false},
		{"empty workspace", http.MethodPost, "/api/v2/workspaces//state-versions", "", false},
		{"empty update", http.MethodPost, "/api/stacks/acme/web/dev/update//complete", "", false},
		{"empty", http.MethodPost, "", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, found := ha.WriteKindOf(tt.method, tt.path)
			if got != tt.want || found != tt.found {
				t.Errorf("WriteKindOf(%s %s) = %q, %t, want %q, %t",
					tt.method, tt.path, got, found, tt.want, tt.found)
			}

			if tt.found {
				return
			}

			report := ha.NewRetryReport([]ha.CLIVersion{{Tool: "pulumi", Version: "3.262.0"}})
			attempt := ha.WriteAttempt{
				Version: "pulumi 3.262.0",
				Method:  tt.method,
				Path:    tt.path,
				Status:  http.StatusServiceUnavailable,
				Round:   0,
				At:      time.Date(2026, time.November, 23, 12, 0, 0, 0, time.UTC),
			}

			if err := report.Add(attempt); err == nil {
				t.Errorf("Add accepted %s %s, which is no write kind", tt.method, tt.path)
			}
		})
	}
}
