// Package idempotencytest is the conformance suite for idempotency.Store.
// Every built-in store runs it, and a custom store should too:
//
//	func TestIdempotencyConformance(t *testing.T) {
//		t.Parallel()
//		idempotencytest.Run(t, func(t *testing.T) idempotency.Store { return newMyStore(t) })
//	}
package idempotencytest

import (
	"bytes"
	"errors"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/idempotency"
)

// Factory returns a fresh, empty store for one subtest.
type Factory func(t *testing.T) idempotency.Store

// Run runs every case of the suite against the stores that newStore returns.
func Run(t *testing.T, newStore Factory) {
	t.Helper()

	cases := []struct {
		name string
		test func(t *testing.T, s idempotency.Store)
	}{
		{"the first request starts and holds a token", testStarts},
		{"a second request before the answer is in progress", testInProgress},
		{"a request with the same body replays the stored answer", testReplay},
		{"a request with another body is a reuse, pending or done", testReused},
		{"a record is scoped by principal, method, path and key", testScopes},
		{"an invalid ref is refused and changes nothing", testInvalidRef},
		{"complete refuses a token that does not hold the record", testCompleteToken},
		{"complete refuses an answer with a status that the store never keeps", testCompleteStatus},
		{"abandon frees the key and ignores a token that does not hold it", testAbandon},
		{"a pending record expires after the pending period", testPendingExpiry},
		{"a record expires after the record period", testRecordExpiry},
		{"sweep deletes the expired records and keeps the others", testSweep},
		{"two requests with one key give one start", testConcurrent},
		{"an answer round-trips headers and a binary body", testAnswerBytes},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.test(t, newStore(t))
		})
	}
}

// The statuses of the answers of the cases.
const (
	statusCreated = 201
	statusGone    = 410
)

// epochMillis is the time of the first request of most cases.
const epochMillis = 1_700_000_000_000

func epoch() time.Time { return time.UnixMilli(epochMillis).UTC() }

// ref returns a valid record name with the key k.
func ref(k string) idempotency.Ref {
	return idempotency.Ref{
		Principal: "alice", Method: "POST", Path: "/api/dustpan/v1/namespaces/dev/profiles", Key: idempotency.Key(k),
	}
}

// payload is the request bytes of most cases.
func payload() []byte { return []byte(`{"name":"aws-prod"}`) }

func answer() idempotency.Answer {
	return idempotency.Answer{
		Status:  statusCreated,
		Headers: map[string]string{"Content-Type": "application/json", "Etag": `"1"`},
		Body:    []byte(`{"id":"p1"}`),
	}
}

func begin(t *testing.T, s idempotency.Store, r idempotency.Ref, material []byte, at time.Time) idempotency.Result {
	t.Helper()

	res, err := s.Begin(t.Context(), r, material, at)
	if err != nil {
		t.Fatalf("begin %q: %v", r.Key, err)
	}

	return res
}

func wantOutcome(t *testing.T, got idempotency.Result, want idempotency.Outcome) {
	t.Helper()

	if got.Outcome != want {
		t.Fatalf("outcome = %d, want %d", got.Outcome, want)
	}
}

func started(t *testing.T, s idempotency.Store, r idempotency.Ref, at time.Time) idempotency.Result {
	t.Helper()

	res := begin(t, s, r, payload(), at)
	wantOutcome(t, res, idempotency.Started)

	if res.Token == "" {
		t.Fatal("a started request holds no token")
	}

	return res
}

func testStarts(t *testing.T, s idempotency.Store) {
	t.Helper()

	started(t, s, ref("k1"), epoch())
}

func testInProgress(t *testing.T, s idempotency.Store) {
	t.Helper()

	started(t, s, ref("k1"), epoch())
	wantOutcome(t, begin(t, s, ref("k1"), payload(), epoch().Add(time.Second)), idempotency.InProgress)
}

func testReplay(t *testing.T, s idempotency.Store) {
	t.Helper()

	first := started(t, s, ref("k1"), epoch())

	if err := s.Complete(t.Context(), ref("k1"), first.Token, answer()); err != nil {
		t.Fatalf("complete: %v", err)
	}

	got := begin(t, s, ref("k1"), payload(), epoch().Add(time.Minute))
	wantOutcome(t, got, idempotency.Replayed)

	want := answer()
	if got.Answer.Status != want.Status || !bytes.Equal(got.Answer.Body, want.Body) {
		t.Fatalf("replayed answer = %+v, want %+v", got.Answer, want)
	}

	if got.Token != "" {
		t.Fatalf("a replay holds token %q, want none", got.Token)
	}

	// A replay never changes the record: a second replay answers the same.
	again := begin(t, s, ref("k1"), payload(), epoch().Add(time.Minute+time.Minute))
	wantOutcome(t, again, idempotency.Replayed)
}

func testReused(t *testing.T, s idempotency.Store) {
	t.Helper()

	first := started(t, s, ref("k1"), epoch())
	other := []byte(`{"name":"other"}`)

	wantOutcome(t, begin(t, s, ref("k1"), other, epoch()), idempotency.Reused)

	if err := s.Complete(t.Context(), ref("k1"), first.Token, answer()); err != nil {
		t.Fatalf("complete: %v", err)
	}

	wantOutcome(t, begin(t, s, ref("k1"), other, epoch()), idempotency.Reused)
	wantOutcome(t, begin(t, s, ref("k1"), payload(), epoch()), idempotency.Replayed)
}

func testScopes(t *testing.T, s idempotency.Store) {
	t.Helper()

	base := ref("k1")
	first := started(t, s, base, epoch())

	if err := s.Complete(t.Context(), base, first.Token, answer()); err != nil {
		t.Fatalf("complete: %v", err)
	}

	otherPrincipal, otherMethod, otherPath, otherKey := base, base, base, base
	otherPrincipal.Principal = "bob"
	otherMethod.Method = "DELETE"
	otherPath.Path += "/other"
	otherKey.Key = "k2"

	for name, r := range map[string]idempotency.Ref{
		"principal": otherPrincipal, "method": otherMethod, "path": otherPath, "key": otherKey,
	} {
		// The same body in another scope starts: it never replays and never
		// shows that the key exists.
		res := begin(t, s, r, payload(), epoch())
		if res.Outcome != idempotency.Started {
			t.Fatalf("another %s: outcome = %d, want Started", name, res.Outcome)
		}
	}
}

func testInvalidRef(t *testing.T, s idempotency.Store) {
	t.Helper()

	good := ref("k1")
	noPrincipal, noMethod, noPath := good, good, good
	noPrincipal.Principal, noMethod.Method, noPath.Path = "", "", ""

	for name, tc := range map[string]struct {
		ref  idempotency.Ref
		want error
	}{
		"no principal": {noPrincipal, idempotency.ErrInvalid},
		"no method":    {noMethod, idempotency.ErrInvalid},
		"no path":      {noPath, idempotency.ErrInvalid},
		"empty key":    {ref(""), idempotency.ErrInvalidKey},
		"control key":  {ref("a\x00b"), idempotency.ErrInvalidKey},
	} {
		if _, err := s.Begin(t.Context(), tc.ref, payload(), epoch()); !errors.Is(err, tc.want) {
			t.Errorf("%s: begin err = %v, want %v", name, err, tc.want)
		}
	}

	if err := s.Complete(t.Context(), ref(""), "t", answer()); !errors.Is(err, idempotency.ErrInvalidKey) {
		t.Errorf("complete with an empty key: err = %v, want ErrInvalidKey", err)
	}
}

func testCompleteToken(t *testing.T, s idempotency.Store) {
	t.Helper()

	first := started(t, s, ref("k1"), epoch())

	if err := s.Complete(t.Context(), ref("k1"), first.Token+"x", answer()); !errors.Is(err, idempotency.ErrLost) {
		t.Fatalf("complete with a wrong token: err = %v, want ErrLost", err)
	}

	if err := s.Complete(t.Context(), ref("none"), first.Token, answer()); !errors.Is(err, idempotency.ErrLost) {
		t.Fatalf("complete of an unknown key: err = %v, want ErrLost", err)
	}

	// The record is still pending and the right token still completes it.
	wantOutcome(t, begin(t, s, ref("k1"), payload(), epoch()), idempotency.InProgress)

	if err := s.Complete(t.Context(), ref("k1"), first.Token, answer()); err != nil {
		t.Fatalf("complete: %v", err)
	}

	// A record completes once.
	if err := s.Complete(t.Context(), ref("k1"), first.Token, answer()); !errors.Is(err, idempotency.ErrLost) {
		t.Fatalf("second complete: err = %v, want ErrLost", err)
	}
}

func testCompleteStatus(t *testing.T, s idempotency.Store) {
	t.Helper()

	first := started(t, s, ref("k1"), epoch())
	bad := answer()
	bad.Status = 100

	if err := s.Complete(t.Context(), ref("k1"), first.Token, bad); !errors.Is(err, idempotency.ErrInvalid) {
		t.Fatalf("complete with status 100: err = %v, want ErrInvalid", err)
	}

	wantOutcome(t, begin(t, s, ref("k1"), payload(), epoch()), idempotency.InProgress)
}

func testAbandon(t *testing.T, s idempotency.Store) {
	t.Helper()

	first := started(t, s, ref("k1"), epoch())

	if err := s.Abandon(t.Context(), ref("k1"), first.Token+"x"); err != nil {
		t.Fatalf("abandon with a wrong token: %v", err)
	}

	wantOutcome(t, begin(t, s, ref("k1"), payload(), epoch()), idempotency.InProgress)

	if err := s.Abandon(t.Context(), ref("k1"), first.Token); err != nil {
		t.Fatalf("abandon: %v", err)
	}

	if err := s.Abandon(t.Context(), ref("k1"), first.Token); err != nil {
		t.Fatalf("abandon of a gone record: %v", err)
	}

	// The key is free, also for another body.
	res := begin(t, s, ref("k1"), []byte("other"), epoch())
	wantOutcome(t, res, idempotency.Started)

	// A done record is not abandoned.
	if err := s.Complete(t.Context(), ref("k1"), res.Token, answer()); err != nil {
		t.Fatalf("complete: %v", err)
	}

	if err := s.Abandon(t.Context(), ref("k1"), res.Token); err != nil {
		t.Fatalf("abandon of a done record: %v", err)
	}

	wantOutcome(t, begin(t, s, ref("k1"), []byte("other"), epoch()), idempotency.Replayed)
}

func testPendingExpiry(t *testing.T, s idempotency.Store) {
	t.Helper()

	first := started(t, s, ref("k1"), epoch())

	almost := epoch().Add(idempotency.PendingTTL - time.Millisecond)
	wantOutcome(t, begin(t, s, ref("k1"), payload(), almost), idempotency.InProgress)

	late := epoch().Add(idempotency.PendingTTL + time.Millisecond)
	retry := begin(t, s, ref("k1"), payload(), late)
	wantOutcome(t, retry, idempotency.Started)

	if retry.Token == first.Token {
		t.Fatal("a retry that takes an expired record holds the old token")
	}

	// The first request completes late: it must not store its answer.
	if err := s.Complete(t.Context(), ref("k1"), first.Token, answer()); !errors.Is(err, idempotency.ErrLost) {
		t.Fatalf("complete of an expired request: err = %v, want ErrLost", err)
	}

	// The retry still holds the key.
	wantOutcome(t, begin(t, s, ref("k1"), payload(), late), idempotency.InProgress)

	if err := s.Complete(t.Context(), ref("k1"), retry.Token, answer()); err != nil {
		t.Fatalf("complete of the retry: %v", err)
	}
}

func testRecordExpiry(t *testing.T, s idempotency.Store) {
	t.Helper()

	first := started(t, s, ref("k1"), epoch())

	if err := s.Complete(t.Context(), ref("k1"), first.Token, answer()); err != nil {
		t.Fatalf("complete: %v", err)
	}

	almost := epoch().Add(idempotency.RecordTTL - time.Millisecond)
	wantOutcome(t, begin(t, s, ref("k1"), payload(), almost), idempotency.Replayed)

	late := epoch().Add(idempotency.RecordTTL + time.Millisecond)
	wantOutcome(t, begin(t, s, ref("k1"), []byte("other"), late), idempotency.Started)
}

func testSweep(t *testing.T, s idempotency.Store) {
	t.Helper()

	done := started(t, s, ref("done"), epoch())

	if err := s.Complete(t.Context(), ref("done"), done.Token, answer()); err != nil {
		t.Fatalf("complete: %v", err)
	}

	started(t, s, ref("pending"), epoch().Add(idempotency.RecordTTL-time.Minute))

	now := epoch().Add(idempotency.RecordTTL + time.Second)

	n, err := s.Sweep(t.Context(), now)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}

	// The done record is past the record period. The pending record is
	// younger than the pending period at that time? It is 1 minute old at
	// the end of the record period of the first, so it stays.
	if n != 1 {
		t.Fatalf("sweep deleted %d records, want 1", n)
	}

	n, err = s.Sweep(t.Context(), now.Add(idempotency.PendingTTL))
	if err != nil {
		t.Fatalf("second sweep: %v", err)
	}

	if n != 1 {
		t.Fatalf("second sweep deleted %d records, want the pending one", n)
	}

	n, err = s.Sweep(t.Context(), now.Add(idempotency.PendingTTL))
	if err != nil || n != 0 {
		t.Fatalf("third sweep = %d, %v, want 0, nil", n, err)
	}
}

func testConcurrent(t *testing.T, s idempotency.Store) {
	t.Helper()

	const callers = 16

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		results = map[idempotency.Outcome]int{}
	)

	for range callers {
		wg.Go(func() {
			res, err := s.Begin(t.Context(), ref("k1"), payload(), epoch())
			if err != nil {
				t.Errorf("begin: %v", err)

				return
			}

			mu.Lock()
			results[res.Outcome]++
			mu.Unlock()
		})
	}

	wg.Wait()

	if results[idempotency.Started] != 1 || results[idempotency.InProgress] != callers-1 {
		t.Fatalf("outcomes = %v, want one Started and %d InProgress", results, callers-1)
	}
}

func testAnswerBytes(t *testing.T, s idempotency.Store) {
	t.Helper()

	first := started(t, s, ref("k1"), epoch())
	want := idempotency.Answer{
		Status:  statusGone,
		Headers: map[string]string{"X-A": "1", "X-B": "é"},
		Body:    []byte{0, 1, 2, 255, '\n'},
	}

	if err := s.Complete(t.Context(), ref("k1"), first.Token, want); err != nil {
		t.Fatalf("complete: %v", err)
	}

	got := begin(t, s, ref("k1"), payload(), epoch())
	wantOutcome(t, got, idempotency.Replayed)

	if got.Answer.Status != want.Status || !bytes.Equal(got.Answer.Body, want.Body) ||
		got.Answer.Headers["X-A"] != "1" || got.Answer.Headers["X-B"] != want.Headers["X-B"] {
		t.Fatalf("answer = %+v, want %+v", got.Answer, want)
	}
}
