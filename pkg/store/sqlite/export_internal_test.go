package sqlite

// Test hook for the write-ahead log guard: it makes the guard interval,
// threshold, warn period and backoff cap small, so black-box tests force
// checkpoints, backoff and warnings without writing tens of megabytes or
// waiting 30 s.
// This file compiles into the test binary only: nothing here ships in
// production. The export_internal_test.go name keeps the white-box package
// under the testpackage exception in .golangci.yml; the conformance tests
// themselves stay in package sqlite_test.

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// walHookMu serializes the tests that tune the guard: SetWALGuardForTest
// holds it from the swap until the returned restore runs, so the values of
// one test never leak into another. This file compiles into the test binary
// only: nothing here ships in production.
//
//nolint:gochecknoglobals // The tuning tests share one lock; each holds it only for its own body.
var walHookMu sync.Mutex

// SetWALGuardForTest replaces the guard interval, threshold, warn period and
// backoff cap and returns a function that restores the previous values. It
// also starts guard tick collection for the test: WALGuardTicksForTest
// reports one entry per past-threshold tick until the restore runs.
// Callers defer the restore, so one test never changes the guard of another.
// The swap holds the guard lock briefly, so a ticking guard never races the
// replacement.
func SetWALGuardForTest(
	interval time.Duration,
	thresholdBytes int64,
	warnAfter time.Duration,
	maxBackoff time.Duration,
) func() {
	walHookMu.Lock()

	walGuardMu.Lock()
	oldInterval, oldThreshold, oldWarnAfter, oldCap := walGuardInterval, walGuardThresholdBytes, walGuardWarnAfter,
		walGuardBackoffCap
	walGuardInterval, walGuardThresholdBytes, walGuardWarnAfter, walGuardBackoffCap = interval, thresholdBytes,
		warnAfter, maxBackoff
	walGuardMu.Unlock()

	walGuardTicksMu.Lock()
	walGuardTicks = nil
	walGuardCollect = true
	walGuardTicksMu.Unlock()

	return func() {
		walGuardTicksMu.Lock()
		walGuardTicks = nil
		walGuardCollect = false
		walGuardTicksMu.Unlock()

		walGuardMu.Lock()
		walGuardInterval, walGuardThresholdBytes, walGuardWarnAfter, walGuardBackoffCap = oldInterval, oldThreshold,
			oldWarnAfter, oldCap
		walGuardMu.Unlock()

		walHookMu.Unlock()
	}
}

// WALGuardTick describes one past-threshold guard tick for tests: when the
// truncate finished, how long the tick took, whether it succeeded, whether
// the tick saw a slow busy attempt, the slow and total attempt counts, and
// the wait the guard slept before the tick.
type WALGuardTick struct {
	At           time.Time
	Waited       time.Duration
	Elapsed      time.Duration
	Succeeded    bool
	SlowBusy     bool
	SlowAttempts int
	Attempts     int
}

// WALGuardTicksForTest returns one entry per past-threshold guard tick since
// SetWALGuardForTest ran. Callers hold no hook lock: the buffer has its own
// lock, so a ticking guard never races the read.
func WALGuardTicksForTest() []WALGuardTick {
	walGuardTicksMu.Lock()
	defer walGuardTicksMu.Unlock()

	out := make([]WALGuardTick, 0, len(walGuardTicks))

	for _, tick := range walGuardTicks {
		out = append(out, WALGuardTick{
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
