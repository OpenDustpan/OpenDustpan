package sqlite

// Test hook for the write-ahead log guard: it makes the guard interval,
// threshold, warn period and backoff cap of one store small, so black-box
// tests force checkpoints, backoff and warnings without writing tens of
// megabytes or waiting 30 s.
// This file compiles into the test binary only: nothing here ships in
// production. The export_internal_test.go name keeps the white-box package
// under the testpackage exception in .golangci.yml; the conformance tests
// themselves stay in package sqlite_test.

import (
	"context"
	"fmt"
	"time"
)

// WithWALGuardForTest sets the guard interval, threshold, warn period and
// backoff cap of the store that it opens, and makes that store collect its
// guard ticks: WALGuardTicksForTest reports one entry per past-threshold tick.
// Other stores keep the production values, so a test that tunes its own store
// changes no other store in the test binary.
func WithWALGuardForTest(
	interval time.Duration,
	thresholdBytes int64,
	warnAfter time.Duration,
	maxBackoff time.Duration,
) Option {
	return func(s *Store) {
		s.walGuardInterval = interval
		s.walGuardThresholdBytes = thresholdBytes
		s.walGuardWarnAfter = warnAfter
		s.walGuardBackoffCap = maxBackoff
		s.walGuardCollect = true
	}
}

// WALGuardTick describes one past-threshold guard tick for tests: the
// database path of the store that ticked, when the truncate finished, how
// long the tick took, whether it succeeded, whether the tick saw a slow busy
// attempt, the slow and total attempt counts, and the wait the guard slept
// before the tick.
type WALGuardTick struct {
	Path         string
	At           time.Time
	Waited       time.Duration
	Elapsed      time.Duration
	Succeeded    bool
	SlowBusy     bool
	SlowAttempts int
	Attempts     int
}

// WALGuardTicksForTest returns one entry per past-threshold guard tick of s
// since s opened with WithWALGuardForTest. The tick buffer has its own lock,
// so a ticking guard never races the read.
func WALGuardTicksForTest(s *Store) []WALGuardTick {
	s.walGuardTicksMu.Lock()
	defer s.walGuardTicksMu.Unlock()

	out := make([]WALGuardTick, 0, len(s.walGuardTicks))

	for _, tick := range s.walGuardTicks {
		out = append(out, WALGuardTick{
			Path:         tick.path,
			At:           tick.at,
			Waited:       tick.waited,
			Elapsed:      tick.elapsed,
			Succeeded:    tick.succeeded,
			SlowBusy:     tick.slowBusy,
			SlowAttempts: tick.slowAttempts,
			Attempts:     tick.attempts,
		})
	}

	return out
}

// WALPragmas holds the runtime write-ahead log pragmas of a store: the
// automatic checkpoint size in frames and the journal size limit in bytes.
type WALPragmas struct {
	AutoCheckpoint   int64
	JournalSizeLimit int64
}

// WALPragmasForTest reads the runtime write-ahead log pragmas of s.
func WALPragmasForTest(ctx context.Context, s *Store) (WALPragmas, error) {
	var pragmas WALPragmas

	s.mu.RLock()
	defer s.mu.RUnlock()

	autoRow := s.conn.QueryRowContext(ctx, `PRAGMA wal_autocheckpoint`)
	if scanErr := autoRow.Scan(&pragmas.AutoCheckpoint); scanErr != nil {
		return WALPragmas{}, fmt.Errorf("read wal_autocheckpoint: %w", scanErr)
	}

	limitRow := s.conn.QueryRowContext(ctx, `PRAGMA journal_size_limit`)
	if scanErr := limitRow.Scan(&pragmas.JournalSizeLimit); scanErr != nil {
		return WALPragmas{}, fmt.Errorf("read journal_size_limit: %w", scanErr)
	}

	return pragmas, nil
}
