package storetest

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/forge"
)

// gitHost holds the Git host store of a store under test. It is a struct, so
// that gitHostOf returns a concrete type.
type gitHost struct{ forge.Store }

// gitHostOf returns the Git host store of s, or skips the test for a store that
// keeps none.
func gitHostOf(t *testing.T, s any) gitHost {
	t.Helper()

	keeper, ok := s.(interface{ GitHost() forge.Store })
	if !ok {
		t.Skip("the store keeps no Git host registration")
	}

	return gitHost{Store: keeper.GitHost()}
}

// restoreOnto exports src in the portable form, stages it on dst and applies it.
// It returns the export, so a caller can compare it with a later export.
func restoreOnto(t *testing.T, src, dst PortableRestoreStore) *portableSink {
	t.Helper()

	sink, sum, files := exportPortable(t, src)

	if _, err := dst.StagePortable(t.Context(), sink.read, sum, files, StageAs(t, dst)); err != nil {
		t.Fatalf("stage portable backup: %v", err)
	}

	if err := dst.ApplyStaging(t.Context()); err != nil {
		t.Fatalf("apply staged backup: %v", err)
	}

	return sink
}

// testRestoreKeepsOldEventSecret proves that a restore brings back a
// registration with an open window of the old event secret, with the three old
// columns set, and that a confirmed rotation restores with the three columns
// NULL. The seed of the other cases holds a registration with no window, so
// they cover the NULL case too.
func testRestoreKeepsOldEventSecret(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	ps := portableRestoreStores(t, newStore(t), newStore(t))
	ctx := t.Context()

	seedBackupStore(t, ps.src)

	src, dst := gitHostOf(t, ps.src), gitHostOf(t, ps.dst)

	stored, err := src.GetRegistration(ctx, forge.KindGitHub)
	if err != nil {
		t.Fatalf("get the seeded registration: %v", err)
	}

	next := stored
	next.OldEventSecretSetID = forge.NewSetID()
	next.OldSealedEventSecret = []byte("sealed-old-event-secret")
	next.OldEventSecretEndsAt = now().Add(7 * 24 * time.Hour).Truncate(time.Millisecond)

	want, err := src.PutRegistration(ctx, next, stored.Version, gateEntry(audit.ServerScope, "git-host-app:put"))
	if err != nil {
		t.Fatalf("put the registration with an open window: %v", err)
	}

	sink := restoreOnto(t, ps.src, ps.dst)

	got, err := dst.GetRegistration(ctx, forge.KindGitHub)
	if err != nil {
		t.Fatalf("get the restored registration: %v", err)
	}

	if got.OldEventSecretSetID != want.OldEventSecretSetID ||
		!bytes.Equal(got.OldSealedEventSecret, want.OldSealedEventSecret) ||
		!got.OldEventSecretEndsAt.Equal(want.OldEventSecretEndsAt) || got.Version != want.Version {
		t.Fatalf("restored registration = %+v, want the window and the version of %+v", got, want)
	}

	again, _, _ := exportPortable(t, ps.dst)
	path := backup.PortablePath(backup.TableGitHostAppRegistrations)

	if !bytes.Equal(again.files[path].Bytes(), sink.files[path].Bytes()) {
		t.Fatal("the registration table after the restore differs from the export")
	}

	checkConfirmedRestore(t, ps, src, dst, want.Version)
}

// checkConfirmedRestore confirms the rotation on the source, restores again and
// expects the registration with no window: NULL in the three columns.
func checkConfirmedRestore(t *testing.T, ps portableStores, src, dst forge.Store, version int64) {
	t.Helper()

	ctx := t.Context()

	confirmed, err := src.ConfirmRotation(
		ctx, forge.KindGitHub, version, gateEntry(audit.ServerScope, "git-host-app:confirm-rotation"),
	)
	if err != nil {
		t.Fatalf("confirm the rotation: %v", err)
	}

	restoreOnto(t, ps.src, ps.dst)

	got, err := dst.GetRegistration(ctx, forge.KindGitHub)
	if err != nil || got.OldEventSecretSetID != "" || len(got.OldSealedEventSecret) != 0 ||
		!got.OldEventSecretEndsAt.IsZero() || got.Version != confirmed.Version {
		t.Fatalf("restored registration after a confirm = %+v, %v; want no window at version %d",
			got, err, confirmed.Version)
	}

	_, err = dst.ConfirmRotation(
		ctx,
		forge.KindGitHub,
		0,
		gateEntry(audit.ServerScope, "git-host-app:confirm-rotation"),
	)
	if !errors.Is(err, forge.ErrNoRotation) {
		t.Fatalf("confirm of the restored registration = %v, want ErrNoRotation", err)
	}
}
