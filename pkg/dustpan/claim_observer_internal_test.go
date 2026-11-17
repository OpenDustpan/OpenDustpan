package dustpan

import (
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/api/stategate"
	"opendustpan.org/opendustpan/internal/metrics"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/job"
)

// claimObserverBound is the bound of the claim poll of pkg/job/claimer.go. The
// bound is unexported there, so the test states the number that the code
// holds.
const claimObserverBound = 64

// TestClaimSkipsMoveTheMetric proves that the store options of the built-in
// store give the claimer an observer, and that the observer counts the
// operations of a poll that reaches the bound on the metrics of the server.
// The test reads the metrics registry only: the counter has no route of its
// own, so there is no request flow to check.
func TestClaimSkipsMoveTheMetric(t *testing.T) {
	t.Parallel()

	st, err := OpenStore(t.Context(), "sqlite:"+filepath.Join(t.TempDir(), "dustpan.db"))
	if err != nil {
		t.Fatalf("open the database: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := st.Close(); closeErr != nil {
			t.Errorf("close the database: %v", closeErr)
		}
	})

	cfg := defaultSettings()
	cfg.observed = metrics.NewServerMetrics("test", time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC))
	WithBuiltinStore(st)(cfg)

	if applyErr := applyBuiltinStore(cfg); applyErr != nil {
		t.Fatalf("apply the built-in store: %v", applyErr)
	}

	if cfg.jobClaimer == nil || cfg.observed == nil {
		t.Fatalf("job claimer = %v, metrics = %v; want both", cfg.jobClaimer, cfg.observed)
	}

	// The guard is off with a reason, so no gate covers a unit and the claim
	// reaches the operations of the store.
	guard, guardErr := claimGates(cfg, stateWriteGates{svc: nil, guard: stategate.Off(reasonNoGateStore)})
	if guardErr != nil {
		t.Fatalf("claim gates: %v", guardErr)
	}

	seedClaimSkips(t, cfg.durableStore, claimObserverBound)

	_, claimErr := cfg.jobClaimer(guard).Claim(t.Context(), job.ClaimRequest{
		Owner: "runner-1", Kinds: []durable.Kind{job.KindWriteBack}, MaxVersions: nil, Lease: time.Minute,
	})
	if !errors.Is(claimErr, durable.ErrNoWork) {
		t.Fatalf("Claim = %v, want durable.ErrNoWork", claimErr)
	}

	want := "dustpan_job_claim_poll_skipped_total " + strconv.Itoa(claimObserverBound)
	if got := scrapeClaimObserverMetrics(t, cfg.observed); !strings.Contains(got, want) {
		t.Fatalf("the registry does not hold %q:\n%s", want, got)
	}
}

// seedClaimSkips starts n operations of the write-back kind that name no phase
// key, so the claimer ends each one failed and skips it.
func seedClaimSkips(t *testing.T, ops durable.Store, n int) {
	t.Helper()

	for i := range n {
		req := durable.StartRequest{
			Key: "claim-skip-" + strconv.Itoa(i), Kind: job.KindWriteBack, Version: 1, Input: nil,
		}
		if _, _, err := ops.Start(t.Context(), req); err != nil {
			t.Fatalf("start operation %d: %v", i, err)
		}
	}
}

// scrapeClaimObserverMetrics returns the metrics output of observed.
func scrapeClaimObserverMetrics(t *testing.T, observed *metrics.ServerMetrics) string {
	t.Helper()

	var out strings.Builder
	if err := observed.Write(&out); err != nil {
		t.Fatalf("write metrics: %v", err)
	}

	return out.String()
}
