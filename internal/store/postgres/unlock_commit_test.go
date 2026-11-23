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

	commit := postgres.NewStateCommitForTest(s, fence)
	_, err = commit.ReleaseLock(ctx, ws.ID, "alice", "lock-1", time.UnixMilli(1_700_000_200_000).UTC())
	if !errors.Is(err, namespace.ErrUnavailable) {
		t.Fatalf("ReleaseLock = %v, want namespace.ErrUnavailable", err)
	}

	got, getErr := s.GetWorkspaceByID(ctx, ws.ID)
	if getErr != nil {
		t.Fatalf("GetWorkspaceByID: %v", getErr)
	}
	if !got.Lock.Locked() || got.Lock.Holder != "alice" {
		t.Fatalf("workspace lock = %+v, want held by alice", got.Lock)
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

	commit := postgres.NewStateCommitForTest(s, fence)
	got, err := commit.ReleaseLock(ctx, ws.ID, "alice", "lock-1", at)
	if err != nil {
		t.Fatalf("ReleaseLock: %v", err)
	}
	if got.Lock.Locked() {
		t.Fatalf("lock = %+v, want unlocked", got.Lock)
	}
}

func seedLockedWorkspace(t *testing.T, s *postgres.Store) (workspace.Workspace, namespace.Fence) {
	t.Helper()

	ws, fence := seedLockWorkspace(t, s)
	locked := ws
	locked.Lock = workspace.Lock{
		LockedAt: time.UnixMilli(1_700_000_050_000).UTC(), ID: "lock-1", Holder: "alice", Reason: "reason",
	}
	locked.Revision++
	locked.UpdatedAt = locked.Lock.LockedAt
	if err := s.SaveWorkspace(t.Context(), locked, fence); err != nil {
		t.Fatalf("lock workspace: %v", err)
	}

	return locked, fence
}
