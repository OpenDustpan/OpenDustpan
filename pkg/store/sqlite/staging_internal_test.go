package sqlite

// Test-only helper for the staging conformance case and the discard-error
// test: it exposes the path of the current staging area, if any. This file
// compiles into the test binary only: nothing here ships in production. The
// _internal_test.go name keeps the white-box package under the testpackage
// exception in .golangci.yml; the conformance tests themselves stay in
// package sqlite_test.

import (
	"os"
	"path/filepath"
	"testing"
)

// StagingPathForTest returns the path of the kept staging file, or empty
// when no staging area exists.
func (s *Store) StagingPathForTest() string {
	s.stagingMu.Lock()
	defer s.stagingMu.Unlock()

	return s.stagingPath
}

// TestDiscardStagingReportsRemoveError blocks the staged path with a
// non-empty directory, so the remove fails: DiscardStaging must report the
// error and retain the staging path for retry. After removing the blocker,
// discarding succeeds and clears the path, matching the PostgreSQL adapter.
func TestDiscardStagingReportsRemoveError(t *testing.T) {
	t.Parallel()

	blocked := filepath.Join(t.TempDir(), "staging.db")

	if err := os.Mkdir(blocked, 0o700); err != nil {
		t.Fatalf("create blocker directory: %v", err)
	}

	if err := os.WriteFile(filepath.Join(blocked, "child"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write blocker child: %v", err)
	}

	// A staging-only handle with no database: DiscardStaging touches only the
	// staging path, never the connection.
	s := &Store{stagingPath: blocked} //nolint:exhaustruct_v5 // Test constructs a staging-only handle with no database.

	if err := s.DiscardStaging(t.Context()); err == nil {
		t.Fatal("discard a blocked staging area: err = nil, want the remove error")
	}

	if got := s.StagingPathForTest(); got != blocked {
		t.Fatalf("staging path after a failed discard = %q, want %q retained for retry", got, blocked)
	}

	if err := os.Remove(filepath.Join(blocked, "child")); err != nil {
		t.Fatalf("remove blocker child: %v", err)
	}

	if err := os.Remove(blocked); err != nil {
		t.Fatalf("remove blocker directory: %v", err)
	}

	if err := s.DiscardStaging(t.Context()); err != nil {
		t.Fatalf("discard after removing the blocker: %v", err)
	}

	if got := s.StagingPathForTest(); got != "" {
		t.Fatalf("staging path after discard = %q, want none", got)
	}
}
