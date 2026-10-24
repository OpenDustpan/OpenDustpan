package postgres

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/store/internal/jobstore"
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
