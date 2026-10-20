package dustpan

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// takeoverLease is the short lease of the takeover test, so the test
// waits only briefly for the expiry of a holder that stopped.
const takeoverLease = 300 * time.Millisecond

// TestRunnerTakeoverCompletesChainMigration proves that when one holder
// claims the audit chain migration and stops without releasing it, another
// holder completes it after the lease expires: the second holder runs the
// real migration step, and a step that the first holder already recorded
// does not run again.
func TestRunnerTakeoverCompletesChainMigration(t *testing.T) {
	t.Parallel()

	chain, change, op := setupTakeoverChain(t)
	claimAndRecordTakeoverStep(t, chain, op, change)
	waitForTakeoverExpiry(t, chain, op)
	runs := runTakeoverChainRunner(t, chain)
	assertTakeoverChainComplete(t, chain, op, runs)
}

// setupTakeoverChain answers a chain with one entry, a change to off and
// its started operation.
func setupTakeoverChain(t *testing.T) (*auditChain, audit.ChainChange, durable.Operation) {
	t.Helper()

	chain := newTestAuditChain(t, audit.ChainOff)

	ns := namespace.Namespace{
		ID:              namespace.NewID(),
		CreatedAt:       time.Now(),
		Name:            "acme",
		HomeRegion:      "eu-west",
		FailoverVersion: 1,
		Overrides:       namespace.Overrides{Forced: false, Unlocked: false},
	}

	if createErr := chain.namespaces.CreateNamespace(t.Context(), ns); createErr != nil {
		t.Fatalf("create namespace: %v", createErr)
	}

	stored := appendTakeoverEntry(t, chain, ns)

	if stored.Hash == "" {
		t.Fatal("entry with the chain on: no hash, want the chain on")
	}

	change := audit.ChainChange{At: time.Now(), By: "alice", To: audit.ChainStateOff}

	input, marshalErr := json.Marshal(change)
	if marshalErr != nil {
		t.Fatalf("encode change: %v", marshalErr)
	}

	op, _, startErr := chain.durableOps.Start(t.Context(), durable.StartRequest{
		Key:     auditChainKey,
		Kind:    auditChainKind,
		Version: auditChainVersion,
		Input:   input,
	})
	if startErr != nil {
		t.Fatalf("start migration operation: %v", startErr)
	}

	return chain, change, op
}

// appendTakeoverEntry appends one reveal entry to ns.
func appendTakeoverEntry(t *testing.T, chain *auditChain, ns namespace.Namespace) audit.Entry {
	t.Helper()

	stored, appendErr := chain.entries.Append(t.Context(), ns.Fence(), audit.Entry{
		At:            time.Now(),
		Namespace:     ns.Name,
		Subject:       "admin",
		IDPSubject:    "",
		TokenID:       "",
		Action:        "unit:read-secrets",
		Object:        "opentofu:acme/prod",
		ObjectID:      "",
		Version:       "",
		ValuePath:     "",
		ValueCount:    nil,
		Changes:       nil,
		SourceIP:      "",
		UserAgent:     "",
		Channel:       audit.ChannelAPI,
		Kind:          audit.KindReveal,
		Outcome:       audit.OutcomeSuccess,
		RequestID:     "req-1",
		PrevHash:      "",
		Hash:          "",
		Sequence:      0,
		FenceVersion:  0,
		Reason:        "",
		PrincipalKind: "",
		JobID:         "",
	})
	if appendErr != nil {
		t.Fatalf("append audit entry: %v", appendErr)
	}

	return stored
}

// claimAndRecordTakeoverStep claims op as the old holder, runs the real
// migration and records its step, then stops without a release or an end
// write, as a killed instance does.
func claimAndRecordTakeoverStep(
	t *testing.T, chain *auditChain, op durable.Operation, change audit.ChainChange,
) {
	t.Helper()

	claimed, claimErr := chain.durableOps.Claim(t.Context(), durable.ClaimRequest{
		Owner: "old", Kinds: []durable.Kind{auditChainKind}, MaxVersions: nil, Lease: takeoverLease,
	})
	if claimErr != nil {
		t.Fatalf("claim: %v", claimErr)
	}

	if claimed.ID != op.ID {
		t.Fatalf("claimed operation = %s, want %s", claimed.ID, op.ID)
	}

	fences, fencesErr := chain.fences(t.Context())
	if fencesErr != nil {
		t.Fatalf("fences: %v", fencesErr)
	}

	if migrateErr := audit.MigrateChain(t.Context(), chain.store, fences, change); migrateErr != nil {
		t.Fatalf("migrate the chain: %v", migrateErr)
	}

	recordErr := chain.durableOps.RecordStep(t.Context(), claimed.Claim.Step(auditChainStep), nil)
	if recordErr != nil {
		t.Fatalf("record the migration step: %v", recordErr)
	}
}

// waitForTakeoverExpiry waits until the claim of op expires.
func waitForTakeoverExpiry(t *testing.T, chain *auditChain, op durable.Operation) {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)

	for time.Now().Before(deadline) {
		got, getErr := chain.durableOps.Get(t.Context(), op.ID)
		if getErr != nil {
			t.Fatalf("get operation: %v", getErr)
		}

		if got.Claim.ExpiresAt.Before(time.Now()) {
			return
		}

		pauseTakeover(t)
	}

	t.Fatal("claim did not expire")
}

// pauseTakeover waits a short time, or fails the test when it ends first.
func pauseTakeover(t *testing.T) {
	t.Helper()

	timer := time.NewTimer(10 * time.Millisecond)
	defer timer.Stop()

	select {
	case <-timer.C:
	case <-t.Context().Done():
		t.Fatal("test context ended")
	}
}

// runTakeoverChainRunner runs the second holder once with the real
// migration step, and answers how often the step ran.
func runTakeoverChainRunner(t *testing.T, chain *auditChain) *atomic.Int32 {
	t.Helper()

	var runs atomic.Int32

	second, runnerErr := durable.NewRunner(durable.RunnerConfig{
		Store: chain.durableOps,
		Owner: "new",
		Kinds: []durable.KindSpec{{
			Kind:       auditChainKind,
			Version:    auditChainVersion,
			MinVersion: auditChainVersion,
			Steps: []durable.Step{{
				Name: auditChainStep,
				Run: func(ctx context.Context, sc durable.StepContext) ([]byte, error) {
					runs.Add(1)

					return chain.runMigrationStep(ctx, sc)
				},
			}},
		}},
		Lease:        takeoverLease,
		RenewEvery:   0,
		PollEvery:    0,
		MaxAttempts:  0,
		ExpiryLimit:  0,
		Backoff:      durable.Backoff{Base: 0, Max: 0},
		ClaimBackoff: durable.Backoff{Base: 0, Max: 0},
		WriteTimeout: 0,
		Deferred:     nil,
		Logger:       nil,
		Rand:         func() float64 { return 0 },
	})
	if runnerErr != nil {
		t.Fatalf("new runner: %v", runnerErr)
	}

	if runErr := second.RunOnce(t.Context()); runErr != nil {
		t.Fatalf("RunOnce: %v", runErr)
	}

	return &runs
}

// assertTakeoverChainComplete proves that the second holder skipped the
// recorded step, ended the operation succeeded as a takeover, and left the
// chain migrated.
func assertTakeoverChainComplete(t *testing.T, chain *auditChain, op durable.Operation, runs *atomic.Int32) {
	t.Helper()

	if got := runs.Load(); got != 0 {
		t.Fatalf("second holder ran the recorded step %d times, want 0", got)
	}

	got, getErr := chain.durableOps.Get(t.Context(), op.ID)
	if getErr != nil {
		t.Fatalf("get operation: %v", getErr)
	}

	if got.State != durable.StateSucceeded {
		t.Fatalf("operation = %+v, want succeeded", got)
	}

	if got.Expiries != 1 {
		t.Fatalf("expiries = %d, want 1 for the takeover", got.Expiries)
	}

	if len(got.Steps) != 1 || got.Steps[0].Name != auditChainStep {
		t.Fatalf("steps = %+v, want one recorded migrate step", got.Steps)
	}

	rec, recordErr := chain.store.ChainRecord(t.Context())
	if recordErr != nil {
		t.Fatalf("chain record: %v", recordErr)
	}

	if rec.State != audit.ChainStateOff || rec.Pending {
		t.Fatalf("chain record = %+v, want off with no wait", rec)
	}
}
