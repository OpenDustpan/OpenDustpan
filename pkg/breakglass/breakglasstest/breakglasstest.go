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
		{"each write refuses a fence of another scope", testFenceScope},
		{"record refuses a verdict that fails the rules of a review", testRecordInvalid},
		{"record checks the stored use, not the use of the caller", testRecordStoredUse},
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

// entry returns a valid audit entry of the kind change in scope.
func entry(scope namespace.Name, action string) audit.Entry {
	return audit.Entry{
		At:           now(),
		Namespace:    scope,
		Subject:      "server",
		IDPSubject:   "",
		TokenID:      "",
		Action:       action,
		Object:       scope.String(),
		Version:      "",
		ValuePath:    "",
		ValueCount:   nil,
		Changes:      nil,
		SourceIP:     "192.0.2.1",
		UserAgent:    "suite",
		Channel:      audit.ChannelAPI,
		Kind:         audit.KindChange,
		Outcome:      audit.OutcomeSuccess,
		RequestID:    "req-suite",
		ObjectID:     "",
		PrevHash:     "",
		Hash:         "",
		Sequence:     0,
		FenceVersion: 0,
	}
}

// zero is the fence of a write in audit.ServerScope.
func zero() namespace.Fence { return namespace.Fence{Namespace: "", FailoverVersion: 0} }

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
			Subject: "alice", People: []string{"alice", "carol"}, Reason: "Roll back", At: now(),
			Until: time.Time{},
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

	got, err := h.Store.Open(t.Context(), r, fence, entry(r.Use.Scope, openAction(r)))
	if err != nil {
		t.Fatalf("Open %s: %v", r.ID, err)
	}

	return got
}

// session returns the review of a break-glass account session that ended
// an hour before now.
func session(id, ref string) breakglass.Review {
	r := review(id, ref, now())
	r.Use.Kind, r.Use.Scope, r.Use.Object = breakglass.KindAccountSession, audit.ServerScope, ""
	r.Use.At, r.Use.Until = now().Add(-2*time.Hour), now().Add(-time.Hour)

	return r
}

// openAction returns the action of the audit entry of an open.
func openAction(r breakglass.Review) string {
	if r.Use.Scope == audit.ServerScope {
		return "server:review-break-glass"
	}

	return "namespace:review-break-glass"
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

	r := open(t, h, zero(), session("bgr-1", "ses-1"))

	next := r
	next.Step = 1

	if err := h.Store.Advance(t.Context(), next, zero()); err != nil {
		t.Fatalf("Advance: %v", err)
	}

	r.Version = 2

	err := h.Store.Record(t.Context(), accept(r), zero(), entry(audit.ServerScope, "server:review-break-glass"))
	if err != nil {
		t.Fatalf("Record: %v", err)
	}

	entries, err := h.Domain.AuditEntries(t.Context(), audit.ServerScope)
	if err != nil || len(entries) != 2 {
		t.Fatalf("server audit entries = %d (%v), want 2", len(entries), err)
	}
}

func testStaleFence(t *testing.T, h Harness) {
	t.Helper()

	old := setup(t, h)
	r := open(t, h, old, review("bgr-1", "job-1", now()))

	if _, err := h.Domain.BumpFence(t.Context(), old); err != nil {
		t.Fatalf("BumpFence: %v", err)
	}

	writes := []struct {
		name  string
		write func() error
	}{
		{"open", func() error {
			_, err := h.Store.Open(t.Context(), review("bgr-2", "job-2", now()), old, entry(shop, openAction(r)))

			return err
		}},
		{"record", func() error {
			return h.Store.Record(t.Context(), accept(r), old, entry(shop, "namespace:review-break-glass"))
		}},
		{"advance", func() error {
			next := r
			next.Step = 1

			return h.Store.Advance(t.Context(), next, old)
		}},
	}

	for _, w := range writes {
		if err := w.write(); !errors.Is(err, namespace.ErrFenced) {
			t.Fatalf("%s: err = %v, want namespace.ErrFenced", w.name, err)
		}
	}

	unchanged(t, h, r, shop)

	if _, err := h.Store.Get(t.Context(), "bgr-2"); !errors.Is(err, breakglass.ErrNotFound) {
		t.Fatalf("Get after a fenced open: err = %v, want ErrNotFound", err)
	}
}

func testFenceScope(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)

	otherFence, err := h.Domain.CreateNamespace(t.Context(), other)
	if err != nil {
		t.Fatalf("CreateNamespace: %v", err)
	}

	inShop := open(t, h, fence, review("bgr-1", "job-1", now()))
	inServer := open(t, h, zero(), session("bgr-2", "ses-1"))

	cases := []struct {
		name  string
		r     breakglass.Review
		fence namespace.Fence
	}{
		{"a zero fence for a namespace", inShop, zero()},
		{"the fence of another namespace", inShop, otherFence},
		{"a fence for the server scope", inServer, fence},
	}

	for i := range cases {
		tc := &cases[i]
		fresh := tc.r
		fresh.ID, fresh.Use.Ref = "bgr-new", "new"

		_, err := h.Store.Open(t.Context(), fresh, tc.fence, entry(fresh.Use.Scope, openAction(fresh)))
		if !errors.Is(err, breakglass.ErrInvalid) {
			t.Fatalf("%s: Open: err = %v, want ErrInvalid", tc.name, err)
		}

		_, err = h.Store.Open(t.Context(), tc.r, tc.fence, entry(tc.r.Use.Scope, openAction(tc.r)))
		if !errors.Is(err, breakglass.ErrInvalid) {
			t.Fatalf("%s: second Open: err = %v, want ErrInvalid", tc.name, err)
		}

		err = h.Store.Record(t.Context(), accept(tc.r), tc.fence, entry(tc.r.Use.Scope, openAction(tc.r)))
		if !errors.Is(err, breakglass.ErrNotFound) {
			t.Fatalf("%s: Record: err = %v, want ErrNotFound", tc.name, err)
		}

		next := tc.r
		next.Step = 1

		if err := h.Store.Advance(t.Context(), next, tc.fence); !errors.Is(err, breakglass.ErrNotFound) {
			t.Fatalf("%s: Advance: err = %v, want ErrNotFound", tc.name, err)
		}

		if _, err := h.Store.Get(t.Context(), "bgr-new"); !errors.Is(err, breakglass.ErrNotFound) {
			t.Fatalf("%s: Get bgr-new: err = %v, want ErrNotFound", tc.name, err)
		}
	}

	unchanged(t, h, inShop, shop)
	unchanged(t, h, inServer, audit.ServerScope)

	if entries, err := h.Domain.AuditEntries(t.Context(), other); err != nil || len(entries) != 0 {
		t.Fatalf("audit entries of %s = %d (%v), want 0", other, len(entries), err)
	}
}

func testRecordInvalid(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	r := open(t, h, fence, review("bgr-1", "job-1", now()))
	early := session("bgr-2", "ses-1")
	early.Use.Until = now().Add(time.Hour)
	inUse := open(t, h, zero(), early)

	cases := []struct {
		name   string
		r      breakglass.Review
		fence  namespace.Fence
		change func(r *breakglass.Review)
		want   error
	}{
		{"no reviewer", r, fence, func(r *breakglass.Review) { r.Verdict.Reviewer = "" }, breakglass.ErrInvalid},
		{
			"the subject",
			r,
			fence,
			func(r *breakglass.Review) { r.Verdict.Reviewer = "Alice" },
			breakglass.ErrSelfReview,
		},
		{
			"a person of the use",
			r,
			fence,
			func(r *breakglass.Review) { r.Verdict.Reviewer = "carol" },
			breakglass.ErrSelfReview,
		},
		{
			"a pending state",
			r,
			fence,
			func(r *breakglass.Review) { r.State = breakglass.StatePending },
			breakglass.ErrInvalid,
		},
		{
			"a dispute with no note",
			r,
			fence,
			func(r *breakglass.Review) { r.State = breakglass.StateDisputed },
			breakglass.ErrInvalid,
		},
		{"no authentication", r, fence, func(r *breakglass.Review) { r.Verdict.Auth = "" }, breakglass.ErrInvalid},
		{"an old authentication", r, fence, func(r *breakglass.Review) {
			r.Verdict.AuthAt = now().Add(-breakglass.Freshness - time.Second)
		}, breakglass.ErrInvalid},
		{"no time", r, fence, func(r *breakglass.Review) { r.Verdict.ReviewedAt = time.Time{} }, breakglass.ErrInvalid},
		{"a session in its time limit", inUse, zero(), func(*breakglass.Review) {}, breakglass.ErrInUse},
	}

	for i := range cases {
		tc := &cases[i]
		v := accept(tc.r)
		tc.change(&v)

		err := h.Store.Record(t.Context(), v, tc.fence, entry(tc.r.Use.Scope, openAction(tc.r)))
		if !errors.Is(err, tc.want) {
			t.Fatalf("%s: Record: err = %v, want %v", tc.name, err, tc.want)
		}
	}

	unchanged(t, h, r, shop)
	unchanged(t, h, inUse, audit.ServerScope)
}

func testRecordStoredUse(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	r := accept(open(t, h, fence, review("bgr-1", "job-1", now())))
	r.Use.Subject, r.Use.People = "dave", []string{"dave"}
	r.Verdict.Reviewer = "carol"

	err := h.Store.Record(t.Context(), r, fence, entry(shop, "namespace:review-break-glass"))
	if !errors.Is(err, breakglass.ErrSelfReview) {
		t.Fatalf("Record: err = %v, want ErrSelfReview", err)
	}

	got, err := h.Store.Get(t.Context(), "bgr-1")
	if err != nil || got.Use.Subject != "alice" || got.State != breakglass.StatePending {
		t.Fatalf("Get = %+v (%v), want the pending review of alice", got, err)
	}
}

// unchanged fails t when the stored review of r has another version,
// state or step than r, or when scope holds another audit entry than the
// entry of the open.
func unchanged(t *testing.T, h Harness, r breakglass.Review, scope namespace.Name) {
	t.Helper()

	got, err := h.Store.Get(t.Context(), r.ID)
	if err != nil || got.Version != r.Version || got.State != r.State || got.Step != r.Step {
		t.Fatalf("Get %s = %+v (%v), want it unchanged", r.ID, got, err)
	}

	entries, err := h.Domain.AuditEntries(t.Context(), scope)
	if err != nil || len(entries) != 1 {
		t.Fatalf("audit entries of %s = %d (%v), want 1", scope, len(entries), err)
	}
}

func ids(rs []breakglass.Review) []string {
	out := make([]string, 0, len(rs))
	for i := range rs {
		out = append(out, rs[i].ID)
	}

	return out
}
