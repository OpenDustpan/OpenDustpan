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

	"opendustpan.org/opendustpan/internal/cost"
	"opendustpan.org/opendustpan/pkg/audit"
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
		{"an estimate of one origin never replaces the other", testEstimateOrigins},
		{"pending estimates are those of the highest phase of a job", testPendingPhase},
		{"a write refuses an audit entry of another namespace", testEntryNamespace},
		{"an estimate of a blueprint of the installation takes a zero fence", testServerEstimate},
		{"a subject keeps the namespace of its estimate", testEstimateNamespace},
		{"a read of an estimate of another scope is ErrNotFound", testEstimateReadScope},
		{"a read of a status of a budget of another namespace is ErrNotFound", testStatusReadScope},
		{"every write refuses a wrong or stale fence", testFences},
		{"budgets create, update once and list", testBudgets},
		{"an update keeps the namespace of a budget", testBudgetNamespace},
		{"a status saves with its version", testStatus},
		{"a status keeps the thresholds that warned", testWarned},
		{"delete removes the budget and its status", testDeleteBudget},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.run(t, newHarness(t))
		})
	}
}

const (
	shop  = namespace.Name("shop")
	other = namespace.Name("other")
)

func now() time.Time { return time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC) }

// entry returns a valid audit entry of the kind change in shop.
func entry(action string) audit.Entry {
	return audit.Entry{
		At: now(), Namespace: shop, Subject: "alice", IDPSubject: "", TokenID: "",
		Action: action, Object: shop.String(), Version: "", ValuePath: "", ValueCount: nil, Changes: nil,
		SourceIP: "192.0.2.1", UserAgent: "suite", Channel: audit.ChannelAPI, Kind: audit.KindChange,
		Outcome: audit.OutcomeSuccess, RequestID: "req-suite", ObjectID: "", PrevHash: "", Hash: "",
		Sequence: 0, FenceVersion: 0,
		Reason:        "",
		PrincipalKind: "",
		JobID:         "",
	}
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
		ID: id, Subject: subject(job), Namespace: shop, Unit: "opentofu:shop/web", Source: "test", Currency: "EUR",
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

	got, err := h.Estimates.GetEstimate(t.Context(), shop, subject("job-1"), cost.OriginEstimator)
	if err != nil || got.ID != "est-2" || got.After != eur(31) {
		t.Fatalf("GetEstimate = %+v (%v), want est-2 with +31", got, err)
	}

	_, err = h.Estimates.GetEstimate(t.Context(), shop, subject("job-none"), cost.OriginEstimator)
	if !errors.Is(err, cost.ErrNotFound) {
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

// testEstimateOrigins stores an estimate of an Estimator adapter and an
// estimate from a client for one subject, and reads back both. A second
// estimate of the adapter replaces only the first one of the adapter.
func testEstimateOrigins(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	put(t, h, fence, estimate(t, "est-1", "job-1", 10))

	pushed := estimate(t, "est-2", "job-1", 31)
	pushed.Source = cost.SourceAPI
	put(t, h, fence, pushed)
	put(t, h, fence, estimate(t, "est-3", "job-1", 12))

	got, err := h.Estimates.GetEstimate(t.Context(), shop, subject("job-1"), cost.OriginEstimator)
	if err != nil || got.ID != "est-3" {
		t.Fatalf("GetEstimate of the adapter = %+v (%v), want est-3", got, err)
	}

	got, err = h.Estimates.GetEstimate(t.Context(), shop, subject("job-1"), cost.OriginAPI)
	if err != nil || got.ID != "est-2" || got.After != eur(31) {
		t.Fatalf("GetEstimate of the client = %+v (%v), want est-2 with +31", got, err)
	}
}

// testPendingPhase stores estimates of the phases 0 and 2 of one open
// job. Only the estimates of phase 2 are pending, one of each origin.
func testPendingPhase(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	if err := h.Domain.SetJobOpen(t.Context(), shop, "job-1", true); err != nil {
		t.Fatalf("SetJobOpen: %v", err)
	}

	first := estimate(t, "est-1", "job-1", 10)
	later := estimate(t, "est-2", "job-1", 31)
	later.Subject.Phase = 2
	pushed := estimate(t, "est-3", "job-1", 40)
	pushed.Subject.Phase, pushed.Source = 2, cost.SourceAPI

	for _, e := range []*cost.Estimate{&later, &first, &pushed} {
		put(t, h, fence, *e)
	}

	got, err := h.Estimates.PendingEstimates(t.Context(), shop)
	if err != nil || len(got) != 2 {
		t.Fatalf("PendingEstimates = %+v (%v), want est-2 and est-3", got, err)
	}

	for i := range got {
		if got[i].ID != "est-2" && got[i].ID != "est-3" {
			t.Fatalf("PendingEstimates holds %s of phase %d, want only phase 2", got[i].ID, got[i].Subject.Phase)
		}
	}
}

// testEntryNamespace runs each write that takes an audit entry with the
// good fence and an entry in the namespace other. Each write returns
// ErrInvalid and changes nothing, in shop and in other.
func testEntryNamespace(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	createOther(t, h)

	if err := h.Budgets.CreateBudget(t.Context(), budget(), fence, entry("namespace:manage-budgets")); err != nil {
		t.Fatalf("CreateBudget: %v", err)
	}

	e := entry("namespace:manage-budgets")
	e.Namespace = other
	b := budget()
	b.Version, b.Amount = 1, eur(5000)
	created := budget()
	created.ID = "bgt-9"

	writes := map[string]func() error{
		"PutEstimate": func() error {
			return h.Estimates.PutEstimate(t.Context(), estimate(t, "est-9", "job-9", 10), fence, &e)
		},
		"CreateBudget": func() error { return h.Budgets.CreateBudget(t.Context(), created, fence, e) },
		"UpdateBudget": func() error { return h.Budgets.UpdateBudget(t.Context(), b, fence, e) },
		"DeleteBudget": func() error { return h.Budgets.DeleteBudget(t.Context(), "bgt-1", fence, e) },
	}

	for name, write := range writes {
		if err := write(); !errors.Is(err, cost.ErrInvalid) {
			t.Fatalf("%s with an entry of other: err = %v, want ErrInvalid", name, err)
		}
	}

	assertUnchanged(t, h)

	entries, err := h.Domain.AuditEntries(t.Context(), other)
	if err != nil || len(entries) != 0 {
		t.Fatalf("audit entries of other = %d (%v), want none", len(entries), err)
	}
}

func blueprintEstimate(t *testing.T) cost.Estimate {
	t.Helper()

	r := []cost.ResourceCost{{
		Key: "aws_vpc.main", Type: "aws_vpc", Action: cost.ActionCreate,
		Before: eur(0), After: eur(12), Unpriced: false, Note: "",
	}}

	e, err := cost.Summed(cost.Estimate{
		ID: "est-b", Subject: cost.Subject{Job: "", Phase: 0, Blueprint: "vpc", BlueprintVersion: "1.0.0"},
		Namespace: audit.ServerScope, Unit: "", Source: "test", Currency: "EUR",
		Before: eur(0), After: eur(0), Resources: r, CreatedAt: now(),
	})
	if err != nil {
		t.Fatalf("Summed: %v", err)
	}

	return e
}

func testServerEstimate(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	e := blueprintEstimate(t)

	if err := h.Estimates.PutEstimate(t.Context(), e, fence, nil); !errors.Is(err, cost.ErrInvalid) {
		t.Fatalf("PutEstimate in the server scope with a fence: err = %v, want ErrInvalid", err)
	}

	put(t, h, namespace.Fence{Namespace: "", FailoverVersion: 0}, e)

	got, err := h.Estimates.GetEstimate(t.Context(), audit.ServerScope, e.Subject, cost.OriginEstimator)
	if err != nil || got.ID != "est-b" {
		t.Fatalf("GetEstimate = %+v (%v), want est-b", got, err)
	}
}

func testEstimateNamespace(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	otherFence := createOther(t, h)
	put(t, h, fence, estimate(t, "est-1", "job-1", 10))

	e := estimate(t, "est-2", "job-1", 20)
	e.Namespace = other

	if err := h.Estimates.PutEstimate(t.Context(), e, otherFence, nil); !errors.Is(err, cost.ErrInvalid) {
		t.Fatalf("PutEstimate of a subject of shop in other: err = %v, want ErrInvalid", err)
	}

	got, err := h.Estimates.GetEstimate(t.Context(), shop, subject("job-1"), cost.OriginEstimator)
	if err != nil || got.ID != "est-1" {
		t.Fatalf("GetEstimate = %+v (%v), want est-1", got, err)
	}
}

func testEstimateReadScope(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	createOther(t, h)
	put(t, h, fence, estimate(t, "est-1", "job-1", 10))

	b := blueprintEstimate(t)
	put(t, h, namespace.Fence{Namespace: "", FailoverVersion: 0}, b)

	for _, tc := range []struct {
		name  string
		scope namespace.Name
		s     cost.Subject
	}{
		{"a plan of shop read in other", other, subject("job-1")},
		{"a plan of shop read in the server scope", audit.ServerScope, subject("job-1")},
		{"a blueprint of the installation read in shop", shop, b.Subject},
	} {
		_, err := h.Estimates.GetEstimate(t.Context(), tc.scope, tc.s, cost.OriginEstimator)
		if !errors.Is(err, cost.ErrNotFound) {
			t.Fatalf("GetEstimate of %s: err = %v, want ErrNotFound", tc.name, err)
		}
	}

	got, err := h.Estimates.GetEstimate(t.Context(), shop, subject("job-1"), cost.OriginEstimator)
	if err != nil || got.ID != "est-1" {
		t.Fatalf("GetEstimate in shop = %+v (%v), want est-1", got, err)
	}
}

func testStatusReadScope(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	createOther(t, h)

	if err := h.Budgets.CreateBudget(t.Context(), budget(), fence, entry("namespace:manage-budgets")); err != nil {
		t.Fatalf("CreateBudget: %v", err)
	}

	p := cost.PeriodOf(now())
	st := cost.Status{
		Budget: "bgt-1", Period: p, Actual: eur(100), Forecast: eur(100), Pending: eur(0), Projected: eur(100),
		Percent: 2, Crossed: nil, Warned: nil, Skipped: 0, SkippedNegative: 0, Level: cost.LevelOK, AsOf: now(),
	}

	if err := h.Budgets.SaveStatus(t.Context(), st, 0, fence); err != nil {
		t.Fatalf("SaveStatus: %v", err)
	}

	if _, _, err := h.Budgets.LastStatus(t.Context(), other, "bgt-1", p); !errors.Is(err, cost.ErrNotFound) {
		t.Fatalf("LastStatus in other: err = %v, want ErrNotFound", err)
	}

	if _, version, err := h.Budgets.LastStatus(t.Context(), shop, "bgt-1", p); err != nil || version != 1 {
		t.Fatalf("LastStatus in shop = version %d (%v), want 1", version, err)
	}
}

// createOther creates the namespace other and returns its fence.
func createOther(t *testing.T, h Harness) namespace.Fence {
	t.Helper()

	fence, err := h.Domain.CreateNamespace(t.Context(), other)
	if err != nil {
		t.Fatalf("CreateNamespace %s: %v", other, err)
	}

	return fence
}

// fenceCase is one fence that a write gets, and the error it wants.
type fenceCase struct {
	name  string
	fence func(shopFence, otherFence, stale namespace.Fence) namespace.Fence
}

func fenceCases() []fenceCase {
	return []fenceCase{
		{"a zero fence", func(_, _, _ namespace.Fence) namespace.Fence {
			return namespace.Fence{Namespace: "", FailoverVersion: 0}
		}},
		{"the fence of another namespace", func(_, o, _ namespace.Fence) namespace.Fence { return o }},
		{"a stale fence", func(_, _, s namespace.Fence) namespace.Fence { return s }},
	}
}

// fenceWrite is one write method of the stores, with the sentinel that
// it returns for a fence of another namespace or a zero fence.
type fenceWrite struct {
	name  string
	scope error
	write func(t *testing.T, h Harness, fence namespace.Fence) error
}

func fenceWrites() []fenceWrite {
	manage := entry("namespace:manage-budgets")
	status := cost.Status{
		Budget:   "bgt-1",
		Period:   cost.PeriodOf(now()),
		Actual:   eur(1),
		Forecast: eur(1),
		Pending:  eur(0),
		Projected: eur(
			1,
		),
		Percent:         0,
		Crossed:         nil,
		Warned:          nil,
		Skipped:         0,
		SkippedNegative: 0,
		Level:           cost.LevelOK,
		AsOf:            now(),
	}

	return []fenceWrite{
		{"PutEstimate", cost.ErrInvalid, func(t *testing.T, h Harness, f namespace.Fence) error {
			t.Helper()

			return h.Estimates.PutEstimate(t.Context(), estimate(t, "est-9", "job-9", 10), f, nil)
		}},
		{"CreateBudget", cost.ErrInvalid, func(t *testing.T, h Harness, f namespace.Fence) error {
			t.Helper()

			b := budget()
			b.ID = "bgt-9"

			return h.Budgets.CreateBudget(t.Context(), b, f, manage)
		}},
		{"UpdateBudget", cost.ErrNotFound, func(t *testing.T, h Harness, f namespace.Fence) error {
			t.Helper()

			b := budget()
			b.Version, b.Amount = 1, eur(5000)

			return h.Budgets.UpdateBudget(t.Context(), b, f, manage)
		}},
		{"DeleteBudget", cost.ErrNotFound, func(t *testing.T, h Harness, f namespace.Fence) error {
			t.Helper()

			return h.Budgets.DeleteBudget(t.Context(), "bgt-1", f, manage)
		}},
		{"SaveStatus", cost.ErrNotFound, func(t *testing.T, h Harness, f namespace.Fence) error {
			t.Helper()

			return h.Budgets.SaveStatus(t.Context(), status, 0, f)
		}},
	}
}

// testFences runs each write method with each wrong fence. Each write
// fails and changes nothing: the budget bgt-1 keeps version 1, no
// estimate or status is stored, and no audit entry is appended.
func testFences(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	otherFence := createOther(t, h)

	if err := h.Budgets.CreateBudget(t.Context(), budget(), fence, entry("namespace:manage-budgets")); err != nil {
		t.Fatalf("CreateBudget: %v", err)
	}

	current, err := h.Domain.BumpFence(t.Context(), fence)
	if err != nil {
		t.Fatalf("BumpFence: %v", err)
	}

	for _, w := range fenceWrites() {
		for _, fc := range fenceCases() {
			want := w.scope
			if fc.name == "a stale fence" {
				want = namespace.ErrFenced
			}

			if err := w.write(t, h, fc.fence(current, otherFence, fence)); !errors.Is(err, want) {
				t.Fatalf("%s with %s: err = %v, want %v", w.name, fc.name, err, want)
			}
		}
	}

	assertUnchanged(t, h)
}

func assertUnchanged(t *testing.T, h Harness) {
	t.Helper()

	got, err := h.Budgets.Budgets(t.Context(), shop)
	if err != nil || len(got) != 1 || got[0].Version != 1 {
		t.Fatalf("Budgets = %+v (%v), want bgt-1 at version 1 only", got, err)
	}

	_, err = h.Estimates.GetEstimate(t.Context(), shop, subject("job-9"), cost.OriginEstimator)
	if !errors.Is(err, cost.ErrNotFound) {
		t.Fatalf("GetEstimate after refused writes: err = %v, want ErrNotFound", err)
	}

	_, _, err = h.Budgets.LastStatus(t.Context(), shop, "bgt-1", cost.PeriodOf(now()))
	if !errors.Is(err, cost.ErrNotFound) {
		t.Fatalf("LastStatus after refused writes: err = %v, want ErrNotFound", err)
	}

	entries, err := h.Domain.AuditEntries(t.Context(), shop)
	if err != nil || len(entries) != 1 {
		t.Fatalf("audit entries = %d (%v), want the entry of the create only", len(entries), err)
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

	b.ID = "bgt-none"
	if err := h.Budgets.UpdateBudget(t.Context(), b, fence, manage); !errors.Is(err, cost.ErrNotFound) {
		t.Fatalf("UpdateBudget of an unknown budget: err = %v, want ErrNotFound", err)
	}
}

func testBudgetNamespace(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	createOther(t, h)
	manage := entry("namespace:manage-budgets")

	if err := h.Budgets.CreateBudget(t.Context(), budget(), fence, manage); err != nil {
		t.Fatalf("CreateBudget: %v", err)
	}

	b := budget()
	b.Version, b.Namespace = 1, other

	if err := h.Budgets.UpdateBudget(t.Context(), b, fence, manage); !errors.Is(err, cost.ErrInvalid) {
		t.Fatalf("UpdateBudget to another namespace: err = %v, want ErrInvalid", err)
	}

	got, err := h.Budgets.Budgets(t.Context(), other)
	if err != nil || len(got) != 0 {
		t.Fatalf("Budgets of other = %+v (%v), want none", got, err)
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
		Budget:          "bgt-1",
		Period:          p,
		Actual:          eur(3300),
		Forecast:        eur(3300),
		Pending:         eur(0),
		Projected:       eur(3300),
		Percent:         82,
		Crossed:         []int{80},
		Warned:          []int{80},
		Skipped:         0,
		SkippedNegative: 0,
		Level:           cost.LevelWarning,
		AsOf:            now(),
	}

	if err := h.Budgets.SaveStatus(t.Context(), st, 0, fence); err != nil {
		t.Fatalf("SaveStatus: %v", err)
	}

	if err := h.Budgets.SaveStatus(t.Context(), st, 0, fence); !errors.Is(err, cost.ErrConflict) {
		t.Fatalf("SaveStatus on version 0 again: err = %v, want ErrConflict", err)
	}

	got, version, err := h.Budgets.LastStatus(t.Context(), shop, "bgt-1", p)
	if err != nil || version != 1 || got.Percent != 82 {
		t.Fatalf("LastStatus = %+v, %d (%v); want 82%% at version 1", got, version, err)
	}

	november := cost.Period{Year: 2026, Month: time.November}

	_, _, err = h.Budgets.LastStatus(t.Context(), shop, "bgt-1", november)
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

	if err := h.Budgets.SaveStatus(t.Context(), st, 0, fence); !errors.Is(err, cost.ErrNotFound) {
		t.Fatalf("SaveStatus of a deleted budget: err = %v, want ErrNotFound", err)
	}
}

// testWarned saves a status of 85 percent that warned at 80, then a status
// of 70 percent, and reads back that the threshold 80 stays warned in the
// month, so that a later 85 percent does not warn again.
func testWarned(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	if err := h.Budgets.CreateBudget(t.Context(), budget(), fence, entry("namespace:manage-budgets")); err != nil {
		t.Fatalf("CreateBudget: %v", err)
	}

	p := cost.PeriodOf(now())
	high := cost.Status{
		Budget:          "bgt-1",
		Period:          p,
		Actual:          eur(3400),
		Forecast:        eur(3400),
		Pending:         eur(0),
		Projected:       eur(3400),
		Percent:         85,
		Crossed:         []int{80},
		Warned:          nil,
		Skipped:         0,
		SkippedNegative: 0,
		Level:           cost.LevelWarning,
		AsOf:            now(),
	}

	high, warn := cost.MarkWarned(nil, high)
	if len(warn) != 1 || warn[0] != 80 {
		t.Fatalf("MarkWarned = %v, want [80]", warn)
	}

	if err := h.Budgets.SaveStatus(t.Context(), high, 0, fence); err != nil {
		t.Fatalf("SaveStatus 85%%: %v", err)
	}

	low := high
	low.Percent, low.Crossed, low.Level = 70, nil, cost.LevelOK

	prev, version, err := h.Budgets.LastStatus(t.Context(), shop, "bgt-1", p)
	if err != nil {
		t.Fatalf("LastStatus: %v", err)
	}

	low, _ = cost.MarkWarned(&prev, low)
	if err = h.Budgets.SaveStatus(t.Context(), low, version, fence); err != nil {
		t.Fatalf("SaveStatus 70%%: %v", err)
	}

	prev, _, err = h.Budgets.LastStatus(t.Context(), shop, "bgt-1", p)
	if err != nil || len(prev.Warned) != 1 || prev.Warned[0] != 80 || prev.Percent != 70 {
		t.Fatalf("LastStatus = %+v (%v), want 70%% with 80 warned", prev, err)
	}

	if _, warn = cost.MarkWarned(&prev, high); len(warn) != 0 {
		t.Fatalf("MarkWarned at 85%% again = %v, want none", warn)
	}
}
