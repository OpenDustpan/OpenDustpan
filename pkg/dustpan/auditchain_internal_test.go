package dustpan

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// errNoStep is the refusal of stepFreshness.
var errNoStep = errors.New("no MFA step")

// stepFreshness asks for an MFA step that the caller did not pass.
type stepFreshness struct{}

func (stepFreshness) RequireFresh(context.Context, string) error { return errNoStep }

// newTestAuditChain answers the chain of a fresh SQLite store with setting,
// where a change to the other state waits.
func newTestAuditChain(t *testing.T, setting audit.ChainSetting) *auditChain {
	t.Helper()

	st, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "dustpan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := st.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	cfg := defaultSettings()
	cfg.namespaceStore = st
	cfg.auditChain = setting

	chain, err := newAuditChain(cfg, nil)
	if err != nil || chain == nil {
		t.Fatalf("new audit chain = %v, %v", chain, err)
	}

	err = st.SaveChainRecord(t.Context(), audit.ChainRecord{
		ChangedAt: time.Now(), ChangedBy: "", State: audit.ChainStateOn, Pending: true,
		MigratingAt: time.Time{}, LogReplaced: false,
	})
	if err != nil {
		t.Fatalf("save chain record: %v", err)
	}

	return chain
}

// approver is the caller of the approvals of the tests.
func approver() audit.Entry {
	return audit.Entry{
		At: time.Time{}, Namespace: "", Subject: "alice", IDPSubject: "", TokenID: "tok-1",
		Action: audit.ActionSetChain, Object: "", ObjectID: "", Version: "", ValuePath: "",
		ValueCount: nil, Changes: nil, SourceIP: "192.0.2.1", UserAgent: "test",
		Channel: audit.ChannelAPI, Kind: "", Outcome: "", RequestID: "req-1",
		PrevHash: "", Hash: "", Sequence: 0,
		FenceVersion: 0,
	}
}

// TestApproveOffNeedsTheMFAStep proves that once the MFA check asks for a
// step, an approval of a change to off without it is refused before it
// writes anything, and that a change to on asks for no step.
func TestApproveOffNeedsTheMFAStep(t *testing.T) {
	t.Parallel()

	off := newTestAuditChain(t, audit.ChainOff)
	off.freshness = stepFreshness{}

	if err := off.Approve(t.Context(), approver()); !errors.Is(err, audit.ErrChainStepRequired) ||
		!errors.Is(err, errNoStep) {
		t.Fatalf("approve off without the step: %v, want ErrChainStepRequired", err)
	}

	if off.migrating.Load() || len(off.queue) != 0 {
		t.Fatal("a refused approval holds the migration")
	}

	on := newTestAuditChain(t, audit.ChainOn)
	on.freshness = stepFreshness{}

	if err := on.Approve(t.Context(), approver()); err != nil {
		t.Fatalf("approve on: %v, want no MFA step", err)
	}

	if len(on.queue) != 1 {
		t.Fatalf("queued changes = %d, want 1", len(on.queue))
	}

	if err := on.Approve(t.Context(), approver()); !errors.Is(err, audit.ErrChainMigrating) {
		t.Fatalf("second approval: %v, want ErrChainMigrating", err)
	}
}

// TestApproveOffWithoutMFAAsksForNoStep proves that without an MFA check,
// which built-in MFA does not have yet, a change to off asks for no step.
func TestApproveOffWithoutMFAAsksForNoStep(t *testing.T) {
	t.Parallel()

	off := newTestAuditChain(t, audit.ChainOff)

	if err := off.Approve(t.Context(), approver()); err != nil {
		t.Fatalf("approve off: %v", err)
	}

	got := <-off.queue
	if got.To != audit.ChainStateOff || got.By != "alice" {
		t.Fatalf("queued change = %+v, want off by alice", got)
	}
}

// TestApprovalAndRestoreRefuseEachOther proves that a restore is refused
// while a change waits, and an approval is refused while a restore runs.
func TestApprovalAndRestoreRefuseEachOther(t *testing.T) {
	t.Parallel()

	chain := newTestAuditChain(t, audit.ChainOff)

	if err := chain.admitRestore(t.Context()); !errors.Is(err, audit.ErrChainChangeWaits) {
		t.Fatalf("restore while a change waits: %v, want ErrChainChangeWaits", err)
	}

	chain.restoreRunning = func(context.Context) (bool, error) { return true, nil }

	if err := chain.Approve(t.Context(), approver()); !errors.Is(err, backup.ErrRestoreRunning) {
		t.Fatalf("approve while a restore runs: %v, want ErrRestoreRunning", err)
	}

	if chain.migrating.Load() {
		t.Fatal("a refused approval holds the migration")
	}
}

// TestApproveWithNothingToApprove proves that an approval while no change
// waits is refused.
func TestApproveWithNothingToApprove(t *testing.T) {
	t.Parallel()

	chain := newTestAuditChain(t, audit.ChainOn)

	rec, err := chain.store.ChainRecord(t.Context())
	if err != nil {
		t.Fatalf("chain record: %v", err)
	}

	rec.Pending = false
	if err := chain.store.SaveChainRecord(t.Context(), rec); err != nil {
		t.Fatalf("save chain record: %v", err)
	}

	if err := chain.Approve(t.Context(), approver()); !errors.Is(err, audit.ErrNoChainChange) {
		t.Fatalf("approve with nothing waiting: %v, want ErrNoChainChange", err)
	}

	if err := chain.admitRestore(t.Context()); err != nil {
		t.Fatalf("restore with nothing waiting: %v, want it admitted", err)
	}
}

// TestConcurrentApprovalsOnOneStore proves that two instances on the same
// store approve at the same time, and exactly one wins: the other answers
// ErrChainMigrating.
func TestConcurrentApprovalsOnOneStore(t *testing.T) {
	t.Parallel()

	st, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "dustpan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := st.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	newChain := func() *auditChain {
		t.Helper()

		cfg := defaultSettings()
		cfg.namespaceStore = st
		cfg.auditChain = audit.ChainOff

		chain, err := newAuditChain(cfg, nil)
		if err != nil || chain == nil {
			t.Fatalf("new audit chain = %v, %v", chain, err)
		}

		return chain
	}

	first, second := newChain(), newChain()

	saveErr := st.SaveChainRecord(t.Context(), audit.ChainRecord{
		ChangedAt: time.Now(), ChangedBy: "", State: audit.ChainStateOn, Pending: true,
		MigratingAt: time.Time{}, LogReplaced: false,
	})
	if saveErr != nil {
		t.Fatalf("save chain record: %v", saveErr)
	}

	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		won  int
		held int
	)

	for _, chain := range []*auditChain{first, second} {
		wg.Go(func() {
			err := chain.Approve(t.Context(), approver())

			mu.Lock()
			defer mu.Unlock()

			switch {
			case err == nil:
				won++
			case errors.Is(err, audit.ErrChainMigrating):
				held++
			default:
				t.Errorf("approve: %v, want nil or ErrChainMigrating", err)
			}
		})
	}

	wg.Wait()

	if won != 1 || held != 1 {
		t.Fatalf("concurrent approvals: won %d, held %d, want 1 and 1", won, held)
	}
}
