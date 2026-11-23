package postgres_test

import (
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/pgtest"
	"opendustpan.org/opendustpan/internal/store/postgres"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/workspace"
)

func TestUnlockRollsBackOnOutage(t *testing.T) {
	t.Parallel()

	base := outageBase(t)
	schemaURL := pgtest.MigratedURL(t, base)
	s, err := postgres.Open(t.Context(), schemaURL)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(s.Close)

	ctx := t.Context()
	ws, fence := seedLockedWorkspace(t, s)

	breakWorkspaceWrite(t, s, "break_unlock")

	err = s.SaveWorkspace(ctx, withTestRelease(ws, time.UnixMilli(1_700_000_200_000).UTC()), fence)
	if !errors.Is(err, namespace.ErrUnavailable) {
		t.Fatalf("SaveWorkspace release = %v, want namespace.ErrUnavailable", err)
	}

	got, getErr := s.GetWorkspaceByID(ctx, ws.ID)
	if getErr != nil {
		t.Fatalf("GetWorkspaceByID: %v", getErr)
	}
	if !got.Lock.Locked() || got.Lock.Holder != "alice" {
		t.Fatalf("workspace lock = %+v, want held by alice", got.Lock)
	}
}

func TestUnlockRetryWithSameLockIdentityReleases(t *testing.T) {
	t.Parallel()

	base := outageBase(t)
	schemaURL := pgtest.MigratedURL(t, base)
	s, err := postgres.Open(t.Context(), schemaURL)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(s.Close)

	ctx := t.Context()
	ws, fence := seedLockedWorkspace(t, s)
	release := withTestRelease(ws, time.UnixMilli(1_700_000_200_000).UTC())

	breakWorkspaceWrite(t, s, "break_unlock_retry")

	err = s.SaveWorkspace(ctx, release, fence)
	if !errors.Is(err, namespace.ErrUnavailable) {
		t.Fatalf("SaveWorkspace release = %v, want namespace.ErrUnavailable", err)
	}

	// The database comes back: the outage trigger goes away.
	const restore = `DROP TRIGGER break_unlock_retry ON workspaces`
	if _, dropErr := postgres.Pool(s).Exec(ctx, restore); dropErr != nil {
		t.Fatalf("drop outage trigger: %v", dropErr)
	}

	if retryErr := s.SaveWorkspace(ctx, release, fence); retryErr != nil {
		t.Fatalf("retry SaveWorkspace release: %v", retryErr)
	}

	got, err := s.GetWorkspaceByID(ctx, ws.ID)
	if err != nil {
		t.Fatalf("GetWorkspaceByID: %v", err)
	}
	if got.Lock.Locked() {
		t.Fatalf("lock = %+v, want unlocked", got.Lock)
	}
	if got.LastRelease.LockID != ws.Lock.ID || got.LastRelease.Holder != ws.Lock.Holder {
		t.Fatalf("last release = %+v, want %s held by %s", got.LastRelease, ws.Lock.ID, ws.Lock.Holder)
	}
}

func TestUnlock(t *testing.T) {
	t.Parallel()

	base := outageBase(t)
	schemaURL := pgtest.MigratedURL(t, base)
	s, err := postgres.Open(t.Context(), schemaURL)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(s.Close)

	ctx := t.Context()
	ws, fence := seedLockedWorkspace(t, s)
	at := time.UnixMilli(1_700_000_200_000).UTC()

	if saveErr := s.SaveWorkspace(ctx, withTestRelease(ws, at), fence); saveErr != nil {
		t.Fatalf("SaveWorkspace release: %v", saveErr)
	}

	got, err := s.GetWorkspaceByID(ctx, ws.ID)
	if err != nil {
		t.Fatalf("GetWorkspaceByID: %v", err)
	}
	if got.Lock.Locked() {
		t.Fatalf("lock = %+v, want unlocked", got.Lock)
	}
	if got.LastRelease.LockID != "lock-1" || got.LastRelease.By != "alice" {
		t.Fatalf("last release = %+v, want lock-1 by alice", got.LastRelease)
	}
}

func seedLockedWorkspace(t *testing.T, s *postgres.Store) (workspace.Workspace, namespace.Fence) {
	t.Helper()

	ws, fence := seedLockWorkspace(t, s)
	locked := withTestLock(ws, time.UnixMilli(1_700_000_050_000).UTC())
	if err := s.SaveWorkspace(t.Context(), locked, fence); err != nil {
		t.Fatalf("lock workspace: %v", err)
	}

	return locked, fence
}

// withTestRelease returns ws one revision on, with the lock of alice released
// by alice at at.
func withTestRelease(ws workspace.Workspace, at time.Time) workspace.Workspace {
	released := ws
	released.LastRelease = workspace.Release{
		At: at, LockID: ws.Lock.ID, Holder: ws.Lock.Holder, By: "alice", Kind: workspace.ReleaseNormal,
	}
	released.Lock = workspace.Lock{LockedAt: time.Time{}, ID: "", Holder: "", Reason: ""}
	released.Revision++
	released.UpdatedAt = at

	return released
}
