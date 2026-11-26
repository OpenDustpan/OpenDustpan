package postgres

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"opendustpan.org/opendustpan/internal/pgtest"
	"opendustpan.org/opendustpan/internal/store/jobstore"
	"opendustpan.org/opendustpan/pkg/job"
)

// TestJobIndexError checks that a unique violation of an unknown index keeps
// no driver error in its chain. The driver error is not needed, so no
// caller can reach it.
func TestJobIndexError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		code       string
		constraint string
		want       error
	}{
		{name: "holder index", code: uniqueViolation, constraint: "jobs_one_change", want: jobstore.ErrHolderIndex},
		{name: "key index", code: uniqueViolation, constraint: "jobs_key", want: jobstore.ErrKeyIndex},
		{name: "other index", code: uniqueViolation, constraint: "jobs_list", want: job.ErrConflict},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			driverErr := new(pgconn.PgError)
			driverErr.Code = tt.code
			driverErr.ConstraintName = tt.constraint

			got := jobIndexError("insert job", driverErr)

			if !errors.Is(got, tt.want) {
				t.Fatalf("jobIndexError() = %v, want an error that matches %v", got, tt.want)
			}

			if pgErr, ok := errors.AsType[*pgconn.PgError](got); ok {
				t.Fatalf("jobIndexError() = %v, want no driver error in the chain, got code %s", got, pgErr.Code)
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
// stored key for an update that carries the clear signal, whatever the key of
// the update is.
func TestUpdateJobClearsKeyOnly(t *testing.T) {
	t.Parallel()

	s, err := Open(t.Context(), pgtest.MigratedURL(t, testPostgresURL(t)))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(s.Close)

	id := seedStagedJobRow(t, s)

	err = s.ExecForTest(t.Context(), `UPDATE jobs SET idempotency_key = $1 WHERE id = $2`, "key-1", string(id))
	if err != nil {
		t.Fatalf("set the key: %v", err)
	}

	row, err := s.queries.GetJob(t.Context(), string(id))
	if err != nil {
		t.Fatalf("get the job: %v", err)
	}

	r := jobRow(row)
	r.MarkClearKey()
	r.Version, r.UpdatedMs = row.Version+1, row.UpdatedAtMs+1

	ok, err := updateJobRow(t.Context(), s.queries, r, row.Version)
	if err != nil || !ok {
		t.Fatalf("update with the clear signal = %t, %v; want a written row", ok, err)
	}

	if got := storedIdempotencyKey(t, s, id); got != "" {
		t.Fatalf("the key after the clear = %q, want empty", got)
	}
}

// TestUpdateJobKeepsStoredKey proves that the UpdateJob query keeps the
// stored key for an update with no clear signal, whether the update carries
// another key or no key.
func TestUpdateJobKeepsStoredKey(t *testing.T) {
	t.Parallel()

	s, err := Open(t.Context(), pgtest.MigratedURL(t, testPostgresURL(t)))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(s.Close)

	id := seedStagedJobRow(t, s)

	err = s.ExecForTest(t.Context(), `UPDATE jobs SET idempotency_key = $1 WHERE id = $2`, "key-1", string(id))
	if err != nil {
		t.Fatalf("set the key: %v", err)
	}

	for _, key := range []string{"key-2", ""} {
		row, getErr := s.queries.GetJob(t.Context(), string(id))
		if getErr != nil {
			t.Fatalf("get the job: %v", getErr)
		}

		r := jobRow(row)
		r.Key = key
		r.Version, r.UpdatedMs = row.Version+1, row.UpdatedAtMs+1

		ok, updateErr := updateJobRow(t.Context(), s.queries, r, row.Version)
		if updateErr != nil || !ok {
			t.Fatalf("update with key %q = %t, %v; want a written row", key, ok, updateErr)
		}

		if got := storedIdempotencyKey(t, s, id); got != "key-1" {
			t.Fatalf("the key after an update with key %q = %q, want key-1", key, got)
		}
	}
}

// TestUpdateJobNeverSetsKey proves that the UpdateJob query never sets a key
// on a job that has none.
func TestUpdateJobNeverSetsKey(t *testing.T) {
	t.Parallel()

	s, err := Open(t.Context(), pgtest.MigratedURL(t, testPostgresURL(t)))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(s.Close)

	id := seedStagedJobRow(t, s)

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
