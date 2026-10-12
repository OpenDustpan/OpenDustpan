package sqlite

// Test hook for the write-ahead log guard: it makes the guard interval,
// threshold and warn period small, so black-box tests force checkpoints and
// warnings without writing tens of megabytes or waiting 30 s. This file
// compiles into the test binary only: nothing here ships in production. The
// _internal_test.go name keeps the white-box package under the testpackage
// exception in .golangci.yml; the conformance tests themselves stay in
// package sqlite_test.

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

// SetWALGuardForTest replaces the guard interval, threshold and warn period
// and returns a function that restores the previous values. Callers defer the
// restore, so one test never changes the guard of another. The swap holds the
// guard lock briefly, so a ticking guard never races the replacement.
func SetWALGuardForTest(interval time.Duration, thresholdBytes int64, warnAfter time.Duration) func() {
	walHookMu.Lock()

	walGuardMu.Lock()
	oldInterval, oldThreshold, oldWarnAfter := walGuardInterval, walGuardThresholdBytes, walGuardWarnAfter
	walGuardInterval, walGuardThresholdBytes, walGuardWarnAfter = interval, thresholdBytes, warnAfter
	walGuardMu.Unlock()

	return func() {
		walGuardMu.Lock()
		walGuardInterval, walGuardThresholdBytes, walGuardWarnAfter = oldInterval, oldThreshold, oldWarnAfter
		walGuardMu.Unlock()

		walHookMu.Unlock()
	}
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
