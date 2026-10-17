package postgres

import (
	"bytes"
	"context"
	"testing"

	"opendustpan.org/opendustpan/pkg/backup"
)

// TestRestoreKeepsTheLiveSchedules proves that both swaps of a restore keep
// the live schedules: the staged schema holds an older schedule, the live
// schema holds one with a later next run, and after the swap the live
// schema holds only the live schedule with its next run. A restored next
// run would fire a tick that already ran.
func TestRestoreKeepsTheLiveSchedules(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		apply func(ctx context.Context, s *Store) error
	}{
		{name: "replace the access tables", apply: func(ctx context.Context, s *Store) error {
			return s.ApplyStaging(ctx)
		}},
		{name: "keep the access tables", apply: func(ctx context.Context, s *Store) error {
			return s.ApplyStagingKeepingAccess(ctx)
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := t.Context()
			s := openKeepLockStore(t, testPostgresURL(t))
			stageEmptyBackup(t, s)

			staged := "INSERT INTO " + pgIdent(s.stagingSchema) + ".durable_schedules" +
				" (name, interval_ms, next_run_at_ms) VALUES ('from-backup', 1000, 3)"
			if _, err := s.pool.Exec(ctx, staged); err != nil {
				t.Fatalf("insert staged schedule: %v", err)
			}

			live := "INSERT INTO durable_schedules (name, interval_ms, next_run_at_ms, last_claimed_at_ms)" +
				" VALUES ('live', 1000, 5, 4)"
			if _, err := s.pool.Exec(ctx, live); err != nil {
				t.Fatalf("insert live schedule: %v", err)
			}

			if err := tc.apply(ctx, s); err != nil {
				t.Fatalf("apply staging: %v", err)
			}

			checkLiveSchedule(t, s)
		})
	}
}

// stageEmptyBackup exports the empty store s and stages the export. The
// export leaves the schedules out, so the summary counts no schedule rows.
func stageEmptyBackup(t *testing.T, s *Store) {
	t.Helper()

	ctx := t.Context()
	sink := &keepLockSink{files: map[string]*bytes.Buffer{}}

	sum, files, err := s.ExportPortable(ctx, sink.open)
	if err != nil {
		t.Fatalf("export portable: %v", err)
	}

	summary, err := s.StagePortable(ctx, sink.read, sum, files)
	if err != nil {
		t.Fatalf("stage portable: %v", err)
	}

	if count, ok := summary.RowCounts[backup.TableDurableSchedules]; ok {
		t.Fatalf("staged summary counts %d schedule rows, want no count: a backup leaves the schedules out", count)
	}
}

// checkLiveSchedule fails t unless the live schema of s holds only the live
// schedule, with the next run and the last claim that it had before the
// swap.
func checkLiveSchedule(t *testing.T, s *Store) {
	t.Helper()

	var (
		name        string
		nextRun     int64
		lastClaimed int64
		rows        int
	)

	read := "SELECT name, next_run_at_ms, COALESCE(last_claimed_at_ms, 0), (SELECT count(*) FROM durable_schedules)" +
		" FROM durable_schedules"
	if err := s.pool.QueryRow(t.Context(), read).Scan(&name, &nextRun, &lastClaimed, &rows); err != nil {
		t.Fatalf("read schedules after the restore: %v", err)
	}

	if name != "live" || nextRun != 5 || lastClaimed != 4 || rows != 1 {
		t.Fatalf("schedules after the restore = %q next run %d last claim %d (%d rows), "+
			"want only the live schedule with next run 5 and last claim 4", name, nextRun, lastClaimed, rows)
	}
}
