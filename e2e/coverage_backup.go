//go:build e2e

package e2e

import (
	"context"
	"net/http"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/storecheck"
	blobfs "opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/durable/durabletest"
	"opendustpan.org/opendustpan/pkg/maintenance"
)

// coverageMaintenance answers normal state and cancels nothing: the route
// table needs a maintenance service, and the coverage gate never serves a
// request.
type coverageMaintenance struct{}

func (coverageMaintenance) Get(_ context.Context) (maintenance.State, error) {
	return maintenance.StateNormal, nil
}

func (coverageMaintenance) Enter(_ context.Context, _ bool) (maintenance.EnterResult, error) {
	return maintenance.EnterResult{Canceled: nil}, nil
}

func (coverageMaintenance) Leave(context.Context) error { return nil }

// coverageStoreCheckRecords holds no store check row: the route table needs a
// store check service to mount the routes, and the coverage gate never serves
// a request.
type coverageStoreCheckRecords struct{}

func (coverageStoreCheckRecords) StartCheck(
	context.Context, storecheck.Record, durable.StartRequest, time.Time,
) error {
	return storecheck.ErrRunning
}

func (coverageStoreCheckRecords) GetCheck(context.Context, storecheck.ID) (storecheck.Record, error) {
	return storecheck.Record{}, storecheck.ErrNotFound
}

func (coverageStoreCheckRecords) ListChecks(context.Context) ([]storecheck.Record, error) {
	return nil, nil
}

func (coverageStoreCheckRecords) FinishCheck(
	context.Context, durable.StepFence, storecheck.ID, storecheck.Finish,
) error {
	return storecheck.ErrNotFound
}

func (coverageStoreCheckRecords) FailEndedChecks(context.Context, time.Time) (int, error) {
	return 0, nil
}

// coverageStoreChecks returns the store check service of the route table.
func coverageStoreChecks(tb testing.TB) *storecheck.Service {
	tb.Helper()

	blobs, err := blobfs.Open(tb.TempDir())
	if err != nil {
		tb.Fatalf("blob store: %v", err)
	}

	tb.Cleanup(func() {
		if closeErr := blobs.Close(); closeErr != nil {
			tb.Errorf("close blob store: %v", closeErr)
		}
	})

	svc, err := storecheck.NewService(storecheck.Config{
		Records: coverageStoreCheckRecords{}, Operations: durabletest.NewMem().Store(), Blobs: blobs, Backup: nil,
		Audit: coverageAuditStore{}, Admit: nil,
		Limits: storecheck.Limits{MaxObjects: 0, MaxTotalBytes: 0, MaxObjectBytes: 0, Timeout: 0, Cooldown: 0},
		Now:    nil, Owner: "", Lease: 0, Logger: nil,
	})
	if err != nil {
		tb.Fatalf("store check service: %v", err)
	}

	return svc
}

// backupCoveringTest names the test that proves the backup routes instead of
// a CLI: no CLI version in the matrix calls them.
const backupCoveringTest = "TestBackups"

// backupAllowEntries returns the operator backup routes that no CLI calls.
// The harness steps of backupCoveringTest drive them directly.
func backupAllowEntries() []AllowEntry {
	return []AllowEntry{
		{
			Method: http.MethodPost, Path: "/api/server/backups",
			CoveringTest: backupCoveringTest,
			Reason: "Operator route: no CLI starts a backup. The harness step " +
				"starts one and checks the second start conflicts.",
		},
		{
			Method: http.MethodGet, Path: "/api/server/backups",
			CoveringTest: backupCoveringTest,
			Reason: "Operator route: no CLI lists backups. The harness step " +
				"lists them newest first.",
		},
		{
			Method: http.MethodGet, Path: "/api/server/backups/{id}",
			CoveringTest: backupCoveringTest,
			Reason: "Operator route: no CLI reads a backup. The harness step " +
				"polls it until it succeeds and checks the manifest.",
		},
		{
			Method: http.MethodGet, Path: "/api/server/backups/{id}/check",
			CoveringTest: "TestBackupCheck",
			Reason: "Operator route: no CLI in the matrix calls it. The " +
				"opendustpan backup check binary calls it with Go-http-client, " +
				"which never counts as CLI traffic, and TestBackupCheck proves " +
				"it with the check handler tests in check_test.go.",
		},
		{
			Method: http.MethodGet, Path: "/api/dustpan/v1/upgrade-check",
			CoveringTest: "TestUpgradeCheck",
			Reason: "Operator route: no CLI in the matrix calls it. The " +
				"opendustpan upgrade check binary calls it with Go-http-client, " +
				"which never counts as CLI traffic, and TestUpgradeCheck proves " +
				"it with the upgrade handler tests in upgrade_test.go.",
		},
	}
}

// storeCheckCoveringTest names the test that proves the store check routes
// with the binary command: no CLI of the matrix calls them.
const storeCheckCoveringTest = "TestStoreCheck"

// storeCheckAllowEntries returns the store check routes. The opendustpan
// store check binary calls them with Go-http-client, which never counts as CLI
// traffic, so the steps of storeCheckCoveringTest drive them.
func storeCheckAllowEntries() []AllowEntry {
	return []AllowEntry{
		{
			Method: http.MethodPost, Path: "/api/dustpan/v1/store-checks",
			CoveringTest: storeCheckCoveringTest,
			Reason: "Operator route: no CLI of the matrix starts a store check. The opendustpan store " +
				"check binary starts one, and the harness steps check the cooldown and the refusal of a target " +
				"that is not on the list.",
		},
		{
			Method: http.MethodGet, Path: "/api/dustpan/v1/store-checks",
			CoveringTest: storeCheckCoveringTest,
			Reason: "Operator route: no CLI of the matrix lists store checks. The harness step lists them " +
				"newest first.",
		},
		{
			Method: http.MethodGet, Path: "/api/dustpan/v1/store-checks/{id}",
			CoveringTest: storeCheckCoveringTest,
			Reason: "Operator route: no CLI of the matrix reads a store check. The opendustpan store check " +
				"binary polls it until the check ends, and the harness step reads the report.",
		},
	}
}

// maintenanceCoveringTest names the test that proves the maintenance routes
// instead of a CLI: no CLI version in the matrix calls them.
const maintenanceCoveringTest = "TestMaintenance"

// maintenanceAllowEntries returns the operator maintenance routes that no
// CLI calls. The harness steps of maintenanceCoveringTest drive them
// directly: entering, refusing active work, force-canceling it, and leaving.
func maintenanceAllowEntries() []AllowEntry {
	return []AllowEntry{
		{
			Method: http.MethodGet, Path: "/api/dustpan/v1/maintenance",
			CoveringTest: maintenanceCoveringTest,
			Reason: "Operator route: no CLI reads the installation state. " +
				"The harness step reads it before and after the enter.",
		},
		{
			Method: http.MethodPost, Path: "/api/dustpan/v1/maintenance/enter",
			CoveringTest: maintenanceCoveringTest,
			Reason: "Operator route: no CLI enters maintenance mode. The " +
				"harness step refuses active work, then force-cancels it.",
		},
		{
			Method: http.MethodPost, Path: "/api/dustpan/v1/maintenance/leave",
			CoveringTest: maintenanceCoveringTest,
			Reason: "Operator route: no CLI leaves maintenance mode. The " +
				"harness step leaves after the 503 checks.",
		},
	}
}

// restoreCoveringTest names the test that proves the restore routes instead
// of a CLI: the restore command calls them with the Go User-Agent, which
// never counts toward route coverage.
const restoreCoveringTest = "TestBackupRestore"

// restoreAllowEntries returns the operator restore routes that only the
// restore command calls. The steps of restoreCoveringTest drive them through
// the new command: restore into an empty installation, restore over live
// data, check-only, a failed check, a fenced lease, and an undo.
func restoreAllowEntries() []AllowEntry {
	return []AllowEntry{
		{
			Method: http.MethodPost, Path: "/api/server/restores",
			CoveringTest: restoreCoveringTest,
			Reason: "Operator route: only the restore command starts a restore. " +
				"The harness step starts one through it and checks the second start conflicts.",
		},
		{
			Method: http.MethodGet, Path: "/api/server/restores",
			CoveringTest: restoreCoveringTest,
			Reason: "Operator route: only the restore command lists restores. " +
				"The harness step lists them to find the backup taken before the restore.",
		},
		{
			Method: http.MethodGet, Path: "/api/server/restores/{id}",
			CoveringTest: restoreCoveringTest,
			Reason: "Operator route: only the restore command reads a restore. " +
				"The harness step polls it until it succeeds and checks the manifest.",
		},
		{
			Method: http.MethodPost, Path: "/api/server/restores/{id}/cancel",
			CoveringTest: "TestCancelRestoreBeforeTheSwap",
			Reason: "Operator route: no CLI command cancels a restore. The tests of the installation API " +
				"cancel a restore before the swap, after the swap and after the end.",
		},
	}
}
