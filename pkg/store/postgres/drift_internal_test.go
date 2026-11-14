package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/drift"
	"opendustpan.org/opendustpan/pkg/graph"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/internal/driftstore"
	"opendustpan.org/opendustpan/pkg/store/postgres/internal/db"
)

const driftRowSQL = `INSERT INTO drift_results (unit_id, namespace_id, tool, job_id, status, checked_at_ms, truncated,
resources_json) SELECT 'wsp-1', id, 'opentofu', 'job-1', 'drifted', 1800000000000, false,
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

	if _, err := s.pool.Exec(t.Context(), driftRowSQL); err != nil {
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
		"truncated with one resource": `UPDATE drift_results SET truncated = true`,
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

		s := openForSavedViews(t)
		seedDriftRow(t, s)

		if err := checkStagedDriftResults(t.Context(), s.pool, currentSchema(t, s)); err != nil {
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

		s := openForSavedViews(t)
		seedDriftRow(t, s)

		// The table refuses the status by its own check, so such a row cannot exist.
		_, err := s.pool.Exec(t.Context(), `UPDATE drift_results SET status = 'unknown'`)
		if err == nil || !strings.Contains(err.Error(), "check constraint") {
			t.Fatalf("change the status = %v, want the refusal of the CHECK of the table", err)
		}
	})
}

// checkTamperedDriftRow changes the only row with update, and checks that the
// restore check refuses it and that a read does not serve it.
func checkTamperedDriftRow(t *testing.T, update string) {
	t.Helper()

	s := openForSavedViews(t)
	seedDriftRow(t, s)

	if _, err := s.pool.Exec(t.Context(), update); err != nil {
		t.Fatalf("change the row: %v", err)
	}

	if err := checkStagedDriftResults(t.Context(), s.pool, currentSchema(t, s)); !errors.Is(err, backup.ErrCorrupt) {
		t.Fatalf("check = %v, want ErrCorrupt", err)
	}

	var unitID, tool string

	row := s.pool.QueryRow(t.Context(), `SELECT unit_id, tool FROM drift_results`)
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

	s := openForSavedViews(t)
	seedDriftRow(t, s)

	// The foreign key refuses this change, so the test drops the key in its
	// own schema first.
	for _, stmt := range []string{
		`ALTER TABLE drift_results DROP CONSTRAINT drift_results_namespace_id_fkey`,
		`UPDATE drift_results SET namespace_id = 'ns-gone'`,
	} {
		if _, execErr := s.pool.Exec(t.Context(), stmt); execErr != nil {
			t.Fatalf("%s: %v", stmt, execErr)
		}
	}

	err := checkStagedDriftResults(t.Context(), s.pool, currentSchema(t, s))
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

// TestDriftPutSameJobIsAtomic proves that the check of the job of a stored
// drift result is atomic with the write of the result. Two transactions that
// read the same state can both write the result of one job; only the write
// that commits first stays, and the other reports drift.ErrStale.
func TestDriftPutSameJobIsAtomic(t *testing.T) {
	t.Parallel()

	const (
		jobOne = job.ID("job-1")
		jobTwo = job.ID("job-2")
		t1     = int64(1_900_000_000_000)
		t2     = int64(1_900_000_000_001)
	)

	cases := []driftPutCase{
		{
			name: "the earlier time of the same job commits first", first: driftRow(jobOne, t1),
			second: driftRow(jobOne, t2), stale: true, wantJob: jobOne, wantMs: t1,
		},
		{
			name: "the later time of the same job commits first", first: driftRow(jobOne, t2),
			second: driftRow(jobOne, t1), stale: true, wantJob: jobOne, wantMs: t2,
		},
		{
			name: "a newer result of another job is admissible", first: driftRow(jobOne, t1),
			second: driftRow(jobTwo, t2), stale: false, wantJob: jobTwo, wantMs: t2,
		},
		{
			name: "an older result of another job is not admissible", first: driftRow(jobTwo, t2),
			second: driftRow(jobOne, t1), stale: true, wantJob: jobTwo, wantMs: t2,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := openForSavedViews(t)
			seedDriftNamespace(t, s)

			checkDriftPutOrder(t, s, tc)
		})
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

// checkDriftPutOrder runs one order of driftPutCase: transaction A reads the
// state of the unit before B writes, B stores its row and commits, and A then
// stores its row. It ends both transactions and checks the stored row.
func checkDriftPutOrder(t *testing.T, s *Store, tc driftPutCase) {
	t.Helper()

	ctx := t.Context()
	unit := graph.Unit{Namespace: "acme", Tool: "opentofu", ID: "wsp-1"}

	txA, err := s.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("case %s: begin A: %v", tc.name, err)
	}

	defer endDriftTx(t, txA, "case "+tc.name+": A")

	a := driftTx{q: db.New(txA)}

	_, found, err := a.Latest(ctx, unit)
	if err != nil || found {
		t.Fatalf("case %s: A reads the state before B: found = %t, err = %v, want no row", tc.name, found, err)
	}

	txB, err := s.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("case %s: begin B: %v", tc.name, err)
	}

	defer endDriftTx(t, txB, "case "+tc.name+": B")

	err = (driftTx{q: db.New(txB)}).Put(ctx, tc.first)
	if err != nil {
		t.Fatalf("case %s: put of B = %v, want nil", tc.name, err)
	}

	commitDriftTx(t, txB, "case "+tc.name+": B")

	// A writes after the commit of B. The write of the same job must not
	// replace the stored row.
	wantDriftPut(t, tc, a.Put(ctx, tc.second))

	commitDriftTx(t, txA, "case "+tc.name+": A")

	latest, err := s.DriftResults().GetLatest(ctx, unit)
	if err != nil {
		t.Fatalf("case %s: read the stored row: %v", tc.name, err)
	}

	if latest.JobID != tc.wantJob || latest.CheckedAt.UnixMilli() != tc.wantMs {
		t.Fatalf("case %s: stored row = job %s at %d, want job %s at %d", tc.name, latest.JobID,
			latest.CheckedAt.UnixMilli(), tc.wantJob, tc.wantMs)
	}
}

// wantDriftPut fails the test when the write of A did not give the wanted
// answer: drift.ErrStale for a stale write, nil for an admissible one. It
// does not stop the test, so that the check of the stored row runs too.
func wantDriftPut(t *testing.T, tc driftPutCase, err error) {
	t.Helper()

	if tc.stale && !errors.Is(err, drift.ErrStale) {
		t.Errorf("case %s: put of A = %v, want an error that wraps drift.ErrStale", tc.name, err)
	}

	if !tc.stale && err != nil {
		t.Errorf("case %s: put of A = %v, want nil", tc.name, err)
	}
}

// commitDriftTx commits a transaction of the case and fails the test on an
// error.
func commitDriftTx(t *testing.T, tx pgx.Tx, what string) {
	t.Helper()

	if err := tx.Commit(t.Context()); err != nil {
		t.Fatalf("%s: commit: %v", what, err)
	}
}

// endDriftTx rolls back a transaction of the case that the test did not
// commit. A transaction that the test committed gives pgx.ErrTxClosed, which
// is not a failure.
func endDriftTx(t *testing.T, tx pgx.Tx, what string) {
	t.Helper()

	if err := tx.Rollback(context.WithoutCancel(t.Context())); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
		t.Errorf("%s: roll back: %v", what, err)
	}
}
