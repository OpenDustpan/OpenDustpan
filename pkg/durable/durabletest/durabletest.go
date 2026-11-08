// Package durabletest is the conformance suite for durable.Store
// implementations. Every store adapter runs it:
//
//	func TestDurableConformance(t *testing.T) {
//		t.Parallel()
//		durabletest.Run(t, func(t *testing.T) durabletest.Harness { return newHarness(t) })
//	}
//
// Some cases wait for a short lease to expire on the clock of the store, so
// the suite takes a few seconds. See Harness for what an adapter must give.
package durabletest

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// ErrInjected is the error that the suite returns from an after hook of a
// Domain method, to make the method roll back its transaction.
var ErrInjected = errors.New("durabletest: injected failure")

// Harness is one fresh, empty database for one case.
type Harness struct {
	// Open returns a new store handle on the database of the harness. Two
	// handles act as two instances. Each case opens the handles it needs.
	// skew is the offset of the clock of the instance that holds the
	// handle. An adapter must take an injected clock for every time that it
	// reads in Go, and Open must give the handle a clock at time.Now plus
	// skew. Then the clock cases catch a decision that uses the clock of the
	// instance instead of the clock of the store. Only an adapter that reads
	// no time in Go can ignore skew.
	Open func(t *testing.T, skew time.Duration) durable.Store
	// Domain writes domain rows in the transactions of the store. The
	// adapter implements it with test tables or with real domain tables.
	Domain Domain
}

// Hooks are the calls that a Domain method makes inside its transaction.
// When a hook returns an error, the transaction rolls back and the method
// returns that error. A nil hook does nothing. The hooks never use the
// store.
type Hooks struct {
	Before func() error
	After  func() error
}

// Run calls h, or does nothing for a nil h.
func (Hooks) Run(h func() error) error {
	if h == nil {
		return nil
	}

	return h()
}

// Factory returns a fresh harness for one case.
type Factory func(t *testing.T) Harness

// Domain is the domain side of the cases where a domain write and the store
// of durable operations share one transaction. Each method is one
// transaction.
type Domain interface {
	// CreateNamespace creates a namespace and returns its fence.
	CreateNamespace(ctx context.Context, name namespace.Name) (namespace.Fence, error)
	// BumpFences increments the failover version of every namespace, as a
	// restore swap does.
	BumpFences(ctx context.Context) error
	// WriteStep checks ns and checks and records the step of f with the
	// rules of durable.Store.RecordStep, and stores value in a domain row.
	// It returns namespace.ErrFenced for a stale ns, and the errors of
	// RecordStep. Inside the transaction, it calls hooks.Before first,
	// before any check, and hooks.After last, after all its writes.
	WriteStep(ctx context.Context, f durable.StepFence, ns namespace.Fence, value string, hooks Hooks) error
	// StartWith stores value in a domain row and starts the operation of
	// req with the rules of durable.Store.Start. After both writes, inside
	// the transaction, it calls hooks.After.
	StartWith(ctx context.Context, req durable.StartRequest, value string, hooks Hooks) (durable.Operation, error)
	// Values returns the stored domain values, in any order.
	Values(ctx context.Context) ([]string, error)
}

const (
	// shortLease expires inside one case.
	shortLease = 300 * time.Millisecond
	// liveLease stays live for the few calls of a case that needs a live
	// claim, also on a busy database under -race.
	liveLease = 2 * time.Second
	// firstLease is the lease of a claim that a case extends and then waits
	// for the end of the first lease. It is long enough that a slow store
	// cannot end it before the Extend.
	firstLease = expiryWait
	// extendFirstLease is the lease of the first claim of the Extend case.
	// The case waits for the end of the first lease, so half of firstLease
	// keeps the case short, and it is long enough that a slow store cannot
	// end the claim before the Extend.
	extendFirstLease = firstLease / 2
	// instanceSkew is the clock offset of the instances in the clock case.
	instanceSkew = time.Hour
	// longLease does not expire inside one case.
	longLease = 5 * time.Minute
	// expiryWait is the longest time a case waits for a short lease or a
	// retry delay to pass on the clock of the store.
	expiryWait = 10 * time.Second
	// pollEvery is the interval of the wait.
	pollEvery = 25 * time.Millisecond
	// racers is the number of goroutines that race in the concurrent cases.
	racers = 8
	// clockSlack is the largest difference allowed between two times that
	// the store takes from one clock reading, for a store that keeps
	// milliseconds.
	clockSlack = 2 * time.Millisecond

	kindA  durable.Kind = "kind-a"
	kindB  durable.Kind = "kind-b"
	ownerA              = "instance-a"
	ownerB              = "instance-b"
)

// Run executes the conformance suite against harnesses from newHarness.
func Run(t *testing.T, newHarness Factory) {
	t.Helper()

	cases := []struct {
		name string
		run  func(t *testing.T, h Harness)
	}{
		{"start validates its request", testStartValidates},
		{"two starts with one key give one operation", testStartKeyOnce},
		{"concurrent starts with one key give one operation", testStartKeyRace},
		{"start records the version, and a second start keeps the first", testStartVersion},
		{"a start key of another kind conflicts", testStartKeyConflict},
		{"get of an unknown ID is ErrNotFound", testGetUnknown},
		{"claim validates its request", testClaimValidates},
		{"claim takes only the requested kinds", testClaimKinds},
		{"a claim skips a version above its limit", testClaimMaxVersions},
		{"concurrent claims of one operation give one claim", testClaimRace},
		{"a live claim blocks another claim", testLiveClaimBlocks},
		{"the expiry comes from the clock of the store", testExpiryFromStoreClock},
		{"an instance clock ahead or behind cannot move a claim", testInstanceClockSkew},
		{"another instance claims an expired operation with a new token", testTakeover},
		{"only a takeover of an expired claim counts an expiry", testExpiries},
		{"only Fail counts a failure", testFailures},
		{"an expired claim cannot write", testExpiredClaimCannotWrite},
		{"a claim that expires inside a step transaction cannot write", testExpiryInsideStepTx},
		{"a replaced claim cannot write", testReplacedClaimCannotWrite},
		{"extend moves the expiry and keeps the claim live", testExtend},
		{"extend to a shorter lease moves the expiry earlier", testExtendShorter},
		{"a release by a non-holder changes nothing", testReleaseByNonHolder},
		{"a release returns the operation to pending", testRelease},
		{"a give-back returns the operation to pending, and a non-holder changes nothing", testGiveBack},
		{"a give-back during a cancel is ErrCanceled and changes nothing", testGiveBackCanceled},
		{"a recorded step stays done after a takeover", testStepDoneAfterTakeover},
		{"complete ends the operation succeeded", testComplete},
		{"a final failure ends the operation failed with its error", testFinalFailure},
		{"a retry waits for its delay on the clock of the store", testRetryDelay},
		{"a cancel ends a pending operation", testCancelPending},
		{"a cancel stops a running operation at its next write", testCancelRunning},
		{"a canceled operation cannot succeed, and fail or release ends it", testCancelBlocksComplete},
		{"a claim ends an expired operation with a cancel", testCancelExpired},
		{"a claim with TakeCanceled takes an expired operation with a cancel", testTakeCanceled},
		{"a cancel of an ended operation is ErrFinished", testCancelFinished},
		{"a cancel before a step validates its request", testCancelBeforeValidates},
		{"a cancel before a step ends a pending operation", testCancelBeforePending},
		{"a cancel before a step stops a running operation and its step record", testCancelBeforeRunning},
		{"a cancel before a recorded step is ErrStepDone and changes nothing", testCancelBeforeRecorded},
		{"a cancel before a step and the record of the step have one winner", testCancelBeforeRace},
		{"status from another instance shows holder, expiry and attempt", testStatusFromOtherInstance},
		{"list filters and orders operations", testList},
		{"a start in a rolled-back domain write leaves no operation", testStartInDomainTx},
		{"a step write and its step record commit together", testStepWriteAtomic},
		{"a lost claim cannot write a domain row", testStepWriteLostClaim},
		{"a step write after a cancel changes nothing", testStepWriteCanceled},
		{"a step write from before a restore fails", testStepWriteAfterRestore},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.run(t, newHarness(t))
		})
	}
}

func testStartValidates(t *testing.T, h Harness) {
	t.Helper()

	s := h.Open(t, 0)

	for _, req := range []durable.StartRequest{
		{Key: "", Kind: kindA, Version: 0, Input: nil},
		{Key: "k", Kind: "", Version: 0, Input: nil},
		{Key: "k", Kind: kindA, Version: -1, Input: nil},
	} {
		if _, _, err := s.Start(t.Context(), req); !errors.Is(err, durable.ErrInvalid) {
			t.Errorf("Start(%+v) error = %v, want ErrInvalid", req, err)
		}
	}
}

func testStartKeyOnce(t *testing.T, h Harness) {
	t.Helper()

	a, b := h.Open(t, 0), h.Open(t, 0)

	first, created, err := a.Start(
		t.Context(),
		durable.StartRequest{Key: "k1", Kind: kindA, Version: 0, Input: []byte("one")},
	)
	if err != nil || !created {
		t.Fatalf("first Start = created %v, error %v; want a new operation", created, err)
	}

	checkNew(t, first, "k1", kindA, "one")

	second, created, err := b.Start(
		t.Context(),
		durable.StartRequest{Key: "k1", Kind: kindA, Version: 0, Input: []byte("two")},
	)
	if err != nil {
		t.Fatalf("second Start: %v", err)
	}

	if created || second.ID != first.ID || string(second.Input) != "one" {
		t.Fatalf("second Start = %+v, created %v; want the first operation, not created", second, created)
	}

	if ops := mustList(t, a, durable.Filter{Kind: "", States: nil, Limit: 0}); len(ops) != 1 {
		t.Fatalf("List after two starts with one key has %d operations, want 1", len(ops))
	}
}

func testStartKeyRace(t *testing.T, h Harness) {
	t.Helper()

	stores := []durable.Store{h.Open(t, 0), h.Open(t, 0)}

	ids := make([]durable.ID, racers)
	errs := make([]error, racers)
	created := make([]bool, racers)

	var wg sync.WaitGroup

	for i := range racers {
		wg.Go(func() {
			op, c, err := stores[i%len(stores)].Start(
				t.Context(),
				durable.StartRequest{Key: "race", Kind: kindA, Version: 0, Input: nil},
			)
			ids[i], created[i], errs[i] = op.ID, c, err
		})
	}

	wg.Wait()

	news := 0

	for i := range racers {
		if errs[i] != nil {
			t.Fatalf("Start %d: %v", i, errs[i])
		}

		if ids[i] != ids[0] {
			t.Fatalf("Start %d returned ID %q, Start 0 returned %q; want one ID", i, ids[i], ids[0])
		}

		if created[i] {
			news++
		}
	}

	if news != 1 {
		t.Fatalf("%d concurrent starts reported created, want exactly 1", news)
	}
}

func testStartVersion(t *testing.T, h Harness) {
	t.Helper()

	a, b := h.Open(t, 0), h.Open(t, 0)

	first, _, err := a.Start(t.Context(), durable.StartRequest{Key: "k", Kind: kindA, Version: 2, Input: nil})
	if err != nil || first.Version != 2 {
		t.Fatalf("Start with version 2 = %+v, %v; want version 2", first, err)
	}

	second, _, err := b.Start(t.Context(), durable.StartRequest{Key: "k", Kind: kindA, Version: 3, Input: nil})
	if err != nil || second.Version != 2 {
		t.Fatalf("second Start with version 3 = %+v, %v; want the first operation with version 2", second, err)
	}

	if got := mustGet(t, b, first.ID); got.Version != 2 {
		t.Fatalf("Get after two starts: version %d, want 2", got.Version)
	}
}

func testStartKeyConflict(t *testing.T, h Harness) {
	t.Helper()

	s := h.Open(t, 0)
	mustStart(t, s, "k", kindA)

	_, _, err := s.Start(t.Context(), durable.StartRequest{Key: "k", Kind: kindB, Version: 0, Input: nil})
	if !errors.Is(err, durable.ErrKeyConflict) {
		t.Fatalf("Start with a key of another kind: error = %v, want ErrKeyConflict", err)
	}
}

func testGetUnknown(t *testing.T, h Harness) {
	t.Helper()

	if _, err := h.Open(t, 0).Get(t.Context(), "no-such-operation"); !errors.Is(err, durable.ErrNotFound) {
		t.Fatalf("Get of an unknown ID: error = %v, want ErrNotFound", err)
	}
}

func testClaimValidates(t *testing.T, h Harness) {
	t.Helper()

	s := h.Open(t, 0)
	mustStart(t, s, "k", kindA)

	for _, req := range []durable.ClaimRequest{
		{Owner: "", Kinds: []durable.Kind{kindA}, MaxVersions: nil, Lease: longLease, TakeCanceled: false},
		{Owner: ownerA, Kinds: nil, MaxVersions: nil, Lease: longLease, TakeCanceled: false},
		{Owner: ownerA, Kinds: []durable.Kind{""}, MaxVersions: nil, Lease: longLease, TakeCanceled: false},
		{Owner: ownerA, Kinds: []durable.Kind{kindA}, MaxVersions: nil, Lease: 0, TakeCanceled: false},
		{Owner: ownerA, Kinds: []durable.Kind{kindA}, MaxVersions: map[durable.Kind]int{kindA: -1}, Lease: longLease, TakeCanceled: false},
	} {
		if _, err := s.Claim(t.Context(), req); !errors.Is(err, durable.ErrInvalid) {
			t.Errorf("Claim(%+v) error = %v, want ErrInvalid", req, err)
		}
	}
}

func testClaimKinds(t *testing.T, h Harness) {
	t.Helper()

	s := h.Open(t, 0)
	op := mustStart(t, s, "k", kindA)

	if _, err := s.Claim(t.Context(), claimReq(ownerA, longLease, kindB)); !errors.Is(err, durable.ErrNoWork) {
		t.Fatalf("Claim of kind-b with only a kind-a operation: error = %v, want ErrNoWork", err)
	}

	got, err := s.Claim(t.Context(), claimReq(ownerA, longLease, kindB, kindA))
	if err != nil {
		t.Fatalf("Claim of kind-b or kind-a: %v", err)
	}

	if got.ID != op.ID {
		t.Fatalf("Claim returned %q, want %q", got.ID, op.ID)
	}
}

func testClaimMaxVersions(t *testing.T, h Harness) {
	t.Helper()

	old, current := h.Open(t, 0), h.Open(t, 0)

	newer, _, err := current.Start(t.Context(), durable.StartRequest{Key: "k2", Kind: kindA, Version: 2, Input: nil})
	if err != nil {
		t.Fatalf("Start of version 2: %v", err)
	}

	pause(t, 2*clockSlack)

	older, _, err := current.Start(t.Context(), durable.StartRequest{Key: "k1", Kind: kindA, Version: 1, Input: nil})
	if err != nil {
		t.Fatalf("Start of version 1: %v", err)
	}

	limited := claimReq(ownerA, longLease, kindA, kindB)
	limited.MaxVersions = map[durable.Kind]int{kindA: 1}

	got, err := old.Claim(t.Context(), limited)
	if err != nil || got.ID != older.ID {
		t.Fatalf("Claim with version limit 1 = %+v, %v; want the operation of version 1, not the older one of 2",
			got, err)
	}

	_, err = old.Claim(t.Context(), limited)
	if !errors.Is(err, durable.ErrNoWork) {
		t.Fatalf("Claim with version limit 1 and only version 2 left: error = %v, want ErrNoWork", err)
	}

	// A kind with no entry in MaxVersions has no limit.
	other := claimReq(ownerB, longLease, kindA)
	other.MaxVersions = map[durable.Kind]int{kindB: 0}

	got, err = current.Claim(t.Context(), other)
	if err != nil || got.ID != newer.ID {
		t.Fatalf("Claim with no limit for kind-a = %+v, %v; want the operation of version 2", got, err)
	}
}

func testClaimRace(t *testing.T, h Harness) {
	t.Helper()

	stores := []durable.Store{h.Open(t, 0), h.Open(t, 0)}
	op := mustStart(t, stores[0], "k", kindA)

	results := make([]durable.Operation, racers)
	errs := make([]error, racers)

	var wg sync.WaitGroup

	for i := range racers {
		wg.Go(func() {
			results[i], errs[i] = stores[i%len(stores)].Claim(
				t.Context(), claimReq(fmt.Sprintf("racer-%d", i), longLease, kindA),
			)
		})
	}

	wg.Wait()

	winners := 0

	for i := range racers {
		switch {
		case errs[i] == nil:
			winners++

			if results[i].ID != op.ID {
				t.Errorf("racer %d claimed %q, want %q", i, results[i].ID, op.ID)
			}
		case errors.Is(errs[i], durable.ErrNoWork):
		default:
			t.Errorf("racer %d: error = %v, want nil or ErrNoWork", i, errs[i])
		}
	}

	if winners != 1 {
		t.Fatalf("%d of %d concurrent claims succeeded, want exactly 1", winners, racers)
	}

	if got := mustGet(t, stores[1], op.ID); got.Attempt != 1 {
		t.Fatalf("attempt after one claim = %d, want 1", got.Attempt)
	}
}

func testLiveClaimBlocks(t *testing.T, h Harness) {
	t.Helper()

	a, b := h.Open(t, 0), h.Open(t, 0)
	op := mustStart(t, a, "k", kindA)
	held := mustClaim(t, a, ownerA, longLease, kindA)

	if _, err := b.Claim(t.Context(), claimReq(ownerB, longLease, kindA)); !errors.Is(err, durable.ErrNoWork) {
		t.Fatalf("Claim of an operation with a live claim: error = %v, want ErrNoWork", err)
	}

	got := mustGet(t, b, op.ID)
	if !sameClaim(got.Claim, held.Claim) || got.State != durable.StateRunning || got.Attempt != 1 {
		t.Fatalf("operation after a blocked claim = %+v, want the claim of %s unchanged", got, ownerA)
	}
}

func testExpiryFromStoreClock(t *testing.T, h Harness) {
	t.Helper()

	s := h.Open(t, 0)
	mustStart(t, s, "k", kindA)
	held := mustClaim(t, s, ownerA, longLease, kindA)

	gap := held.Claim.ExpiresAt.Sub(held.UpdatedAt)
	if gap < longLease-clockSlack || gap > longLease+clockSlack {
		t.Fatalf("expiry %v minus claim time %v = %v, want the lease %v", held.Claim.ExpiresAt, held.UpdatedAt, gap,
			longLease)
	}
}

func testTakeover(t *testing.T, h Harness) {
	t.Helper()

	a, b := h.Open(t, 0), h.Open(t, 0)
	op := mustStart(t, a, "k", kindA)
	first := mustClaim(t, a, ownerA, shortLease, kindA)

	second := claimWhenExpired(t, b)
	if second.ID != op.ID {
		t.Fatalf("takeover claimed %q, want %q", second.ID, op.ID)
	}

	if second.Claim.Token <= first.Claim.Token || second.Claim.Owner != ownerB || second.Attempt != 2 {
		t.Fatalf("claim after takeover = %+v (attempt %d), want owner %s, a token above %d and attempt 2",
			second.Claim, second.Attempt, ownerB, first.Claim.Token)
	}
}

func testExpiries(t *testing.T, h Harness) {
	t.Helper()

	a, b := h.Open(t, 0), h.Open(t, 0)
	op := mustStart(t, a, "k", kindA)

	first := mustClaim(t, a, ownerA, longLease, kindA)
	if first.Expiries != 0 {
		t.Fatalf("expiries after a first claim = %d, want 0", first.Expiries)
	}

	if err := a.Release(t.Context(), first.Claim); err != nil {
		t.Fatalf("Release: %v", err)
	}

	again := mustClaim(t, b, ownerB, longLease, kindA)
	if again.Expiries != 0 {
		t.Fatalf("expiries after a claim that follows a release = %d, want 0", again.Expiries)
	}

	if err := b.Fail(t.Context(), again.Claim, durable.Failure{Message: "x", Final: false, RetryAfter: 0}); err != nil {
		t.Fatalf("Fail with a retry: %v", err)
	}

	short := mustClaim(t, a, ownerA, shortLease, kindA)
	if short.Expiries != 0 {
		t.Fatalf("expiries after a claim that follows a retry = %d, want 0", short.Expiries)
	}

	taken := claimWhenExpired(t, b)
	if taken.ID != op.ID || taken.Expiries != 1 || mustGet(t, a, op.ID).Expiries != 1 {
		t.Fatalf("expiries after a takeover of an expired claim = %d, want 1", taken.Expiries)
	}
}

func testFailures(t *testing.T, h Harness) {
	t.Helper()

	a, b := h.Open(t, 0), h.Open(t, 0)
	op := mustStart(t, a, "k", kindA)

	first := mustClaim(t, a, ownerA, longLease, kindA)
	if err := a.Release(t.Context(), first.Claim); err != nil {
		t.Fatalf("Release: %v", err)
	}

	second := mustClaim(t, b, ownerB, longLease, kindA)
	if second.Failures != 0 {
		t.Fatalf("failures after a release = %d, want 0", second.Failures)
	}

	retry := durable.Failure{Message: "x", Final: false, RetryAfter: 0}
	if err := b.Fail(t.Context(), second.Claim, retry); err != nil {
		t.Fatalf("Fail with a retry: %v", err)
	}

	short := mustClaim(t, a, ownerA, shortLease, kindA)
	if short.Failures != 1 {
		t.Fatalf("failures after one Fail = %d, want 1", short.Failures)
	}

	taken := claimWhenExpired(t, b)
	if taken.Failures != 1 {
		t.Fatalf("failures after a takeover = %d, want 1", taken.Failures)
	}

	if err := b.Fail(t.Context(), taken.Claim, durable.Failure{Message: "y", Final: true, RetryAfter: 0}); err != nil {
		t.Fatalf("final Fail: %v", err)
	}

	if got := mustGet(t, a, op.ID); got.Failures != 2 || got.State != durable.StateFailed {
		t.Fatalf("operation after a final Fail = %+v, want failed with 2 failures", got)
	}
}

func testExpiredClaimCannotWrite(t *testing.T, h Harness) {
	t.Helper()

	s := h.Open(t, 0)
	mustStart(t, s, "k", kindA)
	held := mustClaim(t, s, ownerA, shortLease, kindA)
	waitPast(t, s, held.ID, held.Claim.ExpiresAt)

	if _, err := s.Extend(t.Context(), held.Claim, longLease); !errors.Is(err, durable.ErrClaimLost) {
		t.Errorf("Extend of an expired claim: error = %v, want ErrClaimLost", err)
	}

	if err := s.RecordStep(t.Context(), held.Claim.Step("one"), nil); !errors.Is(err, durable.ErrClaimLost) {
		t.Errorf("RecordStep with an expired claim: error = %v, want ErrClaimLost", err)
	}

	if err := s.Complete(t.Context(), held.Claim); !errors.Is(err, durable.ErrClaimLost) {
		t.Errorf("Complete with an expired claim: error = %v, want ErrClaimLost", err)
	}

	err := s.Fail(t.Context(), held.Claim, durable.Failure{Message: "late", Final: true, RetryAfter: 0})
	if !errors.Is(err, durable.ErrClaimLost) {
		t.Errorf("Fail with an expired claim: error = %v, want ErrClaimLost", err)
	}

	if err := s.Release(t.Context(), held.Claim); !errors.Is(err, durable.ErrClaimLost) {
		t.Errorf("Release of an expired claim: error = %v, want ErrClaimLost", err)
	}

	got := mustGet(t, s, held.ID)
	if got.State != durable.StateRunning || len(got.Steps) != 0 || got.LastError != "" ||
		!sameClaim(got.Claim, held.Claim) {
		t.Fatalf("operation after writes of an expired claim = %+v, want it unchanged", got)
	}
}

func testReplacedClaimCannotWrite(t *testing.T, h Harness) {
	t.Helper()

	a, b := h.Open(t, 0), h.Open(t, 0)
	mustStart(t, a, "k", kindA)
	old := mustClaim(t, a, ownerA, shortLease, kindA)
	live := claimWhenExpired(t, b)

	if _, err := a.Extend(t.Context(), old.Claim, longLease); !errors.Is(err, durable.ErrClaimLost) {
		t.Errorf("Extend of a replaced claim: error = %v, want ErrClaimLost", err)
	}

	if err := a.RecordStep(t.Context(), old.Claim.Step("one"), nil); !errors.Is(err, durable.ErrClaimLost) {
		t.Errorf("RecordStep with a replaced claim: error = %v, want ErrClaimLost", err)
	}

	if err := a.Complete(t.Context(), old.Claim); !errors.Is(err, durable.ErrClaimLost) {
		t.Errorf("Complete with a replaced claim: error = %v, want ErrClaimLost", err)
	}

	err := a.Fail(t.Context(), old.Claim, durable.Failure{Message: "late", Final: true, RetryAfter: 0})
	if !errors.Is(err, durable.ErrClaimLost) {
		t.Errorf("Fail with a replaced claim: error = %v, want ErrClaimLost", err)
	}

	got := mustGet(t, b, live.ID)
	if !sameClaim(got.Claim, live.Claim) || got.State != durable.StateRunning || len(got.Steps) != 0 ||
		got.LastError != "" {
		t.Fatalf("operation after writes of a replaced claim = %+v, want the live claim of %s unchanged", got, ownerB)
	}
}

func testExtend(t *testing.T, h Harness) {
	t.Helper()

	a, b := h.Open(t, 0), h.Open(t, 0)
	mustStart(t, a, "k", kindA)
	held := mustClaim(t, a, ownerA, extendFirstLease, kindA)

	extended, err := a.Extend(t.Context(), held.Claim, longLease)
	if err != nil {
		t.Fatalf("Extend of a live claim: %v", err)
	}

	if extended.Token != held.Claim.Token || extended.Owner != ownerA ||
		!extended.ExpiresAt.After(held.Claim.ExpiresAt) {
		t.Fatalf("Extend returned %+v, want the same token and owner with a later expiry than %v", extended,
			held.Claim.ExpiresAt)
	}

	pause(t, time.Until(held.Claim.ExpiresAt)+2*pollEvery)

	if _, err := b.Claim(t.Context(), claimReq(ownerB, longLease, kindA)); !errors.Is(err, durable.ErrNoWork) {
		t.Fatalf("Claim after the first lease, before the extended expiry: error = %v, want ErrNoWork", err)
	}

	if _, err := a.Extend(t.Context(), extended, 0); !errors.Is(err, durable.ErrInvalid) {
		t.Fatalf("Extend with a lease of 0: error = %v, want ErrInvalid", err)
	}
}

// testExtendShorter proves that Extend sets the expiry to now plus the lease,
// also when that is earlier than the expiry of the claim. The job and audit
// suites shorten a long lease with Extend to end a claim fast.
func testExtendShorter(t *testing.T, h Harness) {
	t.Helper()

	a := h.Open(t, 0)
	mustStart(t, a, "k", kindA)
	held := mustClaim(t, a, ownerA, longLease, kindA)

	shorter, err := a.Extend(t.Context(), held.Claim, liveLease)
	if err != nil {
		t.Fatalf("Extend to a shorter lease: %v", err)
	}

	if shorter.Token != held.Claim.Token || shorter.Owner != ownerA {
		t.Fatalf("Extend returned %+v, want the same token and owner as %+v", shorter, held.Claim)
	}

	if !shorter.ExpiresAt.Before(held.Claim.ExpiresAt) {
		t.Fatalf("Extend to %v did not move the expiry earlier: %v after %v", liveLease, shorter.ExpiresAt,
			held.Claim.ExpiresAt)
	}

	if got := mustGet(t, a, held.ID); !sameClaim(got.Claim, shorter) {
		t.Fatalf("operation after Extend to a shorter lease = %+v, want the claim %+v", got.Claim, shorter)
	}
}

func testReleaseByNonHolder(t *testing.T, h Harness) {
	t.Helper()

	a, b := h.Open(t, 0), h.Open(t, 0)
	mustStart(t, a, "k", kindA)
	held := mustClaim(t, a, ownerA, longLease, kindA)

	forged := held.Claim
	forged.Token++
	forged.Owner = ownerB

	if err := b.Release(t.Context(), forged); !errors.Is(err, durable.ErrClaimLost) {
		t.Fatalf("Release with a token that is not live: error = %v, want ErrClaimLost", err)
	}

	if got := mustGet(t, b, held.ID); !sameClaim(got.Claim, held.Claim) || got.State != durable.StateRunning {
		t.Fatalf("operation after a release by a non-holder = %+v, want the claim of %s unchanged", got, ownerA)
	}
}

func testRelease(t *testing.T, h Harness) {
	t.Helper()

	a, b := h.Open(t, 0), h.Open(t, 0)
	mustStart(t, a, "k", kindA)
	held := mustClaim(t, a, ownerA, longLease, kindA)

	if err := a.Release(t.Context(), held.Claim); err != nil {
		t.Fatalf("Release by the holder: %v", err)
	}

	got := mustGet(t, b, held.ID)
	if got.State != durable.StatePending || !noClaim(got.Claim) || got.Attempt != 1 {
		t.Fatalf("operation after a release = %+v, want pending, no claim, attempt 1", got)
	}

	again := mustClaim(t, b, ownerB, longLease, kindA)
	if again.Claim.Token <= held.Claim.Token || again.Attempt != 2 {
		t.Fatalf("claim after a release = %+v (attempt %d), want a token above %d and attempt 2", again.Claim,
			again.Attempt, held.Claim.Token)
	}
}

func testGiveBack(t *testing.T, h Harness) {
	t.Helper()

	a, b := h.Open(t, 0), h.Open(t, 0)
	mustStart(t, a, "k", kindA)
	held := mustClaim(t, a, ownerA, longLease, kindA)

	forged := held.Claim
	forged.Token++
	forged.Owner = ownerB

	if err := b.GiveBack(t.Context(), forged); !errors.Is(err, durable.ErrClaimLost) {
		t.Fatalf("GiveBack with a token that is not live: error = %v, want ErrClaimLost", err)
	}

	mustBeUnchanged(t, b, held, "a give-back by a non-holder")

	if err := a.GiveBack(t.Context(), held.Claim); err != nil {
		t.Fatalf("GiveBack by the holder: %v", err)
	}

	got := mustGet(t, b, held.ID)
	if got.State != durable.StatePending || !noClaim(got.Claim) || got.Attempt != 1 || got.Failures != 0 {
		t.Fatalf("operation after a give-back = %+v, want pending, no claim, attempt 1, no failure", got)
	}

	if err := a.GiveBack(t.Context(), held.Claim); !errors.Is(err, durable.ErrClaimLost) {
		t.Fatalf("second GiveBack of one claim: error = %v, want ErrClaimLost", err)
	}

	again := mustClaim(t, b, ownerB, longLease, kindA)
	if again.Claim.Token <= held.Claim.Token || again.Attempt != 2 {
		t.Fatalf("claim after a give-back = %+v (attempt %d), want a token above %d and attempt 2", again.Claim,
			again.Attempt, held.Claim.Token)
	}
}

// testGiveBackCanceled checks that GiveBack refuses an operation with a
// requested cancel and leaves it to the claim after the expiry, and that a
// non-holder gets ErrClaimLost before ErrCanceled.
func testGiveBackCanceled(t *testing.T, h Harness) {
	t.Helper()

	a, b := h.Open(t, 0), h.Open(t, 0)
	mustStart(t, a, "k", kindA)
	held := mustClaim(t, a, ownerA, longLease, kindA)

	if err := b.Cancel(t.Context(), held.ID); err != nil {
		t.Fatalf("Cancel of a running operation: %v", err)
	}

	forged := held.Claim
	forged.Token++

	if err := b.GiveBack(t.Context(), forged); !errors.Is(err, durable.ErrClaimLost) {
		t.Fatalf("GiveBack with a token that is not live during a cancel: error = %v, want ErrClaimLost", err)
	}

	if err := a.GiveBack(t.Context(), held.Claim); !errors.Is(err, durable.ErrCanceled) {
		t.Fatalf("GiveBack after Cancel: error = %v, want ErrCanceled", err)
	}

	got := mustGet(t, b, held.ID)
	if got.State != durable.StateRunning || !got.CancelRequested || !sameClaim(got.Claim, held.Claim) ||
		got.Failures != 0 {
		t.Fatalf("operation after a refused give-back = %+v, want running with its claim and the cancel", got)
	}

	if err := a.Release(t.Context(), held.Claim); err != nil {
		t.Fatalf("Release after a refused give-back: %v", err)
	}

	if got := mustGet(t, b, held.ID); got.State != durable.StateCanceled {
		t.Fatalf("state after the release = %s, want canceled", got.State)
	}
}

func testStepDoneAfterTakeover(t *testing.T, h Harness) {
	t.Helper()

	a, b := h.Open(t, 0), h.Open(t, 0)
	mustStart(t, a, "k", kindA)
	first := mustClaim(t, a, ownerA, longLease, kindA)

	if err := a.RecordStep(t.Context(), first.Claim.Step("one"), []byte("result-one")); err != nil {
		t.Fatalf("RecordStep by the holder: %v", err)
	}

	err := a.RecordStep(t.Context(), first.Claim.Step("one"), []byte("again"))
	if !errors.Is(err, durable.ErrStepDone) {
		t.Fatalf("second RecordStep of one step: error = %v, want ErrStepDone", err)
	}

	shorten(t, a, first.Claim)

	second := claimWhenExpired(t, b)

	rec, ok := second.Step("one")
	if !ok || string(rec.Result) != "result-one" || len(second.Steps) != 1 {
		t.Fatalf("steps after a takeover = %+v, want step one with result-one", second.Steps)
	}

	err = b.RecordStep(t.Context(), second.Claim.Step("one"), []byte("new"))
	if !errors.Is(err, durable.ErrStepDone) {
		t.Fatalf("RecordStep of a done step by the new holder: error = %v, want ErrStepDone", err)
	}

	// The claim check comes before the step check.
	err = a.RecordStep(t.Context(), first.Claim.Step("one"), []byte("stale"))
	if !errors.Is(err, durable.ErrClaimLost) {
		t.Fatalf("RecordStep of a done step by the replaced holder: error = %v, want ErrClaimLost", err)
	}

	if err := b.RecordStep(t.Context(), second.Claim.Step("two"), []byte("result-two")); err != nil {
		t.Fatalf("RecordStep of the next step by the new holder: %v", err)
	}

	got := mustGet(t, a, second.ID)
	if names := stepNames(got); !slices.Equal(names, []string{"one", "two"}) {
		t.Fatalf("steps = %v, want [one two] in record order", names)
	}
}

func testComplete(t *testing.T, h Harness) {
	t.Helper()

	a, b := h.Open(t, 0), h.Open(t, 0)
	mustStart(t, a, "k", kindA)
	held := mustClaim(t, a, ownerA, longLease, kindA)

	if err := a.Complete(t.Context(), held.Claim); err != nil {
		t.Fatalf("Complete by the holder: %v", err)
	}

	got := mustGet(t, b, held.ID)
	if got.State != durable.StateSucceeded || !noClaim(got.Claim) {
		t.Fatalf("operation after Complete = %+v, want succeeded with no claim", got)
	}

	if err := a.Complete(t.Context(), held.Claim); !errors.Is(err, durable.ErrClaimLost) {
		t.Fatalf("second Complete: error = %v, want ErrClaimLost", err)
	}

	if _, err := b.Claim(t.Context(), claimReq(ownerB, longLease, kindA)); !errors.Is(err, durable.ErrNoWork) {
		t.Fatalf("Claim after success: error = %v, want ErrNoWork", err)
	}
}

func testFinalFailure(t *testing.T, h Harness) {
	t.Helper()

	a, b := h.Open(t, 0), h.Open(t, 0)
	mustStart(t, a, "k", kindA)
	held := mustClaim(t, a, ownerA, longLease, kindA)

	err := a.Fail(t.Context(), held.Claim, durable.Failure{Message: "disk full", Final: true, RetryAfter: 0})
	if err != nil {
		t.Fatalf("final Fail by the holder: %v", err)
	}

	got := mustGet(t, b, held.ID)
	if got.State != durable.StateFailed || got.LastError != "disk full" || !noClaim(got.Claim) ||
		got.Attempt != 1 {
		t.Fatalf("operation after a final failure = %+v, want failed with last error %q", got, "disk full")
	}

	if _, err := b.Claim(t.Context(), claimReq(ownerB, longLease, kindA)); !errors.Is(err, durable.ErrNoWork) {
		t.Fatalf("Claim after a final failure: error = %v, want ErrNoWork", err)
	}
}

func testRetryDelay(t *testing.T, h Harness) {
	t.Helper()

	a, b := h.Open(t, 0), h.Open(t, 0)
	mustStart(t, a, "k", kindA)
	held := mustClaim(t, a, ownerA, longLease, kindA)

	err := a.Fail(t.Context(), held.Claim, durable.Failure{Message: "timeout", Final: false, RetryAfter: liveLease})
	if err != nil {
		t.Fatalf("Fail with a retry: %v", err)
	}

	got := mustGet(t, b, held.ID)
	if got.State != durable.StatePending || got.LastError != "timeout" || !noClaim(got.Claim) {
		t.Fatalf("operation after a retry = %+v, want pending with last error %q", got, "timeout")
	}

	if gap := got.NotBefore.Sub(got.UpdatedAt); gap < liveLease-clockSlack || gap > liveLease+clockSlack {
		t.Fatalf("NotBefore %v minus update time %v = %v, want the retry delay %v", got.NotBefore, got.UpdatedAt,
			gap, liveLease)
	}

	if _, err := b.Claim(t.Context(), claimReq(ownerB, longLease, kindA)); !errors.Is(err, durable.ErrNoWork) {
		t.Fatalf("Claim before the retry delay: error = %v, want ErrNoWork", err)
	}

	again := claimWhenExpired(t, b)
	if again.Attempt != 2 || again.LastError != "timeout" {
		t.Fatalf("claim after the retry delay = %+v, want attempt 2 and the last error kept", again)
	}
}

func testCancelPending(t *testing.T, h Harness) {
	t.Helper()

	a, b := h.Open(t, 0), h.Open(t, 0)
	op := mustStart(t, a, "k", kindA)

	if err := b.Cancel(t.Context(), op.ID); err != nil {
		t.Fatalf("Cancel of a pending operation: %v", err)
	}

	if got := mustGet(t, a, op.ID); got.State != durable.StateCanceled {
		t.Fatalf("state after Cancel of a pending operation = %s, want canceled", got.State)
	}

	if err := b.Cancel(t.Context(), op.ID); err != nil {
		t.Fatalf("second Cancel: %v, want nil", err)
	}

	if _, err := a.Claim(t.Context(), claimReq(ownerA, longLease, kindA)); !errors.Is(err, durable.ErrNoWork) {
		t.Fatalf("Claim after Cancel: error = %v, want ErrNoWork", err)
	}

	if err := b.Cancel(t.Context(), "no-such-operation"); !errors.Is(err, durable.ErrNotFound) {
		t.Fatalf("Cancel of an unknown ID: error = %v, want ErrNotFound", err)
	}
}

func testCancelRunning(t *testing.T, h Harness) {
	t.Helper()

	a, b := h.Open(t, 0), h.Open(t, 0)
	mustStart(t, a, "k", kindA)
	held := mustClaim(t, a, ownerA, longLease, kindA)

	if err := b.Cancel(t.Context(), held.ID); err != nil {
		t.Fatalf("Cancel of a running operation: %v", err)
	}

	got := mustGet(t, b, held.ID)
	if got.State != durable.StateRunning || !got.CancelRequested {
		t.Fatalf("operation after Cancel of a running operation = %+v, want running with the cancel requested", got)
	}

	if _, err := a.Extend(t.Context(), held.Claim, longLease); !errors.Is(err, durable.ErrCanceled) {
		t.Fatalf("Extend after Cancel: error = %v, want ErrCanceled", err)
	}

	if err := a.RecordStep(t.Context(), held.Claim.Step("one"), nil); !errors.Is(err, durable.ErrCanceled) {
		t.Fatalf("RecordStep after Cancel: error = %v, want ErrCanceled", err)
	}

	mustBeUnchanged(t, b, held, "holder writes that a cancel refused")

	if err := a.Release(t.Context(), held.Claim); err != nil {
		t.Fatalf("Release after Cancel: %v", err)
	}

	got = mustGet(t, b, held.ID)
	if got.State != durable.StateCanceled || len(got.Steps) != 0 || !noClaim(got.Claim) {
		t.Fatalf("operation after the release = %+v, want canceled, no step, no claim", got)
	}
}

func testCancelBlocksComplete(t *testing.T, h Harness) {
	t.Helper()

	for _, end := range []string{"complete", "fail", "release"} {
		t.Run(end, func(t *testing.T) {
			t.Parallel()

			s := h.Open(t, 0)
			kind := durable.Kind("kind-" + end)
			mustStart(t, s, "k-"+end, kind)
			held := mustClaim(t, s, ownerA, longLease, kind)

			if err := s.Cancel(t.Context(), held.ID); err != nil {
				t.Fatalf("Cancel: %v", err)
			}

			var err error

			want, wantState := error(nil), durable.StateCanceled

			switch end {
			case "complete":
				err = s.Complete(t.Context(), held.Claim)
				want, wantState = durable.ErrCanceled, durable.StateRunning
			case "fail":
				err = s.Fail(t.Context(), held.Claim, durable.Failure{Message: "x", Final: false, RetryAfter: 0})
			default:
				err = s.Release(t.Context(), held.Claim)
			}

			if !errors.Is(err, want) {
				t.Fatalf("%s after Cancel: error = %v, want %v", end, err, want)
			}

			if got := mustGet(t, s, held.ID); got.State != wantState {
				t.Fatalf("state after %s of a canceled operation = %s, want %s", end, got.State, wantState)
			}
		})
	}
}

func testCancelExpired(t *testing.T, h Harness) {
	t.Helper()

	a, b := h.Open(t, 0), h.Open(t, 0)
	op := mustStart(t, a, "k", kindA)
	held := mustClaim(t, a, ownerA, liveLease, kindA)

	if err := b.Cancel(t.Context(), op.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	waitPast(t, b, op.ID, held.Claim.ExpiresAt)

	if _, err := b.Claim(t.Context(), claimReq(ownerB, longLease, kindA)); !errors.Is(err, durable.ErrNoWork) {
		t.Fatalf("Claim of an expired operation with a cancel: error = %v, want ErrNoWork", err)
	}

	if got := mustGet(t, b, op.ID); got.State != durable.StateCanceled {
		t.Fatalf("state after the claim found the cancel = %s, want canceled", got.State)
	}
}

func testTakeCanceled(t *testing.T, h Harness) {
	t.Helper()

	a, b := h.Open(t, 0), h.Open(t, 0)
	op := mustStart(t, a, "k", kindA)
	held := mustClaim(t, a, ownerA, liveLease, kindA)

	if err := b.Cancel(t.Context(), op.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	// While the claim of the holder lives, nobody takes the operation.
	req := claimReq(ownerB, longLease, kindA)
	req.TakeCanceled = true

	if _, err := b.Claim(t.Context(), req); !errors.Is(err, durable.ErrNoWork) {
		t.Fatalf("Claim with TakeCanceled of a live operation with a cancel: error = %v, want ErrNoWork", err)
	}

	waitPast(t, b, op.ID, held.Claim.ExpiresAt)

	taken, err := b.Claim(t.Context(), req)
	if err != nil {
		t.Fatalf("Claim with TakeCanceled of an expired operation with a cancel: error = %v, want the operation", err)
	}

	if taken.ID != op.ID || taken.State != durable.StateRunning || !taken.CancelRequested ||
		taken.Claim.Owner != ownerB || taken.Claim.Token != held.Claim.Token+1 || taken.Expiries != 1 {
		t.Fatalf("operation taken = %s, owner %q, token %d, cancel %v, expiries %d; want running for %s, token %d, "+
			"cancel requested, 1 expiry", taken.State, taken.Claim.Owner, taken.Claim.Token, taken.CancelRequested,
			taken.Expiries, ownerB, held.Claim.Token+1)
	}

	endTaken(t, a, b, &held, &taken)
}

// endTaken checks that the replaced holder lost its claim of a taken
// operation, and that the new holder cannot extend it, and that its release
// ends the operation canceled.
func endTaken(t *testing.T, a, b durable.Store, held, taken *durable.Operation) {
	t.Helper()

	if err := a.Release(t.Context(), held.Claim); !errors.Is(err, durable.ErrClaimLost) {
		t.Fatalf("Release by the replaced holder: error = %v, want ErrClaimLost", err)
	}

	if _, err := b.Extend(t.Context(), taken.Claim, longLease); !errors.Is(err, durable.ErrCanceled) {
		t.Fatalf("Extend of a claim with a cancel: error = %v, want ErrCanceled", err)
	}

	if err := b.Release(t.Context(), taken.Claim); err != nil {
		t.Fatalf("Release of the taken operation: %v", err)
	}

	if got := mustGet(t, b, taken.ID); got.State != durable.StateCanceled {
		t.Fatalf("state after the release of the taken operation = %s, want canceled", got.State)
	}
}

func testCancelFinished(t *testing.T, h Harness) {
	t.Helper()

	s := h.Open(t, 0)
	mustStart(t, s, "k", kindA)
	held := mustClaim(t, s, ownerA, longLease, kindA)

	if err := s.Complete(t.Context(), held.Claim); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	if err := s.Cancel(t.Context(), held.ID); !errors.Is(err, durable.ErrFinished) {
		t.Fatalf("Cancel of a succeeded operation: error = %v, want ErrFinished", err)
	}

	if got := mustGet(t, s, held.ID); got.State != durable.StateSucceeded {
		t.Fatalf("state after Cancel of a succeeded operation = %s, want succeeded", got.State)
	}
}

func testStatusFromOtherInstance(t *testing.T, h Harness) {
	t.Helper()

	a, b := h.Open(t, 0), h.Open(t, 0)
	mustStart(t, a, "k", kindA)
	held := mustClaim(t, a, ownerA, longLease, kindA)

	got := mustGet(t, b, held.ID)
	if !sameClaim(got.Claim, held.Claim) || got.Attempt != 1 || got.State != durable.StateRunning {
		t.Fatalf("status from another instance = %+v, want holder %s, token %d, expiry %v, attempt 1", got, ownerA,
			held.Claim.Token, held.Claim.ExpiresAt)
	}

	listed := mustList(t, b, durable.Filter{Kind: kindA, States: []durable.State{durable.StateRunning}, Limit: 0})
	if len(listed) != 1 || !sameClaim(listed[0].Claim, got.Claim) {
		t.Fatalf("List from another instance = %+v, want the one running operation with its claim", listed)
	}
}

func testList(t *testing.T, h Harness) {
	t.Helper()

	s := h.Open(t, 0)

	// The pauses give each operation its own creation time on a store that
	// keeps milliseconds, so the order does not depend on the IDs.
	first := mustStart(t, s, "k1", kindA)
	pause(t, 2*clockSlack)
	second := mustStart(t, s, "k2", kindB)
	pause(t, 2*clockSlack)
	third := mustStart(t, s, "k3", kindA)

	if err := s.Cancel(t.Context(), third.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	all := mustList(t, s, durable.Filter{Kind: "", States: nil, Limit: 0})
	if ids := opIDs(all); !slices.Equal(ids, []durable.ID{first.ID, second.ID, third.ID}) {
		t.Fatalf("List of all = %v, want [%s %s %s] oldest first", ids, first.ID, second.ID, third.ID)
	}

	kindAOps := mustList(t, s, durable.Filter{Kind: kindA, States: nil, Limit: 0})
	if ids := opIDs(kindAOps); !slices.Equal(ids, []durable.ID{first.ID, third.ID}) {
		t.Fatalf("List of kind-a = %v, want [%s %s]", ids, first.ID, third.ID)
	}

	pendingA := mustList(t, s, durable.Filter{Kind: kindA, States: []durable.State{durable.StatePending}, Limit: 0})
	if ids := opIDs(pendingA); !slices.Equal(ids, []durable.ID{first.ID}) {
		t.Fatalf("List of pending kind-a = %v, want [%s]", ids, first.ID)
	}

	limited := mustList(t, s, durable.Filter{Kind: "", States: nil, Limit: 2})
	if ids := opIDs(limited); !slices.Equal(ids, []durable.ID{first.ID, second.ID}) {
		t.Fatalf("List with limit 2 = %v, want [%s %s]", ids, first.ID, second.ID)
	}
}

func testStartInDomainTx(t *testing.T, h Harness) {
	t.Helper()

	s := h.Open(t, 0)
	req := durable.StartRequest{Key: "k", Kind: kindA, Version: 0, Input: nil}

	_, err := h.Domain.StartWith(t.Context(), req, "rolled-back", Hooks{Before: nil, After: failAfter})
	if !errors.Is(err, ErrInjected) {
		t.Fatalf("StartWith that fails: error = %v, want ErrInjected", err)
	}

	if ops := mustList(t, s, durable.Filter{Kind: "", States: nil, Limit: 0}); len(ops) != 0 {
		t.Fatalf("operations after a rolled-back start = %+v, want none", ops)
	}

	if vals := mustValues(t, h.Domain); len(vals) != 0 {
		t.Fatalf("domain values after a rolled-back start = %v, want none", vals)
	}

	op, err := h.Domain.StartWith(t.Context(), req, "kept", Hooks{Before: nil, After: nil})
	if err != nil {
		t.Fatalf("StartWith: %v", err)
	}

	if got := mustGet(t, s, op.ID); got.Key != "k" || got.State != durable.StatePending {
		t.Fatalf("operation after a committed start = %+v, want pending with key k", got)
	}

	if vals := mustValues(t, h.Domain); !slices.Equal(vals, []string{"kept"}) {
		t.Fatalf("domain values after a committed start = %v, want [kept]", vals)
	}
}

func testStepWriteAtomic(t *testing.T, h Harness) {
	t.Helper()

	s := h.Open(t, 0)
	ns := mustNamespace(t, h.Domain, "ns-atomic")
	mustStart(t, s, "k", kindA)
	held := mustClaim(t, s, ownerA, longLease, kindA)

	err := h.Domain.WriteStep(
		t.Context(),
		held.Claim.Step("one"),
		ns,
		"rolled-back",
		Hooks{Before: nil, After: failAfter},
	)
	if !errors.Is(err, ErrInjected) {
		t.Fatalf("WriteStep that fails: error = %v, want ErrInjected", err)
	}

	if got := mustGet(t, s, held.ID); len(got.Steps) != 0 {
		t.Fatalf("steps after a rolled-back step write = %+v, want none", got.Steps)
	}

	if vals := mustValues(t, h.Domain); len(vals) != 0 {
		t.Fatalf("domain values after a rolled-back step write = %v, want none", vals)
	}

	err = h.Domain.WriteStep(t.Context(), held.Claim.Step("one"), ns, "kept", Hooks{Before: nil, After: nil})
	if err != nil {
		t.Fatalf("WriteStep: %v", err)
	}

	if _, ok := mustGet(t, s, held.ID).Step("one"); !ok {
		t.Fatal("step one is not recorded after a committed step write")
	}

	err = h.Domain.WriteStep(t.Context(), held.Claim.Step("one"), ns, "twice", Hooks{Before: nil, After: nil})
	if !errors.Is(err, durable.ErrStepDone) {
		t.Fatalf("second WriteStep of one step: error = %v, want ErrStepDone", err)
	}

	if vals := mustValues(t, h.Domain); !slices.Equal(vals, []string{"kept"}) {
		t.Fatalf("domain values = %v, want [kept]: a done step must not write again", vals)
	}
}

func testStepWriteLostClaim(t *testing.T, h Harness) {
	t.Helper()

	a, b := h.Open(t, 0), h.Open(t, 0)
	ns := mustNamespace(t, h.Domain, "ns-lost")
	mustStart(t, a, "k", kindA)
	old := mustClaim(t, a, ownerA, shortLease, kindA)
	live := claimWhenExpired(t, b)

	err := h.Domain.WriteStep(t.Context(), old.Claim.Step("one"), ns, "late", Hooks{Before: nil, After: nil})
	if !errors.Is(err, durable.ErrClaimLost) {
		t.Fatalf("WriteStep with a replaced claim: error = %v, want ErrClaimLost", err)
	}

	if vals := mustValues(t, h.Domain); len(vals) != 0 {
		t.Fatalf("domain values after a write of a lost claim = %v, want none", vals)
	}

	if got := mustGet(t, b, live.ID); len(got.Steps) != 0 {
		t.Fatalf("steps after a write of a lost claim = %+v, want none", got.Steps)
	}
}

func testStepWriteAfterRestore(t *testing.T, h Harness) {
	t.Helper()

	s := h.Open(t, 0)
	ns := mustNamespace(t, h.Domain, "ns-restore")
	mustStart(t, s, "k", kindA)
	held := mustClaim(t, s, ownerA, longLease, kindA)

	if err := h.Domain.BumpFences(t.Context()); err != nil {
		t.Fatalf("BumpFences: %v", err)
	}

	err := h.Domain.WriteStep(t.Context(), held.Claim.Step("one"), ns, "late", Hooks{Before: nil, After: nil})
	if !errors.Is(err, namespace.ErrFenced) {
		t.Fatalf("WriteStep with a fence from before the restore: error = %v, want namespace.ErrFenced", err)
	}

	if got := mustGet(t, s, held.ID); len(got.Steps) != 0 {
		t.Fatalf("steps after a fenced step write = %+v, want none", got.Steps)
	}

	if vals := mustValues(t, h.Domain); len(vals) != 0 {
		t.Fatalf("domain values after a fenced step write = %v, want none", vals)
	}
}

func testInstanceClockSkew(t *testing.T, h Harness) {
	t.Helper()

	ahead, behind, plain := h.Open(t, instanceSkew), h.Open(t, -instanceSkew), h.Open(t, 0)

	// An instance behind by one hour holds a claim.
	mustStart(t, behind, "k1", kindA)
	held := mustClaim(t, behind, ownerB, liveLease, kindA)

	if gap := held.Claim.ExpiresAt.Sub(held.UpdatedAt); gap < liveLease-clockSlack || gap > liveLease+clockSlack {
		t.Fatalf("expiry minus claim time from an instance behind = %v, want the lease %v", gap, liveLease)
	}

	// An instance ahead by one hour cannot take it over early.
	if _, err := ahead.Claim(t.Context(), claimReq(ownerA, longLease, kindA)); !errors.Is(err, durable.ErrNoWork) {
		t.Fatalf("Claim of a live claim from an instance one hour ahead: error = %v, want ErrNoWork", err)
	}

	// The instance behind cannot keep it after the expiry on the store clock.
	waitPast(t, plain, held.ID, held.Claim.ExpiresAt)

	if _, err := behind.Extend(t.Context(), held.Claim, longLease); !errors.Is(err, durable.ErrClaimLost) {
		t.Fatalf("Extend of an expired claim from an instance one hour behind: error = %v, want ErrClaimLost", err)
	}

	err := behind.RecordStep(t.Context(), held.Claim.Step("one"), nil)
	if !errors.Is(err, durable.ErrClaimLost) {
		t.Fatalf("RecordStep of an expired claim from an instance one hour behind: error = %v, want ErrClaimLost", err)
	}

	// A retry delay that the instance behind sets is not one hour short.
	again := mustClaim(t, behind, ownerB, longLease, kindA)

	err = behind.Fail(t.Context(), again.Claim, durable.Failure{Message: "x", Final: false, RetryAfter: liveLease})
	if err != nil {
		t.Fatalf("Fail with a retry from an instance one hour behind: %v", err)
	}

	if _, err := plain.Claim(t.Context(), claimReq(ownerA, longLease, kindA)); !errors.Is(err, durable.ErrNoWork) {
		t.Fatalf("Claim before a retry delay that an instance one hour behind set: error = %v, want ErrNoWork", err)
	}
}

func testExpiryInsideStepTx(t *testing.T, h Harness) {
	t.Helper()

	s := h.Open(t, 0)
	ns := mustNamespace(t, h.Domain, "ns-long-tx")
	mustStart(t, s, "k", kindA)
	held := mustClaim(t, s, ownerA, shortLease, kindA)

	// The pause inside the transaction outlasts the lease. A store that
	// takes the time of the check from the start of the transaction sees
	// the claim as live.
	outlast := func() error {
		pause(t, shortLease+4*pollEvery)

		return nil
	}

	err := h.Domain.WriteStep(t.Context(), held.Claim.Step("one"), ns, "late", Hooks{Before: outlast, After: nil})
	if !errors.Is(err, durable.ErrClaimLost) {
		t.Fatalf("WriteStep whose claim expired inside the transaction: error = %v, want ErrClaimLost", err)
	}

	if vals := mustValues(t, h.Domain); len(vals) != 0 {
		t.Fatalf("domain values after a write of an expired claim = %v, want none", vals)
	}

	if got := mustGet(t, s, held.ID); len(got.Steps) != 0 {
		t.Fatalf("steps after a write of an expired claim = %+v, want none", got.Steps)
	}
}

func testStepWriteCanceled(t *testing.T, h Harness) {
	t.Helper()

	s := h.Open(t, 0)
	ns := mustNamespace(t, h.Domain, "ns-cancel")
	mustStart(t, s, "k", kindA)
	held := mustClaim(t, s, ownerA, longLease, kindA)

	if err := s.Cancel(t.Context(), held.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	err := h.Domain.WriteStep(t.Context(), held.Claim.Step("one"), ns, "after-cancel", Hooks{Before: nil, After: nil})
	if !errors.Is(err, durable.ErrCanceled) {
		t.Fatalf("WriteStep after Cancel: error = %v, want ErrCanceled", err)
	}

	if vals := mustValues(t, h.Domain); len(vals) != 0 {
		t.Fatalf("domain values after a write that a cancel refused = %v, want none", vals)
	}

	mustBeUnchanged(t, s, held, "a write that a cancel refused")
}

// mustBeUnchanged fails the test unless the operation of held is still
// running under the claim of held, with no step.
func mustBeUnchanged(t *testing.T, s durable.Store, held durable.Operation, after string) {
	t.Helper()

	got := mustGet(t, s, held.ID)
	if got.State != durable.StateRunning || len(got.Steps) != 0 || !sameClaim(got.Claim, held.Claim) {
		t.Fatalf("operation after %s = %+v, want it unchanged", after, got)
	}
}

// failAfter makes a Domain method roll back after its writes.
func failAfter() error { return ErrInjected }

// checkNew fails the test unless op is a new pending operation with key,
// kind and input.
func checkNew(t *testing.T, op durable.Operation, key string, kind durable.Kind, input string) {
	t.Helper()

	if op.ID == "" || op.State != durable.StatePending || string(op.Input) != input || op.Kind != kind ||
		op.Key != key || op.Attempt != 0 || op.Expiries != 0 || op.Failures != 0 || len(op.Steps) != 0 {
		t.Fatalf(
			"Start returned %+v, want a new pending operation of %s with key %s and input %s",
			op,
			kind,
			key,
			input,
		)
	}
}

func claimReq(owner string, lease time.Duration, kinds ...durable.Kind) durable.ClaimRequest {
	return durable.ClaimRequest{Owner: owner, Kinds: kinds, MaxVersions: nil, Lease: lease, TakeCanceled: false}
}

func mustStart(t *testing.T, s durable.Store, key string, kind durable.Kind) durable.Operation {
	t.Helper()

	op, _, err := s.Start(t.Context(), durable.StartRequest{Key: key, Kind: kind, Version: 0, Input: nil})
	if err != nil {
		t.Fatalf("Start(%q): %v", key, err)
	}

	return op
}

func mustClaim(
	t *testing.T,
	s durable.Store,
	owner string,
	lease time.Duration,
	kinds ...durable.Kind,
) durable.Operation {
	t.Helper()

	op, err := s.Claim(t.Context(), claimReq(owner, lease, kinds...))
	if err != nil {
		t.Fatalf("Claim by %s: %v", owner, err)
	}

	if op.State != durable.StateRunning || op.Claim.Owner != owner || op.Claim.Operation != op.ID {
		t.Fatalf("Claim by %s returned %+v, want a running operation with the claim of %s", owner, op, owner)
	}

	return op
}

func mustGet(t *testing.T, s durable.Store, id durable.ID) durable.Operation {
	t.Helper()

	op, err := s.Get(t.Context(), id)
	if err != nil {
		t.Fatalf("Get(%q): %v", id, err)
	}

	return op
}

func mustList(t *testing.T, s durable.Store, f durable.Filter) []durable.Operation {
	t.Helper()

	ops, err := s.List(t.Context(), f)
	if err != nil {
		t.Fatalf("List(%+v): %v", f, err)
	}

	return ops
}

func mustValues(t *testing.T, d Domain) []string {
	t.Helper()

	vals, err := d.Values(t.Context())
	if err != nil {
		t.Fatalf("Values: %v", err)
	}

	slices.Sort(vals)

	return vals
}

func mustNamespace(t *testing.T, d Domain, name namespace.Name) namespace.Fence {
	t.Helper()

	f, err := d.CreateNamespace(t.Context(), name)
	if err != nil {
		t.Fatalf("CreateNamespace(%q): %v", name, err)
	}

	return f
}

// shorten sets the lease of the claim c to shortLease with Extend, after the
// last write that the case needs under a live claim, and fails the test at
// once when the expiry does not move earlier. A case claims with longLease
// and calls shorten, so that a slow store cannot end the claim before a
// write: only the wait for the end depends on time.
func shorten(t *testing.T, s durable.Store, c durable.Claim) {
	t.Helper()

	shorter, err := s.Extend(t.Context(), c, shortLease)
	if err != nil {
		t.Fatalf("Extend to a short lease: %v", err)
	}

	if !shorter.ExpiresAt.Before(c.ExpiresAt) {
		t.Fatalf("Extend to %v did not move the expiry earlier: %v after %v", shortLease, shorter.ExpiresAt,
			c.ExpiresAt)
	}
}

// claimWhenExpired claims an operation of kindA for ownerB as soon as one
// can run on the clock of the store. It fails the test after expiryWait.
func claimWhenExpired(t *testing.T, s durable.Store) durable.Operation {
	t.Helper()

	owner, kind := ownerB, kindA

	ctx, cancel := context.WithTimeout(t.Context(), expiryWait)
	defer cancel()

	for {
		op, err := s.Claim(ctx, claimReq(owner, longLease, kind))
		if err == nil {
			return op
		}

		if !errors.Is(err, durable.ErrNoWork) {
			t.Fatalf("Claim by %s: %v", owner, err)
		}

		select {
		case <-ctx.Done():
			t.Fatalf("no operation of %s could be claimed within %v", kind, expiryWait)
		case <-time.After(pollEvery):
		}
	}
}

// waitPast waits until a claim of a probe operation shows that the clock of
// the store passed at. A probe is a fresh operation of its own kind: its
// claim time is the store clock. The probe claim has a lease that does not
// expire inside the case, so that its Complete does not fail with
// ErrClaimLost when a slow store takes longer than a short lease between
// the claim and the Complete.
func waitPast(t *testing.T, s durable.Store, id durable.ID, at time.Time) {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), expiryWait)
	defer cancel()

	probeKind := durable.Kind("probe-" + string(id))

	for i := 0; ; i++ {
		_, _, err := s.Start(
			ctx,
			durable.StartRequest{Key: fmt.Sprintf("%s-%d", probeKind, i), Kind: probeKind, Version: 0, Input: nil},
		)
		if err != nil {
			t.Fatalf("start a clock probe: %v", err)
		}

		probe, err := s.Claim(ctx, claimReq("probe", longLease, probeKind))
		if err != nil {
			t.Fatalf("claim a clock probe: %v", err)
		}

		if err := s.Complete(ctx, probe.Claim); err != nil {
			t.Fatalf("complete a clock probe: %v", err)
		}

		if probe.UpdatedAt.After(at) {
			return
		}

		select {
		case <-ctx.Done():
			t.Fatalf("the clock of the store did not pass %v within %v", at, expiryWait)
		case <-time.After(pollEvery):
		}
	}
}

// pause waits for d, or fails the test when its context ends first.
func pause(t *testing.T, d time.Duration) {
	t.Helper()

	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-t.Context().Done():
		t.Fatal("the test ended during a pause")
	case <-timer.C:
	}
}

// sameClaim reports whether a and b are one claim. It compares times with
// Equal, because a store can return a time with another location.
func sameClaim(a, b durable.Claim) bool {
	return a.Operation == b.Operation && a.Owner == b.Owner && a.Token == b.Token && a.ExpiresAt.Equal(b.ExpiresAt)
}

// noClaim reports whether c is the zero claim.
func noClaim(c durable.Claim) bool {
	return c.Operation == "" && c.Owner == "" && c.Token == 0 && c.ExpiresAt.IsZero()
}

func stepNames(op durable.Operation) []string {
	names := make([]string, 0, len(op.Steps))
	for _, s := range op.Steps {
		names = append(names, s.Name)
	}

	return names
}

func opIDs(ops []durable.Operation) []durable.ID {
	ids := make([]durable.ID, 0, len(ops))
	for i := range ops {
		ids = append(ids, ops[i].ID)
	}

	return ids
}
