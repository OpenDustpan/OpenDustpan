package notifytest

import (
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/notify"
)

// changeTarget gives the sink snk-1 at version 1 a new target, with the
// audit entry of subject.
func changeTarget(t *testing.T, h Harness, fence namespace.Fence, subject string) {
	t.Helper()

	changed := sink("snk-1", shop)
	changed.Version, changed.Target = 1, "hooks.example.net"

	if err := h.Subscriptions.PutSink(t.Context(), changed, nil, fence, manageBy(subject)); err != nil {
		t.Fatalf("PutSink with a new target: %v", err)
	}
}

// deliverOne claims the due delivery with the oldest time at the time at,
// and records it as delivered.
func deliverOne(t *testing.T, h Harness, fence namespace.Fence, at time.Time) {
	t.Helper()

	got := claim(t, h, fence, at, 1)
	if len(got) != 1 {
		t.Fatalf("Claim = %s, want one", ids(got))
	}

	if err := h.Deliveries.Record(t.Context(), fence, attempt(got[0], at)); err != nil {
		t.Fatalf("Record: %v", err)
	}
}

// states returns the state of each delivery of ds, by ID.
func states(ds []notify.Delivery) string {
	slices.SortFunc(ds, func(a, b notify.Delivery) int {
		switch {
		case a.ID < b.ID:
			return -1
		case a.ID > b.ID:
			return 1
		default:
			return 0
		}
	})

	out := make([]string, 0, len(ds))
	for i := range ds {
		out = append(out, ds[i].ID+"="+string(ds[i].State))
	}

	return fmt.Sprint(out)
}

// putSecond creates the sink snk-2 in shop.
func putSecond(t *testing.T, h Harness, fence namespace.Fence) {
	t.Helper()

	s := sink("snk-2", shop)
	if err := h.Subscriptions.PutSink(t.Context(), s, fresh(t.Context(), s), fence, manage()); err != nil {
		t.Fatalf("PutSink snk-2: %v", err)
	}
}

// testSinkChangeSkipsPending checks that a change of the target of a sink
// skips each pending delivery of its subscriptions, a claimed one
// included, and leaves the stopped deliveries and the deliveries of
// another sink as they are.
func testSinkChangeSkipsPending(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	subscribe(t, h, fence)

	putSecond(t, h, fence)

	two := subscription("snk-2")
	two.ID = "sub-2"

	if err := h.Subscriptions.PutSubscription(t.Context(), two, fence, manage()); err != nil {
		t.Fatalf("PutSubscription sub-2: %v", err)
	}

	ofTwo := delivery("dlv-4", "ntf-1", now().Add(3*time.Minute))
	ofTwo.Subscription, ofTwo.Sink = "sub-2", "snk-2"

	enqueue(t, h, fence,
		delivery("dlv-1", "ntf-3", now().Add(2*time.Minute)),
		delivery("dlv-2", "ntf-2", now().Add(time.Minute)),
		delivery("dlv-3", "ntf-1", now()),
		ofTwo,
	)

	deliverOne(t, h, fence, now())

	held, err := h.Deliveries.Claim(t.Context(), fence, claimAt(now().Add(time.Minute), 1))
	if err != nil || len(held) != 1 {
		t.Fatalf("Claim = %s (%v), want one", ids(held), err)
	}

	changeTarget(t, h, fence, "alice")

	if got := states(history(t, h, shop)); got != "[dlv-1=skipped dlv-2=skipped dlv-3=delivered]" {
		t.Fatalf("deliveries of sub-1 after the sink change = %s, want the pending ones skipped", got)
	}

	ds := history(t, h, shop)
	for i := range ds {
		if ds[i].State == notify.DeliverySkipped && !ds[i].DeliveredAt.Equal(now()) {
			t.Fatalf("skipped %s stopped at %s, want %s", ds[i].ID, ds[i].DeliveredAt, now())
		}
	}

	err = h.Deliveries.Record(t.Context(), fence, attempt(held[0], now().Add(time.Minute)))
	if !errors.Is(err, notify.ErrConflict) {
		t.Fatalf("Record of a claim from before the sink change: err = %v, want ErrConflict", err)
	}

	if got := claim(t, h, fence, now().Add(time.Hour), 10); ids(got) != "[dlv-4]" {
		t.Fatalf("Claim after the sink change = %s, want only dlv-4 of the other sink", ids(got))
	}
}

// testRedeliverAfterSinkChange checks that Redeliver refuses a delivery
// while its subscription is suspended, has another creator, or sends to
// another sink, and accepts it again when the creator of the delivery
// approves the subscription for the sink of the delivery.
func testRedeliverAfterSinkChange(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	subscribe(t, h, fence)
	enqueue(t, h, fence, delivery("dlv-1", "ntf-1", now()))
	deliverOne(t, h, fence, now())
	changeTarget(t, h, fence, "alice")

	redeliver := func(when string, want error) {
		t.Helper()

		err := h.Deliveries.Redeliver(
			t.Context(), "dlv-1", now().Add(notify.RedeliverAfter), fence, manage(),
		)
		if !errors.Is(err, want) || (want == nil && err != nil) {
			t.Fatalf("Redeliver %s: err = %v, want %v", when, err, want)
		}
	}

	redeliver("while the subscription is suspended", notify.ErrConflict)

	put := func(subject, sinkID string) {
		t.Helper()

		s := subscriptionOf(t, h, "sub-1")
		s.State, s.Reason, s.Sink = notify.SubscriptionStateActive, "", sinkID

		if err := h.Subscriptions.PutSubscription(t.Context(), s, fence, manageBy(subject)); err != nil {
			t.Fatalf("PutSubscription of %s: %v", subject, err)
		}
	}

	put("bob", "snk-1")
	redeliver("after a resume of another person", notify.ErrConflict)

	putSecond(t, h, fence)

	put("alice", "snk-2")
	redeliver("when the subscription sends to another sink", notify.ErrConflict)

	if got := states(history(t, h, shop)); got != "[dlv-1=delivered]" {
		t.Fatalf("dlv-1 after the refused redeliveries = %s, want delivered", got)
	}

	put("alice", "snk-1")
	redeliver("when the creator of the delivery approved the sink", nil)
}

// testSinkChangeNotices checks that a change of the target of a sink
// appends one notice subscription.suspended for each active subscription
// of the sink, to the administrators and to its earlier creator, and that
// a new name appends none.
func testSinkChangeNotices(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	subscribe(t, h, fence)

	three := subscription("snk-1")
	three.ID = "sub-3"

	paused := subscription("snk-1")
	paused.ID, paused.State, paused.Reason = "sub-4", notify.SubscriptionStateSuspended, "paused"

	puts := []struct {
		s       notify.Subscription
		subject string
	}{{three, "carol"}, {paused, "alice"}}

	for i := range puts {
		err := h.Subscriptions.PutSubscription(t.Context(), puts[i].s, fence, manageBy(puts[i].subject))
		if err != nil {
			t.Fatalf("PutSubscription %s: %v", puts[i].s.ID, err)
		}
	}

	renamed := sink("snk-1", shop)
	renamed.Version, renamed.Name = 1, "Ops team"

	if err := h.Subscriptions.PutSink(t.Context(), renamed, nil, fence, manage()); err != nil {
		t.Fatalf("PutSink with a new name: %v", err)
	}

	if got, err := h.Outbox.After(t.Context(), shop, 0, 10); err != nil || len(got) != 0 {
		t.Fatalf("notifications after a new name = %d (%v), want none", len(got), err)
	}

	changed := renamed
	changed.Version, changed.Target = 2, "hooks.example.net"

	if err := h.Subscriptions.PutSink(t.Context(), changed, nil, fence, manageBy("dave")); err != nil {
		t.Fatalf("PutSink with a new target: %v", err)
	}

	got, err := h.Outbox.After(t.Context(), shop, 0, 10)
	if err != nil || len(got) != 2 {
		t.Fatalf("notifications after a new target = %d (%v), want one for sub-1 and one for sub-3", len(got), err)
	}

	creators := map[string]string{"sub-1": "alice", "sub-3": "carol"}

	for i := range got {
		checkNotice(t, got[i], creators)
	}

	if got[0].ObjectID == got[1].ObjectID {
		t.Fatalf("notices = %s and %s, want one for each subscription", got[0].ObjectID, got[1].ObjectID)
	}
}

// checkNotice checks that n is the notice of the sink change of dave to
// the sink version 3 for a subscription of creators, to the
// administrators and to its creator.
func checkNotice(t *testing.T, n notify.Notification, creators map[string]string) {
	t.Helper()

	creator, ok := creators[n.ObjectID]
	if !ok || n.Type != notify.SubscriptionSuspended || n.Scope != shop || n.Subject != "dave" ||
		n.Attributes["sink"] != "snk-1" || n.Attributes["subscription"] != n.ObjectID {
		t.Fatalf("notice = %+v, want subscription.suspended of sub-1 or sub-3 by dave", n)
	}

	if n.Object != notify.NotificationsObject(shop) || n.DedupeKey != n.ObjectID+"/sink-changed/3" {
		t.Fatalf("notice of %s has object %s and dedupe key %s, want the notifications of shop and %s/sink-changed/3",
			n.ObjectID, n.Object, n.DedupeKey, n.ObjectID)
	}

	want := []notify.Recipient{
		{Principal: "", Group: "", Admins: true},
		{Principal: creator, Group: "", Admins: false},
	}
	if !slices.Equal(n.Recipients, want) {
		t.Fatalf("recipients of the notice of %s = %+v, want the administrators and %s", n.ObjectID,
			n.Recipients, creator)
	}
}

// testSinkSecretRefs checks that a ref of a sink names one value for
// good: the store refuses a new value under a ref that the scope holds or
// held, and a ref with no value of the sink.
func testSinkSecretRefs(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	putSink(t, h, fence)
	checkSecrets(t, h, "[key of itm-snk-1-url key of itm-snk-1]")

	s := sink("snk-1", shop)
	s.Version = 1

	refused := []struct {
		name    string
		s       notify.Sink
		secrets []notify.SealedSecret
	}{
		{"a new value under the stored ref", s, []notify.SealedSecret{sealed(t.Context(), s, "itm-snk-1", "new")}},
		{
			"a new value for a ref that the sink does not name",
			s,
			[]notify.SealedSecret{secret(t.Context(), s, "itm-x")},
		},
		{"a new sink with no value", sink("snk-2", shop), nil},
		{
			"a new sink that names the ref of another sink",
			withRefs(sink("snk-2", shop), "itm-snk-1-url", "itm-snk-1"),
			nil,
		},
	}

	for i := range refused {
		tc := &refused[i]

		err := h.Subscriptions.PutSink(t.Context(), tc.s, tc.secrets, fence, manage())
		if !errors.Is(err, notify.ErrInvalid) {
			t.Fatalf("PutSink with %s: err = %v, want ErrInvalid", tc.name, err)
		}
	}

	checkSecrets(t, h, "[key of itm-snk-1-url key of itm-snk-1]")

	// A new URL item keeps the signing key: only the first ref is new.
	newURL := withRefs(s, "itm-snk-1-url2", "itm-snk-1")
	err := h.Subscriptions.PutSink(
		t.Context(),
		newURL,
		[]notify.SealedSecret{secret(t.Context(), s, "itm-snk-1-url2")},
		fence,
		manage(),
	)
	if err != nil {
		t.Fatalf("PutSink with a new URL item: %v", err)
	}

	checkSecrets(t, h, "[key of itm-snk-1-url2 key of itm-snk-1]")

	rotated := withRefs(s, "itm-snk-1-b-url", "itm-snk-1-b")
	rotated.Version = 2

	err = h.Subscriptions.PutSink(
		t.Context(),
		rotated,
		[]notify.SealedSecret{secret(t.Context(), s, "itm-snk-1-b-url"), secret(t.Context(), s, "itm-snk-1-b")},
		fence,
		manage(),
	)
	if err != nil {
		t.Fatalf("PutSink with new refs: %v", err)
	}

	checkSecrets(t, h, "[key of itm-snk-1-b-url key of itm-snk-1-b]")

	back := withRefs(s, "itm-snk-1-url", "itm-snk-1")
	back.Version = 3

	for _, secrets := range [][]notify.SealedSecret{{secret(t.Context(), s, "itm-snk-1")}, nil} {
		err = h.Subscriptions.PutSink(t.Context(), back, secrets, fence, manage())
		if !errors.Is(err, notify.ErrInvalid) {
			t.Fatalf("PutSink back to a dropped ref with %d new values: err = %v, want ErrInvalid", len(secrets), err)
		}
	}

	if err = h.Subscriptions.DeleteSink(t.Context(), "snk-1", fence, manage()); err != nil {
		t.Fatalf("DeleteSink: %v", err)
	}

	again := withRefs(sink("snk-1", shop), "itm-snk-1-b-url", "itm-snk-1-b")
	err = h.Subscriptions.PutSink(t.Context(), again, fresh(t.Context(), again), fence, manage())
	if !errors.Is(err, notify.ErrInvalid) {
		t.Fatalf("PutSink of a new sink with a ref of a deleted sink: err = %v, want ErrInvalid", err)
	}

	for _, scope := range []namespace.Name{shop, other} {
		if _, err := h.Subscriptions.SinkSecrets(t.Context(), scope, "snk-1"); !errors.Is(err, notify.ErrNotFound) {
			t.Fatalf("SinkSecrets of a deleted sink in %s: err = %v, want ErrNotFound", scope, err)
		}
	}
}

// withRefs returns s with the secret refs refs.
func withRefs(s notify.Sink, refs ...string) notify.Sink {
	s.SecretRefs = refs

	return s
}

// checkSecrets checks the values of the secret items of the sink snk-1
// in shop, opened with the sealer of the suite, and that another scope
// reads none.
func checkSecrets(t *testing.T, h Harness, want string) {
	t.Helper()

	got, err := h.Subscriptions.SinkSecrets(t.Context(), shop, "snk-1")
	if err != nil {
		t.Fatalf("SinkSecrets: %v", err)
	}

	s := withRefs(sink("snk-1", shop))
	for _, g := range got {
		s.SecretRefs = append(s.SecretRefs, g.Ref())
	}

	values, err := suiteSealer().Open(t.Context(), s, got)
	if err != nil {
		t.Fatalf("Open the values of SinkSecrets: %v", err)
	}

	if fmt.Sprintf("%s", values) != want {
		t.Fatalf("SinkSecrets = %s, want %s", values, want)
	}

	if _, err := h.Subscriptions.SinkSecrets(t.Context(), other, "snk-1"); !errors.Is(err, notify.ErrNotFound) {
		t.Fatalf("SinkSecrets of another scope: err = %v, want ErrNotFound", err)
	}
}
