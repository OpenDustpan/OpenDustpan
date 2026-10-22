package audittest

import (
	"context"
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/logsink"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// FeedStore is every port the feed cases exercise: namespaces for fixtures,
// the audit store to append entries, and the feed and sink leases under test.
type FeedStore interface {
	Store
	audit.ServerStore
	audit.Feed
	audit.SinkLeaser
}

// FeedFactory returns a fresh, empty store for one feed subtest.
type FeedFactory func(t *testing.T) FeedStore

// feedEntries is the count of entries the feed cases append: more than one
// page of the limit the cases read with.
const feedEntries = 7

// feedLimit is the limit the paging case reads with.
const feedLimit = 3

// RunFeed executes the conformance cases of audit.Feed against stores
// produced by newStore.
func RunFeed(t *testing.T, newStore FeedFactory) {
	t.Helper()

	cases := []struct {
		test func(t *testing.T, s FeedStore)
		name string
	}{
		{name: "after answers entries above a sequence oldest first", test: testAfterPages},
		{name: "after on a namespace with no entries is empty", test: testAfterEmpty},
		{name: "after answers only entries of its namespace", test: testAfterIsolation},
		{name: "a sink with no cursor reads zero", test: testCursorZero},
		{name: "a cursor keeps its sink and namespace", test: testCursorKeys},
		{name: "a cursor never moves back", test: testCursorNeverBack},
		{name: "advance with a stale fence fails", test: testCursorStaleFence},
		{name: "advance in a missing namespace fails", test: testCursorMissingNamespace},
		{name: "the server scope reads and keeps a cursor with no fence", test: testServerScopeFeed},
		{name: "a sink lease is taken when no row exists", test: testLeaseTake},
		{name: "the holder renews its sink lease", test: testLeaseRenewByHolder},
		{name: "another holder is refused while the lease is live", test: testLeaseRefusedWhileLive},
		{name: "a sink lease is taken after expiry", test: testLeaseTakenAfterExpiry},
		{name: "a released sink lease is taken by another holder", test: testLeaseRelease},
		{name: "a sink lease with an unusable name, holder or ttl is refused", test: testLeaseValidates},
		{name: "a release by a non-holder fails and changes nothing", test: testLeaseReleaseByNonHolder},
		{name: "a stale holder cannot advance a cursor", test: testStaleHolderCannotAdvance},
		{name: "a claim of another sink cannot advance a cursor", test: testClaimOfAnotherSinkCannotAdvance},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.test(t, newStore(t))
		})
	}
}

// discardSink drops every batch it receives.
type discardSink struct{}

func (discardSink) Write(context.Context, []logsink.Record) error { return nil }
func (discardSink) Close() error                                  { return nil }

// CheckShipperRefusesLeaselessStore proves that NewShipper refuses a
// store-backed feed with no lease store: the cursor writes of a real store
// check the claim, so a shipper with a zero claim would end every round
// cleanly with nothing shipped.
func CheckShipperRefusesLeaselessStore(t *testing.T, s FeedStore) {
	t.Helper()

	_, err := audit.NewShipper(audit.ShipperConfig{
		Feed:          s,
		Namespaces:    s,
		Logger:        nil,
		OnShipped:     nil,
		OnFailure:     nil,
		OnSkipped:     nil,
		OnLag:         nil,
		OnLagEntries:  nil,
		Now:           nil,
		Sinks:         []audit.ShipSink{{Sink: discardSink{}, Name: "siem", Kind: "test", Interval: 0}},
		InstanceID:    "",
		Leases:        nil,
		LeaseTTL:      0,
		RenewInterval: 0,
	})
	if !errors.Is(err, audit.ErrInvalidShipper) {
		t.Fatalf("new shipper with a store-backed feed and no leases: got %v, want ErrInvalidShipper", err)
	}
}

// appendN appends n entries to ns and answers them in order.
func appendN(t *testing.T, s FeedStore, ns namespace.Namespace, n int) []audit.Entry {
	t.Helper()

	out := make([]audit.Entry, 0, n)

	for i := range n {
		out = append(out, appendEntry(t, s, ns.Fence(), newEntry(ns.Name, "alice", "req-"+string(rune('a'+i)), now())))
	}

	return out
}

// after reads at most feedLimit entries and fails the test on error.
func after(t *testing.T, s FeedStore, ns namespace.Name, seq int64) []audit.Entry {
	t.Helper()

	got, err := s.After(t.Context(), ns, seq, feedLimit)
	if err != nil {
		t.Fatalf("after %d: %v", seq, err)
	}

	return got
}

// cursor reads a cursor and fails the test on error.
func cursor(t *testing.T, s FeedStore, sink string, ns namespace.Name) int64 {
	t.Helper()

	got, err := s.SinkCursor(t.Context(), sink, ns)
	if err != nil {
		t.Fatalf("sink cursor %s/%s: %v", sink, ns, err)
	}

	return got
}

// sinkLease is the live claim of one sink in a conformance test.
type sinkLease struct {
	sink  string
	claim durable.Claim
}

// advance moves the cursor of the sink of lease with its claim and fails
// the test on error.
func advance(t *testing.T, s FeedStore, fence namespace.Fence, lease sinkLease, seq int64) {
	t.Helper()

	if err := s.AdvanceSinkCursor(t.Context(), fence, lease.claim, lease.sink, seq); err != nil {
		t.Fatalf("advance %s to %d: %v", lease.sink, seq, err)
	}
}

func testAfterPages(t *testing.T, s FeedStore) {
	t.Helper()

	ns := newNamespace(t, s)
	want := appendN(t, s, ns, feedEntries)

	var got []audit.Entry

	var seq int64

	for {
		page := after(t, s, ns.Name, seq)
		if len(page) > feedLimit {
			t.Fatalf("page holds %d entries, limit %d", len(page), feedLimit)
		}

		if len(page) == 0 {
			break
		}

		got = append(got, page...)
		seq = page[len(page)-1].Sequence
	}

	if len(got) != len(want) {
		t.Fatalf("read %d entries, want %d", len(got), len(want))
	}

	for i := range want {
		if got[i].Sequence != want[i].Sequence || got[i].Hash != want[i].Hash || got[i].RequestID != want[i].RequestID {
			t.Fatalf("entry %d: got sequence %d request %q, want %d %q",
				i, got[i].Sequence, got[i].RequestID, want[i].Sequence, want[i].RequestID)
		}
	}
}

func testAfterEmpty(t *testing.T, s FeedStore) {
	t.Helper()

	ns := newNamespace(t, s)

	if got := after(t, s, ns.Name, 0); len(got) != 0 {
		t.Fatalf("after on an empty namespace: got %d entries", len(got))
	}

	appendN(t, s, ns, 2)

	if got := after(t, s, ns.Name, 2); len(got) != 0 {
		t.Fatalf("after the last sequence: got %d entries", len(got))
	}
}

func testAfterIsolation(t *testing.T, s FeedStore) {
	t.Helper()

	ns := newNamespace(t, s)
	other := otherNamespace(t, s)

	appendN(t, s, ns, 2)
	appendN(t, s, other, 1)

	got := after(t, s, ns.Name, 0)
	for i := range got {
		if got[i].Namespace != ns.Name {
			t.Fatalf("after %s answered an entry of %s", ns.Name, got[i].Namespace)
		}
	}

	if got := after(t, s, other.Name, 0); len(got) != 1 {
		t.Fatalf("after %s: got %d entries, want 1", other.Name, len(got))
	}
}

func testCursorZero(t *testing.T, s FeedStore) {
	t.Helper()

	ns := newNamespace(t, s)

	if got := cursor(t, s, "siem", ns.Name); got != 0 {
		t.Fatalf("cursor of a new sink: got %d, want 0", got)
	}
}

func testCursorKeys(t *testing.T, s FeedStore) {
	t.Helper()

	ns := newNamespace(t, s)
	other := otherNamespace(t, s)

	siem := acquireLease(t, s, "siem", "ins-holder-a", 30*time.Second)
	archive := acquireLease(t, s, "archive", "ins-holder-a", 30*time.Second)

	advance(t, s, ns.Fence(), siem, 4)
	advance(t, s, ns.Fence(), archive, 2)
	advance(t, s, other.Fence(), siem, 9)

	checks := []struct {
		sink string
		ns   namespace.Name
		want int64
	}{
		{sink: "siem", ns: ns.Name, want: 4},
		{sink: "archive", ns: ns.Name, want: 2},
		{sink: "siem", ns: other.Name, want: 9},
		{sink: "archive", ns: other.Name, want: 0},
	}

	for _, c := range checks {
		if got := cursor(t, s, c.sink, c.ns); got != c.want {
			t.Fatalf("cursor %s/%s: got %d, want %d", c.sink, c.ns, got, c.want)
		}
	}
}

func testCursorNeverBack(t *testing.T, s FeedStore) {
	t.Helper()

	ns := newNamespace(t, s)
	claim := acquireLease(t, s, "siem", "ins-holder-a", 30*time.Second)

	advance(t, s, ns.Fence(), claim, 5)
	advance(t, s, ns.Fence(), claim, 3)

	if got := cursor(t, s, "siem", ns.Name); got != 5 {
		t.Fatalf("cursor after a lower advance: got %d, want 5", got)
	}

	advance(t, s, ns.Fence(), claim, 6)

	if got := cursor(t, s, "siem", ns.Name); got != 6 {
		t.Fatalf("cursor after a higher advance: got %d, want 6", got)
	}
}

func testCursorStaleFence(t *testing.T, s FeedStore) {
	t.Helper()

	ns := newNamespace(t, s)
	claim := acquireLease(t, s, "siem", "ins-holder-a", 30*time.Second)
	stale := namespace.Fence{Namespace: ns.Name, FailoverVersion: ns.FailoverVersion + 1}

	err := s.AdvanceSinkCursor(t.Context(), stale, claim.claim, "siem", 1)
	if !errors.Is(err, namespace.ErrFenced) {
		t.Fatalf("advance with a stale fence: got %v, want ErrFenced", err)
	}

	if got := cursor(t, s, "siem", ns.Name); got != 0 {
		t.Fatalf("cursor after a fenced advance: got %d, want 0", got)
	}
}

func testCursorMissingNamespace(t *testing.T, s FeedStore) {
	t.Helper()

	claim := acquireLease(t, s, "siem", "ins-holder-a", 30*time.Second)
	missing := namespace.Fence{Namespace: "ghost", FailoverVersion: 1}

	err := s.AdvanceSinkCursor(t.Context(), missing, claim.claim, "siem", 1)
	if !errors.Is(err, namespace.ErrNotFound) {
		t.Fatalf("advance in a missing namespace: got %v, want namespace.ErrNotFound", err)
	}
}

func testServerScopeFeed(t *testing.T, s FeedStore) {
	t.Helper()

	e := newEntry(audit.ServerScope, "alice", "req-server", now())
	e.Kind = audit.KindChange
	e.Object = audit.ServerScope.String()

	stored, err := s.AppendServer(t.Context(), e)
	if err != nil {
		t.Fatalf("append server entry: %v", err)
	}

	got := after(t, s, audit.ServerScope, 0)
	if len(got) != 1 || got[0].Sequence != stored.Sequence || got[0].Hash != stored.Hash {
		t.Fatalf("after on the server scope: got %d entries, want the one appended", len(got))
	}

	claim := acquireLease(t, s, "siem", "ins-holder-a", 30*time.Second)
	advance(t, s, namespace.Fence{Namespace: audit.ServerScope, FailoverVersion: 0}, claim, stored.Sequence)

	if c := cursor(t, s, "siem", audit.ServerScope); c != stored.Sequence {
		t.Fatalf("server scope cursor: got %d, want %d", c, stored.Sequence)
	}
}

// acquireLease takes or renews the lease of sink for holder and answers
// its live claim. It fails the test when another instance holds the lease.
func acquireLease(t *testing.T, s FeedStore, sink, holder string, ttl time.Duration) sinkLease {
	t.Helper()

	claim, err := s.AcquireSinkLease(t.Context(), sink, holder, ttl)
	if err != nil {
		t.Fatalf("acquire lease %s for %s: %v", sink, holder, err)
	}

	lease := sinkLease{sink: sink, claim: claim}
	checkClaim(t, lease, holder)

	return lease
}

// releaseLease releases the claim of lease and fails the test on error.
func releaseLease(t *testing.T, s FeedStore, lease sinkLease) {
	t.Helper()

	if err := s.ReleaseSinkLease(t.Context(), lease.claim); err != nil {
		t.Fatalf("release lease of %s: %v", lease.claim.Operation, err)
	}
}

// checkClaim fails the test unless the claim of lease is the live claim of
// holder on its sink operation: the operation is set, the owner is holder
// and the token is not zero.
func checkClaim(t *testing.T, lease sinkLease, holder string) {
	t.Helper()

	claim := lease.claim

	if claim.Operation == "" || claim.Owner != holder || claim.Token == 0 || claim.ExpiresAt.IsZero() {
		t.Fatalf("claim = %+v, want the live claim of %s", claim, holder)
	}
}

func testLeaseTake(t *testing.T, s FeedStore) {
	t.Helper()

	checkClaim(t, acquireLease(t, s, "siem", "ins-holder-a", 30*time.Second), "ins-holder-a")
}

func testLeaseRenewByHolder(t *testing.T, s FeedStore) {
	t.Helper()

	first := acquireLease(t, s, "siem", "ins-holder-a", 30*time.Second)
	second := acquireLease(t, s, "siem", "ins-holder-a", 30*time.Second)

	if second.claim.Operation != first.claim.Operation || second.claim.Token != first.claim.Token ||
		second.claim.Owner != "ins-holder-a" {
		t.Fatalf("renew lease by the holder: got %+v after %+v, want one claim with one token", second, first)
	}
}

func testLeaseRefusedWhileLive(t *testing.T, s FeedStore) {
	t.Helper()

	ns := newNamespace(t, s)
	held := acquireLease(t, s, "siem", "ins-holder-a", 30*time.Second)

	_, err := s.AcquireSinkLease(t.Context(), "siem", "ins-holder-b", 30*time.Second)
	if !errors.Is(err, durable.ErrNoWork) {
		t.Fatalf("acquire by another holder while live: got %v, want ErrNoWork", err)
	}

	advance(t, s, ns.Fence(), held, 1)

	if got := cursor(t, s, "siem", ns.Name); got != 1 {
		t.Fatalf("cursor after a refused acquire: got %d, want 1", got)
	}
}

// testLeaseTakenAfterExpiry proves that a sink lease is taken after its
// expiry with a higher token for the new holder.
func testLeaseTakenAfterExpiry(t *testing.T, s FeedStore) {
	t.Helper()

	first := acquireLease(t, s, "siem", "ins-holder-a", 150*time.Millisecond)

	second := acquireWhenExpired(t, s)

	if second.claim.Operation != first.claim.Operation || second.claim.Token <= first.claim.Token ||
		second.claim.Owner != "ins-holder-b" {
		t.Fatalf("claim after expiry: got %+v after %+v, want a higher token for ins-holder-b", second, first)
	}
}

// acquireWhenExpired acquires the siem sink for ins-holder-b as soon as no
// live claim holds it, and fails the test after 10 seconds.
func acquireWhenExpired(t *testing.T, s FeedStore) sinkLease {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)

	for {
		claim, err := s.AcquireSinkLease(t.Context(), "siem", "ins-holder-b", 30*time.Second)
		if err == nil {
			return sinkLease{sink: "siem", claim: claim}
		}

		if !errors.Is(err, durable.ErrNoWork) {
			t.Fatalf("acquire lease siem for ins-holder-b: %v", err)
		}

		if time.Now().After(deadline) {
			t.Fatal("lease was not taken after expiry within 10 seconds")
		}

		waitLeaseExpiry(t)
	}
}

// waitLeaseExpiry waits a short time for a sink lease to end, or stops the
// test when its context ends.
func waitLeaseExpiry(t *testing.T) {
	t.Helper()

	timer := time.NewTimer(50 * time.Millisecond)
	defer timer.Stop()

	select {
	case <-t.Context().Done():
		t.Fatal("lease was not taken after expiry within 10 seconds: test context done")
	case <-timer.C:
	}
}

func testLeaseRelease(t *testing.T, s FeedStore) {
	t.Helper()

	first := acquireLease(t, s, "siem", "ins-holder-a", 30*time.Second)
	releaseLease(t, s, first)

	second := acquireLease(t, s, "siem", "ins-holder-b", 30*time.Second)

	if second.claim.Operation != first.claim.Operation || second.claim.Token <= first.claim.Token {
		t.Fatalf("take lease after release: got %+v after %+v, want a higher token", second, first)
	}
}

func testLeaseValidates(t *testing.T, s FeedStore) {
	t.Helper()

	for _, tc := range []struct {
		name   string
		sink   string
		holder string
		ttl    time.Duration
	}{
		{name: "empty sink", sink: "", holder: "ins-holder-a", ttl: 30 * time.Second},
		{name: "empty holder", sink: "siem", holder: "", ttl: 30 * time.Second},
		{name: "zero ttl", sink: "siem", holder: "ins-holder-a", ttl: 0},
		{name: "negative ttl", sink: "siem", holder: "ins-holder-a", ttl: -time.Second},
		{name: "ttl below one millisecond", sink: "siem", holder: "ins-holder-a", ttl: 500 * time.Microsecond},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := s.AcquireSinkLease(t.Context(), tc.sink, tc.holder, tc.ttl)
			if !errors.Is(err, audit.ErrInvalidSinkLease) {
				t.Fatalf("acquire lease %q for %q with ttl %s: got %v, want ErrInvalidSinkLease",
					tc.sink, tc.holder, tc.ttl, err)
			}
		})
	}
}

func testLeaseReleaseByNonHolder(t *testing.T, s FeedStore) {
	t.Helper()

	held := acquireLease(t, s, "siem", "ins-holder-a", 30*time.Second)

	forged := held
	forged.claim.Token++
	forged.claim.Owner = "ins-holder-b"

	err := s.ReleaseSinkLease(t.Context(), forged.claim)
	if !errors.Is(err, durable.ErrClaimLost) {
		t.Fatalf("release with a claim that is not live: got %v, want ErrClaimLost", err)
	}

	ns := newNamespace(t, s)
	advance(t, s, ns.Fence(), held, 1)

	if got := cursor(t, s, "siem", ns.Name); got != 1 {
		t.Fatalf("cursor after a release by a non-holder: got %d, want 1", got)
	}
}

// testStaleHolderCannotAdvance proves the claim rule of the sink lease:
// holder A loses its claim, holder B claims, and the next cursor write of
// A fails with durable.ErrClaimLost and changes nothing, while B advances
// the cursor.
func testStaleHolderCannotAdvance(t *testing.T, s FeedStore) {
	t.Helper()

	ns := newNamespace(t, s)

	// The first lease outlasts the first write, so a slow store cannot end
	// it before the write. After the write, A renews with a short lease and
	// B takes the sink once that lease ends: only the wait depends on time.
	old := acquireLease(t, s, "siem", "ins-holder-a", 30*time.Second)
	advance(t, s, ns.Fence(), old, 4)

	short := acquireLease(t, s, "siem", "ins-holder-a", 150*time.Millisecond)
	if short.claim.Token != old.claim.Token {
		t.Fatalf("renew of a live claim: got %+v after %+v, want the same token", short, old)
	}

	live := acquireWhenExpired(t, s)
	if live.claim.Token <= old.claim.Token {
		t.Fatalf("claim after expiry: got %+v after %+v, want a higher token", live, old)
	}

	err := s.AdvanceSinkCursor(t.Context(), ns.Fence(), old.claim, "siem", 5)
	if !errors.Is(err, durable.ErrClaimLost) {
		t.Fatalf("advance by a stale holder: got %v, want ErrClaimLost", err)
	}

	if got := cursor(t, s, "siem", ns.Name); got != 4 {
		t.Fatalf("cursor after an advance by a stale holder: got %d, want 4", got)
	}

	advance(t, s, ns.Fence(), live, 5)

	if got := cursor(t, s, "siem", ns.Name); got != 5 {
		t.Fatalf("cursor after an advance by the live holder: got %d, want 5", got)
	}
}

// testClaimOfAnotherSinkCannotAdvance proves that a cursor write checks the
// operation of its claim: a claim with the live token of the sink but the
// operation of another sink fails with durable.ErrClaimLost and changes
// nothing, while the live claim of the sink still advances the cursor.
func testClaimOfAnotherSinkCannotAdvance(t *testing.T, s FeedStore) {
	t.Helper()

	ns := newNamespace(t, s)

	siem := acquireLease(t, s, "siem", "ins-holder-a", 30*time.Second)
	other := acquireLease(t, s, "archive", "ins-holder-a", 30*time.Second)

	forged := other.claim
	forged.Token = siem.claim.Token

	err := s.AdvanceSinkCursor(t.Context(), ns.Fence(), forged, "siem", 1)
	if !errors.Is(err, durable.ErrClaimLost) {
		t.Fatalf("advance with the claim of another sink: got %v, want ErrClaimLost", err)
	}

	if got := cursor(t, s, "siem", ns.Name); got != 0 {
		t.Fatalf("cursor after an advance with the claim of another sink: got %d, want 0", got)
	}

	advance(t, s, ns.Fence(), siem, 1)

	if got := cursor(t, s, "siem", ns.Name); got != 1 {
		t.Fatalf("cursor after an advance by the live holder: got %d, want 1", got)
	}
}

// otherNamespace creates the namespace globex homed in eu-west.
func otherNamespace(t *testing.T, s Store) namespace.Namespace {
	t.Helper()

	ns := namespace.Namespace{
		ID:              namespace.NewID(),
		CreatedAt:       now(),
		Name:            "globex",
		HomeRegion:      "eu-west",
		FailoverVersion: 1,
		Overrides:       namespace.Overrides{Forced: false, Unlocked: false},
	}

	if err := s.CreateNamespace(t.Context(), ns); err != nil {
		t.Fatalf("create namespace %q: %v", ns.Name, err)
	}

	return ns
}

// FeedMaintenanceStore is a FeedStore that also keeps the installation state.
type FeedMaintenanceStore interface {
	FeedStore
	maintenance.Store
}

// RunFeedInMaintenance checks that a sink cursor advances in maintenance
// mode at both scopes: a refused advance would make the shipper deliver the
// same entries again in a loop.
func RunFeedInMaintenance(t *testing.T, newStore func(t *testing.T) FeedMaintenanceStore) {
	t.Helper()

	s := newStore(t)
	ns := newNamespace(t, s)
	claim := acquireLease(t, s, "siem", "ins-holder-a", 30*time.Second)

	if err := s.SetState(t.Context(), maintenance.StateMaintenance); err != nil {
		t.Fatalf("set installation state to maintenance: %v", err)
	}

	advance(t, s, ns.Fence(), claim, 4)
	advance(t, s, namespace.Fence{Namespace: audit.ServerScope, FailoverVersion: 0}, claim, 2)

	if got := cursor(t, s, "siem", ns.Name); got != 4 {
		t.Fatalf("namespace cursor after an advance in maintenance mode: got %d, want 4", got)
	}

	if got := cursor(t, s, "siem", audit.ServerScope); got != 2 {
		t.Fatalf("server cursor after an advance in maintenance mode: got %d, want 2", got)
	}
}
