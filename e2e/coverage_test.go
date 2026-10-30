//go:build e2e

package e2e_test

import (
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/e2e"
)

// TestCoverageAllowlistEntriesAreServed proves every allowlist entry names a
// served route: a renamed or removed route must update the allowlist with it,
// so the list cannot go stale.
func TestCoverageAllowlistEntriesAreServed(t *testing.T) {
	t.Parallel()

	served := map[e2e.RouteRef]bool{}
	for _, route := range e2e.ServedRoutes(t) {
		served[route] = true
	}

	for _, allow := range e2e.CoverageAllowlist() {
		ref := e2e.RouteRef{Method: allow.Method, Path: allow.Path}

		if !served[ref] {
			t.Errorf("allowlist entry %q names no served route", ref)
		}

		if allow.CoveringTest == "" || allow.Reason == "" {
			t.Errorf("allowlist entry %q needs a covering test and a reason", ref)
		}
	}
}

// cliEntry builds a recorded request from a real CLI binary. Only the
// routing fields vary; the rest stay empty because matching never reads
// them.
func cliEntry(method, path, userAgent string, status int) e2e.Entry {
	return e2e.Entry{
		Step:            "",
		At:            time.Time{},
		Method:          method,
		Path:            path,
		Host:            "",
		Query:           "",
		UserAgent:       userAgent,
		Backend:         "",
		Accept:          "",
		ContentType:     "",
		RequestBody:     "",
		ResponseBody:    "",
		ResponseHeaders: nil,
		ResponseCut:     false,
		Seq:             0,
		Status:          status,
		RequestBytes:    0,
		ResponseBytes:   0,
	}
}

// TestUncoveredRoutesFindsMissing proves the coverage check fails for a
// served route with no CLI-made request, excuses allowlisted routes, and
// ignores harness requests and probes of unserved paths.
func TestUncoveredRoutesFindsMissing(t *testing.T) {
	t.Parallel()

	served := []e2e.RouteRef{
		{Method: "GET", Path: "/api/v2/workspaces/{id}/current-state-version"},
		{Method: "GET", Path: "/api/v2/workspaces/{id}"},
		{Method: "GET", Path: "/health/ready"},
	}
	allowed := []e2e.AllowEntry{
		{Method: "GET", Path: "/api/v2/workspaces/{id}", CoveringTest: "TestReadWorkspaceByID", Reason: "console-only"},
		{Method: "GET", Path: "/health/ready", CoveringTest: "TestHealthReadyOK", Reason: "operator probe"},
	}

	entries := []e2e.Entry{
		cliEntry("GET", "/api/v2/workspaces/ws-abc123/current-state-version", "OpenTofu/1.12.7", 200),
		cliEntry("GET", "/api/v2/workspaces/ws-abc123", "Go-http-client/1.1", 200),
		cliEntry("PATCH", "/api/v2/workspaces/ws-abc123", "OpenTofu/1.12.7", 405),
	}

	uncovered := e2e.UncoveredRoutes(served, allowed, entries)
	if len(uncovered) != 0 {
		t.Fatalf("uncovered = %v, want none", uncovered)
	}

	cliOnly := []e2e.Entry{
		cliEntry("PATCH", "/api/v2/workspaces/ws-abc123", "OpenTofu/1.12.7", 405),
	}

	uncovered = e2e.UncoveredRoutes(served[:1], nil, cliOnly)
	if len(uncovered) != 1 || uncovered[0].Path != "/api/v2/workspaces/{id}/current-state-version" {
		t.Fatalf("uncovered = %v, want the current-state-version route", uncovered)
	}
}

// TestUncoveredRoutesMatchPlaceholders proves one placeholder segment holds
// any single path segment, including IDs and capability tokens.
func TestUncoveredRoutesMatchPlaceholders(t *testing.T) {
	t.Parallel()

	served := []e2e.RouteRef{
		{Method: "PUT", Path: "/api/v2/state-uploads/{id}/{token}"},
		{Method: "GET", Path: "/api/stacks/{namespace}/{project}/{stack}"},
	}
	entries := []e2e.Entry{
		cliEntry(
			"PUT",
			"/api/v2/state-uploads/sv-abc123/ZzU8Y0cynv12VNxa69mKrMiiR0A-uX_u-abc",
			"OpenTofu/1.12.7",
			200,
		),
		cliEntry("GET", "/api/stacks/acme/e2e-project/dev", "pulumi-cli/1 (v3.250.0; linux; cmd=stack)", 200),
	}

	if uncovered := e2e.UncoveredRoutes(served, nil, entries); len(uncovered) != 0 {
		t.Fatalf("uncovered = %v, want none", uncovered)
	}
}

// TestUncoveredRoutesRejectsPartialSegments proves a pattern never holds a
// path with more or fewer segments.
func TestUncoveredRoutesRejectsPartialSegments(t *testing.T) {
	t.Parallel()

	served := []e2e.RouteRef{
		{Method: "GET", Path: "/api/v2/state-versions/{state_version_id}"},
	}
	entries := []e2e.Entry{
		cliEntry("GET", "/api/v2/state-versions/sv-abc123/download", "OpenTofu/1.12.7", 200),
	}

	uncovered := e2e.UncoveredRoutes(served, nil, entries)
	if len(uncovered) != 1 || !strings.Contains(uncovered[0].Path, "{state_version_id}") {
		t.Fatalf("uncovered = %v, want the state version route", uncovered)
	}
}

// TestServedRoutesIncludeHTTPBackend proves the served routes hold every
// route of the OpenTofu http backend, so the coverage check sees a new
// http backend route as it sees a route of any other adapter.
func TestServedRoutesIncludeHTTPBackend(t *testing.T) {
	t.Parallel()

	served := map[e2e.RouteRef]bool{}
	for _, route := range e2e.ServedRoutes(t) {
		served[route] = true
	}

	for _, method := range []string{"GET", "POST", "LOCK", "UNLOCK"} {
		ref := e2e.RouteRef{Method: method, Path: "/tofu/state/{namespace}/{workspace}"}
		if !served[ref] {
			t.Errorf("served routes miss %q", ref)
		}
	}
}
