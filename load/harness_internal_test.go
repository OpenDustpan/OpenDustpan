package main

// The tests stay white-box (package main): a main package cannot be
// imported, so package main_test could not reach the harness. The command
// packages follow the same form (cmd/opendustpan/*_test.go).

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
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
	cfg.JournalEntries = []int{2, 6}
	cfg.JournalBatches = 3
	cfg.AccessRequests = 5

	return cfg
}

// smokeCheckpointBytes fits one small deployment in the smoke run.
const smokeCheckpointBytes = 8192

// openSmokeServer starts one SQLite instance for the smoke run.
func openSmokeServer(t *testing.T, ns string) servers {
	t.Helper()

	ctx := t.Context()
	logger := slog.New(slog.DiscardHandler)

	served, cleanup, err := openServers(ctx, logger, t.TempDir(), ns)
	if err != nil {
		t.Fatalf("open servers: %v", err)
	}

	t.Cleanup(func() {
		cleanup(context.WithoutCancel(ctx))
	})

	return served
}

func TestHarnessSmoke(t *testing.T) {
	t.Parallel()

	cfg := smokeConfig()
	served := openSmokeServer(t, cfg.Namespace)

	report, err := Execute(t.Context(), cfg, served)
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

	checkUpdateRuns(t, report, cfg.ContentionAttempts)
	checkJournal(t, report.Journal, cfg)
	checkAccess(t, report.Access)

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
	served := openSmokeServer(t, "load-fixtures")
	fx := newTarget(served.urls, harnessToken(), "load-fixtures")

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

// checkUpdateRuns checks both update runs: on one stack the stack lock lets
// one update start, and on one stack per attempt every update starts.
func checkUpdateRuns(t *testing.T, report Report, attempts int) {
	t.Helper()

	if report.UpdateConflicts.Started != 1 {
		t.Fatalf("update conflicts started = %d, want 1: %+v", report.UpdateConflicts.Started, report.UpdateConflicts)
	}

	if report.NamespaceUpdates.Started != attempts {
		t.Fatalf("namespace updates started = %d, want %d: %+v",
			report.NamespaceUpdates.Started, attempts, report.NamespaceUpdates)
	}
}

// checkJournal checks the journal runs. A release build answers journal
// version 0 and the harness then sends no batch; a build with the
// journalrecording tag runs one update for each entry count.
func checkJournal(t *testing.T, got JournalResult, cfg Config) {
	t.Helper()

	if !got.Supported {
		if len(got.Runs) != 0 {
			t.Fatalf("journal runs = %d without support, want 0", len(got.Runs))
		}

		return
	}

	if len(got.Runs) != len(cfg.JournalEntries) {
		t.Fatalf("journal runs = %d, want %d", len(got.Runs), len(cfg.JournalEntries))
	}

	for i, run := range got.Runs {
		if run.EntriesPerBatch != cfg.JournalEntries[i] || run.Batches != cfg.JournalBatches {
			t.Fatalf("journal run %d = %+v, want %d entries in %d batches",
				i, run, cfg.JournalEntries[i], cfg.JournalBatches)
		}

		if run.AppendP50Ms <= 0 || run.CompleteMs <= 0 || run.BatchBytes <= 0 {
			t.Fatalf("journal run %d not measured: %+v", i, run)
		}
	}
}

// checkAccess checks that every access timing ran and that the refused
// requests were refused.
func checkAccess(t *testing.T, got AccessResult) {
	t.Helper()

	for _, route := range []AccessRoute{got.RunnerRoute, got.JobsRoute} {
		if route.GateOffP50Ms <= 0 || route.GateNoListP50Ms <= 0 || route.GateListP50Ms <= 0 ||
			route.GateRefusedP50Ms <= 0 {
			t.Fatalf("access timing missing: %+v", route)
		}

		if route.RefusedStatus != http.StatusNotFound && route.RefusedStatus != http.StatusForbidden {
			t.Fatalf("refused status = %d, want 404 or 403: %+v", route.RefusedStatus, route)
		}
	}

	if got.ListEntries != len(allowlistPrefixes()) {
		t.Fatalf("list entries = %d, want %d", got.ListEntries, len(allowlistPrefixes()))
	}
}

// TestRefusalComesFromTheList proves the refused timing measures the list:
// the deny namespace answers 200 on the server without IP allowlists, and
// the same request is refused on the server that checks them, while the list
// namespace, whose list holds the client, is allowed there.
func TestRefusalComesFromTheList(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	served := openSmokeServer(t, "load-refusal")
	fx := newTarget(served.urls, harnessToken(), "load-refusal")
	deny := accessPathsOf(served.denyNS, served.denyJobID)
	allowed := accessPathsOf(served.listNS, served.listJobID)

	for _, path := range []string{deny.runner, deny.jobs} {
		if _, err := fx.timeGet(ctx, served.urls[0]+path, 1, []int{http.StatusOK}); err != nil {
			t.Fatalf("%s without IP allowlists: %v", path, err)
		}

		if _, err := fx.timeGet(ctx, served.gateURL+path, 1, refusedStatuses()); err != nil {
			t.Fatalf("%s with the refusing list: %v", path, err)
		}
	}

	for _, path := range []string{allowed.runner, allowed.jobs} {
		if _, err := fx.timeGet(ctx, served.gateURL+path, 1, []int{http.StatusOK}); err != nil {
			t.Fatalf("%s with the list that holds the client: %v", path, err)
		}
	}
}

func TestJournalEntriesValid(t *testing.T) {
	t.Parallel()

	for _, ok := range [][]int{nil, {2}, {2, 20, 200}} {
		if err := journalEntriesValid(ok); err != nil {
			t.Fatalf("journalEntriesValid(%v) = %v, want nil", ok, err)
		}
	}

	for _, bad := range [][]int{{0}, {1}, {3}, {-2}, {2, 5}} {
		if err := journalEntriesValid(bad); !errors.Is(err, errBadJournalEntries) {
			t.Fatalf("journalEntriesValid(%v) = %v, want errBadJournalEntries", bad, err)
		}
	}
}
