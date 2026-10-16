package storetest

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/stack"
)

// testRestoreKeepsAccessTables proves that a restore without
// server:restore-access keeps the current access tables and restores the
// other data, while a restore with it brings the access tables back. It
// takes a backup, revokes a token and a grant, adds a stack, restores the
// backup keeping the access tables, and proves the token stays revoked, the
// grant stays revoked, and the extra stack is gone. It then restores the
// same backup replacing the access tables and proves the token and the grant
// are back. Stores without the principal, token, access and portable ports
// skip it: the fake of this package is one.
func testRestoreKeepsAccessTables(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	s := newStore(t)
	ctx := t.Context()

	ps, ok := s.(PortableRestoreStore)
	if !ok {
		t.Skip("the store has no portable export and stage")
	}

	keeper, ok := s.(backup.AccessKeepingApplier)
	if !ok {
		t.Skip("the store keeps no access tables on restore")
	}

	principals, ok := s.(principal.Store)
	if !ok {
		t.Skip("the store keeps no principals")
	}

	tokens, ok := s.(accesstoken.Store)
	if !ok {
		t.Skip("the store keeps no access tokens")
	}

	grants, ok := s.(access.Store)
	if !ok {
		t.Skip("the store keeps no grants")
	}

	_ = principals

	seed := seedBackupStore(t, s)
	sink, sum, files := exportPortable(t, ps)

	if err := tokens.RevokeToken(ctx, "tok-used", ServerChange(now(), "token:tok-used")); err != nil {
		t.Fatalf("revoke token: %v", err)
	}

	fence, entry := GrantWrite(t, s, identity.NamespaceObject("acme"), now(), "grant:grt-ops")

	if err := grants.RevokeGrant(ctx, "grt-ops", fence, entry); err != nil {
		t.Fatalf("revoke grant: %v", err)
	}

	extra := mustCreateStack(t, s, seed.acme, "extra")

	if _, err := ps.StagePortable(ctx, sink.read, sum, files); err != nil {
		t.Fatalf("stage backup: %v", err)
	}

	if err := keeper.ApplyStagingKeepingAccess(ctx); err != nil {
		t.Fatalf("apply keeping the access tables: %v", err)
	}

	kept, err := tokens.GetToken(ctx, "tok-used")
	if err != nil {
		t.Fatalf("get kept token: %v", err)
	}

	if kept.RevokedAt.IsZero() {
		t.Fatal("token tok-used is active after a restore that keeps the access tables, want revoked")
	}

	if _, err := grants.GetGrant(ctx, "grt-ops"); !errors.Is(err, access.ErrNotFound) {
		t.Fatalf("get kept grant: err = %v, want ErrNotFound", err)
	}

	if _, err := s.GetStack(ctx, extra.Ref); !errors.Is(err, stack.ErrNotFound) {
		t.Fatalf("get extra stack after a keep restore: err = %v, want ErrNotFound", err)
	}

	if got := mustGetStack(t, s, seed.prod.Ref); got.ID != seed.prod.ID {
		t.Fatalf("restored stack = %s, want %s", got.ID, seed.prod.ID)
	}

	if _, err := ps.StagePortable(ctx, sink.read, sum, files); err != nil {
		t.Fatalf("stage backup again: %v", err)
	}

	if err := s.ApplyStaging(ctx); err != nil {
		t.Fatalf("apply replacing the access tables: %v", err)
	}

	replaced, err := tokens.GetToken(ctx, "tok-used")
	if err != nil {
		t.Fatalf("get replaced token: %v", err)
	}

	if !replaced.RevokedAt.IsZero() {
		t.Fatal("token tok-used is revoked after a restore that replaces the access tables, want active")
	}

	if _, err := grants.GetGrant(ctx, "grt-ops"); err != nil {
		t.Fatalf("get replaced grant: %v", err)
	}
}
