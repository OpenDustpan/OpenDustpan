package postgres

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/backup"
)

// planBodyJSON returns the stored body of a plan with the given parts.
func planBodyJSON(id, parts string) string {
	return fmt.Sprintf(`{"id":%q,"itemKey":"ik-1","stateVersion":"sv-1","parts":%s,`+
		`"createdAt":"2026-10-21T09:00:00Z","expiresAt":"2027-10-21T09:00:00Z","redactedAt":"0001-01-01T00:00:00Z"}`,
		id, parts)
}

// artifactHash is a SHA-256 in hexadecimal.
const artifactHash = "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"

// stagedPlanCases lists the bodies of a plan row that a restore must refuse.
func stagedPlanCases() []struct{ name, body string } {
	part := func(kind, hash string, size int) string {
		return fmt.Sprintf(`[{"kind":%q,"size":%d,"sha256":%q,"secret":false}]`, kind, size, hash)
	}

	return []struct{ name, body string }{
		{"a body that is not JSON", `not json`},
		{"a body of another plan", planBodyJSON("pln-other", part("plan-display", artifactHash, 1))},
		{"a plan with three parts", planBodyJSON("pln-1", `[`+
			strings.Repeat(`{"kind":"plan-display","size":1,"sha256":"`+artifactHash+`","secret":false},`, 2)+
			`{"kind":"plan-apply","size":1,"sha256":"`+artifactHash+`","secret":false}]`)},
		{"a part of an unknown kind", planBodyJSON("pln-1", part("other", artifactHash, 1))},
		{"a part with a hash that is not a SHA-256", planBodyJSON("pln-1", part("plan-display", "xyz", 1))},
		{
			"a part with an upper case hash",
			planBodyJSON("pln-1", part("plan-display", strings.ToUpper(artifactHash), 1)),
		},
		{"a part with a negative size", planBodyJSON("pln-1", part("plan-display", artifactHash, -1))},
	}
}

// insertPlanRow inserts a row of a plan in the namespace acme.
func insertPlanRow(t *testing.T, s *Store, body string) {
	t.Helper()

	_, err := s.pool.Exec(t.Context(), `INSERT INTO job_plans (id, namespace_id, job_id, body, created_at_ms)
VALUES ('pln-1', (SELECT id FROM namespaces WHERE name = 'acme'), 'job-1', $1, 1)`, body)
	if err != nil {
		t.Fatalf("insert plan row: %v", err)
	}
}

// TestCheckStagedJobArtifacts proves the restore check of the plan, log and
// chunk rows: it accepts the rows that the store wrote, and it refuses a plan
// body that the store would not have written.
func TestCheckStagedJobArtifacts(t *testing.T) {
	t.Parallel()

	good := planBodyJSON("pln-1", `[{"kind":"plan-display","size":1,"sha256":"`+artifactHash+`","secret":false}]`)

	t.Run("a plan that the store wrote passes", func(t *testing.T) {
		t.Parallel()

		s := openForSavedViews(t)
		seedItemKey(t, s)
		insertPlanRow(t, s, good)

		if err := checkStagedJobArtifacts(t.Context(), s.pool, currentSchema(t, s)); err != nil {
			t.Fatalf("check = %v, want nil", err)
		}
	})

	for _, tc := range stagedPlanCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := openForSavedViews(t)
			seedItemKey(t, s)
			insertPlanRow(t, s, tc.body)

			err := checkStagedJobArtifacts(t.Context(), s.pool, currentSchema(t, s))
			if !errors.Is(err, backup.ErrCorrupt) {
				t.Fatalf("check = %v, want ErrCorrupt", err)
			}
		})
	}
}

// TestCheckStagedJobArtifactsRefusesAMissingNamespace proves that a plan, a
// log version or a chunk of a namespace that the backup does not hold is
// corrupt. An inner join would skip such a row.
func TestCheckStagedJobArtifactsRefusesAMissingNamespace(t *testing.T) {
	t.Parallel()

	rows := map[string]string{
		"a plan": `INSERT INTO job_plans (id, namespace_id, job_id, body, created_at_ms)
VALUES ('pln-1', 'ns-gone', 'job-1', '{}', 1)`,
		"a log version": `INSERT INTO job_logs (id, namespace_id, job_id, phase, step, token, created_at_ms)
VALUES ('jlv-1', 'ns-gone', 'job-1', 0, 'plan', 1, 1)`,
		"a chunk": `INSERT INTO job_log_chunks (log_id, seq, namespace_id, sha256, size, confirmed)
VALUES ('jlv-1', 0, 'ns-gone', 'x', 1, 0)`,
	}

	for what, insert := range rows {
		t.Run(what, func(t *testing.T) {
			t.Parallel()

			s := openForSavedViews(t)
			seedItemKey(t, s)

			// The foreign key refuses the row, so the test drops the keys in its
			// own schema first.
			for _, stmt := range []string{
				`ALTER TABLE job_plans DROP CONSTRAINT job_plans_namespace_id_fkey`,
				`ALTER TABLE job_logs DROP CONSTRAINT job_logs_namespace_id_fkey`,
				`ALTER TABLE job_log_chunks DROP CONSTRAINT job_log_chunks_namespace_id_fkey`,
				insert,
			} {
				if _, execErr := s.pool.Exec(t.Context(), stmt); execErr != nil {
					t.Fatalf("%s: %v", stmt, execErr)
				}
			}

			err := checkStagedJobArtifacts(t.Context(), s.pool, currentSchema(t, s))
			if !errors.Is(err, backup.ErrCorrupt) {
				t.Fatalf("check = %v, want ErrCorrupt", err)
			}
		})
	}
}
