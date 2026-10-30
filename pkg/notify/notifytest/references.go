package notifytest

import (
	"errors"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/notify"
)

// testEnqueueReferences enqueues deliveries of the scope shop that name a sink,
// a subscription or a notification of another scope. Each call fails with
// ErrNotFound and stores nothing, so a delivery never points at a row that
// another scope owns.
func testEnqueueReferences(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)

	otherFence, err := h.Domain.CreateNamespace(t.Context(), other)
	if err != nil {
		t.Fatalf("CreateNamespace: %v", err)
	}

	otherSink := sink("snk-o", other)
	putErr := h.Subscriptions.PutSink(t.Context(), otherSink, fresh(t.Context(), otherSink), otherFence,
		entry(other))
	if putErr != nil {
		t.Fatalf("PutSink in other: %v", putErr)
	}

	serverSink := sink("snk-s", audit.ServerScope)
	putErr = h.Subscriptions.PutSink(t.Context(), serverSink, fresh(t.Context(), serverSink), zeroFence(),
		entry(audit.ServerScope))
	if putErr != nil {
		t.Fatalf("PutSink in the server scope: %v", putErr)
	}

	otherSub := subscriptionIn(other, "snk-o")
	otherSub.ID = "sub-o"

	if putErr := h.Subscriptions.PutSubscription(t.Context(), otherSub, otherFence, entry(other)); putErr != nil {
		t.Fatalf("PutSubscription in other: %v", putErr)
	}

	appendErr := h.Outbox.Append(t.Context(), otherFence,
		[]notify.Notification{sample("ntf-o", other)})
	if appendErr != nil {
		t.Fatalf("Append in other: %v", appendErr)
	}

	tests := []struct {
		name   string
		change func(d *notify.Delivery)
	}{
		{"a sink of another namespace", func(d *notify.Delivery) { d.Sink = "snk-o" }},
		{"a sink of the server scope", func(d *notify.Delivery) { d.Sink = "snk-s" }},
		{"a subscription of another namespace", func(d *notify.Delivery) { d.Subscription = "sub-o" }},
		{"a notification of another namespace", func(d *notify.Delivery) { d.Notification = "ntf-o" }},
	}

	for _, tc := range tests {
		d := delivery("dlv-x", "ntf-1", now())
		tc.change(&d)

		if enqErr := h.Deliveries.Enqueue(t.Context(), fence, []notify.Delivery{d}); !errors.Is(enqErr,
			notify.ErrNotFound) {
			t.Fatalf("Enqueue naming %s: err = %v, want ErrNotFound", tc.name, enqErr)
		}
	}

	if scopes, dueErr := h.Deliveries.DueScopes(t.Context(), now()); dueErr != nil || len(scopes) != 0 {
		t.Fatalf("DueScopes = %v (%v), want none: a refused call stores nothing", scopes, dueErr)
	}
}

// testSubscriptionNewVersion puts a new subscription with a version above 0.
// It is ErrConflict, because a new subscription has no stored version.
func testSubscriptionNewVersion(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	putSink(t, h, fence)

	s := subscription("snk-1")
	s.Version = 3

	if err := h.Subscriptions.PutSubscription(t.Context(), s, fence, manage()); !errors.Is(err, notify.ErrConflict) {
		t.Fatalf("PutSubscription of a new subscription at version 3: err = %v, want ErrConflict", err)
	}

	if subs, err := h.Subscriptions.Subscriptions(t.Context(), shop); err != nil || len(subs) != 0 {
		t.Fatalf("Subscriptions = %v (%v), want none", subs, err)
	}
}

// testSubscriptionOtherScope puts a subscription whose ID a stored subscription
// of another scope has. It is ErrNotFound, so a caller learns nothing of the
// subscriptions of another scope, and the stored one stays as it was.
func testSubscriptionOtherScope(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	subscribe(t, h, fence)

	otherFence, err := h.Domain.CreateNamespace(t.Context(), other)
	if err != nil {
		t.Fatalf("CreateNamespace: %v", err)
	}

	otherSink := sink("snk-o", other)
	putErr := h.Subscriptions.PutSink(t.Context(), otherSink, fresh(t.Context(), otherSink), otherFence,
		entry(other))
	if putErr != nil {
		t.Fatalf("PutSink in other: %v", putErr)
	}

	s := subscriptionIn(other, "snk-o")
	s.Version = 1

	if putErr := h.Subscriptions.PutSubscription(t.Context(), s, otherFence, entry(other)); !errors.Is(putErr,
		notify.ErrNotFound) {
		t.Fatalf("PutSubscription over a subscription of another scope: err = %v, want ErrNotFound", putErr)
	}

	if got := subscriptionOf(t, h, "sub-1"); got.Version != 1 || got.Sink != "snk-1" {
		t.Fatalf("stored subscription = %+v, want it unchanged", got)
	}
}

// testNotFoundIsUniform names a stored row of another scope by its ID, with
// the fence of the scope shop, and names an unknown row the same way. Both
// answer ErrNotFound with the same text, apart from the ID, so a caller cannot
// tell a row of another scope from a row that does not exist.
func testNotFoundIsUniform(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)

	otherFence, err := h.Domain.CreateNamespace(t.Context(), other)
	if err != nil {
		t.Fatalf("CreateNamespace: %v", err)
	}

	otherSink := sink("snk-o", other)
	if putErr := h.Subscriptions.PutSink(t.Context(), otherSink, fresh(t.Context(), otherSink), otherFence,
		entry(other)); putErr != nil {
		t.Fatalf("PutSink in other: %v", putErr)
	}

	otherSub := subscriptionIn(other, "snk-o")
	otherSub.ID = "sub-o"

	if putErr := h.Subscriptions.PutSubscription(t.Context(), otherSub, otherFence, entry(other)); putErr != nil {
		t.Fatalf("PutSubscription in other: %v", putErr)
	}

	if enqErr := h.Deliveries.Enqueue(t.Context(), otherFence,
		[]notify.Delivery{deliveryIn(other, "dlv-o", "ntf-o", now())}); enqErr != nil {
		t.Fatalf("Enqueue in other: %v", enqErr)
	}

	attemptOf := func(id string) notify.Attempt {
		return notify.Attempt{
			ID: id, Owner: owner, Version: 1, Now: now(), State: notify.DeliveryDone, Attempts: 1,
			NextAt: time.Time{}, LastError: "", DeliveredAt: now(),
		}
	}

	ops := []struct {
		name  string
		other string
		run   func(id string) error
	}{
		{"DeleteSink", "snk-o", func(id string) error {
			return h.Subscriptions.DeleteSink(t.Context(), id, fence, manage())
		}},
		{"DeleteSubscription", "sub-o", func(id string) error {
			return h.Subscriptions.DeleteSubscription(t.Context(), id, fence, manage())
		}},
		{"Redeliver", "dlv-o", func(id string) error {
			return h.Deliveries.Redeliver(t.Context(), id, fence, manage())
		}},
		{"Record", "dlv-o", func(id string) error {
			return h.Deliveries.Record(t.Context(), fence, attemptOf(id))
		}},
	}

	for _, op := range ops {
		unknown := op.other[:len(op.other)-1] + "u"

		gotOther, gotUnknown := op.run(op.other), op.run(unknown)

		if !errors.Is(gotOther, notify.ErrNotFound) || !errors.Is(gotUnknown, notify.ErrNotFound) {
			t.Fatalf("%s: errors = %v and %v, want ErrNotFound for both", op.name, gotOther, gotUnknown)
		}

		a := strings.ReplaceAll(gotOther.Error(), op.other, "<id>")
		b := strings.ReplaceAll(gotUnknown.Error(), unknown, "<id>")

		if a != b {
			t.Fatalf("%s: a row of another scope answers %q, an unknown row %q, want the same text", op.name, a, b)
		}
	}
}
