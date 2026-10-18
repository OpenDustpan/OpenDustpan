// Package eventtest is the conformance suite for event.Outbox,
// event.Subscriptions and event.Deliveries implementations. Every store
// adapter runs it:
//
//	func TestEventConformance(t *testing.T) {
//		t.Parallel()
//		eventtest.Run(t, func(t *testing.T) eventtest.Harness { return newHarness(t) })
//	}
//
// A reference adapter in memory, in the tests of this package, runs the
// suite, so each case runs before the first SQL adapter. The suite of the
// Sender port comes with the first sender adapter.
package eventtest

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/opsmem"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/event"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Harness is one fresh, empty database for one case.
type Harness struct {
	Outbox        event.Outbox
	Subscriptions event.Subscriptions
	Deliveries    event.Deliveries
	Domain        Domain
}

// Domain makes the rows that events need and that other ports own, and
// reads the audit entries that the stores append.
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
		{"append gives increasing sequences and after reads in order", testAppendAfter},
		{"an append with a stale fence stores nothing", testAppendFenced},
		{"a cursor moves only from where it is", testCursor},
		{"a sink in use cannot be deleted", testSinkInUse},
		{"a subscription names a sink of its scope", testSubscriptionScope},
		{"a second put on an older version is ErrConflict", testSubscriptionConflict},
		{"enqueue skips a delivery that exists", testEnqueueOnce},
		{"due lists pending deliveries, oldest first", testDue},
		{"record on an older version is ErrConflict", testRecordConflict},
		{"redeliver makes a delivery pending again", testRedeliver},
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

// manage returns the audit entry of a change to the notifications of
// the namespace shop.
func manage() audit.Entry {
	return opsmem.Entry(shop, "alice", "namespace:manage-notifications", shop.String(), now())
}

func setup(t *testing.T, h Harness) namespace.Fence {
	t.Helper()

	fence, err := h.Domain.CreateNamespace(t.Context(), shop)
	if err != nil {
		t.Fatalf("CreateNamespace: %v", err)
	}

	return fence
}

func sample(id string) event.Event {
	return event.Event{
		ID: id, Type: event.JobFailed, Scope: shop, Object: "opentofu:shop/network", ObjectID: "u-1",
		Subject: "alice", Agent: "", Severity: event.SeverityWarning, Summary: "The apply of network failed.",
		Links: nil, Attributes: nil, Recipients: nil, DedupeKey: id, At: now(), Sequence: 0,
	}
}

func sink(id string, scope namespace.Name) event.Sink {
	return event.Sink{
		ID: id, Scope: scope, Kind: event.SinkWebhook, Name: "Ops", Target: "https://hooks.example.com/ops",
		SecretRef: "itm-1", CreatedBy: "alice", Version: 0,
	}
}

func subscription(id, sinkID string) event.Subscription {
	return event.Subscription{
		ID: id, Scope: shop, Sink: sinkID, Filter: event.Filter{Types: []string{"job.*"}, Objects: nil, MinSeverity: 0},
		State: event.SubscriptionActive, Reason: "", CreatedBy: "alice", Version: 0,
	}
}

func delivery(id, evt string, next time.Time) event.Delivery {
	return event.Delivery{
		ID: id, Event: evt, Scope: shop, Subscription: "sub-1", Sink: "snk-1", Contact: "",
		State: event.DeliveryPending, Attempts: 0, NextAt: next, LastError: "", DeliveredAt: time.Time{}, Version: 0,
	}
}

func subscribe(t *testing.T, h Harness, fence namespace.Fence) {
	t.Helper()

	if err := h.Subscriptions.PutSink(t.Context(), sink("snk-1", shop), fence, manage()); err != nil {
		t.Fatalf("PutSink: %v", err)
	}

	err := h.Subscriptions.PutSubscription(t.Context(), subscription("sub-1", "snk-1"), fence,
		manage())
	if err != nil {
		t.Fatalf("PutSubscription: %v", err)
	}
}

func enqueue(t *testing.T, h Harness, ds ...event.Delivery) {
	t.Helper()

	if err := h.Deliveries.Enqueue(t.Context(), ds); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
}

func testAppendAfter(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	appendThree(t, h, fence)

	all, err := h.Outbox.After(t.Context(), 0, 10)
	if err != nil || len(all) != 3 || all[0].ID != "evt-0" || all[2].ID != "evt-2" {
		t.Fatalf("After(0) = %d events (%v), want evt-0 to evt-2", len(all), err)
	}

	if all[0].Sequence >= all[1].Sequence || all[1].Sequence >= all[2].Sequence {
		t.Fatalf("sequences %d, %d, %d do not increase", all[0].Sequence, all[1].Sequence, all[2].Sequence)
	}

	rest, err := h.Outbox.After(t.Context(), all[0].Sequence, 1)
	if err != nil || len(rest) != 1 || rest[0].ID != "evt-1" {
		t.Fatalf("After(first, 1) = %v (%v), want evt-1", rest, err)
	}

	bad := sample("evt-bad")
	bad.Summary = ""

	if err := h.Outbox.Append(t.Context(), fence, []event.Event{bad}); !errors.Is(err, event.ErrInvalid) {
		t.Fatalf("Append of an invalid event: err = %v, want ErrInvalid", err)
	}
}

// appendThree appends the events evt-0 to evt-2, one at a time.
func appendThree(t *testing.T, h Harness, fence namespace.Fence) {
	t.Helper()

	for i := range 3 {
		if err := h.Outbox.Append(t.Context(), fence, []event.Event{sample(fmt.Sprintf("evt-%d", i))}); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
}

func testAppendFenced(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	if _, err := h.Domain.BumpFence(t.Context(), fence); err != nil {
		t.Fatalf("BumpFence: %v", err)
	}

	err := h.Outbox.Append(t.Context(), fence, []event.Event{sample("evt-1")})
	if !errors.Is(err, namespace.ErrFenced) {
		t.Fatalf("Append: err = %v, want namespace.ErrFenced", err)
	}

	all, err := h.Outbox.After(t.Context(), 0, 10)
	if err != nil || len(all) != 0 {
		t.Fatalf("After = %d events (%v), want none", len(all), err)
	}
}

func testCursor(t *testing.T, h Harness) {
	t.Helper()

	if c, err := h.Outbox.Cursor(t.Context(), "dispatch"); err != nil || c != 0 {
		t.Fatalf("Cursor = %d (%v), want 0", c, err)
	}

	if err := h.Outbox.MoveCursor(t.Context(), "dispatch", 0, 5); err != nil {
		t.Fatalf("MoveCursor: %v", err)
	}

	if err := h.Outbox.MoveCursor(t.Context(), "dispatch", 0, 7); !errors.Is(err, event.ErrConflict) {
		t.Fatalf("MoveCursor from 0 again: err = %v, want ErrConflict", err)
	}

	if c, err := h.Outbox.Cursor(t.Context(), "dispatch"); err != nil || c != 5 {
		t.Fatalf("Cursor = %d (%v), want 5", c, err)
	}
}

func testSinkInUse(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	subscribe(t, h, fence)

	err := h.Subscriptions.DeleteSink(t.Context(), "snk-1", fence, manage())
	if !errors.Is(err, event.ErrInUse) {
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

	other, err := h.Domain.CreateNamespace(t.Context(), "other")
	if err != nil {
		t.Fatalf("CreateNamespace: %v", err)
	}

	err = h.Subscriptions.PutSink(t.Context(), sink("snk-9", "other"), other, manage())
	if err != nil {
		t.Fatalf("PutSink: %v", err)
	}

	err = h.Subscriptions.PutSubscription(t.Context(), subscription("sub-1", "snk-9"), fence,
		manage())
	if !errors.Is(err, event.ErrNotFound) {
		t.Fatalf("PutSubscription with a sink of another scope: err = %v, want ErrNotFound", err)
	}
}

func testSubscriptionConflict(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	subscribe(t, h, fence)

	s := subscription("sub-1", "snk-1")
	s.Version = 1
	s.Filter.MinSeverity = event.SeverityCritical

	if err := h.Subscriptions.PutSubscription(t.Context(), s, fence, manage()); err != nil {
		t.Fatalf("PutSubscription: %v", err)
	}

	if err := h.Subscriptions.PutSubscription(t.Context(), s, fence, manage()); !errors.Is(err, event.ErrConflict) {
		t.Fatalf("second PutSubscription: err = %v, want ErrConflict", err)
	}

	subs, err := h.Subscriptions.Subscriptions(t.Context(), shop)
	if err != nil || len(subs) != 1 || subs[0].Filter.MinSeverity != event.SeverityCritical {
		t.Fatalf("Subscriptions = %+v (%v), want the changed filter", subs, err)
	}
}

func testEnqueueOnce(t *testing.T, h Harness) {
	t.Helper()

	enqueue(t, h, delivery("dlv-1", "evt-1", now()))
	enqueue(t, h, delivery("dlv-2", "evt-1", now()))

	got, err := h.Deliveries.Due(t.Context(), now(), 10)
	if err != nil || len(got) != 1 || got[0].ID != "dlv-1" {
		t.Fatalf("Due = %+v (%v), want dlv-1 only", got, err)
	}
}

func testDue(t *testing.T, h Harness) {
	t.Helper()

	enqueue(t, h,
		delivery("dlv-1", "evt-1", now().Add(-time.Minute)),
		delivery("dlv-2", "evt-2", now().Add(-time.Hour)),
		delivery("dlv-3", "evt-3", now().Add(time.Hour)))

	got, err := h.Deliveries.Due(t.Context(), now(), 10)
	if err != nil || len(got) != 2 || got[0].ID != "dlv-2" || got[1].ID != "dlv-1" {
		t.Fatalf("Due = %+v (%v), want dlv-2 then dlv-1", got, err)
	}
}

func testRecordConflict(t *testing.T, h Harness) {
	t.Helper()

	enqueue(t, h, delivery("dlv-1", "evt-1", now()))

	got, err := h.Deliveries.Due(t.Context(), now(), 1)
	if err != nil || len(got) != 1 {
		t.Fatalf("Due = %v (%v)", got, err)
	}

	d := got[0]
	d.State, d.Attempts, d.DeliveredAt = event.DeliveryDone, 1, now()

	err = h.Deliveries.Record(t.Context(), d)
	if err != nil {
		t.Fatalf("Record: %v", err)
	}

	err = h.Deliveries.Record(t.Context(), d)
	if !errors.Is(err, event.ErrConflict) {
		t.Fatalf("second Record: err = %v, want ErrConflict", err)
	}

	hist, err := h.Deliveries.History(t.Context(), "sub-1", 10)
	if err != nil || len(hist) != 1 || hist[0].State != event.DeliveryDone {
		t.Fatalf("History = %+v (%v), want one delivered", hist, err)
	}
}

func testRedeliver(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	enqueue(t, h, delivery("dlv-1", "evt-1", now()))

	got, err := h.Deliveries.Due(t.Context(), now(), 1)
	if err != nil || len(got) != 1 {
		t.Fatalf("Due = %v (%v)", got, err)
	}

	d := got[0]
	d.State, d.Attempts, d.LastError = event.DeliveryAbandoned, event.MaxAttempts, "receiver answered 500"

	err = h.Deliveries.Record(t.Context(), d)
	if err != nil {
		t.Fatalf("Record: %v", err)
	}

	err = h.Deliveries.Redeliver(t.Context(), "dlv-1", fence, manage())
	if err != nil {
		t.Fatalf("Redeliver: %v", err)
	}

	got, err = h.Deliveries.Due(t.Context(), now().Add(time.Minute), 10)
	if err != nil || len(got) != 1 || got[0].Attempts != 0 || got[0].State != event.DeliveryPending {
		t.Fatalf("Due after Redeliver = %+v (%v), want one pending with no attempts", got, err)
	}

	if err := h.Deliveries.Redeliver(t.Context(), "dlv-none", fence, manage()); !errors.Is(err, event.ErrNotFound) {
		t.Fatalf("Redeliver unknown: err = %v, want ErrNotFound", err)
	}
}
