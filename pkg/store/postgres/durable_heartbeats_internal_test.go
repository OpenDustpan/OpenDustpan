package postgres

import (
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/durable"
)

// TestBeatDeletesStaleBeats proves that a beat deletes the beats that are
// older than the retention time on the clock of the database, and keeps the
// others.
func TestBeatDeletesStaleBeats(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := openKeepLockStore(t, testPostgresURL(t))

	nowMs := time.Now().UnixMilli()
	oldMs := nowMs - scheduleMs(durable.HeartbeatRetention) - 60_000
	freshMs := nowMs - scheduleMs(durable.HeartbeatRetention) + 60_000

	const insert = `INSERT INTO durable_heartbeats (owner, at_ms) VALUES ('dead', $1), ('alive', $2)`

	_, err := s.pool.Exec(ctx, insert, oldMs, freshMs)
	if err != nil {
		t.Fatalf("insert beats: %v", err)
	}

	if err = s.DurableOperations().Beat(ctx, "new"); err != nil {
		t.Fatalf("Beat: %v", err)
	}

	var kept, dead int

	err = s.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM durable_heartbeats WHERE owner IN ('alive', 'new')`).Scan(&kept)
	if err != nil {
		t.Fatalf("count kept beats: %v", err)
	}

	err = s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM durable_heartbeats WHERE owner = 'dead'`).Scan(&dead)
	if err != nil {
		t.Fatalf("count stale beats: %v", err)
	}

	if kept != 2 || dead != 0 {
		t.Errorf("kept beats = %d, stale beats = %d, want 2 and 0", kept, dead)
	}
}
