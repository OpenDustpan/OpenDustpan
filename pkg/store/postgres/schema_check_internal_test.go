package postgres

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/store/internal/migrate"
	"opendustpan.org/opendustpan/pkg/store/schema"
)

// TestVerifyRecordedRefusesAlone sends embedded migrations that differ from
// the recorded row directly to verifyRecorded, as if the migration check had
// let them through. Each one fails with schema.ErrSchemaChanged. The same
// migration and a version that the database does not record pass.
func TestVerifyRecordedRefusesAlone(t *testing.T) {
	t.Parallel()

	s := openAt(t, membershipURL(t), "dev")

	all, err := loadMigrations()
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
		"another sum":      {embedded: otherSum, want: schema.ErrSchemaChanged},
		"another name":     {embedded: otherName, want: schema.ErrSchemaChanged},
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

	if err := verify(all); !errors.Is(err, schema.ErrSchemaChanged) {
		t.Fatalf("verifyRecorded without the sha256 column = %v, want %v", err, schema.ErrSchemaChanged)
	}
}
