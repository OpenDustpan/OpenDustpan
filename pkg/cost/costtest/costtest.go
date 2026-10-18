// Package costtest is the conformance suite for cost.EstimateStore and
// cost.BudgetStore implementations. Every store adapter runs it:
//
//	func TestCostConformance(t *testing.T) {
//		t.Parallel()
//		costtest.Run(t, func(t *testing.T) costtest.Harness { return newHarness(t) })
//	}
//
// A reference adapter in memory, in the tests of this package, runs the
// suite, so each case runs before the first SQL adapter. The suites of the
// Estimator and SpendSource ports come with their first adapter.
package costtest

import (
	"context"
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/opsmem"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/cost"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Harness is one fresh, empty database for one case.
type Harness struct {
	Estimates cost.EstimateStore
	Budgets   cost.BudgetStore
	Domain    Domain
}

// Domain makes the rows that cost data needs and that other ports own, and
// reads the audit entries that the stores append.
type Domain interface {
	// CreateNamespace creates the namespace name and returns its fence.
	CreateNamespace(ctx context.Context, name namespace.Name) (namespace.Fence, error)
	// BumpFence increments the failover version of the namespace of fence.
	BumpFence(ctx context.Context, fence namespace.Fence) (namespace.Fence, error)
	// AuditEntries returns the audit entries of scope, in append order.
	AuditEntries(ctx context.Context, scope namespace.Name) ([]audit.Entry, error)
	// SetJobOpen makes the job id of ns wait or run when open is true, and
	// end when it is false, as the job model does.
	SetJobOpen(ctx context.Context, ns namespace.Name, id string, open bool) error
}

// Factory returns a fresh harness for one case.
type Factory func(t *testing.T) Harness

// Run executes the conformance suite against harnesses from newHarness.
func Run(t *testing.T, newHarness Factory) {
	t.Helper()

	cases := []struct {
		name string
		run  func(t *testing.T, h Harness)
	}{
		{"an estimate replaces the estimate of its subject", testEstimateReplace},
		{"an estimate from a client appends an entry, a scheduled one none", testEstimateAudit},
		{"pending estimates are those of open jobs", testPending},
		{"an estimate with a stale fence changes nothing", testEstimateFence},
		{"budgets create, update once and list", testBudgets},
		{"a status saves with its version", testStatus},
		{"delete removes the budget and its status", testDeleteBudget},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.run(t, newHarness(t))
		})
	}
}

const shop = namespace.Name("shop")

func now() time.Time { return time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC) }

func entry(action string) audit.Entry {
	return opsmem.Entry(shop, "alice", action, shop.String(), now())
}

func eur(units int64) cost.Money {
	return cost.Money{Currency: "EUR", Micros: units * cost.MicrosPerUnit}
}

func setup(t *testing.T, h Harness) namespace.Fence {
	t.Helper()

	fence, err := h.Domain.CreateNamespace(t.Context(), shop)
	if err != nil {
		t.Fatalf("CreateNamespace: %v", err)
	}

	return fence
}

func subject(job string) cost.Subject {
	return cost.Subject{Job: job, Phase: 0, Blueprint: "", BlueprintVersion: ""}
}

func estimate(t *testing.T, id, job string, after int64) cost.Estimate {
	t.Helper()

	r := []cost.ResourceCost{{
		Key: "aws_instance.web", Type: "aws_instance", Action: cost.ActionCreate,
		Before: eur(0), After: eur(after), Unpriced: false, Note: "",
	}}

	e, err := cost.Summed(cost.Estimate{
		ID: id, Subject: subject(job), Unit: "opentofu:shop/web", Source: "test", Currency: "EUR",
		Before: eur(0), After: eur(0), Resources: r, CreatedAt: now(),
	})
	if err != nil {
		t.Fatalf("Summed: %v", err)
	}

	return e
}

func put(t *testing.T, h Harness, fence namespace.Fence, e cost.Estimate) {
	t.Helper()

	if err := h.Estimates.PutEstimate(t.Context(), e, fence, nil); err != nil {
		t.Fatalf("PutEstimate %s: %v", e.ID, err)
	}
}

func budget() cost.Budget {
	return cost.Budget{
		ID: "bgt-1", Namespace: shop, Collection: "", Name: "Shop", Amount: eur(4000),
		Thresholds: cost.DefaultThresholds(), CreatedBy: "alice", Version: 0,
	}
}

func testEstimateReplace(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	put(t, h, fence, estimate(t, "est-1", "job-1", 10))
	put(t, h, fence, estimate(t, "est-2", "job-1", 31))

	got, err := h.Estimates.GetEstimate(t.Context(), subject("job-1"))
	if err != nil || got.ID != "est-2" || got.Delta() != eur(31) {
		t.Fatalf("GetEstimate = %+v (%v), want est-2 with +31", got, err)
	}

	if _, err := h.Estimates.GetEstimate(t.Context(), subject("job-none")); !errors.Is(err, cost.ErrNotFound) {
		t.Fatalf("GetEstimate unknown: err = %v, want ErrNotFound", err)
	}
}

func testEstimateAudit(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	put(t, h, fence, estimate(t, "est-1", "job-1", 10))

	e := entry("job:write-cost")
	if err := h.Estimates.PutEstimate(t.Context(), estimate(t, "est-2", "job-2", 10), fence, &e); err != nil {
		t.Fatalf("PutEstimate: %v", err)
	}

	entries, err := h.Domain.AuditEntries(t.Context(), shop)
	if err != nil || len(entries) != 1 {
		t.Fatalf("audit entries = %d (%v), want 1", len(entries), err)
	}
}

func testPending(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)

	for _, j := range []struct {
		id   string
		open bool
	}{{"job-1", true}, {"job-2", false}} {
		if err := h.Domain.SetJobOpen(t.Context(), shop, j.id, j.open); err != nil {
			t.Fatalf("SetJobOpen: %v", err)
		}
	}

	put(t, h, fence, estimate(t, "est-1", "job-1", 10))
	put(t, h, fence, estimate(t, "est-2", "job-2", 20))

	got, err := h.Estimates.PendingEstimates(t.Context(), shop)
	if err != nil || len(got) != 1 || got[0].ID != "est-1" {
		t.Fatalf("PendingEstimates = %v (%v), want est-1", got, err)
	}
}

func testEstimateFence(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	if _, err := h.Domain.BumpFence(t.Context(), fence); err != nil {
		t.Fatalf("BumpFence: %v", err)
	}

	err := h.Estimates.PutEstimate(t.Context(), estimate(t, "est-1", "job-1", 10), fence, nil)
	if !errors.Is(err, namespace.ErrFenced) {
		t.Fatalf("PutEstimate: err = %v, want namespace.ErrFenced", err)
	}
}

func testBudgets(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	manage := entry("namespace:manage-budgets")

	if err := h.Budgets.CreateBudget(t.Context(), budget(), fence, manage); err != nil {
		t.Fatalf("CreateBudget: %v", err)
	}

	if err := h.Budgets.CreateBudget(t.Context(), budget(), fence, manage); !errors.Is(err, cost.ErrExists) {
		t.Fatalf("second CreateBudget: err = %v, want ErrExists", err)
	}

	b := budget()
	b.Version, b.Amount = 1, eur(5000)

	if err := h.Budgets.UpdateBudget(t.Context(), b, fence, manage); err != nil {
		t.Fatalf("UpdateBudget: %v", err)
	}

	if err := h.Budgets.UpdateBudget(t.Context(), b, fence, manage); !errors.Is(err, cost.ErrConflict) {
		t.Fatalf("stale UpdateBudget: err = %v, want ErrConflict", err)
	}

	got, err := h.Budgets.Budgets(t.Context(), shop)
	if err != nil || len(got) != 1 || got[0].Amount != eur(5000) || got[0].Version != 2 {
		t.Fatalf("Budgets = %+v (%v), want one of EUR 5000 at version 2", got, err)
	}
}

func testStatus(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	manage := entry("namespace:manage-budgets")

	if err := h.Budgets.CreateBudget(t.Context(), budget(), fence, manage); err != nil {
		t.Fatalf("CreateBudget: %v", err)
	}

	p := cost.PeriodOf(now())
	st := cost.Status{
		Budget: "bgt-1", Period: p, Actual: eur(3300), Forecast: eur(3300), Pending: eur(0), Projected: eur(3300),
		Percent: 82, Crossed: []int{80}, Level: cost.LevelWarning, AsOf: now(),
	}

	if err := h.Budgets.SaveStatus(t.Context(), st, 0); err != nil {
		t.Fatalf("SaveStatus: %v", err)
	}

	if err := h.Budgets.SaveStatus(t.Context(), st, 0); !errors.Is(err, cost.ErrConflict) {
		t.Fatalf("SaveStatus on version 0 again: err = %v, want ErrConflict", err)
	}

	got, version, err := h.Budgets.LastStatus(t.Context(), "bgt-1", p)
	if err != nil || version != 1 || got.Percent != 82 {
		t.Fatalf("LastStatus = %+v, %d (%v); want 82%% at version 1", got, version, err)
	}

	november := cost.Period{Year: 2026, Month: time.November}

	_, _, err = h.Budgets.LastStatus(t.Context(), "bgt-1", november)
	if !errors.Is(err, cost.ErrNotFound) {
		t.Fatalf("LastStatus of another month: err = %v, want ErrNotFound", err)
	}
}

func testDeleteBudget(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	manage := entry("namespace:manage-budgets")

	if err := h.Budgets.CreateBudget(t.Context(), budget(), fence, manage); err != nil {
		t.Fatalf("CreateBudget: %v", err)
	}

	if err := h.Budgets.DeleteBudget(t.Context(), "bgt-1", fence, manage); err != nil {
		t.Fatalf("DeleteBudget: %v", err)
	}

	if err := h.Budgets.DeleteBudget(t.Context(), "bgt-1", fence, manage); !errors.Is(err, cost.ErrNotFound) {
		t.Fatalf("second DeleteBudget: err = %v, want ErrNotFound", err)
	}

	var st cost.Status

	st.Budget, st.Period, st.Level, st.AsOf = "bgt-1", cost.PeriodOf(now()), cost.LevelOK, now()

	if err := h.Budgets.SaveStatus(t.Context(), st, 0); !errors.Is(err, cost.ErrNotFound) {
		t.Fatalf("SaveStatus of a deleted budget: err = %v, want ErrNotFound", err)
	}
}
