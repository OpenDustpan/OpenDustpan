package sqlite

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

	for _, id := range accesstest.HostileGrantIDs() {
		_, err := s.conn.ExecContext(t.Context(), `UPDATE grants SET id = ? WHERE id = 'grt-inside'`, id)
		if err == nil {
			t.Fatalf("the grants table stored the ID %.20q", id)
		}
	}
}

// TestCheckStagedGrantIDs is the hostile table of the restore check. The
// constraint is off for the connection of the test, so the check stands alone.
// The test fails when the check is removed.
func TestCheckStagedGrantIDs(t *testing.T) {
	t.Parallel()

	for _, id := range accesstest.HostileGrantIDs() {
		s := stagedGrantStore(t, nil)

		conn, err := s.conn.Conn(t.Context())
		if err != nil {
			t.Fatalf("open a connection: %v", err)
		}

		if _, err = conn.ExecContext(t.Context(), `PRAGMA ignore_check_constraints = ON`); err != nil {
			t.Fatalf("turn the constraints off: %v", err)
		}

		if _, err = conn.ExecContext(t.Context(), `UPDATE grants SET id = ? WHERE id = 'grt-inside'`, id); err != nil {
			t.Fatalf("plant the ID %.20q: %v", id, err)
		}

		if err = conn.Close(); err != nil {
			t.Fatalf("close the connection: %v", err)
		}

		err = checkStagedGrantIDs(t.Context(), s.conn)
		if !errors.Is(err, backup.ErrCorrupt) {
			t.Fatalf("check of the ID %.20q = %v, want ErrCorrupt", id, err)
		}

		if err != nil && len(id) > 3 && strings.Contains(err.Error(), id) {
			t.Fatalf("the error holds the ID: %.80q", err.Error())
		}
	}

	s := stagedGrantStore(t, nil)
	if err := checkStagedGrantIDs(t.Context(), s.conn); err != nil {
		t.Fatalf("check of a valid ID = %v, want nil", err)
	}
}
