package postgres

import (
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// TestRestoreFailsWhenTheBreakGlassCountFails proves that the restore fails
// closed, and changes nothing, when either working break-glass count cannot
// run. A count that fails does not become a zero count: the apply returns
// the error of the count, and the live schema keeps its data.
func TestRestoreFailsWhenTheBreakGlassCountFails(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		breakCount func(t *testing.T, s *Store, staging string)
	}{
		{
			name: "the count before the swap fails",
			breakCount: func(t *testing.T, s *Store, _ string) {
				t.Helper()

				// The count before the swap reads the live passkeys table.
				// The staged area stays valid.
				_, err := s.pool.Exec(t.Context(), `ALTER TABLE passkeys RENAME TO passkeys_hidden`)
				if err != nil {
					t.Fatalf("hide the live passkeys table: %v", err)
				}
			},
		},
		{
			name: "the count after the swap fails",
			breakCount: func(t *testing.T, s *Store, staging string) {
				t.Helper()

				// The count after the swap reads passkeys.suspended_at_ms in
				// the schema that the swap puts in. The dev data check of the
				// swap reads principal_id only, so the apply reaches the
				// count.
				statement := "ALTER TABLE " + stagingTable(staging, "passkeys") +
					" RENAME COLUMN suspended_at_ms TO suspended_until_ms"

				_, err := s.pool.Exec(t.Context(), statement)
				if err != nil {
					t.Fatalf("hide the staged suspended_at_ms column: %v", err)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			checkBreakGlassCountFails(t, openAt(t, membershipURL(t), ""), tt.breakCount)
		})
	}
}

// checkBreakGlassCountFails breaks one working break-glass count of s, runs
// the apply of a staged restore area, and proves that the apply reports the
// unavailable count and changes nothing.
func checkBreakGlassCountFails(
	t *testing.T, s *Store, breakCount func(t *testing.T, s *Store, staging string),
) {
	t.Helper()

	ctx := t.Context()

	// The namespace must exist only in the live data: removeLiveOnlyFromStaged
	// removes it from the staged copy, so a swap would drop it, and the check
	// after the failed apply proves that the live data stayed.
	kept := namespace.Namespace{
		ID:                  namespace.NewID(),
		CreatedAt:           time.UnixMilli(1_700_000_000_000).UTC(),
		Name:                "kept",
		PrimaryInstallation: "eu-west",
		FailoverVersion:     1,
		Overrides:           namespace.Overrides{Forced: false, Unlocked: false},
	}

	if err := s.CreateNamespace(ctx, kept); err != nil {
		t.Fatalf("create the live-only namespace: %v", err)
	}

	before, err := s.GetNamespace(ctx, kept.Name)
	if err != nil {
		t.Fatalf("read the live-only namespace before the apply: %v", err)
	}

	staged := storetest.StageRestoreArea(t, s)

	staging, err := areaSchema(staged.Area)
	if err != nil {
		t.Fatalf("staging schema of the area: %v", err)
	}

	removeLiveOnlyFromStaged(t, s, staging, kept)
	breakCount(t, s, staging)

	if _, applyErr := s.ApplyRestoreArea(ctx, staged.Swap, staged.Area, false); !errors.Is(
		applyErr, principal.ErrBreakGlassUnavailable,
	) {
		t.Fatalf("apply with a failed count = %v, want an error that matches %v",
			applyErr, principal.ErrBreakGlassUnavailable)
	}

	checkLiveOnlyNamespace(t, s, before)
	checkLiveSchemaInUse(t, s, staged, staging)
}

// removeLiveOnlyFromStaged removes the namespace from the staged copy of the
// restore, so the staged data is the snapshot from before the namespace
// existed.
func removeLiveOnlyFromStaged(t *testing.T, s *Store, staging string, kept namespace.Namespace) {
	t.Helper()

	statement := "DELETE FROM " + stagingTable(staging, "namespaces") + " WHERE id = $1"
	if _, err := s.pool.Exec(t.Context(), statement, kept.ID.String()); err != nil {
		t.Fatalf("remove the live-only namespace from the staged copy: %v", err)
	}
}

// checkLiveOnlyNamespace proves that the live row reads the same after the
// failed apply.
func checkLiveOnlyNamespace(t *testing.T, s *Store, before namespace.Namespace) {
	t.Helper()

	after, err := s.GetNamespace(t.Context(), before.Name)
	if err != nil {
		t.Fatalf("read the live-only namespace after the failed apply: %v", err)
	}

	if after.ID != before.ID || after.Name != before.Name ||
		after.FailoverVersion != before.FailoverVersion || !after.CreatedAt.Equal(before.CreatedAt) {
		t.Fatalf("live-only namespace after the failed apply = %+v, want %+v", after, before)
	}
}

// checkLiveSchemaInUse proves that the swap did not commit: the staging
// schema is still the staging schema, and no previous schema exists.
func checkLiveSchemaInUse(t *testing.T, s *Store, staged storetest.StagedArea, staging string) {
	t.Helper()

	exists, err := s.schemaExists(t.Context(), staging)
	if err != nil {
		t.Fatalf("read the staging schema %s: %v", staging, err)
	}

	if !exists {
		t.Fatalf("staging schema %s after the failed apply, want it kept", staging)
	}

	previous, err := previousSchemaOf(staged.Area.Restore)
	if err != nil {
		t.Fatalf("previous schema of the restore: %v", err)
	}

	exists, err = s.schemaExists(t.Context(), previous)
	if err != nil {
		t.Fatalf("read the previous schema %s: %v", previous, err)
	}

	if exists {
		t.Fatalf("previous schema %s after the failed apply, want it absent", previous)
	}
}
