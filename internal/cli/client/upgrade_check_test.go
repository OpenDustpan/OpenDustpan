package client_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/cli/client"
	"opendustpan.org/opendustpan/pkg/store/schema"
)

// serveUpgradeCheck answers one upgrade check document with the given
// journaled count and the embedded migrations, so the verdict stays up to
// date and the report shows only the journaled line under test.
func serveUpgradeCheck(t *testing.T, journaled int64) *httptest.Server {
	t.Helper()

	embedded, binary := schema.EmbeddedMigrationNames()

	return serveApplied(t, journaled, binary, embedded)
}

// serveApplied answers one upgrade check document with the given journaled
// count, schema version and applied migration names.
func serveApplied(t *testing.T, journaled, version int64, applied []string) *httptest.Server {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/dustpan/v1/upgrade-check" {
			http.NotFound(w, r)

			return
		}
		w.Header().Set("Content-Type", "application/json")

		err := json.NewEncoder(w).Encode(map[string]any{
			"version":                 "test",
			"schemaVersion":           version,
			"appliedMigrations":       applied,
			"keyEncryptionKey":        true,
			"runningJournaledUpdates": journaled,
		})
		if err != nil {
			t.Errorf("encode upgrade check answer: %v", err)
		}
	}))
	t.Cleanup(srv.Close)

	return srv
}

// runUpgradeCheck runs the upgrade check against srv and returns its report.
func runUpgradeCheck(t *testing.T, srv *httptest.Server) string {
	t.Helper()

	t.Setenv("DUSTPAN_ADMIN_TOKEN", "test-token")

	var stdout, stderr bytes.Buffer

	args := []string{"--server", srv.URL}

	err := client.UpgradeCheck(t.Context(), args, &stdout, &stderr, "v1.0.0")
	if err != nil {
		t.Fatalf("upgrade check: %v", err)
	}

	return stdout.String()
}

// TestUpgradeCheckReportsNoJournaledUpdates proves the report with no running
// journaled update holds the zero count and no downgrade advice. The test
// sets the admin token, so it cannot run in parallel with other tests.
//
//nolint:paralleltest // The test sets the admin token, so it cannot run in parallel.
func TestUpgradeCheckReportsNoJournaledUpdates(t *testing.T) {
	out := runUpgradeCheck(t, serveUpgradeCheck(t, 0))

	if !strings.Contains(out, "running journaled updates: 0") {
		t.Fatalf("report lacks running journaled updates 0:\n%s", out)
	}

	if strings.Contains(out, "before a downgrade") {
		t.Fatalf("report with 0 journaled updates holds downgrade advice:\n%s", out)
	}
}

// TestUpgradeCheckReportsTwoJournaledUpdates proves the report with two
// running journaled updates holds the count and the downgrade advice. The
// test sets the admin token, so it cannot run in parallel with other tests.
//
//nolint:paralleltest // The test sets the admin token, so it cannot run in parallel.
func TestUpgradeCheckReportsTwoJournaledUpdates(t *testing.T) {
	out := runUpgradeCheck(t, serveUpgradeCheck(t, 2))

	if !strings.Contains(out, "running journaled updates: 2") {
		t.Fatalf("report lacks running journaled updates 2:\n%s", out)
	}

	if !strings.Contains(
		out,
		"Let running journaled updates finish or cancel them before a downgrade.",
	) {
		t.Fatalf("report with 2 journaled updates lacks downgrade advice:\n%s", out)
	}
}

// TestUpgradeCheckRefusesSchemaBeforeBaseline proves that a server whose
// database records a migration from before the baseline gets the verdict
// cannot start and the named refusal, not the newer-schema refusal. The
// test sets the admin token, so it cannot run in parallel with other tests.
func TestUpgradeCheckRefusesSchemaBeforeBaseline(t *testing.T) {
	srv := serveApplied(t, 0, 76, []string{"0075_sign_in_freshness.sql", "0076_sign_in_providers.sql"})

	t.Setenv("DUSTPAN_ADMIN_TOKEN", "test-token")

	var stdout, stderr bytes.Buffer

	err := client.UpgradeCheck(t.Context(), []string{"--server", srv.URL}, &stdout, &stderr, "test-binary")
	if !errors.Is(err, schema.ErrSchemaBeforeBaseline) {
		t.Fatalf("upgrade check: err = %v, want ErrSchemaBeforeBaseline", err)
	}

	if errors.Is(err, schema.ErrSchemaDiverged) {
		t.Fatalf("upgrade check: err = %v, want no diverged-schema refusal", err)
	}

	if out := stdout.String(); !strings.Contains(out, "verdict: "+client.VerdictCannotStart) {
		t.Fatalf("report lacks the verdict %q:\n%s", client.VerdictCannotStart, out)
	}
}
