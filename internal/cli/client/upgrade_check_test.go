package client_test

import (
	"bytes"
	"encoding/json"
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

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/server/upgrade-check" {
			http.NotFound(w, r)

			return
		}
		w.Header().Set("Content-Type", "application/json")

		err := json.NewEncoder(w).Encode(map[string]any{
			"version":                 "test",
			"schemaVersion":           binary,
			"appliedMigrations":       embedded,
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
