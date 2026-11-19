package postgres

import (
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/store/postgres/internal/db"
)

// TestCreateGrantRowTranslatesACheckViolation is the hostile table of the
// translation of a CHECK failure. The Go guard of the ID is bypassed: the
// test calls createGrantRow, so the CHECK constraint of grants.id is the only
// layer that refuses the row. The error must be access.ErrInvalid and its text
// must hold no driver message and no part of the ID. The test fails when the
// translation is removed from createGrantRow.
func TestCreateGrantRowTranslatesACheckViolation(t *testing.T) {
	t.Parallel()

	s := stagedGrantStore(t, nil)
	q := db.New(s.pool)

	for _, id := range []string{"", "grt-a:b"} {
		_, err := createGrantRow(t.Context(), q, db.CreateGrantParams{
			ID:          id,
			Subject:     "principal:prn-ci-acme",
			SubjectKind: "principal",
			SubjectID:   "prn-ci-acme",
			Role:        "viewer",
			Object:      "namespace:acme",
			Target:      "acme",
			ObjectKind:  "namespace",
			Namespace:   "acme",
			Condition:   pgtype.Text{String: "", Valid: false},
			CreatedAtMs: 1_700_000_000_000,
			CreatedBy:   "admin",
		})
		if !errors.Is(err, access.ErrInvalid) {
			t.Fatalf("createGrantRow with the ID %q = %v, want ErrInvalid", id, err)
		}

		for _, banned := range []string{"CHECK", "SQLSTATE", "constraint", "grt-a:b"} {
			if strings.Contains(err.Error(), banned) {
				t.Fatalf("the error holds %q: %q", banned, err.Error())
			}
		}
	}
}
