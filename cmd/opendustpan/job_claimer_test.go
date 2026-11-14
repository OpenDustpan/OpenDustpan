package main

import (
	"log/slog"
	"path/filepath"
	"testing"
)

// TestJobStoreOptionHasTheClaimer proves that serve gives the job store and
// the job claimer to the server: the options for a store that keeps jobs are
// two, the store and the claimer. Without the claimer, a poll of a runner of
// a pool for jobs finds no work. The poll itself is proved in package
// runnerapi, with the same claimer.
func TestJobStoreOptionHasTheClaimer(t *testing.T) {
	t.Parallel()

	db := "sqlite:" + filepath.Join(t.TempDir(), "dustpan.db")

	st, err := openStore(t.Context(), db, slog.New(slog.DiscardHandler), openMigrate, "")
	if err != nil {
		t.Fatalf("open the database: %v", err)
	}

	defer st.close()

	if got := len(jobStoreOption(st, slog.New(slog.DiscardHandler))); got != 2 {
		t.Fatalf("job options for a store with jobs = %d, want 2: the job store and the job claimer", got)
	}
}
