package sqlite

import (
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
)

// TestRestoreFailsWhenTheBreakGlassCountFails proves that the restore fails
// closed, and changes nothing, when either working break-glass count cannot
// run. A failed statement does not end the transaction of a SQLite
// connection, so a count error that the apply ignored would leave the count
// at zero and the floor check would read zero on both sides of the swap.
func TestRestoreFailsWhenTheBreakGlassCountFails(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		breakCount func(t *testing.T, s *Store)
	}{
		{
			name: "the count before the swap fails",
			breakCount: func(t *testing.T, s *Store) {
				t.Helper()

				// The count before the swap reads live.passkeys. The staged
				// file stays valid.
				_, err := s.conn.ExecContext(t.Context(), `ALTER TABLE passkeys RENAME TO passkeys_hidden`)
				if err != nil {
					t.Fatalf("hide the live passkeys table: %v", err)
				}
			},
		},
		{
			name: "the count after the swap fails",
			breakCount: func(t *testing.T, s *Store) {
				t.Helper()

				conn, err := openStagedAccess(t.Context(), s.StagingPathForTest())
				if err != nil {
					t.Fatalf("open the staged file: %v", err)
				}

				defer func() {
					if closeErr := conn.Close(); closeErr != nil {
						t.Errorf("close the staged file: %v", closeErr)
					}
				}()

				// The count after the swap reads main.passkeys.suspended_at_ms.
				// The dev-data check of the swap reads principal_id only, so
				// the apply reaches the count.
				_, err = conn.ExecContext(t.Context(),
					`ALTER TABLE passkeys RENAME COLUMN suspended_at_ms TO suspended_until_ms`)
				if err != nil {
					t.Fatalf("hide the staged suspended_at_ms column: %v", err)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			breakGlassCountCase(t, openSeededKeepStore(t), tt.breakCount)
		})
	}
}

// breakGlassCountCase breaks one count of the store, applies the staged
// file, and proves that the apply reports the unavailable count and keeps the
// live rows. A namespace that only the live database holds proves that the
// failed apply changed nothing: a swap would remove it.
func breakGlassCountCase(t *testing.T, s *Store, breakCount func(t *testing.T, s *Store)) {
	t.Helper()

	ctx := t.Context()

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

	if setErr := s.SetStateIf(ctx, maintenance.StateNormal, maintenance.StateMaintenance); setErr != nil {
		t.Fatalf("enter maintenance mode: %v", setErr)
	}

	breakCount(t, s)

	applyErr := s.ApplyStaging(ctx)
	if !errors.Is(applyErr, principal.ErrBreakGlassUnavailable) {
		t.Fatalf("apply with a failed count = %v, want an error that matches %v",
			applyErr, principal.ErrBreakGlassUnavailable)
	}

	after, err := s.GetNamespace(ctx, kept.Name)
	if err != nil {
		t.Fatalf("read the live-only namespace after the failed apply: %v", err)
	}

	if after.ID != before.ID || after.Name != before.Name ||
		after.FailoverVersion != before.FailoverVersion || !after.CreatedAt.Equal(before.CreatedAt) {
		t.Fatalf("live-only namespace after the failed apply = %+v, want %+v", after, before)
	}
}
