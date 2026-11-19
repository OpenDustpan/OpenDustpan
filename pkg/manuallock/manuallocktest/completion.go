package manuallocktest

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/idempotency"
	"opendustpan.org/opendustpan/pkg/manuallock"
)

// Records returns the replay record store that shares the database of s. The
// suite uses it to prove that a lock write stores the answer of a request in
// its own transaction.
type Records func(t *testing.T, s Store) idempotency.Store

// RunCompletion runs the cases of the answer that PutLock, RemoveLock and
// ForceRemoveLock store when their context carries an idempotency.Completion.
// A store that does not store the answer in the transaction of the write fails
// each case.
func RunCompletion(t *testing.T, newStore Factory, records Records) {
	t.Helper()

	cases := []struct {
		name string
		test func(t *testing.T, s Store, r idempotency.Store)
	}{
		{name: "a lock stores the answer of its request", test: testCompletionPut},
		{name: "a removal stores the answer of its request", test: testCompletionRemove},
		{name: "a forced removal stores the answer of its request", test: testCompletionForce},
		{name: "a refused lock stores no answer", test: testCompletionRefused},
		{name: "a lock with no pending record is rolled back", test: testCompletionLost},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := newStore(t)
			tc.test(t, s, records(t, s))
		})
	}
}

// statusOK is the status of the stored answer.
const statusOK = 200

// errWrongResult reports a result that is not the lock of the holder that the case expects.
var errWrongResult = errors.New("the store passed the wrong result")

// completionBody is the body that the cases store as the answer.
const completionBody = `{"stored":true}`

// begin starts the pending record of a request and returns the ref, the
// material and the Completion that names the record. holder is what the
// answer builder reads from the result, so a case checks what the store passes.
func begin(
	t *testing.T,
	r idempotency.Store,
	key, wantHolder string,
) (idempotency.Ref, []byte, *idempotency.Completion) {
	t.Helper()

	ref := idempotency.Ref{Principal: alice, Method: "POST", Path: "/locks/bulk-lock", Key: idempotency.Key(key)}
	material := []byte("body-" + key)

	started, err := r.Begin(t.Context(), ref, material, time.Now())
	if err != nil || started.Outcome != idempotency.Started {
		t.Fatalf("Begin = %+v, %v, want Started", started, err)
	}

	c := idempotency.NewCompletion(ref, started.Token, func(result any) (idempotency.Answer, error) {
		l, ok := result.(manuallock.Lock)
		if !ok || l.Holder != wantHolder {
			return idempotency.Answer{}, fmt.Errorf("%w: want the lock of %s", errWrongResult, wantHolder)
		}

		return idempotency.Answer{Status: statusOK, Headers: nil, Body: []byte(completionBody)}, nil
	})

	return ref, material, c
}

// wantReplay checks that a second Begin with the same key finds the stored
// answer.
func wantReplay(t *testing.T, r idempotency.Store, ref idempotency.Ref, material []byte) {
	t.Helper()

	res, err := r.Begin(t.Context(), ref, material, time.Now())
	if err != nil || res.Outcome != idempotency.Replayed {
		t.Fatalf("second Begin = %+v, %v, want Replayed: the store kept no answer", res, err)
	}

	if res.Answer.Status != statusOK || string(res.Answer.Body) != completionBody {
		t.Fatalf("stored answer = %+v, want status 200 and %s", res.Answer, completionBody)
	}
}

func testCompletionPut(t *testing.T, s Store, r idempotency.Store) {
	t.Helper()

	f := newFixture(t, s)
	target := manuallock.UnitTarget(f.web)
	ref, material, c := begin(t, r, "put", alice)

	l := manuallock.Lock{Since: now(), Target: target, Holder: alice, Reason: testReason}
	err := s.PutLock(
		idempotency.WithCompletion(t.Context(), c),
		l,
		f.ns.Fence(),
		lockAudit(f.ns.Fence()),
	)
	if err != nil {
		t.Fatalf("PutLock: %v", err)
	}

	if !c.Stored() {
		t.Fatal("PutLock did not store the answer")
	}

	wantReplay(t, r, ref, material)
}

func testCompletionRemove(t *testing.T, s Store, r idempotency.Store) {
	t.Helper()

	f := newFixture(t, s)
	target := manuallock.UnitTarget(f.web)
	f.lock(t, target, alice, testReason)

	ref, material, c := begin(t, r, "remove", alice)

	_, err := s.RemoveLock(
		idempotency.WithCompletion(t.Context(), c), target, alice, f.ns.Fence(), lockAudit(f.ns.Fence()),
	)
	if err != nil {
		t.Fatalf("RemoveLock: %v", err)
	}

	if !c.Stored() {
		t.Fatal("RemoveLock did not store the answer")
	}

	wantReplay(t, r, ref, material)
}

func testCompletionForce(t *testing.T, s Store, r idempotency.Store) {
	t.Helper()

	f := newFixture(t, s)
	target := manuallock.UnitTarget(f.web)
	f.lock(t, target, bob, testReason)

	ref, material, c := begin(t, r, "force", bob)

	_, err := s.ForceRemoveLock(
		idempotency.WithCompletion(t.Context(), c), target, f.ns.Fence(), lockAudit(f.ns.Fence()),
	)
	if err != nil {
		t.Fatalf("ForceRemoveLock: %v", err)
	}

	if !c.Stored() {
		t.Fatal("ForceRemoveLock did not store the answer")
	}

	wantReplay(t, r, ref, material)
}

// testCompletionRefused: a write that fails stores no answer, so a retry does
// the write again after the handler frees the key.
func testCompletionRefused(t *testing.T, s Store, r idempotency.Store) {
	t.Helper()

	f := newFixture(t, s)
	target := manuallock.UnitTarget(f.web)
	f.lock(t, target, bob, testReason)

	ref, material, c := begin(t, r, "refused", alice)

	l := manuallock.Lock{Since: now(), Target: target, Holder: alice, Reason: testReason}

	err := s.PutLock(idempotency.WithCompletion(t.Context(), c), l, f.ns.Fence(), lockAudit(f.ns.Fence()))
	if !errors.Is(err, manuallock.ErrAlreadyLocked) {
		t.Fatalf("PutLock on a locked unit: %v, want ErrAlreadyLocked", err)
	}

	if c.Stored() {
		t.Fatal("a refused lock stored an answer")
	}

	res, beginErr := r.Begin(t.Context(), ref, material, time.Now())
	if beginErr != nil || res.Outcome != idempotency.InProgress {
		t.Fatalf("Begin after the refusal = %+v, %v, want InProgress: the record must stay pending", res, beginErr)
	}
}

// testCompletionLost: the pending record is not in place, so the store cannot
// store the answer, and the lock write rolls back with it.
func testCompletionLost(t *testing.T, s Store, r idempotency.Store) {
	t.Helper()

	f := newFixture(t, s)
	target := manuallock.UnitTarget(f.web)
	ref, _, c := begin(t, r, "lost", alice)

	if err := r.Abandon(t.Context(), ref, c.Token); err != nil {
		t.Fatalf("Abandon: %v", err)
	}

	l := manuallock.Lock{Since: now(), Target: target, Holder: alice, Reason: testReason}

	err := s.PutLock(idempotency.WithCompletion(t.Context(), c), l, f.ns.Fence(), lockAudit(f.ns.Fence()))
	if !errors.Is(err, idempotency.ErrLost) {
		t.Fatalf("PutLock with no pending record: %v, want ErrLost", err)
	}

	if _, getErr := s.GetLock(t.Context(), target); !errors.Is(getErr, manuallock.ErrNotLocked) {
		t.Fatalf("after the lost answer: %v, want ErrNotLocked: the write must roll back", getErr)
	}
}
