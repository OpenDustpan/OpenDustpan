package storetest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/storecheck"
	"opendustpan.org/opendustpan/pkg/durable"
)

// StoreCheckStore is what RunStoreChecks needs: the rows of the checks and
// the durable operations in one database. Witness reads the stored end time
// with a raw query; a store without one skips that case.
type StoreCheckStore struct {
	Records storecheck.Records
	Ops     durable.Store
	Witness NullEndedWitness
}

// StoreCheckFactory returns a fresh, empty store for one subtest of
// RunStoreChecks.
type StoreCheckFactory func(t *testing.T) StoreCheckStore

// RunStoreChecks executes the conformance suite of storecheck.Records against
// stores produced by newStore.
func RunStoreChecks(t *testing.T, newStore StoreCheckFactory) {
	t.Helper()

	cases := []struct {
		test func(t *testing.T, s StoreCheckStore)
		name string
	}{
		{name: "start stores a running row and its operation", test: testCheckStart},
		{
			name: "a row that is not ended stores NULL, a row that is ended stores its time",
			test: testCheckEndedShape,
		},
		{name: "start refuses a second running check and stores nothing", test: testCheckOneRunning},
		{name: "start refuses a taken ID", test: testCheckTakenID},
		{name: "start refuses a row that is not valid", test: testCheckInvalidRow},
		{name: "start refuses an operation of another key or kind", test: testCheckWrongOperation},
		{name: "start refuses a target that ended a check in the cooldown", test: testCheckCooldown},
		{name: "only one of several starts at the same time wins", test: testCheckConcurrentStart},
		{name: "finish stores the end state, the error and the report", test: testCheckFinish},
		{name: "finish of a row that is not running is ErrNotRunning", test: testCheckFinishNotRunning},
		{name: "finish with a stale fence is ErrClaimLost and changes nothing", test: testCheckFinishStale},
		{name: "finish with the fence of another operation is ErrClaimLost", test: testCheckFinishOtherOperation},
		{name: "finish of an unknown ID is ErrNotFound", test: testCheckFinishUnknown},
		{name: "finish with a state that is not an end is ErrNotRunning", test: testCheckFinishBadState},
		{name: "get of an unknown ID is ErrNotFound", test: testCheckGetUnknown},
		{name: "list answers the newest check first", test: testCheckList},
		{name: "fail ended marks failed only the rows whose operation ended", test: testCheckFailEnded},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.test(t, newStore(t))
		})
	}
}

// checkAt is the start time of the first check of a case, a whole number of
// milliseconds, the precision that the stores keep.
func checkAt() time.Time {
	return time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
}

func checkRow(id string, target storecheck.Target, at time.Time) storecheck.Record {
	return storecheck.Record{
		StartedAt: at, EndedAt: time.Time{}, ID: storecheck.ID(id), Target: target, State: storecheck.StateRunning,
		RequestedBy: "admin", Error: "", Report: nil,
	}
}

func checkStart(id string) durable.StartRequest {
	return durable.StartRequest{
		Key: storecheck.OperationKey(storecheck.ID(id)), Kind: storecheck.OperationKind,
		Version: storecheck.OperationVersion, Input: []byte(`{}`),
	}
}

// startCheck starts the check id on target, with the start time at, and fails
// the test on an error. coolingSince is far in the past.
func startCheck(t *testing.T, s StoreCheckStore, id string, target storecheck.Target, at time.Time) {
	t.Helper()

	err := s.Records.StartCheck(
		t.Context(),
		checkRow(id, target, at),
		checkStart(id),
		at.Add(-24*time.Hour),
	)
	if err != nil {
		t.Fatalf("start %s: %v", id, err)
	}
}

// claimCheck claims the operation of a started check and returns the fence of
// its finish step.
func claimCheck(t *testing.T, s StoreCheckStore) durable.StepFence {
	t.Helper()

	op, err := s.Ops.Claim(t.Context(), durable.ClaimRequest{
		Owner:        "test",
		Kinds:        []durable.Kind{storecheck.OperationKind},
		MaxVersions:  nil,
		Lease:        time.Minute,
		TakeCanceled: false,
	})
	if err != nil {
		t.Fatalf("claim: %v", err)
	}

	return op.Claim.Step(storecheck.StepFinish)
}

func endOf(at time.Time) storecheck.Finish {
	return storecheck.Finish{
		EndedAt: at,
		State:   storecheck.StateSucceeded,
		Error:   "",
		Report:  []byte(`{"passed":true}`),
	}
}

// testCheckEndedShape proves the stored shape of ended_at_ms: a running row
// stores NULL and a finished row stores its end time. It reads the column
// with the raw reader of the store, not through the domain record.
func testCheckEndedShape(t *testing.T, s StoreCheckStore) {
	t.Helper()

	if s.Witness == nil {
		t.Skip("store has no raw end-time reader")
	}

	startCheck(t, s, "chk-aaaa", storecheck.TargetBlobs, checkAt())
	requireNotEndedStored(t, s.Witness, "store_checks", "chk-aaaa")

	fence := claimCheck(t, s)
	endedAt := checkAt().Add(time.Minute)

	if err := s.Records.FinishCheck(t.Context(), fence, "chk-aaaa", endOf(endedAt)); err != nil {
		t.Fatalf("finish: %v", err)
	}

	requireEndedStored(t, s.Witness, "store_checks", "chk-aaaa", endedAt)
}

func testCheckStart(t *testing.T, s StoreCheckStore) {
	t.Helper()

	startCheck(t, s, "chk-aaaa", storecheck.TargetBlobs, checkAt())

	got, err := s.Records.GetCheck(t.Context(), "chk-aaaa")
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	if got.State != storecheck.StateRunning || got.Target != storecheck.TargetBlobs || got.RequestedBy != "admin" ||
		!got.StartedAt.Equal(checkAt()) || !got.EndedAt.IsZero() || got.Report != nil || got.Error != "" {
		t.Fatalf("row = %+v", got)
	}

	ops, err := s.Ops.List(t.Context(), durable.Filter{Kind: storecheck.OperationKind, States: nil, Limit: 0})
	if err != nil || len(ops) != 1 || ops[0].Key != storecheck.OperationKey("chk-aaaa") {
		t.Fatalf("operations = %+v, %v", ops, err)
	}
}

func testCheckOneRunning(t *testing.T, s StoreCheckStore) {
	t.Helper()

	startCheck(t, s, "chk-aaaa", storecheck.TargetBlobs, checkAt())

	for _, target := range []storecheck.Target{storecheck.TargetBlobs, storecheck.TargetBackup} {
		err := s.Records.StartCheck(
			t.Context(),
			checkRow("chk-bbbb", target, checkAt()),
			checkStart("chk-bbbb"),
			checkAt().Add(-time.Hour),
		)
		if !errors.Is(err, storecheck.ErrRunning) {
			t.Fatalf("second start on %q err = %v, want ErrRunning", target, err)
		}
	}

	if _, err := s.Records.GetCheck(t.Context(), "chk-bbbb"); !errors.Is(err, storecheck.ErrNotFound) {
		t.Fatalf("refused start stored a row: %v", err)
	}

	ops := s.operations(t)
	if len(ops) != 1 {
		t.Fatalf("refused start stored %d operations, want 1", len(ops))
	}
}

func testCheckTakenID(t *testing.T, s StoreCheckStore) {
	t.Helper()

	startCheck(t, s, "chk-aaaa", storecheck.TargetBlobs, checkAt())

	err := s.Records.StartCheck(
		t.Context(),
		checkRow("chk-aaaa", storecheck.TargetBlobs, checkAt()),
		checkStart("chk-aaaa"),
		checkAt().Add(-time.Hour),
	)
	if !errors.Is(err, storecheck.ErrExists) {
		t.Fatalf("err = %v, want ErrExists", err)
	}
}

func testCheckInvalidRow(t *testing.T, s StoreCheckStore) {
	t.Helper()

	rows := []storecheck.Record{
		checkRow("", storecheck.TargetBlobs, checkAt()),
		checkRow("chk-../x", storecheck.TargetBlobs, checkAt()),
		checkRow("chk-aaaa", "s3://bucket", checkAt()),
		checkRow("chk-aaaa", "", checkAt()),
	}

	noRequester := checkRow("chk-aaaa", storecheck.TargetBlobs, checkAt())
	noRequester.RequestedBy = ""

	notRunning := checkRow("chk-aaaa", storecheck.TargetBlobs, checkAt())
	notRunning.State = storecheck.StateSucceeded

	rows = append(rows, noRequester, notRunning)

	for i := range rows {
		err := s.Records.StartCheck(t.Context(), rows[i], checkStart(rows[i].ID.String()), checkAt().Add(-time.Hour))
		if err == nil {
			t.Errorf("start of %+v succeeded", rows[i])
		}
	}

	if rows := s.rows(t); len(rows) != 0 {
		t.Fatalf("invalid starts stored %d rows", len(rows))
	}
}

func testCheckWrongOperation(t *testing.T, s StoreCheckStore) {
	t.Helper()

	other := checkStart("chk-bbbb")

	wrongKind := checkStart("chk-aaaa")
	wrongKind.Kind = "backup"

	for _, start := range []durable.StartRequest{other, wrongKind} {
		err := s.Records.StartCheck(
			t.Context(),
			checkRow("chk-aaaa", storecheck.TargetBlobs, checkAt()),
			start,
			checkAt().Add(-time.Hour),
		)
		if !errors.Is(err, durable.ErrKeyConflict) {
			t.Fatalf("start with %+v err = %v, want ErrKeyConflict", start, err)
		}
	}
}

func testCheckCooldown(t *testing.T, s StoreCheckStore) {
	t.Helper()

	startCheck(t, s, "chk-aaaa", storecheck.TargetBlobs, checkAt())

	fence := claimCheck(t, s)
	endedAt := checkAt().Add(time.Minute)

	if err := s.Records.FinishCheck(t.Context(), fence, "chk-aaaa", endOf(endedAt)); err != nil {
		t.Fatalf("finish: %v", err)
	}

	later := endedAt.Add(time.Minute)

	// The check ended after coolingSince: the same target waits.
	err := s.Records.StartCheck(
		t.Context(),
		checkRow("chk-bbbb", storecheck.TargetBlobs, later),
		checkStart("chk-bbbb"),
		endedAt.Add(-time.Second),
	)
	if !errors.Is(err, storecheck.ErrCooldown) {
		t.Fatalf("start in the cooldown err = %v, want ErrCooldown", err)
	}

	_, err = s.Records.GetCheck(t.Context(), "chk-bbbb")
	if !errors.Is(err, storecheck.ErrNotFound) {
		t.Fatalf("a refused start stored a row: %v", err)
	}

	// The other target does not wait.
	err = s.Records.StartCheck(
		t.Context(),
		checkRow("chk-cccc", storecheck.TargetBackup, later),
		checkStart("chk-cccc"),
		endedAt.Add(-time.Second),
	)
	if err != nil {
		t.Fatalf("start on the other target: %v", err)
	}

	// After the cooldown, the same target starts again.
	fence = claimCheck(t, s)
	err = s.Records.FinishCheck(t.Context(), fence, "chk-cccc", endOf(later))
	if err != nil {
		t.Fatalf("finish: %v", err)
	}

	err = s.Records.StartCheck(
		t.Context(),
		checkRow("chk-dddd", storecheck.TargetBlobs, later),
		checkStart("chk-dddd"),
		endedAt.Add(time.Second),
	)
	if err != nil {
		t.Fatalf("start after the cooldown: %v", err)
	}
}

func testCheckConcurrentStart(t *testing.T, s StoreCheckStore) {
	t.Helper()

	const starters = 8

	errs := make([]error, starters)

	var wg sync.WaitGroup

	for i := range starters {
		wg.Go(func() {
			id := fmt.Sprintf("chk-race%d", i)
			errs[i] = s.Records.StartCheck(
				context.WithoutCancel(t.Context()),
				checkRow(id, storecheck.TargetBlobs, checkAt()),
				checkStart(id),
				checkAt().Add(-time.Hour),
			)
		})
	}

	wg.Wait()

	won := 0

	for _, err := range errs {
		switch {
		case err == nil:
			won++
		case errors.Is(err, storecheck.ErrRunning):
		default:
			t.Errorf("start err = %v, want nil or ErrRunning", err)
		}
	}

	if won != 1 {
		t.Fatalf("%d starts won, want 1", won)
	}

	if rows := s.rows(t); len(rows) != 1 {
		t.Fatalf("%d rows, want 1", len(rows))
	}
}

func testCheckFinish(t *testing.T, s StoreCheckStore) {
	t.Helper()

	startCheck(t, s, "chk-aaaa", storecheck.TargetBackup, checkAt())

	fence := claimCheck(t, s)
	end := storecheck.Finish{
		EndedAt: checkAt().Add(time.Minute), State: storecheck.StateFailed, Error: "the check budget ended",
		Report: []byte(`{"passed":false}`),
	}

	if err := s.Records.FinishCheck(t.Context(), fence, "chk-aaaa", end); err != nil {
		t.Fatalf("finish: %v", err)
	}

	got := s.firstRow(t)
	if got.State != storecheck.StateFailed || got.Error != end.Error || !bytes.Equal(got.Report, end.Report) ||
		!got.EndedAt.Equal(end.EndedAt) {
		t.Fatalf("row = %+v", got)
	}

	op, err := s.Ops.Get(t.Context(), fence.Operation)
	if err != nil {
		t.Fatalf("get operation: %v", err)
	}

	if _, ok := op.Step(storecheck.StepFinish); !ok {
		t.Fatal("the finish did not record its step")
	}
}

func testCheckFinishNotRunning(t *testing.T, s StoreCheckStore) {
	t.Helper()

	startCheck(t, s, "chk-aaaa", storecheck.TargetBlobs, checkAt())

	op, err := s.Ops.Claim(t.Context(), durable.ClaimRequest{
		Owner:        "test",
		Kinds:        []durable.Kind{storecheck.OperationKind},
		MaxVersions:  nil,
		Lease:        time.Minute,
		TakeCanceled: false,
	})
	if err != nil {
		t.Fatalf("claim: %v", err)
	}

	err = s.Records.FinishCheck(
		t.Context(),
		op.Claim.Step("first"),
		"chk-aaaa",
		endOf(checkAt().Add(time.Minute)),
	)
	if err != nil {
		t.Fatalf("finish: %v", err)
	}

	err = s.Records.FinishCheck(t.Context(), op.Claim.Step("second"), "chk-aaaa", endOf(checkAt().Add(2*time.Minute)))
	if !errors.Is(err, storecheck.ErrNotRunning) {
		t.Fatalf("second finish err = %v, want ErrNotRunning", err)
	}

	got := s.firstRow(t)
	if !got.EndedAt.Equal(checkAt().Add(time.Minute)) {
		t.Fatalf("the second finish changed the end time to %v", got.EndedAt)
	}
}

func testCheckFinishStale(t *testing.T, s StoreCheckStore) {
	t.Helper()

	startCheck(t, s, "chk-aaaa", storecheck.TargetBlobs, checkAt())

	fence := claimCheck(t, s)
	fence.Token++

	err := s.Records.FinishCheck(t.Context(), fence, "chk-aaaa", endOf(checkAt().Add(time.Minute)))
	if !errors.Is(err, durable.ErrClaimLost) {
		t.Fatalf("err = %v, want ErrClaimLost", err)
	}

	if got := s.firstRow(t); got.State != storecheck.StateRunning {
		t.Fatalf("a stale fence changed the row to %q", got.State)
	}
}

func testCheckFinishOtherOperation(t *testing.T, s StoreCheckStore) {
	t.Helper()

	startCheck(t, s, "chk-aaaa", storecheck.TargetBlobs, checkAt())

	// The claim is the live claim of the operation of chk-aaaa. It must not
	// finish a row of another check.
	fence := claimCheck(t, s)

	err := s.Records.FinishCheck(t.Context(), fence, "chk-bbbb", endOf(checkAt().Add(time.Minute)))
	if !errors.Is(err, durable.ErrClaimLost) {
		t.Fatalf("err = %v, want ErrClaimLost", err)
	}

	if got := s.firstRow(t); got.State != storecheck.StateRunning {
		t.Fatalf("the row of the fence changed to %q", got.State)
	}
}

func testCheckFinishUnknown(t *testing.T, s StoreCheckStore) {
	t.Helper()

	// An operation of the kind with the key of a check that has no row.
	if _, _, err := s.Ops.Start(t.Context(), checkStart("chk-aaaa")); err != nil {
		t.Fatalf("start operation: %v", err)
	}

	fence := claimCheck(t, s)

	err := s.Records.FinishCheck(t.Context(), fence, "chk-aaaa", endOf(checkAt()))
	if !errors.Is(err, storecheck.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func testCheckFinishBadState(t *testing.T, s StoreCheckStore) {
	t.Helper()

	startCheck(t, s, "chk-aaaa", storecheck.TargetBlobs, checkAt())

	fence := claimCheck(t, s)

	for _, state := range []storecheck.State{storecheck.StateRunning, "", "done"} {
		end := endOf(checkAt())
		end.State = state

		err := s.Records.FinishCheck(t.Context(), fence, "chk-aaaa", end)
		if !errors.Is(err, storecheck.ErrNotRunning) {
			t.Errorf("finish with state %q err = %v, want ErrNotRunning", state, err)
		}
	}
}

func testCheckGetUnknown(t *testing.T, s StoreCheckStore) {
	t.Helper()

	if _, err := s.Records.GetCheck(t.Context(), "chk-unknown"); !errors.Is(err, storecheck.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func testCheckList(t *testing.T, s StoreCheckStore) {
	t.Helper()

	if rows, err := s.Records.ListChecks(t.Context()); err != nil || len(rows) != 0 {
		t.Fatalf("empty list = %v, %v", rows, err)
	}

	startCheck(t, s, "chk-aaaa", storecheck.TargetBlobs, checkAt())

	fence := claimCheck(t, s)
	if err := s.Records.FinishCheck(t.Context(), fence, "chk-aaaa", endOf(checkAt().Add(time.Minute))); err != nil {
		t.Fatalf("finish: %v", err)
	}

	startCheck(t, s, "chk-bbbb", storecheck.TargetBackup, checkAt().Add(time.Hour))

	rows, err := s.Records.ListChecks(t.Context())
	if err != nil || len(rows) != 2 || rows[0].ID != "chk-bbbb" || rows[1].ID != "chk-aaaa" {
		t.Fatalf("list = %+v, %v; want chk-bbbb then chk-aaaa", rows, err)
	}
}

func testCheckFailEnded(t *testing.T, s StoreCheckStore) {
	t.Helper()

	startCheck(t, s, "chk-aaaa", storecheck.TargetBlobs, checkAt())

	// Nothing ended: no row changes.
	if n, err := s.Records.FailEndedChecks(t.Context(), checkAt()); err != nil || n != 0 {
		t.Fatalf("fail ended = %d, %v; want 0", n, err)
	}

	op, err := s.Ops.Claim(t.Context(), durable.ClaimRequest{
		Owner:        "test",
		Kinds:        []durable.Kind{storecheck.OperationKind},
		MaxVersions:  nil,
		Lease:        time.Minute,
		TakeCanceled: false,
	})
	if err != nil {
		t.Fatalf("claim: %v", err)
	}

	err = s.Ops.Fail(
		t.Context(),
		op.Claim,
		durable.Failure{Message: "adapter error https://s3.example.test", Final: true, RetryAfter: 0},
	)
	if err != nil {
		t.Fatalf("fail operation: %v", err)
	}

	endedAt := checkAt().Add(time.Hour)

	if n, err := s.Records.FailEndedChecks(t.Context(), endedAt); err != nil || n != 1 {
		t.Fatalf("fail ended = %d, %v; want 1", n, err)
	}

	got := s.firstRow(t)
	if got.State != storecheck.StateFailed || got.Error != storecheck.EndedWithoutFinishText ||
		!got.EndedAt.Equal(endedAt) {
		t.Fatalf("row = %+v", got)
	}

	// A second call finds nothing, and the text of the operation never
	// reached the row.
	if n := s.failEnded(t, endedAt); n != 0 {
		t.Fatalf("second fail ended = %d, want 0", n)
	}
}

// firstRow returns the row chk-aaaa, the check that most cases start first,
// and fails the test on an error.
func (s StoreCheckStore) firstRow(t *testing.T) storecheck.Record {
	t.Helper()

	row, err := s.Records.GetCheck(t.Context(), "chk-aaaa")
	if err != nil {
		t.Fatalf("get chk-aaaa: %v", err)
	}

	return row
}

// rows returns every row and fails the test on an error.
func (s StoreCheckStore) rows(t *testing.T) []storecheck.Record {
	t.Helper()

	rows, err := s.Records.ListChecks(t.Context())
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	return rows
}

// operations returns the operations of the kind of a check and fails the test
// on an error.
func (s StoreCheckStore) operations(t *testing.T) []durable.Operation {
	t.Helper()

	ops, err := s.Ops.List(t.Context(), durable.Filter{Kind: storecheck.OperationKind, States: nil, Limit: 0})
	if err != nil {
		t.Fatalf("list operations: %v", err)
	}

	return ops
}

// failEnded calls FailEndedChecks and fails the test on an error.
func (s StoreCheckStore) failEnded(t *testing.T, at time.Time) int {
	t.Helper()

	n, err := s.Records.FailEndedChecks(t.Context(), at)
	if err != nil {
		t.Fatalf("fail ended: %v", err)
	}

	return n
}
