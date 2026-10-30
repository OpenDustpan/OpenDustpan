package notifytest

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/notify"
)

// testEnqueueReferences enqueues deliveries of the scope shop that name a sink,
// a subscription or a notification of another scope. Each call fails with
// ErrInvalid and stores nothing, so a delivery never points at a row that
// another scope owns.
func testEnqueueReferences(t *testing.T, h Harness) {
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

	serverSink := sink("snk-s", audit.ServerScope)
	if putErr := h.Subscriptions.PutSink(t.Context(), serverSink, fresh(t.Context(), serverSink), zeroFence(),
		entry(audit.ServerScope)); putErr != nil {
		t.Fatalf("PutSink in the server scope: %v", putErr)
	}

	otherSub := subscriptionIn(other, "snk-o")
	otherSub.ID = "sub-o"

	if putErr := h.Subscriptions.PutSubscription(t.Context(), otherSub, otherFence, entry(other)); putErr != nil {
		t.Fatalf("PutSubscription in other: %v", putErr)
	}

	if appendErr := h.Outbox.Append(t.Context(), otherFence,
		[]notify.Notification{sample("ntf-o", other)}); appendErr != nil {
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
			notify.ErrInvalid) {
			t.Fatalf("Enqueue naming %s: err = %v, want ErrInvalid", tc.name, enqErr)
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
	if putErr := h.Subscriptions.PutSink(t.Context(), otherSink, fresh(t.Context(), otherSink), otherFence,
		entry(other)); putErr != nil {
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
