package sqlite_test

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/idempotency"
	"opendustpan.org/opendustpan/pkg/idempotency/idempotencytest"
	"opendustpan.org/opendustpan/pkg/maintenance"
)

// TestReducingBeginWritesInMaintenanceMode proves that the record of a
// reducing route is written in maintenance mode, and that the record of any
// other route is refused there. The first check is the one that a route
// that ends access needs. The second check is the guard: it fails if the
// option stops being honored, and it fails if a record of an ordinary route
// starts to pass.
func TestReducingBeginWritesInMaintenanceMode(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "dustpan.db")
	copyTemplate(t, path)

	s := openPath(t, path)
	records := s.Idempotency(idempotencytest.TestWrapper(t, 1))
	ctx := t.Context()
	at := time.Date(2026, time.November, 16, 12, 0, 0, 0, time.UTC)

	if err := s.SetState(ctx, maintenance.StateMaintenance); err != nil {
		t.Fatalf("enter maintenance mode: %v", err)
	}

	ordinary := idempotency.Ref{Principal: "alice", Method: "POST", Path: "/api/dustpan/v1/p", Key: "create"}
	if _, err := records.Begin(ctx, ordinary, []byte("body"), at); !errors.Is(err, maintenance.ErrInMaintenance) {
		t.Fatalf("ordinary begin in maintenance mode = %v, want maintenance.ErrInMaintenance", err)
	}

	reducing := idempotency.Ref{
		Principal: "alice", Method: "DELETE", Path: "/api/dustpan/v1/me/tokens/t1", Key: "revoke",
	}

	started, err := records.Begin(ctx, reducing, []byte("body"), at, idempotency.Reducing)
	if err != nil {
		t.Fatalf("reducing begin in maintenance mode: %v", err)
	}

	if started.Outcome != idempotency.Started || started.Token == "" {
		t.Fatalf("reducing begin = outcome %d, token %q, want a started record", started.Outcome, started.Token)
	}

	// Abandon frees the key in maintenance mode too, so a failed revoke can
	// be retried after the restore.
	if err = records.Abandon(ctx, reducing, started.Token); err != nil {
		t.Fatalf("abandon in maintenance mode: %v", err)
	}

	again, err := records.Begin(ctx, reducing, []byte("body"), at, idempotency.Reducing)
	if err != nil || again.Outcome != idempotency.Started {
		t.Fatalf("begin after abandon = outcome %d, err %v, want started", again.Outcome, err)
	}
}
