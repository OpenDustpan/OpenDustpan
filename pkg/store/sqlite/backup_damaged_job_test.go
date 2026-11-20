package sqlite_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// damagedJobRow is one row of the jobs table that the server cannot read.
type damagedJobRow struct {
	id   string
	unit string
	body string
}

// insertDamagedJobs makes the namespace acme in st and writes each row of rows
// to its jobs table through a second handle, as a damaged database holds it.
func insertDamagedJobs(t *testing.T, st *backupStore, rows []damagedJobRow) {
	t.Helper()

	ns := namespace.Namespace{
		ID: namespace.NewID(), CreatedAt: time.UnixMilli(1_700_000_000_000).UTC(), Name: "acme",
		PrimaryInstallation: "local", FailoverVersion: 1,
		Overrides: namespace.Overrides{Forced: false, Unlocked: false},
	}
	if err := st.CreateNamespace(t.Context(), ns); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	conn, err := sql.Open("sqlite", "file:"+st.path+"?_pragma=busy_timeout(10000)")
	if err != nil {
		t.Fatalf("open the database: %v", err)
	}

	defer func() {
		if closeErr := conn.Close(); closeErr != nil {
			t.Errorf("close the second handle: %v", closeErr)
		}
	}()

	for i, row := range rows {
		_, execErr := conn.ExecContext(t.Context(),
			`INSERT INTO jobs (id, namespace_id, unit_id, seq, state, wait, current_phase, current_changes,
current_operation, idempotency_key, version, body, created_at_ms, updated_at_ms, state_changed_at_ms)
VALUES (?, ?, ?, ?, 'queued', '', 0, 0, '', '', 1, ?, 1700000000000, 1700000000000, 1700000000000)`,
			row.id, string(ns.ID), row.unit, i+1, row.body)
		if execErr != nil {
			t.Fatalf("insert job %s: %v", row.id, execErr)
		}
	}
}

// reasonNotJSON is the reason that the job store gives for a body that is not
// JSON.
const reasonNotJSON = "the body is not JSON"

// runDamagedBackup makes a store with rows, backs it up, and returns the
// backup ID with the backup service, and a restore service over an empty
// store.
func runDamagedBackup(
	t *testing.T, rows []damagedJobRow,
) (backup.ID, *backup.Service, *backup.RestoreService) {
	t.Helper()

	blobs := openUnmanagedBlobs(t)
	source := openUnmanagedBackupStore(t)
	insertDamagedJobs(t, source, rows)

	backups, _ := unmanagedBackupServices(t, source, blobs.live, blobs.target)

	id := backup.ID(ident.NewID("bkp-"))
	if _, err := backups.Run(t.Context(), id); err != nil {
		t.Fatalf("run backup %s: %v", id, err)
	}

	dest := openUnmanagedBackupStore(t)
	_, restores := unmanagedBackupServices(t, dest, blobs.live, blobs.target)

	return id, backups, restores
}

// failedRestore starts a restore of id and runs it to its end. It returns the
// row, which must be failed.
func failedRestore(t *testing.T, restores *backup.RestoreService, id backup.ID, checkOnly bool) backup.RestoreRecord {
	t.Helper()

	restoreID := backup.MintRestoreID()
	req := backup.RestoreRequest{
		BackupID: id, PreBackupID: "", CheckOnly: checkOnly, Force: false, RequestedBy: "tester",
		KeepAccessTables: false,
	}

	if err := restores.Start(t.Context(), restoreID, req); err != nil {
		t.Fatalf("start restore: %v", err)
	}

	return waitFailedRestore(t.Context(), t, restores, restoreID)
}

// waitFailedRestore runs the due steps of restore id until its row ends.
func waitFailedRestore(
	ctx context.Context, t *testing.T, restores *backup.RestoreService, id backup.RestoreID,
) backup.RestoreRecord {
	t.Helper()

	for range 2*len(backup.RestoreSteps()) + 10 {
		row, err := restores.Get(ctx, id)
		if err != nil {
			t.Fatalf("get restore %s: %v", id, err)
		}

		if row.State == backup.RestoreFailed {
			return row
		}

		if row.State == backup.RestoreSucceeded {
			t.Fatalf("restore %s succeeded, want failed", id)
		}

		if runErr := restores.RunOnce(ctx); runErr != nil && !errors.Is(runErr, durable.ErrNoWork) {
			t.Fatalf("run restore %s: %v", id, runErr)
		}
	}

	t.Errorf("restore %s never ended", id)

	var none backup.RestoreRecord

	return none
}

// TestBackupCheckReportsADamagedJobRow proves that backup check and a restore
// agree on a backup with a damaged job row: the check fails with the job ID,
// the unit and the reason, and a restore, with and without check-only,
// refuses it with the same text. This test fails when the check of backup
// check does not read the job rows.
func TestBackupCheckReportsADamagedJobRow(t *testing.T) {
	t.Parallel()

	rows := []damagedJobRow{{id: "job-dmg1", unit: "ws-1", body: "not json"}}
	id, backups, restores := runDamagedBackup(t, rows)

	want := "damaged job job-dmg1 of unit ws-1: " + reasonNotJSON

	checkErr := backups.Check(t.Context(), id)

	damaged, ok := errors.AsType[*backup.DamagedJobError](checkErr)
	if checkErr == nil || !ok || damaged == nil {
		t.Fatalf("check = %v, want a damaged job error", checkErr)
	}

	if !errors.Is(checkErr, backup.ErrCorrupt) || !strings.Contains(checkErr.Error(), want) {
		t.Fatalf("check = %q, want ErrCorrupt with %q", checkErr, want)
	}

	for _, checkOnly := range []bool{true, false} {
		row := failedRestore(t, restores, id, checkOnly)

		if !strings.HasPrefix(row.Error, want) || !strings.HasSuffix(row.Error, backup.ErrCorrupt.Error()) {
			t.Fatalf("restore (check only %v) error = %q, want the text %q", checkOnly, row.Error, want)
		}

		// The restore and the check name the same row with the same text.
		if !strings.Contains(checkErr.Error(), row.Error) {
			t.Fatalf("check = %q does not hold the restore text %q", checkErr, row.Error)
		}
	}
}

// TestBackupCheckReportsEachKindOfDamagedJobRow proves the check on the two
// bodies of the report: text that is not JSON, and an empty object.
func TestBackupCheckReportsEachKindOfDamagedJobRow(t *testing.T) {
	t.Parallel()

	rows := []damagedJobRow{
		{id: "job-dmg1", unit: "ws-1", body: "not json"},
		{id: "job-dmg2", unit: "ws-1", body: "{}"},
	}
	id, backups, restores := runDamagedBackup(t, rows)

	checkErr := backups.Check(t.Context(), id)
	if checkErr == nil || !errors.Is(checkErr, backup.ErrCorrupt) ||
		!strings.Contains(checkErr.Error(), backup.DamagedJobPrefix+"job-dmg") {
		t.Fatalf("check = %v, want ErrCorrupt that names a damaged job", checkErr)
	}

	row := failedRestore(t, restores, id, true)
	if !strings.HasPrefix(row.Error, backup.DamagedJobPrefix+"job-dmg") {
		t.Fatalf("restore error = %q, want a damaged job", row.Error)
	}
}
