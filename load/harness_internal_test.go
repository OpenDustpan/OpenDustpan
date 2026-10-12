package main

// The tests stay white-box (package main): a main package cannot be
// imported, so package main_test could not reach the harness. The command
// packages follow the same form (cmd/opendustpan/*_test.go).

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

// smokeConfig answers a tiny run: one small checkpoint and import, a few
// units, and a few lock rounds. It proves the sequences still replay.
func smokeConfig() Config {
	cfg := DefaultConfig()
	cfg.Namespace = "load-smoke"
	cfg.Token = harnessToken()
	cfg.Store = "sqlite"
	cfg.Units = 12
	cfg.CheckpointSizes = []int{smokeCheckpointBytes}
	cfg.ImportSizes = []int{smokeCheckpointBytes}
	cfg.LockGoroutines = 4
	cfg.LockRounds = 2
	cfg.ListReps = 2
	cfg.SeedWorkers = 4
	cfg.ContentionAttempts = 8

	return cfg
}

// smokeCheckpointBytes fits one small deployment in the smoke run.
const smokeCheckpointBytes = 8192

// openSmokeServer starts one SQLite instance for the smoke run.
func openSmokeServer(t *testing.T, ns string) []string {
	t.Helper()

	ctx := t.Context()
	logger := slog.New(slog.DiscardHandler)

	urls, cleanup, err := openServers(ctx, logger, t.TempDir(), ns)
	if err != nil {
		t.Fatalf("open servers: %v", err)
	}

	t.Cleanup(func() {
		cleanup(context.WithoutCancel(ctx))
	})

	return urls
}

func TestHarnessSmoke(t *testing.T) {
	t.Parallel()

	cfg := smokeConfig()
	urls := openSmokeServer(t, cfg.Namespace)

	report, err := Execute(t.Context(), cfg, urls)
	if err != nil {
		t.Fatalf("run harness: %v", err)
	}

	if len(report.Checkpoints) != 1 {
		t.Fatalf("checkpoints = %d, want 1", len(report.Checkpoints))
	}

	if len(report.Imports) != 1 {
		t.Fatalf("imports = %d, want 1", len(report.Imports))
	}

	if report.Checkpoints[0].FullMs <= 0 || report.Checkpoints[0].DeltaMs <= 0 {
		t.Fatalf("checkpoint latencies not measured: %+v", report.Checkpoints[0])
	}

	if report.Lists.UnitsListed < cfg.Units {
		t.Fatalf("units listed = %d, want at least %d", report.Lists.UnitsListed, cfg.Units)
	}

	if report.WorkspaceLocks.Locks == 0 {
		t.Fatalf("no lock won: %+v", report.WorkspaceLocks)
	}

	if report.Updates.Started == 0 {
		t.Fatalf("no update started: %+v", report.Updates)
	}

	wantInstances := 1
	if os.Getenv(postgresURLEnv) != "" {
		wantInstances = 2
	}

	if report.Database.Instances != wantInstances || report.Database.SecondPresent != (wantInstances > 1) {
		t.Fatalf("database instances = %+v, want %d", report.Database, wantInstances)
	}
}

// TestRecordedFixtures replays the checked-in recorded bodies: the small
// deployment through stack import, and the small state through a workspace
// upload with the serial and lineage the fixture declares.
func TestRecordedFixtures(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	urls := openSmokeServer(t, "load-fixtures")
	fx := newTarget(urls, harnessToken(), "load-fixtures")

	deployment, deploymentErr := os.ReadFile(filepath.Join("testdata", "deployment.json"))
	if deploymentErr != nil {
		t.Fatalf("read deployment fixture: %v", deploymentErr)
	}

	state, stateErr := os.ReadFile(filepath.Join("testdata", "state.json"))
	if stateErr != nil {
		t.Fatalf("read state fixture: %v", stateErr)
	}

	if stackErr := fx.createStack(ctx, "fixture-import"); stackErr != nil {
		t.Fatalf("create stack: %v", stackErr)
	}

	if _, importErr := fx.importStack(ctx, "fixture-import", deployment); importErr != nil {
		t.Fatalf("import fixture: %v", importErr)
	}

	id, workspaceErr := fx.createWorkspace(ctx, "fixture-ws")
	if workspaceErr != nil {
		t.Fatalf("create workspace: %v", workspaceErr)
	}

	if _, lockErr := fx.lockWorkspace(ctx, id); lockErr != nil {
		t.Fatalf("lock workspace: %v", lockErr)
	}

	// The fixture declares serial 1, so the create declares serial 1 too.
	if uploadErr := fx.uploadState(ctx, id, state, 1); uploadErr != nil {
		t.Fatalf("upload fixture state: %v", uploadErr)
	}

	if unlockErr := fx.unlockWorkspace(ctx, id); unlockErr != nil {
		t.Fatalf("unlock workspace: %v", unlockErr)
	}
}
