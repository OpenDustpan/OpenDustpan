package postgres

import (
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/store/internal/migrate"
	"opendustpan.org/opendustpan/pkg/store/schema"
)

// TestVerifyRecordedRefusesAlone sends embedded migrations that differ from
// the recorded row directly to verifyRecorded, as if the migration check had
// let them through. Each one fails with schema.ErrSchemaDiverged. The same
// migration and a version that the database does not record pass.
func TestVerifyRecordedRefusesAlone(t *testing.T) {
	t.Parallel()

	s := openAt(t, membershipURL(t), "dev")

	all, err := migrate.Load(migrations, "migrations")
	if err != nil {
		t.Fatalf("embedded migrations: %v", err)
	}

	same := all[0]

	otherSum := same
	otherSum.SHA256 = "0000000000000000000000000000000000000000000000000000000000000000"

	otherName := same
	otherName.Name = "1000_other.sql"

	unknown := same
	unknown.Version = schema.BaselineVersion + 1

	verify := func(embedded []migrate.Migration) error {
		return pgx.BeginFunc(t.Context(), s.pool, func(tx pgx.Tx) error {
			return verifyRecorded(t.Context(), tx, embedded)
		})
	}

	for name, tc := range map[string]struct {
		embedded migrate.Migration
		want     error
	}{
		"same migration":   {embedded: same, want: nil},
		"another sum":      {embedded: otherSum, want: schema.ErrSchemaDiverged},
		"another name":     {embedded: otherName, want: schema.ErrSchemaDiverged},
		"version not kept": {embedded: unknown, want: nil},
	} {
		err := verify([]migrate.Migration{tc.embedded})
		if !errors.Is(err, tc.want) || (tc.want == nil && err != nil) {
			t.Errorf("%s: verifyRecorded = %v, want %v", name, err, tc.want)
		}
	}

	if _, execErr := s.pool.Exec(t.Context(), `ALTER TABLE schema_migrations DROP COLUMN sha256`); execErr != nil {
		t.Fatalf("drop the sha256 column: %v", execErr)
	}

	if err := verify(all); !errors.Is(err, schema.ErrSchemaDiverged) {
		t.Fatalf("verifyRecorded without the sha256 column = %v, want %v", err, schema.ErrSchemaDiverged)
	}
}

// TestVerifyRecordedRefusesARowThatIsNotASum records a value that is not a
// SHA-256 and passes an embedded migration with the same value, as a caller
// with an empty or a broken sum would. verifyRecorded refuses each one alone,
// without migrate.Check.
func TestVerifyRecordedRefusesARowThatIsNotASum(t *testing.T) {
	t.Parallel()

	for name, value := range map[string]string{
		"empty sum":  "",
		"not hex":    strings.Repeat("z", 64),
		"upper case": strings.Repeat("A", 64),
		"line end":   strings.Repeat("a", 62) + "\r\n",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			s := openAt(t, membershipURL(t), "dev")

			all, err := migrate.Load(migrations, "migrations")
			if err != nil {
				t.Fatalf("embedded migrations: %v", err)
			}

			_, execErr := s.pool.Exec(t.Context(), `UPDATE schema_migrations SET sha256 = $1`, value)
			if execErr != nil {
				t.Fatalf("record %q: %v", value, execErr)
			}

			embedded := all[0]
			embedded.SHA256 = value

			err = pgx.BeginFunc(t.Context(), s.pool, func(tx pgx.Tx) error {
				return verifyRecorded(t.Context(), tx, []migrate.Migration{embedded})
			})
			if !errors.Is(err, schema.ErrSchemaDiverged) {
				t.Fatalf("verifyRecorded of %q = %v, want %v", value, err, schema.ErrSchemaDiverged)
			}
		})
	}
}
