//go:build e2e

package e2e_test

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

func TestTruncateClosesFenceBeforeCutNotice(t *testing.T) {
	t.Parallel()

	_ = t.Context()
	dir := t.TempDir()

	var stderr strings.Builder
	for i := range reportFailureLines {
		fmt.Fprintf(&stderr, "fence test line %d\n", i)
	}

	run := largeRun(e2e.ToolPulumi, e2e.StoreSQLite, "3.250.0", "fence-test-marker", stderr.String())
	if err := e2e.WriteRun(dir, run); err != nil {
		t.Fatalf("write run: %v", err)
	}

	if err := e2e.WriteSummary(dir); err != nil {
		t.Fatalf("write summary: %v", err)
	}

	summary, err := os.ReadFile(filepath.Join(dir, "summary.md")) //nolint:gosec // The test reads the report it wrote.
	if err != nil {
		t.Fatalf("read summary.md: %v", err)
	}

	if !strings.Contains(string(summary), "Cut ") {
		t.Fatal("summary.md does not say how many failure lines were cut")
	}

	if !strings.Contains(string(summary), "```\nCut ") {
		t.Error("summary.md does not close the fence before the cut notice")
	}
}

func TestSummaryKeepsTablesWhenCut(t *testing.T) {
	t.Parallel()

	_ = t.Context()
	dir := t.TempDir()
	writeCutRuns(t, dir)

	summary, err := os.ReadFile(filepath.Join(dir, "summary.md")) //nolint:gosec // The test reads the report it wrote.
	if err != nil {
		t.Fatalf("read summary.md: %v", err)
	}

	if len(summary) >= reportSummaryBudget {
		t.Fatalf("summary.md is %d bytes, want below %d", len(summary), reportSummaryBudget)
	}

	if !strings.Contains(string(summary), "full.md") {
		t.Error("summary.md does not point to full.md")
	}

	if !strings.Contains(string(summary), "Truncated") {
		t.Error("summary.md does not say the report was cut")
	}

	for _, want := range []string{"# Pulumi CLI", "# OpenTofu", "## Steps", "## Requests the CLI made"} {
		if !strings.Contains(string(summary), want) {
			t.Errorf("summary.md misses %q after cut", want)
		}
	}

	firstFailure := bytes.Index(summary, []byte("## Failures:"))
	pulumiTables := bytes.Index(summary, []byte("# Pulumi CLI"))
	tofuTables := bytes.Index(summary, []byte("# OpenTofu"))
	if firstFailure < 0 || pulumiTables < 0 || tofuTables < 0 {
		t.Fatal("summary.md misses tables or failures")
	}

	if pulumiTables > firstFailure || tofuTables > firstFailure {
		t.Error("summary.md cuts a table, want tables before failures")
	}
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

	pulumiSQLite := failureMarker(e2e.ToolPulumi, e2e.StoreSQLite, "3.250.0")
	pulumiPostgres := failureMarker(e2e.ToolPulumi, e2e.StorePostgres, "3.250.0")
	tofuSQLite := failureMarker(e2e.ToolTofu, e2e.StoreSQLite, "1.12.7")
	tofuPostgres := failureMarker(e2e.ToolTofu, e2e.StorePostgres, "1.12.7")

	runs := []e2e.Run{
		largeRun(e2e.ToolPulumi, e2e.StoreSQLite, "3.250.0", pulumiSQLite, failureStderr(pulumiSQLite)),
		largeRun(e2e.ToolPulumi, e2e.StorePostgres, "3.250.0", pulumiPostgres, failureStderr(pulumiPostgres)),
		largeRun(e2e.ToolTofu, e2e.StoreSQLite, "1.12.7", tofuSQLite, failureStderr(tofuSQLite)),
		largeRun(e2e.ToolTofu, e2e.StorePostgres, "1.12.7", tofuPostgres, failureStderr(tofuPostgres)),
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

func writeCutRuns(t *testing.T, dir string) {
	t.Helper()

	pulumiMarker := "cut-marker-pulumi-sqlite-3-250-0"
	tofuMarker := "cut-marker-tofu-sqlite-1-12-7"
	runs := []e2e.Run{
		cutRun(e2e.ToolPulumi, e2e.StoreSQLite, "3.250.0", pulumiMarker),
		cutRun(e2e.ToolTofu, e2e.StoreSQLite, "1.12.7", tofuMarker),
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

func failureMarker(tool string, store e2e.Store, version string) string {
	return "marker-" + tool + "-" + string(store) + "-" + version
}

func failureStderr(marker string) string {
	var stderr strings.Builder
	for i := range reportFailureLines {
		fmt.Fprintf(&stderr, "%s failure detail line %d\n", marker, i)
	}

	return stderr.String()
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

	for _, marker := range []string{
		failureMarker(e2e.ToolPulumi, e2e.StoreSQLite, "3.250.0"),
		failureMarker(e2e.ToolPulumi, e2e.StorePostgres, "3.250.0"),
		failureMarker(e2e.ToolTofu, e2e.StoreSQLite, "1.12.7"),
		failureMarker(e2e.ToolTofu, e2e.StorePostgres, "1.12.7"),
	} {
		if !strings.Contains(string(full), marker) {
			t.Errorf("full.md misses failure text with %q", marker)
		}
	}
}

func cutRun(tool string, store e2e.Store, version, marker string) e2e.Run {
	pad := strings.Repeat("x", 20000)

	var stderr strings.Builder
	for i := range reportFailureLines {
		fmt.Fprintf(&stderr, "%s padded failure line %d %s\n", marker, i, pad)
	}

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
			Failure:  marker + " deploy failed",
			Stderr:   stderr.String(),
			ExitCode: 1,
			Passed:   false,
			Skipped:  false,
		},
	}

	return e2e.Run{
		Tool:           tool,
		Label:          e2e.Latest,
		Store:          store,
		Blobs:          "",
		Version:        version,
		ChecksumSource: "pinned",
		Steps:          steps,
		Requests:       nil,
	}
}

func largeRun(tool string, store e2e.Store, version, marker, stderr string) e2e.Run {
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
			Failure:  marker + " deploy failed",
			Stderr:   stderr,
			ExitCode: 1,
			Passed:   false,
			Skipped:  false,
		},
	}
	requests := make([]e2e.Entry, 0, reportEntriesPerRun)
	for i := range reportEntriesPerRun {
		requests = append(requests, e2e.Entry{
			Step:            "deploy",
			At:              time.Time{},
			Method:          "GET",
			Host:            "",
			Path:            "/api/stacks/acme/project/dev",
			Query:           "",
			UserAgent:       "",
			Backend:         "",
			Accept:          "application/json",
			ContentType:     "",
			RequestBody:     "",
			ResponseBody:    "",
			ResponseHeaders: nil,
			ResponseCut:     false,
			Seq:             i + 1,
			Status:          200,
			RequestBytes:    0,
			ResponseBytes:   0,
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
