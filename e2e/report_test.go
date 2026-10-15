//go:build e2e

package e2e_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

const (
	reportEntriesPerRun = 15000
	reportFailureLines  = 200
	reportSummaryBudget = 900 * 1024
)

func TestWriteSummaryKeepsSummaryBelowLimit(t *testing.T) {
	t.Parallel()

	_ = t.Context()
	dir := t.TempDir()
	writeLargeRuns(t, dir)
	checkSummaryBelowLimit(t, dir)
	checkFullHoldsSections(t, dir)
}

func TestClearReportsRemovesSummaryAndFull(t *testing.T) {
	t.Parallel()

	_ = t.Context()
	dir := t.TempDir()

	for _, name := range []string{"a.json", "summary.md", "full.md"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("old"), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	if err := e2e.ClearReports(dir); err != nil {
		t.Fatalf("clear reports: %v", err)
	}

	for _, name := range []string{"a.json", "summary.md", "full.md"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Errorf("%s still exists after ClearReports", name)
		}
	}
}

func writeLargeRuns(t *testing.T, dir string) {
	t.Helper()

	var stderr strings.Builder
	for range reportFailureLines {
		stderr.WriteString("failure detail line\n")
	}

	runs := []e2e.Run{
		largeRun(e2e.ToolPulumi, e2e.StoreSQLite, "3.250.0", stderr.String()),
		largeRun(e2e.ToolPulumi, e2e.StorePostgres, "3.250.0", stderr.String()),
		largeRun(e2e.ToolTofu, e2e.StoreSQLite, "1.12.7", stderr.String()),
		largeRun(e2e.ToolTofu, e2e.StorePostgres, "1.12.7", stderr.String()),
	}

	for i := range runs {
		if err := e2e.WriteRun(dir, runs[i]); err != nil {
			t.Fatalf("write run: %v", err)
		}
	}

	if err := e2e.WriteSummary(dir); err != nil {
		t.Fatalf("write summary: %v", err)
	}
}

func checkSummaryBelowLimit(t *testing.T, dir string) {
	t.Helper()

	summary, err := os.ReadFile(filepath.Join(dir, "summary.md")) //nolint:gosec // The test reads the report it wrote.
	if err != nil {
		t.Fatalf("read summary.md: %v", err)
	}

	if len(summary) >= reportSummaryBudget {
		t.Fatalf("summary.md is %d bytes, want below %d", len(summary), reportSummaryBudget)
	}

	for _, want := range []string{"## Steps", "## Requests the CLI made", "## Failures:"} {
		if !strings.Contains(string(summary), want) {
			t.Errorf("summary.md misses %q", want)
		}
	}

	if !strings.Contains(string(summary), "full.md") {
		t.Error("summary.md does not point to full.md")
	}

	if !strings.Contains(string(summary), "Cut ") {
		t.Error("summary.md does not say how many failure lines were cut")
	}

	if strings.Contains(string(summary), "| 15000 |") {
		t.Error("summary.md holds the full request log, want only a pointer to full.md")
	}
}

func checkFullHoldsSections(t *testing.T, dir string) {
	t.Helper()

	full, err := os.ReadFile(filepath.Join(dir, "full.md")) //nolint:gosec // The test reads the report it wrote.
	if err != nil {
		t.Fatalf("read full.md: %v", err)
	}

	for _, want := range []string{"## Steps", "## Requests the CLI made", "## Failures:", "## Request log:"} {
		if !strings.Contains(string(full), want) {
			t.Errorf("full.md misses %q", want)
		}
	}

	if !strings.Contains(string(full), "| 15000 |") {
		t.Error("full.md misses the request log rows")
	}

	if !strings.Contains(string(full), "failure detail line") {
		t.Error("full.md misses the full failure text")
	}
}

func largeRun(tool string, store e2e.Store, version, stderr string) e2e.Run {
	steps := []e2e.StepResult{
		{
			Name:     "install",
			Command:  tool + " install",
			Failure:  "",
			Stderr:   "",
			ExitCode: 0,
			Passed:   true,
			Skipped:  false,
		},
		{
			Name:     "deploy",
			Command:  tool + " deploy",
			Failure:  "deploy failed",
			Stderr:   stderr,
			ExitCode: 1,
			Passed:   false,
			Skipped:  false,
		},
	}
	requests := make([]e2e.Entry, 0, reportEntriesPerRun)
	for i := range reportEntriesPerRun {
		requests = append(requests, e2e.Entry{
			Step:          "deploy",
			Method:        "GET",
			Host:          "",
			Path:          "/api/stacks/acme/project/dev",
			Query:         "",
			UserAgent:     "",
			Accept:        "application/json",
			ContentType:   "",
			RequestBody:   "",
			ResponseBody:  "",
			Seq:           i + 1,
			Status:        200,
			RequestBytes:  0,
			ResponseBytes: 0,
		})
	}

	return e2e.Run{
		Tool:           tool,
		Label:          e2e.Latest,
		Store:          store,
		Blobs:          "",
		Version:        version,
		ChecksumSource: "pinned",
		Steps:          steps,
		Requests:       requests,
	}
}
