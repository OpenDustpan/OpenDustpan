package dustpan

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/metrics"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// openScheduleChainStore opens a SQLite store for the chain tests with
// instances, and closes it at the end of the test.
func openScheduleChainStore(t *testing.T) *sqlite.Store {
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

	return st
}

// newInstanceChain returns the chain of one instance on st, with the metrics
// of that instance.
func newInstanceChain(t *testing.T, st *sqlite.Store) *auditChain {
	t.Helper()

	cfg := defaultSettings()
	cfg.namespaceStore = st
	cfg.auditChain = audit.ChainOn

	observed := metrics.NewServerMetrics("test", time.Now())

	chain, err := newAuditChain(cfg, observed)
	if err != nil || chain == nil {
		t.Fatalf("new audit chain = %v, %v", chain, err)
	}

	return chain
}

// TestJobSchedulesRunAuditChainCheckOncePerTick proves that two instances on
// one store run the audit chain check once per tick: no two operations share
// a tick key, and the stored result carries the time of a check. The
// intervals are short for the test; the production interval stays one hour.
func TestJobSchedulesRunAuditChainCheckOncePerTick(t *testing.T) {
	t.Parallel()

	st := openScheduleChainStore(t)

	a := testServer(st.DurableOperations(), "a")
	a.chain, a.auditChainEvery = newInstanceChain(t, st), scheduleTestEvery

	b := testServer(st.DurableOperations(), "b")
	b.chain, b.auditChainEvery = newInstanceChain(t, st), scheduleTestEvery

	aPair, bPair := newPair(t, a), newPair(t, b)

	ctx, cancel := context.WithTimeout(t.Context(), scheduleTestDeadline)
	defer cancel()

	var wg sync.WaitGroup

	for _, r := range []func(context.Context){aPair.Sched.Run, aPair.Run.Run, bPair.Sched.Run, bPair.Run.Run} {
		wg.Go(func() { r(ctx) })
	}

	waitForTwoRuns(t, a, kindAuditChain)
	cancel()
	wg.Wait()

	checkTickKeysOnce(t, a, kindAuditChain)

	state, err := st.CheckState(t.Context())
	if err != nil {
		t.Fatalf("check state: %v", err)
	}

	if state.CheckedAt.IsZero() || state.Invalid != 0 || state.Mark != nil {
		t.Fatalf("stored state after the checks = %+v, want a check time, no invalid scope and no mark", state)
	}
}

// TestJobSchedulesListAuditChainCheckOnlyWithAChain proves that a server
// without the audit chain has no schedule for it.
func TestJobSchedulesListAuditChainCheckOnlyWithAChain(t *testing.T) {
	t.Parallel()

	st := openScheduleChainStore(t)

	without := testServer(st.DurableOperations(), "a")
	with := testServer(st.DurableOperations(), "b")
	with.chain = newInstanceChain(t, st)

	tests := []struct {
		srv  *Server
		name string
		want bool
	}{
		{name: "a server without a chain", srv: without, want: false},
		{name: "a server with a chain", srv: with, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			found := false

			for _, spec := range tt.srv.jobSchedules() {
				if spec.Name == auditChainScheduleName {
					found = true

					if spec.Interval != auditChainCheckInterval {
						t.Fatalf("interval = %v, want %v", spec.Interval, auditChainCheckInterval)
					}
				}
			}

			if found != tt.want {
				t.Fatalf("schedule %s listed = %t, want %t", auditChainScheduleName, found, tt.want)
			}
		})
	}
}

// errCheckStore is the failure of failingChecks.
var errCheckStore = errors.New("check store fails")

// failingChecks is an audit.CheckStore whose every call fails.
type failingChecks struct{}

func (failingChecks) CheckState(context.Context) (audit.CheckState, error) {
	return audit.CheckState{CheckedAt: time.Time{}, Mark: nil, Invalid: 0}, errCheckStore
}

func (failingChecks) SaveCheckResult(context.Context, int) error { return errCheckStore }

func (failingChecks) SaveCheckMark(context.Context, audit.CheckMark) error { return errCheckStore }

// TestAuditChainGaugeReadsTheStoredResult proves that every instance exports
// the stored result, not its own: instance A stores a result and instance B
// exports it. A result that cannot be read exports as one invalid scope.
func TestAuditChainGaugeReadsTheStoredResult(t *testing.T) {
	t.Parallel()

	st := openScheduleChainStore(t)
	a, b := newInstanceChain(t, st), newInstanceChain(t, st)

	tests := []struct {
		name string
		// store is false when no check stored a result yet.
		store   bool
		invalid int
	}{
		{name: "no check ran", store: false, invalid: 0},
		{name: "instance A found 2 scopes", store: true, invalid: 2},
		{name: "a later check found none", store: true, invalid: 0},
	}

	for _, tt := range tests {
		if tt.store {
			a.storeResult(t.Context(), tt.invalid)
		}

		want := "dustpan_audit_chain_invalid " + strconv.Itoa(tt.invalid)

		for name, chain := range map[string]*auditChain{"A": a, "B": b} {
			if got := metricLine(t, chain); !strings.Contains(got, want) {
				t.Fatalf("%s: instance %s metrics:\n%s\nwant %q", tt.name, name, got, want)
			}
		}
	}

	b.checks = failingChecks{}

	if got := metricLine(t, b); !strings.Contains(got, "dustpan_audit_chain_invalid 1") {
		t.Fatalf("metrics with a store that cannot be read:\n%s\nwant one invalid scope: a failed read denies", got)
	}
}
