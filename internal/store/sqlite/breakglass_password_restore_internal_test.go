package sqlite

import (
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
)

// TestRestoreRefusesABreakGlassAccountWithAPassword sends hostile staged data
// straight to the swap of a restore: a break-glass person with a local
// password, as a changed backup or a backup of another version could hold.
// No port of the server makes such a row, so the test writes it into the
// staged file. The apply fails with ErrBreakGlassPassword and keeps the live
// data. The same person with no password, and a person with a password that
// is not a break-glass account, apply.
func TestRestoreRefusesABreakGlassAccountWithAPassword(t *testing.T) {
	t.Parallel()

	const hash = "$argon2id$v=19$m=65536,t=3,p=4$c2FsdHNhbHQ$aGFzaGhhc2hoYXNoaGFzaA"

	tests := []struct {
		name       string
		breakGlass bool
		hash       string
		want       error
	}{
		{
			name:       "a break-glass person with a password",
			breakGlass: true,
			hash:       hash,
			want:       principal.ErrBreakGlassPassword,
		},
		{name: "a break-glass person with no password", breakGlass: true, hash: "", want: nil},
		{name: "a person with a password", breakGlass: false, hash: hash, want: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			breakGlassPasswordCase(t, tt.breakGlass, tt.hash, tt.want)
		})
	}
}

// breakGlassPasswordCase stages the person of one case, applies the staged
// file, and checks the refusal and the live data.
func breakGlassPasswordCase(t *testing.T, breakGlass bool, hash string, want error) {
	t.Helper()

	s := openSeededKeepStore(t)
	stageLocalPerson(t, s, breakGlass, hash)

	kept := createLiveOnlyNamespace(t, s)

	if err := s.SetStateIf(t.Context(), maintenance.StateNormal, maintenance.StateMaintenance); err != nil {
		t.Fatalf("enter maintenance mode: %v", err)
	}

	applyErr := s.ApplyStaging(t.Context())

	if want == nil {
		if applyErr != nil {
			t.Fatalf("apply: %v", applyErr)
		}

		return
	}

	if !errors.Is(applyErr, principal.ErrBreakGlassPassword) {
		t.Fatalf("apply = %v, want an error that matches %v", applyErr, principal.ErrBreakGlassPassword)
	}

	if _, err := s.GetNamespace(t.Context(), kept.Name); err != nil {
		t.Fatalf("read the live-only namespace after the refused apply: %v", err)
	}
}

// stageLocalPerson writes a local person and its local user into the staged
// file of s, with the break-glass flag and the password hash of the case.
func stageLocalPerson(t *testing.T, s *Store, breakGlass bool, hash string) {
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

	const at = int64(1_700_000_000_000)

	_, err = conn.ExecContext(t.Context(), `INSERT INTO principals
(id, subject, display_name, kind, source, created_at_ms, updated_at_ms, namespace, break_glass)
VALUES ('prn-glass', 'glass', '', 'person', 'local', ?, ?, '', ?)`, at, at, breakGlass)
	if err != nil {
		t.Fatalf("stage the person: %v", err)
	}

	passwordSetAt := int64(0)
	if hash != "" {
		passwordSetAt = at
	}

	_, err = conn.ExecContext(t.Context(), `INSERT INTO local_users
(id, principal_id, user_name, password_hash, password_set_at_ms, created_by, created_at_ms)
VALUES ('usr-glass', 'prn-glass', 'glass', ?, ?, 'admin', ?)`, hash, passwordSetAt, at)
	if err != nil {
		t.Fatalf("stage the local user: %v", err)
	}
}

// createLiveOnlyNamespace creates a namespace that only the live data holds:
// a swap would remove it.
func createLiveOnlyNamespace(t *testing.T, s *Store) namespace.Namespace {
	t.Helper()

	kept := namespace.Namespace{
		ID:                  namespace.NewID(),
		CreatedAt:           time.UnixMilli(1_700_000_000_000).UTC(),
		Name:                "kept",
		PrimaryInstallation: "eu-west",
		FailoverVersion:     1,
		Overrides:           namespace.Overrides{Forced: false, Unlocked: false},
	}

	if err := s.CreateNamespace(t.Context(), kept); err != nil {
		t.Fatalf("create the live-only namespace: %v", err)
	}

	return kept
}
