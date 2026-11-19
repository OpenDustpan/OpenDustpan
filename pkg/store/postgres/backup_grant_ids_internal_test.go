package postgres

import (
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/access/accesstest"
	"opendustpan.org/opendustpan/pkg/backup"
)

// TestGrantIDConstraintRefusesAnIDThatIsNotAGrantID is the hostile table of
// the schema layer: the CHECK constraint of grants.id refuses each text, so a
// raw write cannot store it. The test fails when the constraint is removed.
func TestGrantIDConstraintRefusesAnIDThatIsNotAGrantID(t *testing.T) {
	t.Parallel()

	s := stagedGrantStore(t, nil)

	for _, id := range accesstest.HostileGrantIDs {
		if strings.ContainsRune(id, 0) {
			continue
		}

		_, err := s.pool.Exec(t.Context(), `UPDATE grants SET id = $1 WHERE id = 'grt-inside'`, id)
		if err == nil {
			t.Fatalf("the grants table stored the ID %.20q", id)
		}
	}
}

// TestCheckStagedGrantIDs is the hostile table of the restore check. The
// constraint is dropped in the schema of the test, so the check stands alone.
// The test fails when the check is removed.
func TestCheckStagedGrantIDs(t *testing.T) {
	t.Parallel()

	s := stagedGrantStore(t, []string{`ALTER TABLE grants DROP CONSTRAINT grants_id_check`})
	schema := currentSchema(t, s)

	if err := checkStagedGrantIDs(t.Context(), s.pool, schema); err != nil {
		t.Fatalf("check of a valid ID = %v, want nil", err)
	}

	for _, id := range accesstest.HostileGrantIDs {
		if strings.ContainsRune(id, 0) {
			continue
		}

		if _, err := s.pool.Exec(t.Context(), `UPDATE grants SET id = $1`, id); err != nil {
			t.Fatalf("plant the ID %.20q: %v", id, err)
		}

		err := checkStagedGrantIDs(t.Context(), s.pool, schema)
		if !errors.Is(err, backup.ErrCorrupt) {
			t.Fatalf("check of the ID %.20q = %v, want ErrCorrupt", id, err)
		}

		if len(id) > 3 && strings.Contains(err.Error(), id) {
			t.Fatalf("the error holds the ID: %.80q", err.Error())
		}
	}
}
