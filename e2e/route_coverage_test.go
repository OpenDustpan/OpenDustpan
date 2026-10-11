//go:build e2e

package e2e_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

// checkRouteCoverage fails for every served route that no e2e step reached.
// It reads the run reports in dir, keeps only traffic from real CLI binaries
// and compares it with every adapter's route table. A route lands here only
// when no step reached it; console-only and operator routes stay on the
// explicit allowlist with the test that covers them instead.
func checkRouteCoverage(t *testing.T, dir string) {
	t.Helper()

	runs := readCoverageRuns(t, dir)
	if len(runs) == 0 {
		// No run with requests means no evidence: passing here would let a
		// broken report step hide every missing e2e step, so fail instead.
		t.Errorf("no e2e run with requests in %s: route coverage needs CLI traffic", dir)

		return
	}

	var entries []e2e.Entry

	for i := range runs {
		entries = append(entries, runs[i].Requests...)
	}

	served := e2e.ServedRoutes(t)
	scope := coverageScope(served, runs)
	allowed := e2e.CoverageAllowlist()
	uncovered := e2e.UncoveredRoutes(scope, allowed, entries)

	t.Logf("route coverage: %d served, %d in scope, %d allowlisted, %d uncovered",
		len(served), len(scope), len(allowed), len(uncovered))

	for _, route := range uncovered {
		t.Errorf("route %q was reached by no e2e step: add a step to e2e/e2e_test.go or allowlist it", route)
	}
}

// readCoverageRuns returns the run reports in dir. A missing or broken
// report fails the test instead of silently shrinking the coverage.
func readCoverageRuns(t *testing.T, dir string) []e2e.Run {
	t.Helper()

	names, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		t.Errorf("list reports: %v", err)

		return nil
	}

	var runs []e2e.Run

	for _, name := range names {
		data, err := os.ReadFile(name) //nolint:gosec // The test reads its own reports by a fixed pattern.
		if err != nil {
			t.Errorf("read report %s: %v", name, err)

			continue
		}

		var run e2e.Run

		if err := json.Unmarshal(data, &run); err != nil {
			t.Errorf("decode report %s: %v", name, err)

			continue
		}

		if len(run.Requests) > 0 {
			runs = append(runs, run)
		}
	}

	return runs
}

// coverageScope limits the served routes to the adapters the reports
// exercised: a run filtered to one tool must not fail for the other tool's
// routes. Console, installation and probe routes are always in scope, so a
// new one still needs an e2e step or an allowlist entry.
func coverageScope(served []e2e.RouteRef, runs []e2e.Run) []e2e.RouteRef {
	tools := map[string]bool{}

	for i := range runs {
		tools[runs[i].Tool] = true
	}

	var out []e2e.RouteRef

	for _, route := range served {
		if !inCoverageScope(route, tools) {
			continue
		}

		out = append(out, route)
	}

	return out
}

// inCoverageScope reports whether route's adapter ran: the Pulumi CLI table
// needs a Pulumi run, the TFE-compatible table needs an OpenTofu run, and
// every other table is always in scope.
func inCoverageScope(route e2e.RouteRef, tools map[string]bool) bool {
	switch adapterOf(route) {
	case e2e.ToolPulumi:
		return tools[e2e.ToolPulumi]
	case e2e.ToolTofu:
		return tools[e2e.ToolTofu]
	default:
		return true
	}
}

// adapterOf maps a served route to the tool that calls it, or "" for the
// tool-neutral tables that are always in scope.
func adapterOf(route e2e.RouteRef) string {
	switch {
	case strings.HasPrefix(route.Path, "/api/v2/") || strings.HasPrefix(route.Path, "/.well-known/"):
		return e2e.ToolTofu
	case strings.HasPrefix(route.Path, "/api/inventory/") ||
		strings.HasPrefix(route.Path, "/api/server/") ||
		strings.HasPrefix(route.Path, "/health/"):
		return ""
	case strings.HasPrefix(route.Path, "/api/"):
		return e2e.ToolPulumi
	default:
		return ""
	}
}
