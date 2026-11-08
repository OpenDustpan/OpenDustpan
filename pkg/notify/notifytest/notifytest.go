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
// suite, so each case runs before the first SQL adapter. RunContacts is the
// suite of notify.Contacts, which the identity package runs; a reference
// adapter in memory runs it here too. RunSender is the suite of notify.Sender,
// which each sender adapter runs against a test receiver.
package notifytest

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/fakecred"
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
		{"append refuses a repeated notification ID", testAppendRepeatedID},
		{"enqueue refuses a repeated or invalid delivery", testEnqueueID},
		{"history shows only the deliveries of its scope", testHistoryScope},
		{"history pages by the keyset cursor", testHistoryCursor},
		{"history counts the deliveries at or before a cursor", testHistoryCountBefore},
		{"record needs a live claim and changes only the result", testRecordClaim},
		{"redeliver of a pending delivery is ErrConflict", testRedeliverPending},
		{"put subscription refuses an invalid subscription", testSubscriptionValidate},
		{"an audit entry of another scope changes nothing", testEntryScope},
		{"each put of a subscription makes its subject the creator", testSubscriptionCreator},
		{"a change of the target or the secrets of a sink suspends its subscriptions", testSinkChangeSuspends},
		{"a put of a subscription read before a sink change does not undo the suspend", testSinkChangeRace},
		{"a sink change skips the pending deliveries of its subscriptions", testSinkChangeSkipsPending},
		{"redeliver refuses a delivery that its subscription no longer sends", testRedeliverAfterSinkChange},
		{"a sink change appends one notice for each subscription it suspends", testSinkChangeNotices},
		{"a ref of a sink names one value for good", testSinkSecretRefs},
		{"the store keeps only values sealed for the sink", testSinkSecretsSealed},
		{"concurrent appends make one gap-free sequence", testConcurrentAppend},
		{"concurrent moves of one cursor have one winner", testConcurrentCursor},
		{"concurrent claims never take one delivery twice", testConcurrentClaim},
		{"enqueue refuses a delivery that names a row of another scope", testEnqueueReferences},
		{"a row of another scope answers like a row that does not exist", testNotFoundIsUniform},
		{"a new subscription with a version above 0 is ErrConflict", testSubscriptionNewVersion},
		{"a put over a subscription of another scope is ErrNotFound", testSubscriptionOtherScope},
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
		At:            now(),
		Namespace:     scope,
		Subject:       "alice",
		IDPSubject:    "",
		TokenID:       "",
		Action:        "namespace:manage-notifications",
		Object:        scope.String(),
		Version:       "",
		ValuePath:     "",
		ValueCount:    nil,
		Changes:       nil,
		SourceIP:      "192.0.2.1",
		UserAgent:     "suite",
		Channel:       audit.ChannelAPI,
		Kind:          audit.KindChange,
		Outcome:       audit.OutcomeSuccess,
		RequestID:     "req-suite",
		ObjectID:      "",
		PrevHash:      "",
		Hash:          "",
		Sequence:      0,
		FenceVersion:  0,
		Reason:        "",
		PrincipalKind: "",
		JobID:         "",
	}
}

// manageBy returns the audit entry of a change of the principal subject to
// the notifications of the namespace shop.
func manageBy(subject string) audit.Entry {
	e := manage()
	e.Subject = subject

	return e
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
		ID:         id,
		Scope:      scope,
		Kind:       notify.SinkWebhook,
		Name:       "Ops",
		Platform:   "",
		Target:     "hooks.example.com",
		SecretRefs: []string{"itm-" + id + "-url", "itm-" + id},
		CreatedBy:  "alice",
		Version:    0,
	}
}

func subscription(sinkID string) notify.Subscription { return subscriptionIn(shop, sinkID) }

func subscriptionIn(scope namespace.Name, sinkID string) notify.Subscription {
	return notify.Subscription{
		ID: "sub-1", Scope: scope, Sink: sinkID,
		Filter: notify.Filter{Types: []string{"job.*"}, Objects: nil, MinSeverity: 0},
		State:  notify.SubscriptionStateActive, Reason: "", CreatedBy: "alice", Version: 0,
	}
}

func delivery(id, ntf string, next time.Time) notify.Delivery { return deliveryIn(shop, id, ntf, next) }

func deliveryIn(scope namespace.Name, id, ntf string, next time.Time) notify.Delivery {
	return notify.Delivery{
		ID:           id,
		Notification: ntf,
		Scope:        scope,
		Subscription: "sub-1",
		Sink:         "snk-1",
		Contact:      "",
		Principal:    "alice",
		State:        notify.DeliveryPending,
		Attempts:     0,
		NextAt:       next,
		LastError:    "",
		DeliveredAt:  time.Time{},
		Owner:        "",
		LeaseUntil:   time.Time{},
		Version:      0,
	}
}

func putSink(t *testing.T, h Harness, fence namespace.Fence) {
	t.Helper()

	s := sink("snk-1", shop)
	if err := h.Subscriptions.PutSink(t.Context(), s, fresh(t.Context(), s), fence, manage()); err != nil {
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

func move(from, to int64) notify.CursorMove { return moveIn(shop, from, to) }

func moveIn(scope namespace.Name, from, to int64) notify.CursorMove {
	return notify.CursorMove{Name: "dispatch", Scope: scope, From: from, To: to}
}

func claimAt(at time.Time, limit int) notify.ClaimRequest { return claimIn(shop, at, limit) }

func claimIn(scope namespace.Name, at time.Time, limit int) notify.ClaimRequest {
	return notify.ClaimRequest{Scope: scope, Owner: owner, Now: at, Lease: lease, Limit: limit}
}

// attempt returns the result of an attempt of the claimed delivery d at
// the time at: delivered at its first attempt.
func attempt(d notify.Delivery, at time.Time) notify.Attempt {
	return notify.Attempt{
		ID: d.ID, Owner: d.Owner, Version: d.Version, Now: at, State: notify.DeliveryDone, Attempts: 1,
		NextAt: time.Time{}, LastError: "", DeliveredAt: at,
	}
}

func history(t *testing.T, h Harness, scope namespace.Name) []notify.Delivery {
	t.Helper()

	got, err := h.Deliveries.History(t.Context(), scope, "sub-1", nil, 10)
	if err != nil {
		t.Fatalf("History: %v", err)
	}

	return got
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

	err = h.Subscriptions.PutSink(
		t.Context(),
		sink("snk-9", other),
		fresh(t.Context(), sink("snk-9", other)),
		otherFence,
		entry(other),
	)
	if err != nil {
		t.Fatalf("PutSink: %v", err)
	}

	err = h.Subscriptions.PutSubscription(t.Context(), subscription("snk-9"), fence, manage())
	if !errors.Is(err, notify.ErrNotFound) {
		t.Fatalf("PutSubscription with a sink of another scope: err = %v, want ErrNotFound", err)
	}

	moved := sink("snk-9", shop)
	moved.Version = 1

	if err := h.Subscriptions.PutSink(t.Context(), moved, nil, fence, manage()); !errors.Is(err, notify.ErrNotFound) {
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

	refused := h.Subscriptions.PutSink(t.Context(), s, fresh(t.Context(), s), fence, serverEntry)
	if !errors.Is(refused, notify.ErrInvalid) {
		t.Fatalf("PutSink in the server scope with a namespace fence: err = %v, want ErrInvalid", refused)
	}

	put := fresh(t.Context(), s)
	if err := h.Subscriptions.PutSink(t.Context(), s, put, zeroFence(), entry(audit.ServerScope)); err != nil {
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

	at := now().Add(lease)
	if err := h.Deliveries.Record(t.Context(), fence, attempt(lost[0], at)); !errors.Is(err, notify.ErrConflict) {
		t.Fatalf("Record by the lost claim: err = %v, want ErrConflict", err)
	}

	if err := h.Deliveries.Record(t.Context(), fence, attempt(live[0], at)); err != nil {
		t.Fatalf("Record: %v", err)
	}

	if err := h.Deliveries.Record(t.Context(), fence, attempt(live[0], at)); !errors.Is(err, notify.ErrConflict) {
		t.Fatalf("second Record: err = %v, want ErrConflict", err)
	}

	hist := history(t, h, shop)
	if len(hist) != 1 || hist[0].State != notify.DeliveryDone || hist[0].Owner != "" {
		t.Fatalf("History = %+v, want one delivered with no claim", hist)
	}
}

// abandon claims the delivery dlv-1 and records it as abandoned.
func abandon(t *testing.T, h Harness, fence namespace.Fence, scope namespace.Name) {
	t.Helper()

	got, err := h.Deliveries.Claim(t.Context(), fence, claimIn(scope, now(), 1))
	if err != nil || len(got) != 1 {
		t.Fatalf("Claim = %s (%v), want one", ids(got), err)
	}

	a := attempt(got[0], now())
	a.State, a.Attempts, a.LastError, a.DeliveredAt = notify.DeliveryAbandoned, notify.MaxAttempts,
		notify.ClassTemporary, time.Time{}

	if err := h.Deliveries.Record(t.Context(), fence, a); err != nil {
		t.Fatalf("Record: %v", err)
	}
}

func testRedeliver(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	subscribe(t, h, fence)
	enqueue(t, h, fence, delivery("dlv-1", "ntf-1", now()))
	abandon(t, h, fence, shop)

	if err := h.Deliveries.Redeliver(t.Context(), "dlv-1", fence, manage()); err != nil {
		t.Fatalf("Redeliver: %v", err)
	}

	got := claim(t, h, fence, now().Add(time.Minute), 10)
	if len(got) != 1 || got[0].Attempts != 0 || got[0].State != notify.DeliveryPending {
		t.Fatalf("Claim after Redeliver = %+v, want one pending with no attempts", got)
	}

	if err := h.Deliveries.Redeliver(t.Context(), "dlv-none", fence, manage()); !errors.Is(err, notify.ErrNotFound) {
		t.Fatalf("Redeliver unknown: err = %v, want ErrNotFound", err)
	}
}

func testAppendRepeatedID(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)

	otherFence, err := h.Domain.CreateNamespace(t.Context(), other)
	if err != nil {
		t.Fatalf("CreateNamespace: %v", err)
	}

	appendOne(t, h, fence, sample("ntf-1", shop))

	again := sample("ntf-1", shop)
	again.DedupeKey = "another-fact"

	elsewhere := sample("ntf-1", other)
	elsewhere.DedupeKey = "another-fact"

	twice := sample("ntf-2", shop)
	twiceAgain := sample("ntf-2", shop)
	twiceAgain.DedupeKey = "another-fact"

	cases := []struct {
		name  string
		fence namespace.Fence
		ns    []notify.Notification
	}{
		{"a stored ID with another dedupe key", fence, []notify.Notification{again}},
		{"a stored ID in another scope", otherFence, []notify.Notification{elsewhere}},
		{"one ID twice in a call", fence, []notify.Notification{twice, twiceAgain}},
	}

	for _, tc := range cases {
		if err := h.Outbox.Append(t.Context(), tc.fence, tc.ns); !errors.Is(err, notify.ErrInvalid) {
			t.Fatalf("Append of %s: err = %v, want ErrInvalid", tc.name, err)
		}
	}

	for _, scope := range []namespace.Name{shop, other} {
		all, err := h.Outbox.After(t.Context(), scope, 0, 10)
		if err != nil || (scope == shop) != (len(all) == 1) || (scope == other) != (len(all) == 0) {
			t.Fatalf("After(%s) = %+v (%v), want ntf-1 in shop only", scope, all, err)
		}
	}
}

func testEnqueueID(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)

	otherFence, err := h.Domain.CreateNamespace(t.Context(), other)
	if err != nil {
		t.Fatalf("CreateNamespace: %v", err)
	}

	enqueue(t, h, fence, delivery("dlv-1", "ntf-1", now()))

	theirs := deliveryIn(other, "dlv-1", "ntf-9", now())
	err = h.Deliveries.Enqueue(
		t.Context(),
		otherFence,
		[]notify.Delivery{theirs},
	)
	if !errors.Is(err, notify.ErrNotFound) {
		t.Fatalf("Enqueue of an ID of another scope: err = %v, want ErrNotFound", err)
	}

	bad := invalidDeliveries()
	for i := range bad {
		err := h.Deliveries.Enqueue(t.Context(), fence, []notify.Delivery{bad[i].d, delivery("dlv-8", "ntf-8", now())})
		if !errors.Is(err, notify.ErrInvalid) {
			t.Fatalf("Enqueue of %s: err = %v, want ErrInvalid", bad[i].name, err)
		}
	}

	hist := history(t, h, shop)
	if len(hist) != 1 || hist[0].Notification != "ntf-1" || hist[0].Scope != shop {
		t.Fatalf("History = %+v, want dlv-1 of ntf-1 in shop only", hist)
	}

	if got, err := h.Deliveries.History(t.Context(), other, "sub-1", nil, 10); err != nil || len(got) != 0 {
		t.Fatalf("History(other) = %+v (%v), want none", got, err)
	}
}

func invalidDeliveries() []struct {
	name string
	d    notify.Delivery
} {
	change := func(f func(d *notify.Delivery)) notify.Delivery {
		d := delivery("dlv-2", "ntf-2", now())
		f(&d)

		return d
	}

	return []struct {
		name string
		d    notify.Delivery
	}{
		{"a stored ID", change(func(d *notify.Delivery) { d.ID = "dlv-1" })},
		{"an ID with no prefix", change(func(d *notify.Delivery) { d.ID = "x-2" })},
		{"no principal", change(func(d *notify.Delivery) { d.Principal = "" })},
		{"no subscription and no contact", change(func(d *notify.Delivery) { d.Subscription = "" })},
		{"an unknown state", change(func(d *notify.Delivery) { d.State = "bogus" })},
		{"a delivered state", change(func(d *notify.Delivery) { d.State = notify.DeliveryDone })},
		{"the text of an error", change(func(d *notify.Delivery) { d.LastError = "dial tcp 10.0.0.1" })},
	}
}

func testHistoryScope(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)

	if _, err := h.Domain.CreateNamespace(t.Context(), other); err != nil {
		t.Fatalf("CreateNamespace: %v", err)
	}

	subscribe(t, h, fence)
	enqueue(t, h, fence, delivery("dlv-1", "ntf-1", now()))

	if got, err := h.Deliveries.History(t.Context(), other, "sub-1", nil, 10); !errors.Is(err, notify.ErrNotFound) {
		t.Fatalf("History of a subscription of another scope = %+v (%v), want ErrNotFound", got, err)
	}

	if got := history(t, h, shop); ids(got) != "[dlv-1]" {
		t.Fatalf("History(shop) = %s, want [dlv-1]", ids(got))
	}
}

// testHistoryCursor pages the history with the keyset cursor. Many rows share
// one NextAt, so a page edge between them is the case that a cursor of the
// next time alone would skip or repeat.
func testHistoryCursor(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	subscribe(t, h, fence)

	next := now()
	rows := make([]notify.Delivery, 0, 6)
	for i := 1; i <= 5; i++ {
		rows = append(rows, delivery(fmt.Sprintf("dlv-%d", i), fmt.Sprintf("ntf-%d", i), next))
	}

	rows = append(rows, delivery("dlv-0", "ntf-0", next.Add(-time.Minute)))
	enqueue(t, h, fence, rows...)

	// The newest next time comes first, and the greatest ID first among the
	// rows that share it.
	var (
		seen  []string
		sizes []int
		after *notify.DeliveryCursor
	)

	for pages := 0; ; pages++ {
		if pages > 4 {
			t.Fatalf("the history by cursor does not end: %s", seen)
		}

		got, err := h.Deliveries.History(t.Context(), shop, "sub-1", after, 2)
		if err != nil {
			t.Fatalf("History: %v", err)
		}

		if len(got) == 0 {
			break
		}

		sizes = append(sizes, len(got))
		for i := range got {
			seen = append(seen, got[i].ID)
		}

		last := got[len(got)-1]
		after = &notify.DeliveryCursor{NextAt: last.NextAt, ID: last.ID}
	}

	if want := "[2 2 2]"; fmt.Sprint(sizes) != want {
		t.Fatalf("page sizes = %s, want %s: a page edge skips or repeats rows of one next time",
			fmt.Sprint(sizes), want)
	}

	if want := "[dlv-5 dlv-4 dlv-3 dlv-2 dlv-1 dlv-0]"; fmt.Sprint(seen) != want {
		t.Fatalf("history by cursor = %s, want %s", fmt.Sprint(seen), want)
	}
}

// testHistoryCountBefore counts the deliveries at or before a cursor, capped
// at the limit. The count bounds a read of the history.
func testHistoryCountBefore(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	subscribe(t, h, fence)

	next := now()
	rows := make([]notify.Delivery, 0, 6)
	for i := 1; i <= 5; i++ {
		rows = append(rows, delivery(fmt.Sprintf("dlv-%d", i), fmt.Sprintf("ntf-%d", i), next))
	}

	rows = append(rows, delivery("dlv-0", "ntf-0", next.Add(-time.Minute)))
	enqueue(t, h, fence, rows...)

	// The history order is dlv-5, dlv-4, dlv-3, dlv-2, dlv-1, dlv-0.
	cursor := func(id string, at time.Time) *notify.DeliveryCursor {
		return &notify.DeliveryCursor{NextAt: at, ID: id}
	}

	cases := []struct {
		name  string
		after *notify.DeliveryCursor
		max   int
		want  int
	}{
		{"no cursor", nil, 10, 0},
		{"a cursor in the middle", cursor("dlv-3", next), 10, 3},
		{"a cursor at the oldest", cursor("dlv-0", next.Add(-time.Minute)), 10, 6},
		{"a cursor past the oldest", cursor("dlv-9", next.Add(-time.Hour)), 10, 6},
		{"a cursor newer than every delivery", cursor("dlv-9", next.Add(time.Hour)), 10, 0},
		{"the cap", cursor("dlv-3", next), 2, 2},
		{"a cap above the rows", cursor("dlv-3", next), 100, 3},
	}

	for i := range cases {
		got, err := h.Deliveries.CountBefore(t.Context(), shop, "sub-1", cases[i].after, cases[i].max)
		if err != nil {
			t.Fatalf("CountBefore of %s: %v", cases[i].name, err)
		}

		if got != cases[i].want {
			t.Fatalf("CountBefore of %s = %d, want %d", cases[i].name, got, cases[i].want)
		}
	}

	if got, err := h.Deliveries.CountBefore(t.Context(), other, "sub-1", cursor("dlv-3", next), 10); !errors.Is(
		err,
		notify.ErrNotFound,
	) {
		t.Fatalf("CountBefore of a subscription of another scope = %d (%v), want ErrNotFound", got, err)
	}
}

func testRecordClaim(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	enqueue(t, h, fence, delivery("dlv-1", "ntf-1", now()))

	unclaimed := attempt(delivery("dlv-1", "ntf-1", now()), now())
	unclaimed.Owner, unclaimed.Version = owner, 1

	if err := h.Deliveries.Record(t.Context(), fence, unclaimed); !errors.Is(err, notify.ErrConflict) {
		t.Fatalf("Record with no claim: err = %v, want ErrConflict", err)
	}

	d := claim(t, h, fence, now(), 1)[0]

	stranger := attempt(d, now())
	stranger.Owner = "instance-2"

	leak := attempt(d, now())
	leak.State, leak.LastError = notify.DeliveryAbandoned, "dial tcp 10.0.0.1: "+fakecred.URL(
		"https",
		"user",
		"pw",
		"x",
		"",
	)

	cases := []struct {
		name string
		a    notify.Attempt
		want error
	}{
		{"another owner", stranger, notify.ErrConflict},
		{"a passed lease", attempt(d, now().Add(lease)), notify.ErrConflict},
		{"the text of an error", leak, notify.ErrInvalid},
	}

	for i := range cases {
		if err := h.Deliveries.Record(t.Context(), fence, cases[i].a); !errors.Is(err, cases[i].want) {
			t.Fatalf("Record with %s: err = %v, want %v", cases[i].name, err, cases[i].want)
		}
	}

	if err := h.Deliveries.Record(t.Context(), fence, attempt(d, now())); err != nil {
		t.Fatalf("Record: %v", err)
	}

	got := history(t, h, shop)
	if len(got) != 1 || got[0].Notification != "ntf-1" || got[0].Subscription != "sub-1" ||
		got[0].Sink != "snk-1" || got[0].Principal != "alice" || got[0].State != notify.DeliveryDone ||
		got[0].LastError != "" {
		t.Fatalf("History = %+v, want dlv-1 delivered with its notification, subscription and principal", got)
	}
}

func testRedeliverPending(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	enqueue(t, h, fence, delivery("dlv-1", "ntf-1", now()))
	claim(t, h, fence, now(), 1)

	if err := h.Deliveries.Redeliver(t.Context(), "dlv-1", fence, manage()); !errors.Is(err, notify.ErrConflict) {
		t.Fatalf("Redeliver of a pending delivery: err = %v, want ErrConflict", err)
	}

	if again := claim(t, h, fence, now().Add(lease/2), 1); len(again) != 0 {
		t.Fatalf("Claim under the live claim after Redeliver = %s, want none", ids(again))
	}

	if entries, err := h.Domain.AuditEntries(t.Context(), shop); err != nil || len(entries) != 0 {
		t.Fatalf("audit entries = %d (%v), want none", len(entries), err)
	}
}

func testSubscriptionValidate(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	putSink(t, h, fence)

	cases := []struct {
		name   string
		change func(s *notify.Subscription)
	}{
		{"an ID with no prefix", func(s *notify.Subscription) { s.ID = "x-1" }},
		{"an unknown state", func(s *notify.Subscription) { s.State = "bogus" }},
	}

	for _, tc := range cases {
		s := subscription("snk-1")
		tc.change(&s)

		if err := h.Subscriptions.PutSubscription(t.Context(), s, fence, manage()); !errors.Is(err, notify.ErrInvalid) {
			t.Fatalf("PutSubscription with %s: err = %v, want ErrInvalid", tc.name, err)
		}
	}

	if subs, err := h.Subscriptions.Subscriptions(t.Context(), shop); err != nil || len(subs) != 0 {
		t.Fatalf("Subscriptions = %+v (%v), want none", subs, err)
	}
}

func testEntryScope(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)

	if _, err := h.Domain.CreateNamespace(t.Context(), other); err != nil {
		t.Fatalf("CreateNamespace: %v", err)
	}

	err := h.Subscriptions.PutSink(
		t.Context(),
		sink("snk-2", shop),
		fresh(t.Context(), sink("snk-2", shop)),
		fence,
		entry(other),
	)
	if !errors.Is(err, notify.ErrInvalid) {
		t.Fatalf("PutSink with an audit entry of another scope: err = %v, want ErrInvalid", err)
	}

	putSink(t, h, fence)

	err = h.Subscriptions.DeleteSink(
		t.Context(),
		"snk-1",
		fence,
		entry(other),
	)
	if !errors.Is(err, notify.ErrInvalid) {
		t.Fatalf("DeleteSink with an audit entry of another scope: err = %v, want ErrInvalid", err)
	}

	if sinks, err := h.Subscriptions.Sinks(t.Context(), shop); err != nil || len(sinks) != 1 || sinks[0].ID != "snk-1" {
		t.Fatalf("Sinks = %+v (%v), want snk-1 only", sinks, err)
	}

	for _, scope := range []namespace.Name{shop, other} {
		entries, err := h.Domain.AuditEntries(t.Context(), scope)
		if err != nil || len(entries) != map[namespace.Name]int{shop: 1, other: 0}[scope] {
			t.Fatalf("audit entries of %s = %d (%v), want the entry of the one sink in shop", scope, len(entries), err)
		}
	}
}

// write is one write method of the ports, for the fence cases. prepare
// makes the rows of scope that the write needs, with the good fence. do
// runs the write on the rows of scope with fence. check verifies, with
// the good fence, that the refused write changed nothing. scopeErr is the
// error of a fence of another scope.
type write struct {
	name     string
	prepare  func(t *testing.T, h Harness, fence namespace.Fence, scope namespace.Name)
	do       func(ctx context.Context, h Harness, fence namespace.Fence, scope namespace.Name) error
	check    func(t *testing.T, h Harness, fence namespace.Fence, scope namespace.Name)
	scopeErr error
}

func putSinkIn(t *testing.T, h Harness, fence namespace.Fence, scope namespace.Name) {
	t.Helper()

	s := sink("snk-1", scope)
	if err := h.Subscriptions.PutSink(t.Context(), s, fresh(t.Context(), s), fence, entry(scope)); err != nil {
		t.Fatalf("PutSink: %v", err)
	}
}

func subscribeIn(t *testing.T, h Harness, fence namespace.Fence, scope namespace.Name) {
	t.Helper()
	putSinkIn(t, h, fence, scope)

	err := h.Subscriptions.PutSubscription(
		t.Context(),
		subscriptionIn(scope, "snk-1"),
		fence,
		entry(scope),
	)
	if err != nil {
		t.Fatalf("PutSubscription: %v", err)
	}
}

func enqueueIn(t *testing.T, h Harness, fence namespace.Fence, scope namespace.Name) {
	t.Helper()

	err := h.Deliveries.Enqueue(
		t.Context(),
		fence,
		[]notify.Delivery{deliveryIn(scope, "dlv-1", "ntf-1", now())},
	)
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
}

func claimedIn(t *testing.T, h Harness, fence namespace.Fence, scope namespace.Name) {
	t.Helper()
	enqueueIn(t, h, fence, scope)

	if got, err := h.Deliveries.Claim(t.Context(), fence, claimIn(scope, now(), 1)); err != nil || len(got) != 1 {
		t.Fatalf("Claim = %s (%v), want one", ids(got), err)
	}
}

func abandonedIn(t *testing.T, h Harness, fence namespace.Fence, scope namespace.Name) {
	t.Helper()
	enqueueIn(t, h, fence, scope)
	abandon(t, h, fence, scope)
}

// checkSinks returns a check that scope holds n sinks.
func checkSinks(n int) func(*testing.T, Harness, namespace.Fence, namespace.Name) {
	return func(t *testing.T, h Harness, _ namespace.Fence, scope namespace.Name) {
		t.Helper()

		if got, err := h.Subscriptions.Sinks(t.Context(), scope); err != nil || len(got) != n {
			t.Fatalf("Sinks = %+v (%v), want %d", got, err, n)
		}
	}
}

// checkSubscriptions returns a check that scope holds n subscriptions.
func checkSubscriptions(n int) func(*testing.T, Harness, namespace.Fence, namespace.Name) {
	return func(t *testing.T, h Harness, _ namespace.Fence, scope namespace.Name) {
		t.Helper()

		if got, err := h.Subscriptions.Subscriptions(t.Context(), scope); err != nil || len(got) != n {
			t.Fatalf("Subscriptions = %+v (%v), want %d", got, err, n)
		}
	}
}

// checkDelivery returns a check that dlv-1 is the one delivery of scope,
// in the state want, with the claim of holder ("" for no claim).
func checkDelivery(
	want notify.DeliveryState,
	holder string,
) func(*testing.T, Harness, namespace.Fence, namespace.Name) {
	return func(t *testing.T, h Harness, _ namespace.Fence, scope namespace.Name) {
		t.Helper()

		got := history(t, h, scope)
		if len(got) != 1 || got[0].State != want || got[0].Owner != holder {
			t.Fatalf("History = %+v, want dlv-1 %s, claim of %q", got, want, holder)
		}
	}
}

func checkNoDelivery(t *testing.T, h Harness, _ namespace.Fence, scope namespace.Name) {
	t.Helper()

	if got := history(t, h, scope); len(got) != 0 {
		t.Fatalf("History = %s, want none", ids(got))
	}
}

func checkCursorZero(t *testing.T, h Harness, _ namespace.Fence, scope namespace.Name) {
	t.Helper()

	if c, err := h.Outbox.Cursor(t.Context(), "dispatch", scope); err != nil || c != 0 {
		t.Fatalf("Cursor = %d (%v), want 0", c, err)
	}
}

// checkClaimable checks that the good fence can still claim dlv-1.
func checkClaimable(t *testing.T, h Harness, fence namespace.Fence, scope namespace.Name) {
	t.Helper()

	got, err := h.Deliveries.Claim(t.Context(), fence, claimIn(scope, now(), 1))
	if err != nil || ids(got) != "[dlv-1]" {
		t.Fatalf("Claim with the good fence = %s (%v), want [dlv-1]", ids(got), err)
	}
}

func writes() []write {
	none := func(*testing.T, Harness, namespace.Fence, namespace.Name) {}

	return []write{
		{"Append", none, func(ctx context.Context, h Harness, f namespace.Fence, s namespace.Name) error {
			return h.Outbox.Append(ctx, f, []notify.Notification{sample("ntf-1", s)})
		}, none, notify.ErrInvalid},
		{"MoveCursor", none, func(ctx context.Context, h Harness, f namespace.Fence, s namespace.Name) error {
			return h.Outbox.MoveCursor(ctx, f, moveIn(s, 0, 1))
		}, checkCursorZero, notify.ErrInvalid},
		{"PutSink", none, func(ctx context.Context, h Harness, f namespace.Fence, s namespace.Name) error {
			return h.Subscriptions.PutSink(ctx, sink("snk-1", s), fresh(ctx, sink("snk-1", s)), f, entry(s))
		}, checkSinks(0), notify.ErrInvalid},
		{"DeleteSink", putSinkIn, func(ctx context.Context, h Harness, f namespace.Fence, s namespace.Name) error {
			return h.Subscriptions.DeleteSink(ctx, "snk-1", f, entry(s))
		}, checkSinks(1), notify.ErrNotFound},
		{"PutSubscription", putSinkIn, func(ctx context.Context, h Harness, f namespace.Fence, s namespace.Name) error {
			return h.Subscriptions.PutSubscription(ctx, subscriptionIn(s, "snk-1"), f, entry(s))
		}, checkSubscriptions(0), notify.ErrInvalid},
		{
			"DeleteSubscription",
			subscribeIn,
			func(ctx context.Context, h Harness, f namespace.Fence, s namespace.Name) error {
				return h.Subscriptions.DeleteSubscription(ctx, "sub-1", f, entry(s))
			},
			checkSubscriptions(1),
			notify.ErrNotFound,
		},
		{"Enqueue", none, func(ctx context.Context, h Harness, f namespace.Fence, s namespace.Name) error {
			return h.Deliveries.Enqueue(ctx, f, []notify.Delivery{deliveryIn(s, "dlv-1", "ntf-1", now())})
		}, checkNoDelivery, notify.ErrInvalid},
		{"Claim", enqueueIn, func(ctx context.Context, h Harness, f namespace.Fence, s namespace.Name) error {
			_, err := h.Deliveries.Claim(ctx, f, claimIn(s, now(), 1))

			return err
		}, checkClaimable, notify.ErrInvalid},
		{"Record", claimedIn, func(ctx context.Context, h Harness, f namespace.Fence, s namespace.Name) error {
			d := deliveryIn(s, "dlv-1", "ntf-1", now())
			d.Owner, d.Version = owner, 2

			return h.Deliveries.Record(ctx, f, attempt(d, now()))
		}, checkDelivery(notify.DeliveryPending, owner), notify.ErrNotFound},
		{"Redeliver", abandonedIn, func(ctx context.Context, h Harness, f namespace.Fence, s namespace.Name) error {
			return h.Deliveries.Redeliver(ctx, "dlv-1", f, entry(s))
		}, checkDelivery(notify.DeliveryAbandoned, ""), notify.ErrNotFound},
	}
}

// fenceCase is one kind of wrong fence. server is true for a row of the
// server scope, whose good fence is the zero fence. fence returns the
// wrong fence for the good fence of the namespace shop, and the good fence
// of the row after it, and want returns the error that a write w returns
// for it.
type fenceCase struct {
	name   string
	server bool
	fence  func(t *testing.T, h Harness, good namespace.Fence) (bad, current namespace.Fence)
	want   func(w write) error
}

func fenceCases() []fenceCase {
	return []fenceCase{
		{
			"a zero fence", false,
			func(_ *testing.T, _ Harness, good namespace.Fence) (namespace.Fence, namespace.Fence) {
				return zeroFence(), good
			},
			func(w write) error { return w.scopeErr },
		},
		{
			"the fence of another namespace",
			false,
			func(t *testing.T, h Harness, good namespace.Fence) (namespace.Fence, namespace.Fence) {
				t.Helper()

				f, err := h.Domain.CreateNamespace(t.Context(), other)
				if err != nil {
					t.Fatalf("CreateNamespace: %v", err)
				}

				return f, good
			},
			func(w write) error { return w.scopeErr },
		},
		{
			"a stale fence",
			false,
			func(t *testing.T, h Harness, good namespace.Fence) (namespace.Fence, namespace.Fence) {
				t.Helper()

				bumped, err := h.Domain.BumpFence(t.Context(), good)
				if err != nil {
					t.Fatalf("BumpFence: %v", err)
				}

				return good, bumped
			},
			func(write) error { return namespace.ErrFenced },
		},
		{
			"a namespace fence on a row of the server scope", true,
			func(_ *testing.T, _ Harness, good namespace.Fence) (namespace.Fence, namespace.Fence) {
				return good, zeroFence()
			},
			func(w write) error { return w.scopeErr },
		},
	}
}

// runFences runs every write with a zero fence, the fence of another
// namespace and a stale fence on a row of a namespace, and with a
// namespace fence on a row of the server scope, and checks that each
// fails with nothing written.
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

	shopFence := setup(t, h)

	scope, good := shop, shopFence
	if f.server {
		scope, good = audit.ServerScope, zeroFence()
	}

	w.prepare(t, h, good, scope)

	before, err := h.Domain.AuditEntries(t.Context(), scope)
	if err != nil {
		t.Fatalf("AuditEntries: %v", err)
	}

	bad, current := f.fence(t, h, shopFence)

	if werr := w.do(t.Context(), h, bad, scope); !errors.Is(werr, f.want(w)) {
		t.Fatalf("%s: err = %v, want %v", w.name, werr, f.want(w))
	}

	after, err := h.Domain.AuditEntries(t.Context(), scope)
	if err != nil || len(after) != len(before) {
		t.Fatalf("audit entries = %d (%v), want %d", len(after), err, len(before))
	}

	all, err := h.Outbox.After(t.Context(), scope, 0, 10)
	if err != nil || len(all) != 0 {
		t.Fatalf("After = %d notifications (%v), want none", len(all), err)
	}

	w.check(t, h, current, scope)
}

// subscriptionOf returns the stored subscription id of the namespace shop.
func subscriptionOf(t *testing.T, h Harness, id string) notify.Subscription {
	t.Helper()

	subs, err := h.Subscriptions.Subscriptions(t.Context(), shop)
	if err != nil {
		t.Fatalf("Subscriptions: %v", err)
	}

	i := slices.IndexFunc(subs, func(s notify.Subscription) bool { return s.ID == id })
	if i < 0 {
		t.Fatalf("Subscriptions = %+v, want %s", subs, id)
	}

	return subs[i]
}

func testSubscriptionCreator(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	putSink(t, h, fence)

	s := subscription("snk-1")
	s.CreatedBy = "mallory"

	if err := h.Subscriptions.PutSubscription(t.Context(), s, fence, manage()); err != nil {
		t.Fatalf("PutSubscription: %v", err)
	}

	if got := subscriptionOf(t, h, "sub-1"); got.CreatedBy != "alice" {
		t.Fatalf("CreatedBy after the create = %q, want alice, the subject of the audit entry", got.CreatedBy)
	}

	s.Version, s.CreatedBy, s.State = 1, "alice", notify.SubscriptionStateSuspended
	if err := h.Subscriptions.PutSubscription(t.Context(), s, fence, manageBy("bob")); err != nil {
		t.Fatalf("PutSubscription of bob: %v", err)
	}

	if got := subscriptionOf(t, h, "sub-1"); got.CreatedBy != "bob" {
		t.Fatalf("CreatedBy after the change of bob = %q, want bob", got.CreatedBy)
	}

	s.Version, s.State = 2, notify.SubscriptionStateActive
	if err := h.Subscriptions.PutSubscription(t.Context(), s, fence, manageBy("carol")); err != nil {
		t.Fatalf("PutSubscription of carol: %v", err)
	}

	if got := subscriptionOf(t, h, "sub-1"); got.CreatedBy != "carol" || got.State != notify.SubscriptionStateActive {
		t.Fatalf("after the resume of carol = %+v, want an active subscription of carol", got)
	}
}

func testSinkChangeSuspends(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	subscribe(t, h, fence)
	putSecond(t, h, fence)

	two := subscription("snk-2")
	two.ID = "sub-2"

	if err := h.Subscriptions.PutSubscription(t.Context(), two, fence, manage()); err != nil {
		t.Fatalf("PutSubscription sub-2: %v", err)
	}

	renamed := sink("snk-1", shop)
	renamed.Version, renamed.Name = 1, "Ops team"

	if err := h.Subscriptions.PutSink(t.Context(), renamed, nil, fence, manage()); err != nil {
		t.Fatalf("PutSink with a new name: %v", err)
	}

	if got := subscriptionOf(t, h, "sub-1"); got.State != notify.SubscriptionStateActive || got.Version != 1 {
		t.Fatalf("after a new name of the sink = %+v, want active at version 1", got)
	}

	changeDestination(t, h, fence, renamed)

	if got := subscriptionOf(t, h, "sub-2"); got.State != notify.SubscriptionStateActive {
		t.Fatalf("subscription of another sink = %+v, want active", got)
	}
}

// changeDestination changes the target, then the secrets, of the sink
// renamed, at version 1 and with the subscription sub-1, and checks after
// each change that sub-1 is suspended. It resumes sub-1 after each check.
func changeDestination(t *testing.T, h Harness, fence namespace.Fence, renamed notify.Sink) {
	t.Helper()

	changes := []struct {
		name    string
		change  func(s *notify.Sink)
		secrets []notify.SealedSecret
	}{
		{"a new target", func(s *notify.Sink) { s.Target = "hooks.example.net" }, nil},
		{
			"new secrets", func(s *notify.Sink) { s.SecretRefs = []string{"itm-snk-1", "itm-snk-1-b"} },
			[]notify.SealedSecret{secret(t.Context(), renamed, "itm-snk-1-b")},
		},
	}

	for i, tc := range changes {
		s := renamed
		s.Version = int64(i) + 2
		tc.change(&s)
		renamed = s

		if err := h.Subscriptions.PutSink(t.Context(), s, tc.secrets, fence, manage()); err != nil {
			t.Fatalf("PutSink with %s: %v", tc.name, err)
		}

		got := subscriptionOf(t, h, "sub-1")
		if got.State != notify.SubscriptionStateSuspended || got.Reason != notify.ReasonSinkChanged ||
			got.Version != int64(2*i+2) {
			t.Fatalf("after %s = %+v, want suspended for the sink change at version %d", tc.name, got, 2*i+2)
		}

		resume := got
		resume.State, resume.Reason = notify.SubscriptionStateActive, ""

		if err := h.Subscriptions.PutSubscription(t.Context(), resume, fence, manage()); err != nil {
			t.Fatalf("resume after %s: %v", tc.name, err)
		}
	}
}

func testSinkChangeRace(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	subscribe(t, h, fence)

	stale := subscription("snk-1")
	stale.Version, stale.Filter.MinSeverity = 1, notify.SeverityCritical

	changed := sink("snk-1", shop)
	changed.Version, changed.Target = 1, "hooks.example.net"

	if err := h.Subscriptions.PutSink(t.Context(), changed, nil, fence, manage()); err != nil {
		t.Fatalf("PutSink: %v", err)
	}

	err := h.Subscriptions.PutSubscription(t.Context(), stale, fence, manageBy("bob"))
	if !errors.Is(err, notify.ErrConflict) {
		t.Fatalf("PutSubscription read before the sink change: err = %v, want ErrConflict", err)
	}

	if got := subscriptionOf(t, h, "sub-1"); got.State != notify.SubscriptionStateSuspended {
		t.Fatalf("sub-1 after a refused edit = %+v, want suspended", got)
	}

	for round := 1; round <= 20; round++ {
		raceSinkChange(t, h, fence, round)
	}
}

// raceSinkChange runs a change of the target of a new sink and an edit of
// its subscription, read before the change, at one time. In each order,
// the subscription ends suspended: the edit before the change is
// suspended by it, and the edit after the change gets ErrConflict.
func raceSinkChange(t *testing.T, h Harness, fence namespace.Fence, round int) {
	t.Helper()

	id := fmt.Sprintf("snk-%d", round+1)
	s := sink(id, shop)
	if err := h.Subscriptions.PutSink(t.Context(), s, fresh(t.Context(), s), fence, manage()); err != nil {
		t.Fatalf("PutSink %s: %v", id, err)
	}

	read := subscription(id)
	read.ID = fmt.Sprintf("sub-%d", round+1)

	if err := h.Subscriptions.PutSubscription(t.Context(), read, fence, manage()); err != nil {
		t.Fatalf("PutSubscription %s: %v", read.ID, err)
	}

	changed := sink(id, shop)
	changed.Version, changed.Target = 1, "hooks.example.net"

	edit := read
	edit.Version, edit.Filter.MinSeverity = 1, notify.SeverityCritical

	var wg sync.WaitGroup

	errs := make([]error, 2)

	wg.Go(func() { errs[0] = h.Subscriptions.PutSink(t.Context(), changed, nil, fence, manage()) })
	wg.Go(func() { errs[1] = h.Subscriptions.PutSubscription(t.Context(), edit, fence, manageBy("bob")) })
	wg.Wait()

	if errs[0] != nil {
		t.Fatalf("PutSink %s: %v", id, errs[0])
	}

	if errs[1] != nil && !errors.Is(errs[1], notify.ErrConflict) {
		t.Fatalf("PutSubscription %s: err = %v, want nil or ErrConflict", read.ID, errs[1])
	}

	if got := subscriptionOf(t, h, read.ID); got.State != notify.SubscriptionStateSuspended {
		t.Fatalf("%s after a sink change and an edit read before it = %+v, want suspended", read.ID, got)
	}
}
