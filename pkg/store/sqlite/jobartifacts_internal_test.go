package sqlite

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
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
			`{"kind":"plan-apply","size":1,"sha256":"`+artifactHash+`","secret":true}]`)},
		{"a part of an unknown kind", planBodyJSON("pln-1", part("other", artifactHash, 1))},
		{"a part with a hash that is not a SHA-256", planBodyJSON("pln-1", part("plan-display", "xyz", 1))},
		{
			"a part with an upper case hash",
			planBodyJSON("pln-1", part("plan-display", strings.ToUpper(artifactHash), 1)),
		},
		{"a part with a negative size", planBodyJSON("pln-1", part("plan-display", artifactHash, -1))},
		{"a part above the size limit", planBodyJSON("pln-1", part("plan-display", artifactHash, job.MaxPlanPart+1))},
		{"a plan with one kind twice", planBodyJSON("pln-1", `[`+
			`{"kind":"plan-display","size":1,"sha256":"`+artifactHash+`","secret":false},`+
			`{"kind":"plan-display","size":1,"sha256":"`+artifactHash+`","secret":false}]`)},
	}
}

// insertPlanRow inserts a row of a plan in the namespace acme.
func insertPlanRow(t *testing.T, s *Store, body string) {
	t.Helper()

	_, err := s.conn.ExecContext(t.Context(), `INSERT INTO job_plans (id, namespace_id, job_id, body, created_at_ms)
VALUES ('pln-1', (SELECT id FROM namespaces WHERE name = 'acme'), 'job-1', ?, 1)`, body)
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

		s := openInternalTemplated(t)
		seedItemKey(t, s)
		insertPlanRow(t, s, good)

		if err := checkStagedJobArtifacts(t.Context(), s.conn); err != nil {
			t.Fatalf("check = %v, want nil", err)
		}
	})

	for _, tc := range stagedPlanCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := openInternalTemplated(t)
			seedItemKey(t, s)
			insertPlanRow(t, s, tc.body)

			if err := checkStagedJobArtifacts(t.Context(), s.conn); !errors.Is(err, backup.ErrCorrupt) {
				t.Fatalf("check = %v, want ErrCorrupt", err)
			}
		})
	}
}

// execWithoutKeys runs the statements on a connection that has the foreign key
// check off, and releases the connection before it returns.
func execWithoutKeys(t *testing.T, s *Store, stmts ...string) {
	t.Helper()

	pinned, err := s.conn.Conn(t.Context())
	if err != nil {
		t.Fatalf("take a connection: %v", err)
	}

	for _, stmt := range append([]string{`PRAGMA foreign_keys = OFF`}, stmts...) {
		if _, execErr := pinned.ExecContext(t.Context(), stmt); execErr != nil {
			t.Fatalf("%s: %v", stmt, execErr)
		}
	}

	if closeErr := pinned.Close(); closeErr != nil {
		t.Fatalf("release the connection: %v", closeErr)
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

			s := openInternalTemplated(t)
			seedItemKey(t, s)
			execWithoutKeys(t, s, insert)

			err := checkStagedJobArtifacts(t.Context(), s.conn)
			if !errors.Is(err, backup.ErrCorrupt) {
				t.Fatalf("check = %v, want ErrCorrupt", err)
			}
		})
	}
}

// TestCheckStagedLogChunks proves the restore check of the log chunk rows: a
// chunk row must name a log version of its own namespace and have the rules
// of a reserve.
func TestCheckStagedLogChunks(t *testing.T) {
	t.Parallel()

	// chunkWith inserts a confirmed chunk of the log of job-1 with the blob key
	// key.
	chunkWith := func(logID, ns string, seq, size int64, sha, key string) string {
		return fmt.Sprintf(`INSERT INTO job_log_chunks (log_id, seq, namespace_id, sha256, size, confirmed, blob_key)
VALUES ('%s', %d, (SELECT id FROM namespaces WHERE name = '%s'), '%s', %d, 1, '%s')`, logID, seq, ns, sha, size, key)
	}

	attempt := func(logID string, seq int64) string {
		return fmt.Sprintf("job/job-1/log/%s/%08d-jla-x", logID, seq)
	}

	chunk := func(logID, ns string, seq, size int64, sha string) string {
		return chunkWith(logID, ns, seq, size, sha, attempt(logID, seq))
	}

	cases := []struct {
		name, insert string
		ok           bool
	}{
		{name: "a chunk that the store wrote", insert: chunk("jlv-1", "acme", 0, 1, artifactHash), ok: true},
		{
			name:   "a chunk at the limits",
			insert: chunk("jlv-1", "acme", job.MaxLogChunks-1, job.MaxLogChunk, artifactHash), ok: true,
		},
		{name: "a hash that is not a SHA-256", insert: chunk("jlv-1", "acme", 0, 1, "xyz"), ok: false},
		{name: "an upper case hash", insert: chunk("jlv-1", "acme", 0, 1, strings.ToUpper(artifactHash)), ok: false},
		{
			name:   "a size above the chunk limit",
			insert: chunk("jlv-1", "acme", 0, job.MaxLogChunk+1, artifactHash), ok: false,
		},
		{
			name:   "a number at the chunk count limit",
			insert: chunk("jlv-1", "acme", job.MaxLogChunks, 1, artifactHash),
			ok:     false,
		},
		{name: "a log version that does not exist", insert: chunk("jlv-gone", "acme", 0, 1, artifactHash), ok: false},
		{name: "a log version of another namespace", insert: chunk("jlv-1", "other", 0, 1, artifactHash), ok: false},
		{
			name:   "a confirmed chunk with no blob key",
			insert: chunkWith("jlv-1", "acme", 0, 1, artifactHash, ""),
			ok:     false,
		},
		{
			name:   "a blob key of another chunk",
			insert: chunkWith("jlv-1", "acme", 0, 1, artifactHash, attempt("jlv-1", 1)), ok: false,
		},
		{
			name:   "a blob key of another log version",
			insert: chunkWith("jlv-1", "acme", 0, 1, artifactHash, attempt("jlv-2", 0)), ok: false,
		},
		{
			name: "a blob key that leaves the log",
			insert: chunkWith(
				"jlv-1",
				"acme",
				0,
				1,
				artifactHash,
				"job/job-1/log/jlv-1/../x/00000000-jla-x",
			),
			ok: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := openInternalTemplated(t)
			seedItemKey(t, s)
			createOtherNamespace(t, s)
			execWithoutKeys(t, s, `INSERT INTO job_logs (id, namespace_id, job_id, phase, step, token, created_at_ms)
VALUES ('jlv-1', (SELECT id FROM namespaces WHERE name = 'acme'), 'job-1', 0, 'plan', 1, 1)`)
			execWithoutKeys(t, s, tc.insert)

			err := checkStagedJobArtifacts(t.Context(), s.conn)
			if tc.ok && err != nil {
				t.Fatalf("check = %v, want nil", err)
			}

			if !tc.ok && !errors.Is(err, backup.ErrCorrupt) {
				t.Fatalf("check = %v, want ErrCorrupt", err)
			}
		})
	}
}

// createOtherNamespace creates the namespace "other".
func createOtherNamespace(t *testing.T, s *Store) {
	t.Helper()

	ns := namespace.Namespace{
		ID:                  namespace.NewID(),
		CreatedAt:           time.UnixMilli(1_700_000_000_000).UTC(),
		Name:                "other",
		PrimaryInstallation: "eu-west",
		FailoverVersion:     1,
		Overrides:           namespace.Overrides{Forced: false, Unlocked: false},
	}
	if err := s.CreateNamespace(t.Context(), ns); err != nil {
		t.Fatalf("create namespace: %v", err)
	}
}
