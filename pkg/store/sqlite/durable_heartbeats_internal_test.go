package sqlite

import (
	"path/filepath"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/durable"
)

// TestBeatDeletesStaleBeats proves that a beat deletes the beats that are
// older than the retention time on the clock of the store, and keeps the
// others.
func TestBeatDeletesStaleBeats(t *testing.T) {
	t.Parallel()

	s, err := Open(t.Context(), filepath.Join(t.TempDir(), "dustpan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := s.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	nowMs := time.Now().UnixMilli()
	oldMs := nowMs - durationMs(durable.HeartbeatRetention) - 60_000
	freshMs := nowMs - durationMs(durable.HeartbeatRetention) + 60_000

	const insert = `INSERT INTO durable_heartbeats (owner, at_ms) VALUES ('dead', ?), ('alive', ?)`

	_, err = s.conn.ExecContext(t.Context(), insert, oldMs, freshMs)
	if err != nil {
		t.Fatalf("insert beats: %v", err)
	}

	if err = s.DurableOperations().Beat(t.Context(), "new", time.Minute); err != nil {
		t.Fatalf("Beat: %v", err)
	}

	var kept, dead int

	err = s.conn.QueryRowContext(t.Context(),
		`SELECT COUNT(*) FROM durable_heartbeats WHERE owner IN ('alive', 'new')`).Scan(&kept)
	if err != nil {
		t.Fatalf("count kept beats: %v", err)
	}

	err = s.conn.QueryRowContext(t.Context(),
		`SELECT COUNT(*) FROM durable_heartbeats WHERE owner = 'dead'`).Scan(&dead)
	if err != nil {
		t.Fatalf("count stale beats: %v", err)
	}

	if kept != 2 || dead != 0 {
		t.Errorf("kept beats = %d, stale beats = %d, want 2 and 0", kept, dead)
	}
}
