package sqlite

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/drift"
	"opendustpan.org/opendustpan/pkg/graph"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/internal/driftstore"
	"opendustpan.org/opendustpan/pkg/store/sqlite/internal/db"
)

const driftRowSQL = `INSERT INTO drift_results (unit_id, namespace_id, tool, job_id, status, checked_at_ms, truncated,
resources_json) SELECT 'wsp-1', id, 'opentofu', 'job-1', 'drifted', 1800000000000, 0,
'[{"address":"aws_s3_bucket.logs","kind":"changed"}]' FROM namespaces WHERE name = 'acme'`

// seedDriftNamespace makes the namespace acme for the drift tests of this
// package.
func seedDriftNamespace(t *testing.T, s *Store) {
	t.Helper()

	ns := namespace.Namespace{
		ID:                  namespace.NewID(),
		CreatedAt:           time.UnixMilli(1_700_000_000_000).UTC(),
		Name:                "acme",
		PrimaryInstallation: "eu-west",
		FailoverVersion:     1,
		Overrides:           namespace.Overrides{Forced: false, Unlocked: false},
	}
	if err := s.CreateNamespace(t.Context(), ns); err != nil {
		t.Fatalf("create namespace: %v", err)
	}
}

// seedDriftRow makes the namespace acme and one valid drift result row of the
// unit wsp-1.
func seedDriftRow(t *testing.T, s *Store) {
	t.Helper()

	seedDriftNamespace(t, s)

	if _, err := s.conn.ExecContext(t.Context(), driftRowSQL); err != nil {
		t.Fatalf("insert the drift result: %v", err)
	}
}

// tamperedDriftRows lists the changes that make a stored row fail the rules of
// a new result.
func tamperedDriftRows() map[string]string {
	return map[string]string{
		"resources that are not JSON": `UPDATE drift_results SET resources_json = 'not json'`,
		"empty resources text":        `UPDATE drift_results SET resources_json = ''`,
		"resources with a value": `UPDATE drift_results SET resources_json =
			'[{"address":"a","kind":"changed","value":"x"}]'`,
		"text after the resources": `UPDATE drift_results SET resources_json = '[{"address":"a","kind":"changed"}] []'`,
		"an address with a newline": `UPDATE drift_results SET resources_json =
			'[{"address":"a\nb","kind":"changed"}]'`,
		"an empty address": `UPDATE drift_results SET resources_json = '[{"address":"","kind":"changed"}]'`,
		"an address twice": `UPDATE drift_results SET resources_json =
			'[{"address":"a","kind":"changed"},{"address":"a","kind":"changed"}]'`,
		"a kind that is unknown":      `UPDATE drift_results SET resources_json = '[{"address":"a","kind":"created"}]'`,
		"in-sync with a resource":     `UPDATE drift_results SET status = 'in-sync'`,
		"drifted with no resource":    `UPDATE drift_results SET resources_json = '[]'`,
		"a job that is not a job":     `UPDATE drift_results SET job_id = 'x-1'`,
		"truncated with one resource": `UPDATE drift_results SET truncated = 1`,
		"a tool with a space":         `UPDATE drift_results SET tool = 'open tofu'`,
		"an empty unit ID":            `UPDATE drift_results SET unit_id = ''`,
		"a time before year 1":        `UPDATE drift_results SET checked_at_ms = -62135596800000`,
	}
}

// TestCheckStagedDriftResults proves the restore check: it accepts a row that
// the store wrote, and it refuses each row that a new result would not pass. A
// row that fails is also not served by a read.
func TestCheckStagedDriftResults(t *testing.T) {
	t.Parallel()

	t.Run("a row that the store wrote passes", func(t *testing.T) {
		t.Parallel()

		s := openInternalTemplated(t)
		seedDriftRow(t, s)

		if err := checkStagedDriftResults(t.Context(), s.conn); err != nil {
			t.Fatalf("check = %v, want nil", err)
		}
	})

	for name, update := range tamperedDriftRows() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			checkTamperedDriftRow(t, update)
		})
	}

	t.Run("a status that the table never holds", func(t *testing.T) {
		t.Parallel()

		s := openInternalTemplated(t)
		seedDriftRow(t, s)

		// The table refuses the status by its own check, so such a row cannot exist.
		_, err := s.conn.ExecContext(t.Context(), `UPDATE drift_results SET status = 'unknown'`)
		if err == nil || !strings.Contains(err.Error(), "CHECK") {
			t.Fatalf("change the status = %v, want the refusal of the CHECK of the table", err)
		}
	})
}

// checkTamperedDriftRow changes the only row with update, and checks that the
// restore check refuses it and that a read does not serve it.
func checkTamperedDriftRow(t *testing.T, update string) {
	t.Helper()

	s := openInternalTemplated(t)
	seedDriftRow(t, s)

	if _, err := s.conn.ExecContext(t.Context(), update); err != nil {
		t.Fatalf("change the row: %v", err)
	}

	if err := checkStagedDriftResults(t.Context(), s.conn); !errors.Is(err, backup.ErrCorrupt) {
		t.Fatalf("check = %v, want ErrCorrupt", err)
	}

	var unitID, tool string

	row := s.conn.QueryRowContext(t.Context(), `SELECT unit_id, tool FROM drift_results`)
	if err := row.Scan(&unitID, &tool); err != nil {
		t.Fatalf("read the row: %v", err)
	}

	_, err := s.DriftResults().GetLatest(t.Context(), graph.Unit{Namespace: "acme", Tool: tool, ID: unitID})
	if !errors.Is(err, drift.ErrNotFound) && !errors.Is(err, drift.ErrInvalid) {
		t.Fatalf("get of a bad row = %v, want it not served", err)
	}
}

// TestCheckStagedDriftResultsRefusesAMissingNamespace proves that a result
// whose namespace the backup does not hold is corrupt. An inner join would skip
// such a row, and the restore would bring it in.
func TestCheckStagedDriftResultsRefusesAMissingNamespace(t *testing.T) {
	t.Parallel()

	s := openInternalTemplated(t)
	seedDriftRow(t, s)

	// The foreign key refuses this change, so the connection that makes it
	// switches the key check off. It is closed before the check runs.
	pinned, err := s.conn.Conn(t.Context())
	if err != nil {
		t.Fatalf("take a connection: %v", err)
	}

	for _, stmt := range []string{`PRAGMA foreign_keys = OFF`, `UPDATE drift_results SET namespace_id = 'ns-gone'`} {
		if _, execErr := pinned.ExecContext(t.Context(), stmt); execErr != nil {
			t.Fatalf("%s: %v", stmt, execErr)
		}
	}

	if closeErr := pinned.Close(); closeErr != nil {
		t.Fatalf("release the connection: %v", closeErr)
	}

	err = checkStagedDriftResults(t.Context(), s.conn)
	if !errors.Is(err, backup.ErrCorrupt) || !strings.Contains(fmt.Sprint(err), "has no namespace") {
		t.Fatalf("check = %v, want the refusal of a result with no namespace", err)
	}
}

// driftPutCase is one order of two drift writes of the unit wsp-1: first is
// the row that the writer that commits first stores, second is the row that
// the other writer tries to store after that commit.
type driftPutCase struct {
	name    string
	first   driftstore.Row
	second  driftstore.Row
	stale   bool
	wantJob job.ID
	wantMs  int64
}

// TestDriftPutChecksJobAndTime proves the two conditions of the upsert of a
// drift result: it does not store a result of the job of the stored row, and
// it does not store a result that is older than the stored row. SQLite cannot
// interleave two transactions, so each write runs in its own transaction
// after the one before it commits.
func TestDriftPutChecksJobAndTime(t *testing.T) {
	t.Parallel()

	const (
		jobOne = job.ID("job-1")
		jobTwo = job.ID("job-2")
		t1     = int64(1_900_000_000_000)
		t2     = int64(1_900_000_000_001)
	)

	cases := []driftPutCase{
		{
			name: "the same job at a later time is not stored", first: driftRow(jobOne, t1),
			second: driftRow(jobOne, t2), stale: true, wantJob: jobOne, wantMs: t1,
		},
		{
			name: "the same job at an earlier time is not stored", first: driftRow(jobOne, t2),
			second: driftRow(jobOne, t1), stale: true, wantJob: jobOne, wantMs: t2,
		},
		{
			name: "a newer result of another job replaces the stored row", first: driftRow(jobOne, t1),
			second: driftRow(jobTwo, t2), stale: false, wantJob: jobTwo, wantMs: t2,
		},
		{
			name: "an older result of another job is not stored", first: driftRow(jobOne, t2),
			second: driftRow(jobTwo, t1), stale: true, wantJob: jobOne, wantMs: t2,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := openInternalTemplated(t)
			seedDriftNamespace(t, s)

			checkDriftPutOrder(t, s, tc)
		})
	}
}

// checkDriftPutOrder runs one order of driftPutCase: it stores the first row
// in its own transaction, tries to store the second after that commit, and
// checks the answer and the stored row.
func checkDriftPutOrder(t *testing.T, s *Store, tc driftPutCase) {
	t.Helper()

	if err := putDriftRow(t, s, tc.first); err != nil {
		t.Fatalf("case %s: put of the first row = %v, want nil", tc.name, err)
	}

	wantDriftPut(t, tc, putDriftRow(t, s, tc.second))

	unit := graph.Unit{Namespace: "acme", Tool: "opentofu", ID: "wsp-1"}

	latest, err := s.DriftResults().GetLatest(t.Context(), unit)
	if err != nil {
		t.Fatalf("case %s: read the stored row: %v", tc.name, err)
	}

	if latest.JobID != tc.wantJob || latest.CheckedAt.UnixMilli() != tc.wantMs {
		t.Fatalf("case %s: stored row = job %s at %d, want job %s at %d", tc.name, latest.JobID,
			latest.CheckedAt.UnixMilli(), tc.wantJob, tc.wantMs)
	}
}

// wantDriftPut fails the test when the write of the second row did not give
// the wanted answer: drift.ErrStale for a stale write, nil for an admissible
// one. It does not stop the test, so that the check of the stored row runs
// too.
func wantDriftPut(t *testing.T, tc driftPutCase, err error) {
	t.Helper()

	if tc.stale && !errors.Is(err, drift.ErrStale) {
		t.Errorf("case %s: put of the second row = %v, want an error that wraps drift.ErrStale", tc.name, err)
	}

	if !tc.stale && err != nil {
		t.Errorf("case %s: put of the second row = %v, want nil", tc.name, err)
	}
}

// driftRow returns the row of the unit wsp-1 in acme for jobID at the time,
// as the drift store writes it.
func driftRow(jobID job.ID, checkedAtMs int64) driftstore.Row {
	return driftstore.Row{
		Namespace: "acme", Tool: "opentofu", UnitID: "wsp-1", JobID: string(jobID), Status: "drifted",
		CheckedAtMs: checkedAtMs, Truncated: false,
		ResourcesJSON: `[{"address":"aws_s3_bucket.logs","kind":"changed"}]`,
	}
}

// putDriftRow stores row in its own transaction, as one writer does.
func putDriftRow(t *testing.T, s *Store, row driftstore.Row) error {
	t.Helper()

	tx, err := s.conn.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatalf("begin transaction: %v", err)
	}

	putErr := (driftTx{q: db.New(tx)}).Put(t.Context(), row)

	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	return putErr
}
