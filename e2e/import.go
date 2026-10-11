//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

// NoteStep records an informational step for the report without running the
// CLI. Detail lands in the report, so a scenario records values it read
// outside the CLI output, such as lineage and serial before and after an
// import.
func (e *Env) NoteStep(tb testing.TB, name, detail string) {
	tb.Helper()

	tb.Logf("%s: %s", name, detail)

	e.steps = append(e.steps, StepResult{
		Name:     name,
		Command:  "note",
		Failure:  "",
		Stderr:   clip(detail),
		ExitCode: 0,
		Passed:   true,
		Skipped:  false,
	})
}

// WorkspaceIDByName returns the ID of the workspace named name in Namespace
// through the TFE API as principal A, failing the test when it is missing.
func (e *Env) WorkspaceIDByName(ctx context.Context, tb testing.TB, name string) string {
	tb.Helper()

	target := "/api/v2/organizations/" + Namespace + "/workspaces/" + name

	status, body, err := e.TFEStatusA(ctx, http.MethodGet, target, "")
	if err != nil {
		tb.Fatalf("get workspace: %v", err)
	}

	if status != http.StatusOK {
		tb.Fatalf("get workspace answers %d: %s", status, body)
	}

	var doc workspaceIDDoc

	if err := json.Unmarshal([]byte(body), &doc); err != nil || doc.Data.ID == "" {
		tb.Fatalf("decode workspace id: %v (%s)", err, body)
	}

	return doc.Data.ID
}
