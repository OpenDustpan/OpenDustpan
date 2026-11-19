package postgres

import (
	"errors"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/access/accesstest"
	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/identity"
)

// TestGrantIDConstraintRefusesAnIDThatIsNotAGrantID is the hostile table of
// the schema layer: the CHECK constraint of grants.id refuses each text, so a
// raw write cannot store it. The test fails when the constraint is removed.
func TestGrantIDConstraintRefusesAnIDThatIsNotAGrantID(t *testing.T) {
	t.Parallel()

	s := stagedGrantStore(t, nil)

	for _, id := range accesstest.HostileGrantIDs() {
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

	for _, id := range accesstest.HostileGrantIDs() {
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

		if err != nil && len(id) > 3 && strings.Contains(err.Error(), id) {
			t.Fatalf("the error holds the ID: %.80q", err.Error())
		}
	}
}

// TestCheckStagedRefusesAGrantIDThatIsNotAGrantID is the restore-level test of
// the check of grant IDs. The CHECK constraint is dropped in the staging
// schema with DROP CONSTRAINT IF EXISTS, so the constraint cannot refuse the
// row, and the test calls the whole check of the staged rows. It fails when
// the call of checkStagedGrantIDs is removed from checkStaged.
func TestCheckStagedRefusesAGrantIDThatIsNotAGrantID(t *testing.T) {
	t.Parallel()

	restoreAt := time.UnixMilli(1_700_000_000_000).UTC()

	s := stagedGrantStore(t, []string{`ALTER TABLE grants DROP CONSTRAINT IF EXISTS grants_id_check`})
	schema := currentSchema(t, s)

	if err := checkStaged(t.Context(), s.pool, schema, restoreAt); err != nil {
		t.Fatalf("check of a valid ID = %v, want nil", err)
	}

	for _, id := range accesstest.HostileGrantIDs() {
		if strings.ContainsRune(id, 0) {
			continue
		}

		if _, err := s.pool.Exec(t.Context(), `UPDATE grants SET id = $1`, id); err != nil {
			t.Fatalf("plant the ID %.20q: %v", id, err)
		}

		err := checkStaged(t.Context(), s.pool, schema, restoreAt)
		if !errors.Is(err, backup.ErrCorrupt) {
			t.Fatalf("restore check of the ID %.20q = %v, want ErrCorrupt", id, err)
		}
	}
}

// TestCreateGrantRefusesAnIDThatIsNotAGrantID is the hostile table of the
// write path: a grant with a bad ID, as a bad generator makes it, answers
// ErrInvalid and not a driver error. It fails when the check of insertGrant
// is removed, because the CHECK constraint then answers a driver error.
func TestCreateGrantRefusesAnIDThatIsNotAGrantID(t *testing.T) {
	t.Parallel()

	s := stagedGrantStore(t, nil)

	acme, err := s.GetNamespace(t.Context(), "acme")
	if err != nil {
		t.Fatalf("get namespace: %v", err)
	}

	for _, id := range accesstest.HostileGrantIDs() {
		if strings.ContainsRune(id, 0) {
			continue
		}

		g := access.Grant{
			CreatedAt: time.UnixMilli(1_700_000_000_000).UTC(), Condition: nil,
			Object: identity.NamespaceObject("acme"), ID: id, Role: access.RoleViewer, CreatedBy: "admin",
			Subject: access.Subject{Kind: access.SubjectPrincipal, ID: "prn-ci-acme"},
		}

		entry := stagedGrantEntry("acme", "namespace:manage-access", "grant:invalid")

		err = s.CreateGrant(t.Context(), g, acme.Fence(), entry, nil)
		if !errors.Is(err, access.ErrInvalid) {
			t.Fatalf("CreateGrant with the ID %.20q = %v, want ErrInvalid", id, err)
		}
	}
}
