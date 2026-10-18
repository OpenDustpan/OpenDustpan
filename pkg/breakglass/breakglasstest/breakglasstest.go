// Package breakglasstest is the conformance suite for breakglass.Store
// implementations. Every store adapter runs it:
//
//	func TestBreakGlassConformance(t *testing.T) {
//		t.Parallel()
//		breakglasstest.Run(t, func(t *testing.T) breakglasstest.Harness { return newHarness(t) })
//	}
//
// A reference adapter in memory, in the tests of this package, runs the
// suite, so each case runs before the first SQL adapter.
package breakglasstest

import (
	"context"
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/opsmem"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/breakglass"
	"opendustpan.org/opendustpan/pkg/calendar"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Harness is one fresh, empty database for one case.
type Harness struct {
	Store  breakglass.Store
	Domain Domain
}

// Domain makes the rows that a review needs and that other ports own, and
// reads the audit entries that the store appends.
type Domain interface {
	// CreateNamespace creates the namespace name and returns its fence.
	CreateNamespace(ctx context.Context, name namespace.Name) (namespace.Fence, error)
	// BumpFence increments the failover version of the namespace of fence.
	BumpFence(ctx context.Context, fence namespace.Fence) (namespace.Fence, error)
	// AuditEntries returns the audit entries of scope, in append order.
	AuditEntries(ctx context.Context, scope namespace.Name) ([]audit.Entry, error)
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
		{"open stores a review and appends one entry", testOpen},
		{"a second open of one use returns the first review", testOpenOnce},
		{"get of an unknown ID is ErrNotFound", testGetUnknown},
		{"record stores a verdict once", testRecordOnce},
		{"record on an older version is ErrConflict", testRecordConflict},
		{"advance on an older version is ErrConflict", testAdvanceConflict},
		{"due lists pending reviews with a notice due, oldest first", testDue},
		{"list filters by scope, state and deadline", testList},
		{"a review of a session is in the server scope with no fence", testServerScope},
		{"a stale fence changes nothing", testStaleFence},
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

func entry(scope namespace.Name, action string) audit.Entry {
	return opsmem.Entry(scope, "alice", action, scope.String(), now())
}

func setup(t *testing.T, h Harness) namespace.Fence {
	t.Helper()

	fence, err := h.Domain.CreateNamespace(t.Context(), shop)
	if err != nil {
		t.Fatalf("CreateNamespace: %v", err)
	}

	return fence
}

func review(id, ref string, next time.Time) breakglass.Review {
	return breakglass.Review{
		ID: id,
		Use: breakglass.Use{
			Kind: breakglass.KindFreezeBreak, Ref: ref, Scope: shop, Object: "opentofu:shop/network",
			Subject: "alice", Reason: "Roll back", At: now(),
		},
		State:        breakglass.StatePending,
		CreatedAt:    now(),
		DueAt:        now().Add(31 * time.Hour),
		Calendar:     calendar.DefaultID,
		CalendarFrom: calendar.Scope{Namespace: "", Group: ""},
		Step:         0,
		NextNoticeAt: next,
		Verdict: breakglass.Verdict{
			Reviewer:   "",
			Note:       "",
			Auth:       "",
			AuthAt:     time.Time{},
			ReviewedAt: time.Time{},
		},
		Version: 0,
	}
}

func open(t *testing.T, h Harness, fence namespace.Fence, r breakglass.Review) breakglass.Review {
	t.Helper()

	got, err := h.Store.Open(t.Context(), r, fence, entry(r.Use.Scope, "namespace:break-freeze"))
	if err != nil {
		t.Fatalf("Open %s: %v", r.ID, err)
	}

	return got
}

func accept(r breakglass.Review) breakglass.Review {
	r.State = breakglass.StateAccepted
	r.Verdict = breakglass.Verdict{
		Reviewer: "bob", Note: "", Auth: breakglass.AuthMFA, AuthAt: now(), ReviewedAt: now(),
	}
	r.NextNoticeAt = time.Time{}

	return r
}

func testOpen(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	got := open(t, h, fence, review("bgr-1", "job-1", now()))

	if got.Version != 1 || got.State != breakglass.StatePending {
		t.Fatalf("Open = %+v, want a pending review with version 1", got)
	}

	stored, err := h.Store.Get(t.Context(), "bgr-1")
	if err != nil || stored.Use.Ref != "job-1" || !stored.DueAt.Equal(got.DueAt) {
		t.Fatalf("Get = %+v (%v), want the opened review", stored, err)
	}

	entries, err := h.Domain.AuditEntries(t.Context(), shop)
	if err != nil || len(entries) != 1 {
		t.Fatalf("audit entries = %d (%v), want 1", len(entries), err)
	}
}

func testOpenOnce(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	first := open(t, h, fence, review("bgr-1", "job-1", now()))
	second := open(t, h, fence, review("bgr-2", "job-1", now()))

	if second.ID != first.ID {
		t.Fatalf("second Open = %s, want %s", second.ID, first.ID)
	}

	if _, err := h.Store.Get(t.Context(), "bgr-2"); !errors.Is(err, breakglass.ErrNotFound) {
		t.Fatalf("Get bgr-2: err = %v, want ErrNotFound", err)
	}

	entries, err := h.Domain.AuditEntries(t.Context(), shop)
	if err != nil || len(entries) != 1 {
		t.Fatalf("audit entries = %d (%v), want 1", len(entries), err)
	}
}

func testGetUnknown(t *testing.T, h Harness) {
	t.Helper()

	if _, err := h.Store.Get(t.Context(), "bgr-none"); !errors.Is(err, breakglass.ErrNotFound) {
		t.Fatalf("Get: err = %v, want ErrNotFound", err)
	}
}

func testRecordOnce(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	r := accept(open(t, h, fence, review("bgr-1", "job-1", now())))

	if err := h.Store.Record(t.Context(), r, fence, entry(shop, "namespace:review-break-glass")); err != nil {
		t.Fatalf("Record: %v", err)
	}

	got, err := h.Store.Get(t.Context(), "bgr-1")
	if err != nil || got.State != breakglass.StateAccepted || got.Verdict.Reviewer != "bob" || got.Version != 2 {
		t.Fatalf("Get = %+v (%v), want the verdict with version 2", got, err)
	}

	got = accept(got)

	err = h.Store.Record(t.Context(), got, fence, entry(shop, "namespace:review-break-glass"))
	if !errors.Is(err, breakglass.ErrReviewed) {
		t.Fatalf("second Record: err = %v, want ErrReviewed", err)
	}
}

func testRecordConflict(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	r := open(t, h, fence, review("bgr-1", "job-1", now()))

	next := r
	next.Step, next.NextNoticeAt = 1, now().Add(time.Hour)

	if err := h.Store.Advance(t.Context(), next, fence); err != nil {
		t.Fatalf("Advance: %v", err)
	}

	err := h.Store.Record(t.Context(), accept(r), fence, entry(shop, "namespace:review-break-glass"))
	if !errors.Is(err, breakglass.ErrConflict) {
		t.Fatalf("Record on version 1: err = %v, want ErrConflict", err)
	}
}

func testAdvanceConflict(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	r := open(t, h, fence, review("bgr-1", "job-1", now()))
	r.Step = 1

	if err := h.Store.Advance(t.Context(), r, fence); err != nil {
		t.Fatalf("Advance: %v", err)
	}

	if err := h.Store.Advance(t.Context(), r, fence); !errors.Is(err, breakglass.ErrConflict) {
		t.Fatalf("second Advance: err = %v, want ErrConflict", err)
	}

	entries, err := h.Domain.AuditEntries(t.Context(), shop)
	if err != nil || len(entries) != 1 {
		t.Fatalf("audit entries = %d (%v), want only the entry of the open", len(entries), err)
	}
}

func testDue(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	open(t, h, fence, review("bgr-late", "job-1", now().Add(-time.Minute)))
	open(t, h, fence, review("bgr-early", "job-2", now().Add(-time.Hour)))
	open(t, h, fence, review("bgr-future", "job-3", now().Add(time.Hour)))
	open(t, h, fence, review("bgr-none", "job-4", time.Time{}))

	done := open(t, h, fence, review("bgr-done", "job-5", now().Add(-2*time.Hour)))

	err := h.Store.Record(t.Context(), accept(done), fence, entry(shop, "namespace:review-break-glass"))
	if err != nil {
		t.Fatalf("Record: %v", err)
	}

	got, err := h.Store.Due(t.Context(), now(), 10)
	if err != nil || len(got) != 2 || got[0].ID != "bgr-early" || got[1].ID != "bgr-late" {
		t.Fatalf("Due = %v (%v), want bgr-early then bgr-late", ids(got), err)
	}

	got, err = h.Store.Due(t.Context(), now(), 1)
	if err != nil || len(got) != 1 {
		t.Fatalf("Due with limit 1 = %v (%v)", ids(got), err)
	}
}

func testList(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	open(t, h, fence, review("bgr-1", "job-1", now()))

	late := review("bgr-2", "job-2", now())
	late.DueAt = now().Add(-time.Hour)
	open(t, h, fence, late)

	cases := []struct {
		f    breakglass.Filter
		want int
	}{
		{breakglass.Filter{Scope: shop, State: "", DueBefore: time.Time{}, After: "", Limit: 0}, 2},
		{
			breakglass.Filter{
				Scope:     shop,
				State:     breakglass.StateAccepted,
				DueBefore: time.Time{},
				After:     "",
				Limit:     0,
			},
			0,
		},
		{breakglass.Filter{Scope: shop, State: breakglass.StatePending, DueBefore: now(), After: "", Limit: 0}, 1},
		{breakglass.Filter{Scope: "other", State: "", DueBefore: time.Time{}, After: "", Limit: 0}, 0},
		{breakglass.Filter{Scope: shop, State: "", DueBefore: time.Time{}, After: "bgr-1", Limit: 0}, 1},
	}

	for i, tc := range cases {
		got, err := h.Store.List(t.Context(), tc.f)
		if err != nil || len(got) != tc.want {
			t.Fatalf("case %d: List = %v (%v), want %d", i, ids(got), err, tc.want)
		}
	}
}

func testServerScope(t *testing.T, h Harness) {
	t.Helper()

	r := review("bgr-1", "ses-1", now())
	r.Use.Kind, r.Use.Scope, r.Use.Object = breakglass.KindAccountSession, audit.ServerScope, ""
	open(t, h, namespace.Fence{Namespace: "", FailoverVersion: 0}, r)

	entries, err := h.Domain.AuditEntries(t.Context(), audit.ServerScope)
	if err != nil || len(entries) != 1 {
		t.Fatalf("server audit entries = %d (%v), want 1", len(entries), err)
	}
}

func testStaleFence(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	if _, err := h.Domain.BumpFence(t.Context(), fence); err != nil {
		t.Fatalf("BumpFence: %v", err)
	}

	_, err := h.Store.Open(t.Context(), review("bgr-1", "job-1", now()), fence, entry(shop, "namespace:break-freeze"))
	if !errors.Is(err, namespace.ErrFenced) {
		t.Fatalf("Open: err = %v, want namespace.ErrFenced", err)
	}

	if _, err := h.Store.Get(t.Context(), "bgr-1"); !errors.Is(err, breakglass.ErrNotFound) {
		t.Fatalf("Get after a fenced open: err = %v, want ErrNotFound", err)
	}
}

func ids(rs []breakglass.Review) []string {
	out := make([]string, 0, len(rs))
	for i := range rs {
		out = append(out, rs[i].ID)
	}

	return out
}
