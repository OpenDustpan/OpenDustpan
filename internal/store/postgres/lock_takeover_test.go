package postgres_test

import (
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/pgtest"
	"opendustpan.org/opendustpan/internal/store/postgres"
	"opendustpan.org/opendustpan/pkg/workspace"
)

func TestCommittedLockSurvivesTakeover(t *testing.T) {
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

	afterExpiry := ws.Lock.LockedAt.Add(31 * time.Second)
	commit := postgres.NewStateCommitForTest(s, fence)
	lock := workspace.Lock{LockedAt: afterExpiry, ID: "lock-2", Holder: "bob", Reason: "takeover"}
	got, err := commit.AcquireLock(ctx, ws.ID, lock)
	if err != nil {
		t.Fatalf("AcquireLock takeover: %v", err)
	}
	if !got.Lock.Locked() || got.Lock.Holder != "bob" || got.Lock.ID != "lock-2" {
		t.Fatalf("lock after takeover = %+v, want bob/lock-2", got.Lock)
	}
}
