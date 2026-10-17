package dustpan_test

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// auditChainKind names the durable operation of the migration.
const auditChainKind durable.Kind = "audit-chain-migration"

// migrationOps answers the migration operations of st.
func migrationOps(t *testing.T, st *sqlite.Store) []durable.Operation {
	t.Helper()

	ops, err := st.DurableOperations().List(t.Context(), durable.Filter{
		Kind:   auditChainKind,
		States: nil,
		Limit:  0,
	})
	if err != nil {
		t.Fatalf("list migration operations: %v", err)
	}

	return ops
}

// TestApprovalStartsOneMigrationOperation proves that an approval starts
// one durable operation, and that a second approval while it runs answers
// conflict and starts nothing more.
func TestApprovalStartsOneMigrationOperation(t *testing.T) {
	t.Parallel()

	st := openChainStore(t)
	ns := createChainNamespace(t, st)
	appendChainEntry(t, st, ns, "req-1")

	srv := runChainServer(t, st, audit.ChainOff)
	waitForRecord(t, st, func(rec audit.ChainRecord) bool { return rec.Pending })

	if status, body := srv.do(t, http.MethodPost, "/api/server/audit/chain"); status != http.StatusAccepted {
		t.Fatalf("approve: %d %s, want 202", status, body)
	}

	if got := migrationOps(t, st); len(got) != 1 {
		t.Fatalf("migration operations after the approval = %d, want 1", len(got))
	}

	if status, _ := srv.do(t, http.MethodPost, "/api/server/audit/chain"); status != http.StatusConflict {
		t.Fatalf("second approval: %d, want 409", status)
	}

	if got := migrationOps(t, st); len(got) != 1 {
		t.Fatalf("migration operations after two approvals = %d, want 1", len(got))
	}

	waitForRecord(t, st, func(rec audit.ChainRecord) bool {
		return rec.State == audit.ChainStateOff && !rec.Pending
	})
	waitForMaintenance(t, st, maintenance.StateNormal)
	srv.stop()
}

// TestTwoStartsWithOneKeyGiveOneOperation proves the start key of the
// migration: two starts with one key give one operation.
func TestTwoStartsWithOneKeyGiveOneOperation(t *testing.T) {
	t.Parallel()

	st := openChainStore(t)
	ops := st.DurableOperations()

	req := durable.StartRequest{Key: "audit-chain-migration", Kind: auditChainKind, Version: 1, Input: nil}

	first, created, err := ops.Start(t.Context(), req)
	if err != nil || !created {
		t.Fatalf("first start = %+v, %v, %v; want a new operation", first, created, err)
	}

	second, created, err := ops.Start(t.Context(), req)
	if err != nil || created {
		t.Fatalf("second start = %+v, %v, %v; want the first operation", second, created, err)
	}

	if first.ID != second.ID {
		t.Fatalf("two starts gave %s and %s, want one operation", first.ID, second.ID)
	}
}

// TestRunnerTakeoverCompletesTheMigration proves that when one instance
// stops during the migration, another instance completes it: the first
// runner blocks in its step, its context ends, and the second runner runs
// the step once and ends the operation succeeded.
func TestRunnerTakeoverCompletesTheMigration(t *testing.T) {
	t.Parallel()

	st := openChainStore(t)
	store := st.DurableOperations()

	const kind durable.Kind = "takeover-kind"

	op := startTakeoverOp(t, store, kind)
	stopBlocking := runBlockingRunner(t, store, kind)
	stopBlocking()

	runs := runTakeoverRunner(t, store, kind)
	waitTakeoverSucceeded(t, store, op.ID)

	if runs() != 1 {
		t.Fatalf("second runner ran the step %d times, want 1", runs())
	}
}

// startTakeoverOp starts the takeover operation of kind.
func startTakeoverOp(t *testing.T, store durable.Store, kind durable.Kind) durable.Operation {
	t.Helper()

	op, _, err := store.Start(t.Context(), durable.StartRequest{Key: "k", Kind: kind, Version: 1, Input: nil})
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	return op
}

// takeoverSteps answers the kind spec of kind with run as its step.
func takeoverSteps(kind durable.Kind, run durable.StepFunc) []durable.KindSpec {
	return []durable.KindSpec{{
		Kind:       kind,
		Version:    1,
		MinVersion: 1,
		Steps:      []durable.Step{{Name: "migrate", Run: run}},
	}}
}

// takeoverRunnerConfig answers the runner config of owner with kinds.
func takeoverRunnerConfig(store durable.Store, owner string, kinds []durable.KindSpec) durable.RunnerConfig {
	return durable.RunnerConfig{
		Store:        store,
		Owner:        owner,
		Kinds:        kinds,
		Lease:        300 * time.Millisecond,
		RenewEvery:   0,
		PollEvery:    0,
		MaxAttempts:  0,
		ExpiryLimit:  0,
		Backoff:      durable.Backoff{Base: 0, Max: 0},
		ClaimBackoff: durable.Backoff{Base: 0, Max: 0},
		WriteTimeout: 0,
		Logger:       nil,
		Rand:         nil,
	}
}

// runBlockingRunner runs the first runner until its step blocks, and
// answers the stop function that ends it and waits.
func runBlockingRunner(t *testing.T, store durable.Store, kind durable.Kind) func() {
	t.Helper()

	started := make(chan struct{})
	var once sync.Once

	block := func(ctx context.Context, _ durable.StepContext) ([]byte, error) {
		once.Do(func() { close(started) })
		<-ctx.Done()

		return nil, ctx.Err()
	}

	first, err := durable.NewRunner(takeoverRunnerConfig(store, "first", takeoverSteps(kind, block)))
	if err != nil {
		t.Fatalf("new runner: %v", err)
	}

	ctx, stop := context.WithCancel(t.Context())

	var wg sync.WaitGroup

	wg.Go(func() { first.Run(ctx) })

	select {
	case <-started:
	case <-t.Context().Done():
		t.Fatal("test context ended")
	}

	return func() {
		stop()
		wg.Wait()
	}
}

// runTakeoverRunner runs the second runner once, and answers the count of
// its step runs.
func runTakeoverRunner(t *testing.T, store durable.Store, kind durable.Kind) func() int {
	t.Helper()

	var runs int

	second, err := durable.NewRunner(takeoverRunnerConfig(store, "second", takeoverSteps(kind,
		func(context.Context, durable.StepContext) ([]byte, error) {
			runs++

			return []byte("done"), nil
		})))
	if err != nil {
		t.Fatalf("new runner: %v", err)
	}

	if err := second.RunOnce(t.Context()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	return func() int { return runs }
}

// waitTakeoverSucceeded waits until the operation id ends succeeded.
func waitTakeoverSucceeded(t *testing.T, store durable.Store, id durable.ID) {
	t.Helper()

	deadline := time.Now().Add(30 * time.Second)

	for {
		got, err := store.Get(t.Context(), id)
		if err != nil {
			t.Fatalf("get: %v", err)
		}

		if got.State == durable.StateSucceeded {
			return
		}

		if time.Now().After(deadline) {
			t.Fatalf("operation = %+v, want succeeded", got)
		}

		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-t.Context().Done():
			timer.Stop()
			t.Fatal("test context ended")
		case <-timer.C:
		}
	}
}
