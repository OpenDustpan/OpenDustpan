package postgres

import (
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// TestRestoreRefusesABreakGlassAccountWithAPassword sends hostile staged data
// straight to the swap of a restore: a break-glass person with a local
// password, as a changed backup or a backup of another version could hold.
// No port of the server makes such a row, so the test writes it into the
// staged area. The apply fails with ErrBreakGlassPassword and keeps the live
// data. The same person with no password, and a person with a password that
// is not a break-glass account, pass the check.
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
// area, and checks the refusal and the live data.
func breakGlassPasswordCase(t *testing.T, breakGlass bool, hash string, want error) {
	t.Helper()

	s := openAt(t, membershipURL(t), "")
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

	staged := storetest.StageRestoreArea(t, s)

	staging, err := areaSchema(staged.Area)
	if err != nil {
		t.Fatalf("staging schema of the area: %v", err)
	}

	removeLiveOnlyFromStaged(t, s, staging, kept)
	stageLocalPerson(t, s, staging, breakGlass, hash)

	_, applyErr := s.ApplyRestoreArea(ctx, staged.Swap, staged.Area, false)

	if want == nil {
		if errors.Is(applyErr, principal.ErrBreakGlassPassword) {
			t.Fatalf("apply = %v, want no break-glass password refusal", applyErr)
		}

		return
	}

	if !errors.Is(applyErr, principal.ErrBreakGlassPassword) {
		t.Fatalf("apply = %v, want an error that matches %v", applyErr, principal.ErrBreakGlassPassword)
	}

	checkLiveOnlyNamespace(t, s, before)
	checkLiveSchemaInUse(t, s, staged, staging)
}

// stageLocalPerson writes a local person and its local user into the staged
// area, with the break-glass flag and the password hash of the case.
func stageLocalPerson(t *testing.T, s *Store, staging string, breakGlass bool, hash string) {
	t.Helper()

	const at = int64(1_700_000_000_000)

	_, err := s.pool.Exec(t.Context(), "INSERT INTO "+stagingTable(staging, "principals")+
		" (id, subject, display_name, kind, source, created_at_ms, updated_at_ms, namespace, break_glass)"+
		" VALUES ('prn-glass', 'glass', '', 'person', 'local', $1, $1, '', $2)", at, breakGlass)
	if err != nil {
		t.Fatalf("stage the person: %v", err)
	}

	passwordSetAt := int64(0)
	if hash != "" {
		passwordSetAt = at
	}

	_, err = s.pool.Exec(t.Context(), "INSERT INTO "+stagingTable(staging, "local_users")+
		" (id, principal_id, user_name, password_hash, password_set_at_ms, created_by, created_at_ms)"+
		" VALUES ('usr-glass', 'prn-glass', 'glass', $1, $2, 'admin', $3)", hash, passwordSetAt, at)
	if err != nil {
		t.Fatalf("stage the local user: %v", err)
	}
}
