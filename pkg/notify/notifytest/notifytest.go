// Package notifytest is the conformance suite for notify.Outbox,
// notify.Subscriptions and notify.Deliveries implementations. Every store
// adapter runs it:
//
//	func TestNotifyConformance(t *testing.T) {
//		t.Parallel()
//		notifytest.Run(t, func(t *testing.T) notifytest.Harness { return newHarness(t) })
//	}
//
// A reference adapter in memory, in the tests of this package, runs the
// suite, so each case runs before the first SQL adapter. The suite of the
// Sender port comes with the first sender adapter.
package notifytest

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/notify"
)

// Harness is one fresh, empty database for one case.
type Harness struct {
	Outbox        notify.Outbox
	Subscriptions notify.Subscriptions
	Deliveries    notify.Deliveries
	Domain        Domain
}

// Domain makes the rows that notifications need and that other ports own,
// and reads the audit entries that the stores append.
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
		{"each scope has its own increasing sequence", testAppendAfter},
		{"append skips a repeated dedupe key of a scope", testAppendDedupe},
		{"append refuses an invalid notification and two scopes", testAppendInvalid},
		{"a cursor moves only from where it is", testCursor},
		{"a sink in use cannot be deleted", testSinkInUse},
		{"a subscription names a sink of its scope", testSubscriptionScope},
		{"a second put on an older version is ErrConflict", testSubscriptionConflict},
		{"a row of the server scope takes the zero fence only", testServerScope},
		{"enqueue skips a delivery that exists", testEnqueueOnce},
		{"claim takes due deliveries once until the lease passes", testClaim},
		{"record after a later claim is ErrConflict", testRecordConflict},
		{"redeliver makes a delivery pending again", testRedeliver},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.run(t, newHarness(t))
		})
	}

	runFences(t, newHarness)
}

const (
	shop  = namespace.Name("shop")
	other = namespace.Name("other")
	owner = "instance-1"
	lease = time.Minute
)

func now() time.Time { return time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC) }

// entry returns a valid audit entry of the kind change.
func entry(scope namespace.Name) audit.Entry {
	return audit.Entry{
		At:           now(),
		Namespace:    scope,
		Subject:      "alice",
		IDPSubject:   "",
		TokenID:      "",
		Action:       "namespace:manage-notifications",
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

// manage returns the audit entry of a change to the notifications of the
// namespace shop.
func manage() audit.Entry { return entry(shop) }

func zeroFence() namespace.Fence { return namespace.Fence{Namespace: "", FailoverVersion: 0} }

func setup(t *testing.T, h Harness) namespace.Fence {
	t.Helper()

	fence, err := h.Domain.CreateNamespace(t.Context(), shop)
	if err != nil {
		t.Fatalf("CreateNamespace: %v", err)
	}

	return fence
}

func sample(id string, scope namespace.Name) notify.Notification {
	return notify.Notification{
		ID:         id,
		Type:       notify.JobFailed,
		Scope:      scope,
		Object:     "opentofu:" + scope.String() + "/network",
		ObjectID:   "u-1",
		Subject:    "alice",
		Agent:      "",
		Severity:   notify.SeverityWarning,
		Summary:    "The apply of network failed.",
		Links:      nil,
		Attributes: nil,
		Recipients: nil,
		DedupeKey:  id,
		At:         now(),
		Sequence:   0,
	}
}

func sink(id string, scope namespace.Name) notify.Sink {
	return notify.Sink{
		ID: id, Scope: scope, Kind: notify.SinkWebhook, Name: "Ops", Target: "https://hooks.example.com/ops",
		SecretRefs: []string{"itm-1"}, CreatedBy: "alice", Version: 0,
	}
}

func subscription(sinkID string) notify.Subscription {
	return notify.Subscription{
		ID: "sub-1", Scope: shop, Sink: sinkID,
		Filter: notify.Filter{Types: []string{"job.*"}, Objects: nil, MinSeverity: 0},
		State:  notify.SubscriptionStateActive, Reason: "", CreatedBy: "alice", Version: 0,
	}
}

func delivery(id, ntf string, next time.Time) notify.Delivery {
	return notify.Delivery{
		ID: id, Notification: ntf, Scope: shop, Subscription: "sub-1", Sink: "snk-1", Contact: "",
		State: notify.DeliveryPending, Attempts: 0, NextAt: next, LastError: "", DeliveredAt: time.Time{},
		Owner: "", LeaseUntil: time.Time{}, Version: 0,
	}
}

func putSink(t *testing.T, h Harness, fence namespace.Fence) {
	t.Helper()

	if err := h.Subscriptions.PutSink(t.Context(), sink("snk-1", shop), fence, manage()); err != nil {
		t.Fatalf("PutSink: %v", err)
	}
}

func subscribe(t *testing.T, h Harness, fence namespace.Fence) {
	t.Helper()
	putSink(t, h, fence)

	err := h.Subscriptions.PutSubscription(t.Context(), subscription("snk-1"), fence, manage())
	if err != nil {
		t.Fatalf("PutSubscription: %v", err)
	}
}

func enqueue(t *testing.T, h Harness, fence namespace.Fence, ds ...notify.Delivery) {
	t.Helper()

	if err := h.Deliveries.Enqueue(t.Context(), fence, ds); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
}

func claim(t *testing.T, h Harness, fence namespace.Fence, at time.Time, limit int) []notify.Delivery {
	t.Helper()

	got, err := h.Deliveries.Claim(t.Context(), fence, claimAt(at, limit))
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}

	return got
}

func move(from, to int64) notify.CursorMove {
	return notify.CursorMove{Name: "dispatch", Scope: shop, From: from, To: to}
}

func claimAt(at time.Time, limit int) notify.ClaimRequest {
	return notify.ClaimRequest{Scope: shop, Owner: owner, Now: at, Lease: lease, Limit: limit}
}

func ids(ds []notify.Delivery) string {
	out := make([]string, 0, len(ds))
	for i := range ds {
		out = append(out, ds[i].ID)
	}

	return fmt.Sprint(out)
}

func testAppendAfter(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)

	otherFence, err := h.Domain.CreateNamespace(t.Context(), other)
	if err != nil {
		t.Fatalf("CreateNamespace: %v", err)
	}

	for i := range 3 {
		appendOne(t, h, fence, sample(fmt.Sprintf("ntf-%d", i), shop))
	}

	appendOne(t, h, otherFence, sample("ntf-9", other))

	all, err := h.Outbox.After(t.Context(), shop, 0, 10)
	if err != nil || len(all) != 3 || all[0].ID != "ntf-0" || all[2].ID != "ntf-2" {
		t.Fatalf("After(shop, 0) = %d notifications (%v), want ntf-0 to ntf-2", len(all), err)
	}

	if all[0].Sequence >= all[1].Sequence || all[1].Sequence >= all[2].Sequence {
		t.Fatalf("sequences %d, %d, %d do not increase", all[0].Sequence, all[1].Sequence, all[2].Sequence)
	}

	rest, err := h.Outbox.After(t.Context(), shop, all[0].Sequence, 1)
	if err != nil || len(rest) != 1 || rest[0].ID != "ntf-1" {
		t.Fatalf("After(shop, first, 1) = %v (%v), want ntf-1", rest, err)
	}

	checkOtherScope(t, h)
}

// checkOtherScope checks that the scope other holds ntf-9 only, and that
// both scopes have notifications for a new cursor.
func checkOtherScope(t *testing.T, h Harness) {
	t.Helper()

	others, err := h.Outbox.After(t.Context(), other, 0, 10)
	if err != nil || len(others) != 1 || others[0].ID != "ntf-9" {
		t.Fatalf("After(other, 0) = %v (%v), want ntf-9 only", others, err)
	}

	scopes, err := h.Outbox.Scopes(t.Context(), "dispatch")
	if err != nil || fmt.Sprint(scopes) != "[other shop]" {
		t.Fatalf("Scopes = %v (%v), want [other shop]", scopes, err)
	}
}

func appendOne(t *testing.T, h Harness, fence namespace.Fence, n notify.Notification) {
	t.Helper()

	if err := h.Outbox.Append(t.Context(), fence, []notify.Notification{n}); err != nil {
		t.Fatalf("Append %s: %v", n.ID, err)
	}
}

func testAppendDedupe(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	first, again, repeat := sample("ntf-1", shop), sample("ntf-2", shop), sample("ntf-3", shop)
	again.DedupeKey, repeat.DedupeKey = first.DedupeKey, first.DedupeKey

	if err := h.Outbox.Append(t.Context(), fence, []notify.Notification{first, again}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	if err := h.Outbox.Append(t.Context(), fence, []notify.Notification{repeat}); err != nil {
		t.Fatalf("Append of a repeat: %v", err)
	}

	all, err := h.Outbox.After(t.Context(), shop, 0, 10)
	if err != nil || len(all) != 1 || all[0].ID != "ntf-1" {
		t.Fatalf("After = %+v (%v), want ntf-1 only", all, err)
	}
}

func testAppendInvalid(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	bad := sample("ntf-bad", shop)
	bad.Summary = ""

	cases := []struct {
		name string
		ns   []notify.Notification
	}{
		{"an invalid notification", []notify.Notification{bad}},
		{"two scopes", []notify.Notification{sample("ntf-1", shop), sample("ntf-2", other)}},
	}

	for _, tc := range cases {
		if err := h.Outbox.Append(t.Context(), fence, tc.ns); !errors.Is(err, notify.ErrInvalid) {
			t.Fatalf("Append of %s: err = %v, want ErrInvalid", tc.name, err)
		}
	}

	all, err := h.Outbox.After(t.Context(), shop, 0, 10)
	if err != nil || len(all) != 0 {
		t.Fatalf("After = %d notifications (%v), want none", len(all), err)
	}
}

func testCursor(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	appendOne(t, h, fence, sample("ntf-1", shop))

	all, err := h.Outbox.After(t.Context(), shop, 0, 10)
	if err != nil || len(all) != 1 {
		t.Fatalf("After = %v (%v)", all, err)
	}

	checkCursor(t, h, 0)

	seq := all[0].Sequence
	if err := h.Outbox.MoveCursor(t.Context(), fence, move(0, seq)); err != nil {
		t.Fatalf("MoveCursor: %v", err)
	}

	if err := h.Outbox.MoveCursor(t.Context(), fence, move(0, seq+1)); !errors.Is(err, notify.ErrConflict) {
		t.Fatalf("MoveCursor from 0 again: err = %v, want ErrConflict", err)
	}

	checkCursor(t, h, seq)

	if scopes, err := h.Outbox.Scopes(t.Context(), "dispatch"); err != nil || len(scopes) != 0 {
		t.Fatalf("Scopes after the cursor moved to the end = %v (%v), want none", scopes, err)
	}

	if scopes, err := h.Outbox.Scopes(t.Context(), "audit-hook"); err != nil || fmt.Sprint(scopes) != "[shop]" {
		t.Fatalf("Scopes of another cursor = %v (%v), want [shop]", scopes, err)
	}
}

func checkCursor(t *testing.T, h Harness, want int64) {
	t.Helper()

	if c, err := h.Outbox.Cursor(t.Context(), "dispatch", shop); err != nil || c != want {
		t.Fatalf("Cursor = %d (%v), want %d", c, err, want)
	}
}

func testSinkInUse(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	subscribe(t, h, fence)

	err := h.Subscriptions.DeleteSink(t.Context(), "snk-1", fence, manage())
	if !errors.Is(err, notify.ErrInUse) {
		t.Fatalf("DeleteSink: err = %v, want ErrInUse", err)
	}

	err = h.Subscriptions.DeleteSubscription(t.Context(), "sub-1", fence, manage())
	if err != nil {
		t.Fatalf("DeleteSubscription: %v", err)
	}

	err = h.Subscriptions.DeleteSink(t.Context(), "snk-1", fence, manage())
	if err != nil {
		t.Fatalf("DeleteSink after the subscription: %v", err)
	}

	entries, err := h.Domain.AuditEntries(t.Context(), shop)
	if err != nil || len(entries) != 4 {
		t.Fatalf("audit entries = %d (%v), want 4", len(entries), err)
	}
}

func testSubscriptionScope(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)

	otherFence, err := h.Domain.CreateNamespace(t.Context(), other)
	if err != nil {
		t.Fatalf("CreateNamespace: %v", err)
	}

	err = h.Subscriptions.PutSink(t.Context(), sink("snk-9", other), otherFence, entry(other))
	if err != nil {
		t.Fatalf("PutSink: %v", err)
	}

	err = h.Subscriptions.PutSubscription(t.Context(), subscription("snk-9"), fence, manage())
	if !errors.Is(err, notify.ErrNotFound) {
		t.Fatalf("PutSubscription with a sink of another scope: err = %v, want ErrNotFound", err)
	}

	moved := sink("snk-9", shop)
	moved.Version = 1

	if err := h.Subscriptions.PutSink(t.Context(), moved, fence, manage()); !errors.Is(err, notify.ErrNotFound) {
		t.Fatalf("PutSink over a sink of another scope: err = %v, want ErrNotFound", err)
	}
}

func testSubscriptionConflict(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	subscribe(t, h, fence)

	s := subscription("snk-1")
	s.Version = 1
	s.Filter.MinSeverity = notify.SeverityCritical

	if err := h.Subscriptions.PutSubscription(t.Context(), s, fence, manage()); err != nil {
		t.Fatalf("PutSubscription: %v", err)
	}

	if err := h.Subscriptions.PutSubscription(t.Context(), s, fence, manage()); !errors.Is(err, notify.ErrConflict) {
		t.Fatalf("second PutSubscription: err = %v, want ErrConflict", err)
	}

	subs, err := h.Subscriptions.Subscriptions(t.Context(), shop)
	if err != nil || len(subs) != 1 || subs[0].Filter.MinSeverity != notify.SeverityCritical {
		t.Fatalf("Subscriptions = %+v (%v), want the changed filter", subs, err)
	}
}

func testServerScope(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	s := sink("snk-1", audit.ServerScope)

	serverEntry := entry(audit.ServerScope)

	if err := h.Subscriptions.PutSink(t.Context(), s, fence, serverEntry); !errors.Is(err, notify.ErrInvalid) {
		t.Fatalf("PutSink in the server scope with a namespace fence: err = %v, want ErrInvalid", err)
	}

	if err := h.Subscriptions.PutSink(t.Context(), s, zeroFence(), entry(audit.ServerScope)); err != nil {
		t.Fatalf("PutSink in the server scope with the zero fence: %v", err)
	}

	n := sample("ntf-1", audit.ServerScope)
	if err := h.Outbox.Append(t.Context(), zeroFence(), []notify.Notification{n}); err != nil {
		t.Fatalf("Append in the server scope: %v", err)
	}

	if err := h.Subscriptions.DeleteSink(t.Context(), "snk-1", fence, manage()); !errors.Is(err, notify.ErrNotFound) {
		t.Fatalf("DeleteSink of a server sink with a namespace fence: err = %v, want ErrNotFound", err)
	}

	sinks, err := h.Subscriptions.Sinks(t.Context(), audit.ServerScope)
	if err != nil || len(sinks) != 1 {
		t.Fatalf("Sinks(@server) = %v (%v), want one", sinks, err)
	}
}

func testEnqueueOnce(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	enqueue(t, h, fence, delivery("dlv-1", "ntf-1", now()))
	enqueue(t, h, fence, delivery("dlv-2", "ntf-1", now()))

	if got := claim(t, h, fence, now(), 10); ids(got) != "[dlv-1]" {
		t.Fatalf("Claim = %s, want [dlv-1]", ids(got))
	}

	stray := delivery("dlv-3", "ntf-2", now())
	stray.Scope = other

	mixed := []notify.Delivery{delivery("dlv-4", "ntf-3", now()), stray}
	if err := h.Deliveries.Enqueue(t.Context(), fence, mixed); !errors.Is(err, notify.ErrInvalid) {
		t.Fatalf("Enqueue of two scopes: err = %v, want ErrInvalid", err)
	}
}

func testClaim(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	enqueue(t, h, fence,
		delivery("dlv-1", "ntf-1", now().Add(-time.Minute)),
		delivery("dlv-2", "ntf-2", now().Add(-time.Hour)),
		delivery("dlv-3", "ntf-3", now().Add(time.Hour)))

	if scopes, err := h.Deliveries.DueScopes(t.Context(), now()); err != nil || fmt.Sprint(scopes) != "[shop]" {
		t.Fatalf("DueScopes = %v (%v), want [shop]", scopes, err)
	}

	got := claim(t, h, fence, now(), 10)
	if ids(got) != "[dlv-2 dlv-1]" || got[0].Owner != owner || !got[0].LeaseUntil.Equal(now().Add(lease)) {
		t.Fatalf("Claim = %+v, want dlv-2 then dlv-1 with the claim of %s", got, owner)
	}

	if again := claim(t, h, fence, now().Add(lease/2), 10); len(again) != 0 {
		t.Fatalf("Claim under a live lease = %s, want none", ids(again))
	}

	if scopes, err := h.Deliveries.DueScopes(t.Context(), now().Add(lease/2)); err != nil || len(scopes) != 0 {
		t.Fatalf("DueScopes under a live lease = %v (%v), want none", scopes, err)
	}

	if later := claim(t, h, fence, now().Add(lease), 10); ids(later) != "[dlv-2 dlv-1]" {
		t.Fatalf("Claim after the lease = %s, want dlv-2 then dlv-1", ids(later))
	}

	bad := []struct {
		name  string
		owner string
		lease time.Duration
	}{{"no owner", "", lease}, {"no lease", owner, 0}}

	for _, tc := range bad {
		req := claimAt(now(), 1)
		req.Owner, req.Lease = tc.owner, tc.lease

		_, err := h.Deliveries.Claim(t.Context(), fence, req)
		if !errors.Is(err, notify.ErrInvalid) {
			t.Fatalf("Claim with %s: err = %v, want ErrInvalid", tc.name, err)
		}
	}
}

func testRecordConflict(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	enqueue(t, h, fence, delivery("dlv-1", "ntf-1", now()))

	lost := claim(t, h, fence, now(), 1)
	if len(lost) != 1 {
		t.Fatalf("Claim = %s, want one", ids(lost))
	}

	live := claim(t, h, fence, now().Add(lease), 1)
	if len(live) != 1 {
		t.Fatalf("Claim after the lease = %s, want one", ids(live))
	}

	d := lost[0]
	d.State, d.Attempts, d.DeliveredAt = notify.DeliveryDone, 1, now()

	if err := h.Deliveries.Record(t.Context(), fence, d); !errors.Is(err, notify.ErrConflict) {
		t.Fatalf("Record by the lost claim: err = %v, want ErrConflict", err)
	}

	d = live[0]
	d.State, d.Attempts, d.DeliveredAt = notify.DeliveryDone, 1, now()

	if err := h.Deliveries.Record(t.Context(), fence, d); err != nil {
		t.Fatalf("Record: %v", err)
	}

	if err := h.Deliveries.Record(t.Context(), fence, d); !errors.Is(err, notify.ErrConflict) {
		t.Fatalf("second Record: err = %v, want ErrConflict", err)
	}

	hist, err := h.Deliveries.History(t.Context(), "sub-1", 10)
	if err != nil || len(hist) != 1 || hist[0].State != notify.DeliveryDone || hist[0].Owner != "" {
		t.Fatalf("History = %+v (%v), want one delivered with no claim", hist, err)
	}
}

func testRedeliver(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	enqueue(t, h, fence, delivery("dlv-1", "ntf-1", now()))

	got := claim(t, h, fence, now(), 1)
	if len(got) != 1 {
		t.Fatalf("Claim = %s, want one", ids(got))
	}

	d := got[0]
	d.State, d.Attempts, d.LastError = notify.DeliveryAbandoned, notify.MaxAttempts, notify.ClassTemporary

	if err := h.Deliveries.Record(t.Context(), fence, d); err != nil {
		t.Fatalf("Record: %v", err)
	}

	if err := h.Deliveries.Redeliver(t.Context(), "dlv-1", fence, manage()); err != nil {
		t.Fatalf("Redeliver: %v", err)
	}

	got = claim(t, h, fence, now().Add(time.Minute), 10)
	if len(got) != 1 || got[0].Attempts != 0 || got[0].State != notify.DeliveryPending {
		t.Fatalf("Claim after Redeliver = %+v, want one pending with no attempts", got)
	}

	if err := h.Deliveries.Redeliver(t.Context(), "dlv-none", fence, manage()); !errors.Is(err, notify.ErrNotFound) {
		t.Fatalf("Redeliver unknown: err = %v, want ErrNotFound", err)
	}
}

// write is one write method of the ports, for the fence cases. prepare
// makes the rows that the write needs, with the good fence. do runs the
// write with fence. scopeErr is the error of a fence of another scope.
type write struct {
	name     string
	prepare  func(t *testing.T, h Harness, fence namespace.Fence)
	do       func(ctx context.Context, h Harness, fence namespace.Fence) error
	scopeErr error
}

func writes() []write {
	none := func(*testing.T, Harness, namespace.Fence) {}
	queued := func(t *testing.T, h Harness, fence namespace.Fence) {
		t.Helper()
		enqueue(t, h, fence, delivery("dlv-1", "ntf-1", now()))
	}

	return []write{
		{"Append", none, func(ctx context.Context, h Harness, f namespace.Fence) error {
			return h.Outbox.Append(ctx, f, []notify.Notification{sample("ntf-1", shop)})
		}, notify.ErrInvalid},
		{"MoveCursor", none, func(ctx context.Context, h Harness, f namespace.Fence) error {
			return h.Outbox.MoveCursor(ctx, f, move(0, 1))
		}, notify.ErrInvalid},
		{"PutSink", none, func(ctx context.Context, h Harness, f namespace.Fence) error {
			return h.Subscriptions.PutSink(ctx, sink("snk-1", shop), f, manage())
		}, notify.ErrInvalid},
		{"DeleteSink", putSink, func(ctx context.Context, h Harness, f namespace.Fence) error {
			return h.Subscriptions.DeleteSink(ctx, "snk-1", f, manage())
		}, notify.ErrNotFound},
		{"PutSubscription", putSink, func(ctx context.Context, h Harness, f namespace.Fence) error {
			return h.Subscriptions.PutSubscription(ctx, subscription("snk-1"), f, manage())
		}, notify.ErrInvalid},
		{"DeleteSubscription", subscribe, func(ctx context.Context, h Harness, f namespace.Fence) error {
			return h.Subscriptions.DeleteSubscription(ctx, "sub-1", f, manage())
		}, notify.ErrNotFound},
		{"Enqueue", none, func(ctx context.Context, h Harness, f namespace.Fence) error {
			return h.Deliveries.Enqueue(ctx, f, []notify.Delivery{delivery("dlv-1", "ntf-1", now())})
		}, notify.ErrInvalid},
		{"Claim", queued, func(ctx context.Context, h Harness, f namespace.Fence) error {
			_, err := h.Deliveries.Claim(ctx, f, claimAt(now(), 1))

			return err
		}, notify.ErrInvalid},
		{"Record", queued, func(ctx context.Context, h Harness, f namespace.Fence) error {
			d := delivery("dlv-1", "ntf-1", now())
			d.Version = 1

			return h.Deliveries.Record(ctx, f, d)
		}, notify.ErrNotFound},
		{"Redeliver", queued, func(ctx context.Context, h Harness, f namespace.Fence) error {
			return h.Deliveries.Redeliver(ctx, "dlv-1", f, manage())
		}, notify.ErrNotFound},
	}
}

// fenceCase is one kind of wrong fence: fence returns it, and want
// returns the error that a write w returns for it.
type fenceCase struct {
	name  string
	fence func(t *testing.T, h Harness, good namespace.Fence) namespace.Fence
	want  func(w write) error
}

func fenceCases() []fenceCase {
	return []fenceCase{
		{
			"a zero fence", func(*testing.T, Harness, namespace.Fence) namespace.Fence { return zeroFence() },
			func(w write) error { return w.scopeErr },
		},
		{"the fence of another namespace", func(t *testing.T, h Harness, _ namespace.Fence) namespace.Fence {
			t.Helper()

			f, err := h.Domain.CreateNamespace(t.Context(), other)
			if err != nil {
				t.Fatalf("CreateNamespace: %v", err)
			}

			return f
		}, func(w write) error { return w.scopeErr }},
		{"a stale fence", func(t *testing.T, h Harness, good namespace.Fence) namespace.Fence {
			t.Helper()

			if _, err := h.Domain.BumpFence(t.Context(), good); err != nil {
				t.Fatalf("BumpFence: %v", err)
			}

			return good
		}, func(write) error { return namespace.ErrFenced }},
	}
}

// runFences runs every write with a zero fence, the fence of another
// namespace and a stale fence, and checks that each fails with nothing
// written.
func runFences(t *testing.T, newHarness Factory) {
	t.Helper()

	for _, w := range writes() {
		for _, f := range fenceCases() {
			t.Run(w.name+" with "+f.name, func(t *testing.T) {
				t.Parallel()
				testFence(t, newHarness(t), w, f)
			})
		}
	}
}

func testFence(t *testing.T, h Harness, w write, f fenceCase) {
	t.Helper()

	good := setup(t, h)
	w.prepare(t, h, good)

	before, err := h.Domain.AuditEntries(t.Context(), shop)
	if err != nil {
		t.Fatalf("AuditEntries: %v", err)
	}

	if werr := w.do(t.Context(), h, f.fence(t, h, good)); !errors.Is(werr, f.want(w)) {
		t.Fatalf("%s: err = %v, want %v", w.name, werr, f.want(w))
	}

	after, err := h.Domain.AuditEntries(t.Context(), shop)
	if err != nil || len(after) != len(before) {
		t.Fatalf("audit entries = %d (%v), want %d", len(after), err, len(before))
	}

	all, err := h.Outbox.After(t.Context(), shop, 0, 10)
	if err != nil || len(all) != 0 {
		t.Fatalf("After = %d notifications (%v), want none", len(all), err)
	}
}
