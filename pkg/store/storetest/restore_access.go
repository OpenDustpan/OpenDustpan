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
	ports := keepAccessStores(t, s)

	seed := seedBackupStore(t, s)
	sink, sum, files := exportPortable(t, ports.stager)

	revokeAccessForKeepTest(t, s, ports.tokens, ports.grants)

	extra := mustCreateStack(t, s, seed.acme, "extra")

	stageKeepBackup(t, ports.stager, sink, sum, files)

	if err := ports.keeper.ApplyStagingKeepingAccess(t.Context()); err != nil {
		t.Fatalf("apply keeping the access tables: %v", err)
	}

	checkKeptTokenGrant(t, ports.tokens, ports.grants)
	checkKeptStacks(t, s, &seed, extra)

	stageKeepBackup(t, ports.stager, sink, sum, files)

	if err := s.ApplyStaging(t.Context()); err != nil {
		t.Fatalf("apply replacing the access tables: %v", err)
	}

	checkReplacedAccess(t, ports.tokens, ports.grants)
}

// keepAccessPorts holds the ports that a keep-access restore needs.
type keepAccessPorts struct {
	stager PortableRestoreStore
	keeper backup.AccessKeepingApplier
	tokens accesstoken.Store
	grants access.Store
}

// keepAccessStores returns the ports that a keep-access restore needs, or
// skips when the store misses one.
func keepAccessStores(t *testing.T, s RestoreStore) keepAccessPorts {
	t.Helper()

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

	_ = principals

	tokens, ok := s.(accesstoken.Store)
	if !ok {
		t.Skip("the store keeps no access tokens")
	}

	grants, ok := s.(access.Store)
	if !ok {
		t.Skip("the store keeps no grants")
	}

	return keepAccessPorts{stager: ps, keeper: keeper, tokens: tokens, grants: grants}
}

// revokeAccessForKeepTest revokes one token and one grant, so the keep
// restore must keep the revocations and the replacing restore must undo them.
func revokeAccessForKeepTest(t *testing.T, s RestoreStore, tokens accesstoken.Store, grants access.Store) {
	t.Helper()

	ctx := t.Context()

	if err := tokens.RevokeToken(ctx, "tok-used", ServerChange(now(), "token:tok-used")); err != nil {
		t.Fatalf("revoke token: %v", err)
	}

	fence, entry := GrantWrite(t, s, identity.NamespaceObject("acme"), now(), "grant:grt-ops")

	if err := grants.RevokeGrant(ctx, "grt-ops", fence, entry); err != nil {
		t.Fatalf("revoke grant: %v", err)
	}
}

// stageKeepBackup stages the exported backup, failing on error.
func stageKeepBackup(
	t *testing.T, ps PortableRestoreStore, sink *portableSink, sum backup.Summary, files []backup.PortableFile,
) {
	t.Helper()

	if _, err := ps.StagePortable(t.Context(), sink.read, sum, files); err != nil {
		t.Fatalf("stage backup: %v", err)
	}
}

// checkKeptTokenGrant proves the keep restore kept the token revoked and the
// grant revoked.
func checkKeptTokenGrant(t *testing.T, tokens accesstoken.Store, grants access.Store) {
	t.Helper()

	ctx := t.Context()

	kept, err := tokens.GetToken(ctx, "tok-used")
	if err != nil {
		t.Fatalf("get kept token: %v", err)
	}

	if kept.RevokedAt.IsZero() {
		t.Fatal("token tok-used is active after a restore that keeps the access tables, want revoked")
	}

	_, grantErr := grants.GetGrant(ctx, "grt-ops")
	if !errors.Is(grantErr, access.ErrNotFound) {
		t.Fatalf("get kept grant: err = %v, want ErrNotFound", grantErr)
	}
}

// checkKeptStacks proves the keep restore removed the extra stack and
// restored the seeded stack.
func checkKeptStacks(t *testing.T, s RestoreStore, seed *backupSeed, extra stack.Stack) {
	t.Helper()

	_, stackErr := s.GetStack(t.Context(), extra.Ref)
	if !errors.Is(stackErr, stack.ErrNotFound) {
		t.Fatalf("get extra stack after a keep restore: err = %v, want ErrNotFound", stackErr)
	}

	if got := mustGetStack(t, s, seed.prod.Ref); got.ID != seed.prod.ID {
		t.Fatalf("restored stack = %s, want %s", got.ID, seed.prod.ID)
	}
}

// checkReplacedAccess proves the replacing restore brought the token and the
// grant back from the backup.
func checkReplacedAccess(t *testing.T, tokens accesstoken.Store, grants access.Store) {
	t.Helper()

	ctx := t.Context()

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
