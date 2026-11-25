package sqlite

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"modernc.org/sqlite"

	"opendustpan.org/opendustpan/internal/store/jobstore"
	"opendustpan.org/opendustpan/pkg/job"
)

const (
	createJobsIndexTable = `CREATE TABLE jobs (id TEXT PRIMARY KEY, unit_id TEXT UNIQUE, idempotency_key TEXT UNIQUE)`
	insertJobsIndexRow   = `INSERT INTO jobs (id, unit_id, idempotency_key) VALUES (?, ?, ?)`
)

// jobsIndexDriverErrors holds the driver errors of one unique violation of
// each kind.
type jobsIndexDriverErrors struct {
	holder error
	key    error
	other  error
}

// makeJobsIndexDriverErrors makes one unique violation of each kind in a table
// with the same unique indexes as the jobs table.
func makeJobsIndexDriverErrors(t *testing.T) jobsIndexDriverErrors {
	t.Helper()

	conn, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}

	t.Cleanup(func() {
		if closeErr := conn.Close(); closeErr != nil {
			t.Errorf("close database: %v", closeErr)
		}
	})

	ctx := t.Context()

	if _, err := conn.ExecContext(ctx, createJobsIndexTable); err != nil {
		t.Fatalf("create table error = %v", err)
	}

	if _, err := conn.ExecContext(ctx, insertJobsIndexRow, "a", "u", "k"); err != nil {
		t.Fatalf("insert error = %v", err)
	}

	var errs jobsIndexDriverErrors

	_, errs.holder = conn.ExecContext(ctx, insertJobsIndexRow, "b", "u", "k2")
	_, errs.key = conn.ExecContext(ctx, insertJobsIndexRow, "c", "u3", "k")
	_, errs.other = conn.ExecContext(ctx, insertJobsIndexRow, "a", "u4", "k4")

	return errs
}

// TestJobIndexError checks that a unique violation of an unknown index keeps
// no driver error in its chain. The driver errors come from real inserts.
func TestJobIndexError(t *testing.T) {
	t.Parallel()

	errs := makeJobsIndexDriverErrors(t)

	tests := []struct {
		name      string
		driverErr error
		want      error
	}{
		{name: "holder index", driverErr: errs.holder, want: jobstore.ErrHolderIndex},
		{name: "key index", driverErr: errs.key, want: jobstore.ErrKeyIndex},
		{name: "other index", driverErr: errs.other, want: job.ErrConflict},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if tt.driverErr == nil {
				t.Fatal("insert error = nil, want a unique violation")
			}

			got := jobIndexError("insert job", tt.driverErr)

			if !errors.Is(got, tt.want) {
				t.Fatalf("jobIndexError() = %v, want an error that matches %v", got, tt.want)
			}

			if sqliteErr, ok := errors.AsType[*sqlite.Error](got); ok {
				t.Fatalf("jobIndexError() = %v, want no driver error in the chain, got code %d", got, sqliteErr.Code())
			}
		})
	}

	t.Run("nil", func(t *testing.T) {
		t.Parallel()

		if got := jobIndexError("insert job", nil); got != nil {
			t.Fatalf("jobIndexError(nil) = %v, want nil", got)
		}
	})
}

// TestUpdateJobClearsKeyOnly proves that the UpdateJob query clears the
// stored key when the update carries an empty key.
func TestUpdateJobClearsKeyOnly(t *testing.T) {
	t.Parallel()

	s := openTestStore(t, filepath.Join(t.TempDir(), "dustpan.db"))
	id := seedJobRow(t, s)

	err := s.ExecForTest(t.Context(), `UPDATE jobs SET idempotency_key = ? WHERE id = ?`, "key-1", string(id))
	if err != nil {
		t.Fatalf("set the key: %v", err)
	}

	row, err := s.queries.GetJob(t.Context(), string(id))
	if err != nil {
		t.Fatalf("get the job: %v", err)
	}

	r := jobRow(row)
	r.Key = ""
	r.Version, r.UpdatedMs = row.Version+1, row.UpdatedAtMs+1

	ok, err := updateJobRow(t.Context(), s.queries, r, row.Version)
	if err != nil || !ok {
		t.Fatalf("update with the empty key = %t, %v; want a written row", ok, err)
	}

	if got := storedIdempotencyKey(t, s, id); got != "" {
		t.Fatalf("the key after the clear = %q, want empty", got)
	}
}

// TestUpdateJobKeepsStoredKey proves that the UpdateJob query keeps the
// stored key when the update carries another key that is not empty.
func TestUpdateJobKeepsStoredKey(t *testing.T) {
	t.Parallel()

	s := openTestStore(t, filepath.Join(t.TempDir(), "dustpan.db"))
	id := seedJobRow(t, s)

	err := s.ExecForTest(t.Context(), `UPDATE jobs SET idempotency_key = ? WHERE id = ?`, "key-1", string(id))
	if err != nil {
		t.Fatalf("set the key: %v", err)
	}

	row, err := s.queries.GetJob(t.Context(), string(id))
	if err != nil {
		t.Fatalf("get the job: %v", err)
	}

	r := jobRow(row)
	r.Key = "key-2"
	r.Version, r.UpdatedMs = row.Version+1, row.UpdatedAtMs+1

	ok, err := updateJobRow(t.Context(), s.queries, r, row.Version)
	if err != nil || !ok {
		t.Fatalf("update with another key = %t, %v; want a written row", ok, err)
	}

	if got := storedIdempotencyKey(t, s, id); got != "key-1" {
		t.Fatalf("the key after another key = %q, want key-1", got)
	}
}

// TestUpdateJobNeverSetsKey proves that the UpdateJob query never sets a key
// on a job that has none.
func TestUpdateJobNeverSetsKey(t *testing.T) {
	t.Parallel()

	s := openTestStore(t, filepath.Join(t.TempDir(), "dustpan.db"))
	id := seedJobRow(t, s)

	row, err := s.queries.GetJob(t.Context(), string(id))
	if err != nil {
		t.Fatalf("get the job: %v", err)
	}

	r := jobRow(row)
	r.Key = "key-2"
	r.Version, r.UpdatedMs = row.Version+1, row.UpdatedAtMs+1

	ok, err := updateJobRow(t.Context(), s.queries, r, row.Version)
	if err != nil || !ok {
		t.Fatalf("update with a key on a job without a key = %t, %v; want a written row", ok, err)
	}

	if got := storedIdempotencyKey(t, s, id); got != "" {
		t.Fatalf("the key after a key on a job without a key = %q, want empty", got)
	}
}

// storedIdempotencyKey returns the stored idempotency key of the job id.
func storedIdempotencyKey(t *testing.T, s *Store, id job.ID) string {
	t.Helper()

	row, err := s.queries.GetJob(t.Context(), string(id))
	if err != nil {
		t.Fatalf("get job %s: %v", id, err)
	}

	return row.IdempotencyKey
}
