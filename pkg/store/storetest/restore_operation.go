package storetest

import (
	"bytes"
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/maintenance"
)

// restoreOperationCases are the cases of the ports of a restore that runs as
// a durable operation: backup.RestoreOperations and backup.RestoreStaging. A
// store that does not implement both ports skips them.
func restoreOperationCases() []restoreCase {
	cases := make([]restoreCase, 0, 24+6*len(backup.RestoreSteps()))
	cases = append(cases, []restoreCase{
		{name: "a restore start stores the row and the operation together", test: testRestoreOpStart},
		{name: "a restore start refuses a second running restore", test: testRestoreOpStartRunning},
		{name: "the running work read counts a checking or applying restore", test: testRunningRestore},
		{name: "a restore start refuses a bad ID or a foreign operation", test: testRestoreOpStartRefused},
		{name: "fenced restore writes record their steps", test: testRestoreOpFencedWrites},
		{name: "a stale, canceled or foreign fence cannot write a restore row", test: testRestoreOpFenceRefused},
		{name: "a restore enters maintenance mode under its fence and confirms it", test: testRestoreOpEnter},
		{name: "raced work moves back to normal and records no step", test: testRestoreOpEnterRaced},
		{
			name: "a restore confirms the maintenance mode of an operator as not entered",
			test: testRestoreOpEnterOperator,
		},
		{name: "a stale or canceled fence changes no installation state", test: testRestoreOpEnterRefused},
		{
			name: "a failed restore end leaves only the maintenance mode that it entered",
			test: testRestoreOpFinishLeaves,
		},
		{name: "the audit entry of a restore commits once with its step", test: testRestoreOpAudit},
		{name: "audit events are stored under the fence, once each, in order", test: testRestoreOpEvents},
		{name: "audit events past the limit keep one summary and never fail", test: testRestoreOpEventsPastLimit},
		{name: "the claim is checked with no write", test: testRestoreOpCheckFence},
		{name: "ended operations fail their running restore rows", test: testRestoreOpFailEnded},
		{name: "live operations keep their running restore rows", test: testRestoreOpFailEndedLive},
	}...)
	cases = append(cases, restoreStagingCases()...)

	return append(cases, restoreWorkerCases()...)
}

// restoreCase is one case of RunRestore.
type restoreCase struct {
	test func(t *testing.T, newStore RestoreFactory)
	name string
}

// restorePorts are the ports of a restore that runs as a durable operation.
type restorePorts struct {
	ops     backup.RestoreOperations
	staging backup.RestoreStaging
}

// restorePortsOf returns the restore operation ports of s, and skips the
// case when s does not implement both of them: the ports land together.
func restorePortsOf(t *testing.T, s RestoreStore) restorePorts {
	t.Helper()

	ops, opsOK := s.(backup.RestoreOperations)
	staging, stagingOK := s.(backup.RestoreStaging)

	if !opsOK || !stagingOK {
		t.Skip("the store does not implement the restore operation ports")
	}

	return restorePorts{ops: ops, staging: staging}
}

// restoreOpStart returns the start request of the operation of restore id.
func restoreOpStart(id backup.RestoreID) durable.StartRequest {
	return durable.StartRequest{
		Key: backup.RestoreOperationKey(id), Kind: backup.RestoreOperationKind,
		Version: backup.RestoreOperationVersion, Input: []byte("{}"),
	}
}

// mustStartRestoreOp starts a checking restore with a new ID and its
// operation, and returns the ID.
func mustStartRestoreOp(t *testing.T, ops backup.RestoreOperations) backup.RestoreID {
	t.Helper()

	id := backup.MintRestoreID()
	if err := ops.StartRestore(t.Context(), newRestoreRecord(id, "bkp-op", now()), restoreOpStart(id)); err != nil {
		t.Fatalf("start restore %s: %v", id, err)
	}

	return id
}

// mustClaimRestoreOp claims the restore operation of s with a long lease.
func mustClaimRestoreOp(t *testing.T, s durable.Store, owner string) durable.Operation {
	t.Helper()

	op, err := s.Claim(t.Context(), durable.ClaimRequest{
		Owner: owner, Kinds: []durable.Kind{backup.RestoreOperationKind}, MaxVersions: nil, Lease: time.Hour,
		TakeCanceled: false,
	})
	if err != nil {
		t.Fatalf("claim the restore operation: %v", err)
	}

	return op
}

// restoreOps lists the restore operations of s.
func restoreOps(t *testing.T, s durable.Store) []durable.Operation {
	t.Helper()

	got, err := s.List(t.Context(), durable.Filter{Kind: backup.RestoreOperationKind, States: nil, Limit: 0})
	if err != nil {
		t.Fatalf("list restore operations: %v", err)
	}

	return got
}

// mustHaveStep fails the test unless operation id recorded step.
func mustHaveStep(t *testing.T, s durable.Store, id durable.ID, step string) {
	t.Helper()

	op, err := s.Get(t.Context(), id)
	if err != nil {
		t.Fatalf("get operation %s: %v", id, err)
	}

	if _, ok := op.Step(step); !ok {
		t.Fatalf("operation %s has the steps %+v, want %q", id, op.Steps, step)
	}
}

// mustStepResult fails the test unless operation id recorded step with
// result.
func mustStepResult(t *testing.T, s durable.Store, id durable.ID, step string, result []byte) {
	t.Helper()

	op, err := s.Get(t.Context(), id)
	if err != nil {
		t.Fatalf("get operation %s: %v", id, err)
	}

	rec, ok := op.Step(step)
	if !ok || !bytes.Equal(rec.Result, result) {
		t.Fatalf("result of step %q = %q (recorded %v), want %q", step, rec.Result, ok, result)
	}
}

// mustLackStep fails the test when operation id recorded step.
func mustLackStep(t *testing.T, s durable.Store, id durable.ID, step string) {
	t.Helper()

	op, err := s.Get(t.Context(), id)
	if err != nil {
		t.Fatalf("get operation %s: %v", id, err)
	}

	if _, ok := op.Step(step); ok {
		t.Fatalf("operation %s recorded %q, want no record", id, step)
	}
}

func testRestoreOpStart(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	s := newStore(t)
	ops := restorePortsOf(t, s).ops
	id := mustStartRestoreOp(t, ops)

	row, err := ops.GetRestore(t.Context(), id)
	if err != nil || row.State != backup.RestoreChecking {
		t.Fatalf("restore after the start = %+v, %v, want checking", row, err)
	}

	got := restoreOps(t, s.DurableOperationsPort())
	if len(got) != 1 || got[0].Key != backup.RestoreOperationKey(id) || got[0].State != durable.StatePending {
		t.Fatalf("restore operations after the start = %+v, want one pending operation of %s", got, id)
	}
}

func testRestoreOpStartRunning(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	s := newStore(t)
	ops := restorePortsOf(t, s).ops
	first := mustStartRestoreOp(t, ops)

	second := backup.MintRestoreID()

	err := ops.StartRestore(t.Context(), newRestoreRecord(second, "bkp-op", now()), restoreOpStart(second))
	if !errors.Is(err, backup.ErrRestoreRunning) {
		t.Fatalf("start of a second restore: error = %v, want ErrRestoreRunning", err)
	}

	if _, getErr := ops.GetRestore(t.Context(), second); !errors.Is(getErr, backup.ErrRestoreNotFound) {
		t.Fatalf("get of the refused restore: error = %v, want ErrRestoreNotFound", getErr)
	}

	if got := restoreOps(t, s.DurableOperationsPort()); len(got) != 1 ||
		got[0].Key != backup.RestoreOperationKey(first) {
		t.Fatalf("restore operations after a refused start = %+v, want only the operation of %s", got, first)
	}
}

func testRestoreOpStartRefused(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	id := backup.MintRestoreID()
	other := backup.MintRestoreID()

	foreignKind := restoreOpStart(id)
	foreignKind.Kind = "backup"

	cases := []struct {
		name  string
		id    backup.RestoreID
		start durable.StartRequest
		want  error
	}{
		{name: "an ID of another form", id: "rst-1", start: restoreOpStart("rst-1"), want: backup.ErrInvalidRestoreID},
		{
			name:  "a path in the ID",
			id:    "rst-../../etc",
			start: restoreOpStart("rst-../../etc"),
			want:  backup.ErrInvalidRestoreID,
		},
		{name: "the key of another restore", id: id, start: restoreOpStart(other), want: durable.ErrKeyConflict},
		{name: "an operation of another kind", id: id, start: foreignKind, want: durable.ErrKeyConflict},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := newStore(t)
			ops := restorePortsOf(t, s).ops

			record := newRestoreRecord(tc.id, "bkp-op", now())
			if err := ops.StartRestore(t.Context(), record, tc.start); !errors.Is(err, tc.want) {
				t.Fatalf("start with %s: error = %v, want %v", tc.name, err, tc.want)
			}

			if rows := mustListRestores(t, s); len(rows) != 0 {
				t.Fatalf("restores after a refused start = %+v, want none", rows)
			}

			all, err := s.DurableOperationsPort().List(t.Context(), durable.Filter{Kind: "", States: nil, Limit: 0})
			if err != nil || len(all) != 0 {
				t.Fatalf("operations after a refused start = %+v, %v, want none", all, err)
			}
		})
	}
}

func testRestoreOpEvents(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	s := newStore(t)
	ops := restorePortsOf(t, s).ops
	durableOps := s.DurableOperationsPort()
	ctx := t.Context()
	id := mustStartRestoreOp(t, ops)
	held := mustClaimRestoreOp(t, durableOps, "instance-a")
	enter := held.Claim.Step(backup.RestoreStepEnterMaintenance)

	for _, object := range []string{"first", "second", "first"} {
		if err := ops.RecordRestoreEvent(ctx, enter, id, object); err != nil {
			t.Fatalf("record event %q: %v", object, err)
		}
	}

	// An event records no step: the confirm of the enter does.
	mustLackStep(t, durableOps, held.ID, backup.RestoreStepEnterMaintenance)

	for _, bad := range []string{"", "a line\nbreak", "a return\rhere", strings.Repeat("x", 4096)} {
		if err := ops.RecordRestoreEvent(ctx, enter, id, bad); !errors.Is(err, backup.ErrRestoreEventInvalid) {
			t.Fatalf("record event of %d bytes: error = %v, want ErrRestoreEventInvalid", len(bad), err)
		}
	}

	taken := replaceClaim(t, durableOps, held)

	if err := ops.RecordRestoreEvent(ctx, enter, id, "stale"); !errors.Is(err, durable.ErrClaimLost) {
		t.Fatalf("record event by the replaced holder: error = %v, want ErrClaimLost", err)
	}

	live := taken.Claim.Step(backup.RestoreStepEnterMaintenance)
	if err := ops.RecordRestoreEvent(ctx, live, id, "third"); err != nil {
		t.Fatalf("record event by the live holder: %v", err)
	}

	events, err := ops.RestoreEvents(ctx, id)
	if err != nil || !slices.Equal(events, []string{"first", "second", "third"}) {
		t.Fatalf("events = %q, %v; want first, second, third", events, err)
	}

	if _, err := ops.RestoreEvents(ctx, backup.MintRestoreID()); !errors.Is(err, backup.ErrRestoreNotFound) {
		t.Fatalf("events of an unknown restore: error = %v, want ErrRestoreNotFound", err)
	}
}

func testRestoreOpCheckFence(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	s := newStore(t)
	ops := restorePortsOf(t, s).ops
	durableOps := s.DurableOperationsPort()
	ctx := t.Context()
	id := mustStartRestoreOp(t, ops)
	held := mustClaimRestoreOp(t, durableOps, "instance-a")
	fence := held.Claim.Step("check-fence")

	for range 2 {
		if err := ops.CheckRestoreFence(ctx, fence, id); err != nil {
			t.Fatalf("check by the live holder: %v", err)
		}
	}

	// The check writes nothing: no step and no audit event.
	mustLackStep(t, durableOps, held.ID, "check-fence")

	if events, err := ops.RestoreEvents(ctx, id); err != nil || len(events) != 0 {
		t.Fatalf("events after the checks = %q, %v; want none", events, err)
	}

	if err := ops.CheckRestoreFence(ctx, fence, backup.MintRestoreID()); !errors.Is(err, durable.ErrClaimLost) {
		t.Fatalf("check of another restore under this fence: error = %v, want ErrClaimLost", err)
	}

	taken := replaceClaim(t, durableOps, held)

	if err := ops.CheckRestoreFence(ctx, fence, id); !errors.Is(err, durable.ErrClaimLost) {
		t.Fatalf("check by the replaced holder: error = %v, want ErrClaimLost", err)
	}

	live := taken.Claim.Step("check-fence")
	if err := ops.CheckRestoreFence(ctx, live, id); err != nil {
		t.Fatalf("check by the new holder: %v", err)
	}

	_, err := ops.FinishRestore(ctx, taken.Claim.Step(backup.RestoreStepFinish), backup.RestoreEnd{
		ID: id, State: backup.RestoreFailed, Error: "stopped", EndedAt: now(), DevReport: nil, LeaveMaintenance: false,
	})
	if err != nil {
		t.Fatalf("finish: %v", err)
	}

	if err := ops.CheckRestoreFence(ctx, live, id); !errors.Is(err, backup.ErrRestoreNotRunning) {
		t.Fatalf("check of a finished restore: error = %v, want ErrRestoreNotRunning", err)
	}
}

func testRestoreOpEventsPastLimit(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	s := newStore(t)
	ops := restorePortsOf(t, s).ops
	ctx := t.Context()
	id := mustStartRestoreOp(t, ops)
	held := mustClaimRestoreOp(t, s.DurableOperationsPort(), "instance-a")
	enter := held.Claim.Step(backup.RestoreStepEnterMaintenance)

	// A forced enter can cancel more units than the limit: each record still
	// succeeds.
	for i := range backup.MaxRestoreEvents + 2 {
		if err := ops.RecordRestoreEvent(ctx, enter, id, "cancel "+strconv.Itoa(i)); err != nil {
			t.Fatalf("record event %d: %v", i, err)
		}
	}

	events, err := ops.RestoreEvents(ctx, id)
	if err != nil {
		t.Fatalf("read the events: %v", err)
	}

	if len(events) != backup.MaxRestoreEvents || !strings.Contains(events[len(events)-1], ": up to 3 more") {
		t.Fatalf(
			"events = %d with the last %q, want the limit with a summary of up to 3",
			len(events), events[len(events)-1],
		)
	}
}

func testRestoreOpFencedWrites(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	s := newStore(t)
	ops := restorePortsOf(t, s).ops
	durableOps := s.DurableOperationsPort()
	ctx := t.Context()
	id := mustStartRestoreOp(t, ops)
	held := mustClaimRestoreOp(t, durableOps, "instance-a")

	if err := ops.RecordRestoreReport(ctx, held.Claim.Step(backup.RestoreStepCheck), id, devReport(1)); err != nil {
		t.Fatalf("record the report: %v", err)
	}

	mustHaveStep(t, durableOps, held.ID, backup.RestoreStepCheck)

	result := []byte(`{"digest":"abc","damaged":1,"created":true}`)
	err := ops.MarkRestoreApplying(
		ctx,
		held.Claim.Step(backup.RestoreStepPreBackup),
		id,
		"bkp-pre",
		result,
	)
	if err != nil {
		t.Fatalf("mark applying: %v", err)
	}

	mustHaveStep(t, durableOps, held.ID, backup.RestoreStepPreBackup)
	mustStepResult(t, durableOps, held.ID, backup.RestoreStepPreBackup, result)

	ended := now()
	final := devReport(2)

	row, err := ops.FinishRestore(ctx, held.Claim.Step(backup.RestoreStepFinish), backup.RestoreEnd{
		ID: id, State: backup.RestoreSucceeded, Error: "", EndedAt: ended, DevReport: &final, LeaveMaintenance: false,
	})
	if err != nil {
		t.Fatalf("finish: %v", err)
	}

	mustHaveStep(t, durableOps, held.ID, backup.RestoreStepFinish)

	if row.State != backup.RestoreSucceeded || row.PreBackupID != "bkp-pre" || !row.EndedAt.Equal(ended) ||
		row.DevReport == nil || row.DevReport.Classes[backup.RemovedPrincipals] != 2 {
		t.Fatalf("finished restore = %+v, want succeeded with the pre-backup, the final report and the end time", row)
	}

	_, err = ops.FinishRestore(ctx, held.Claim.Step("finish-again"), backup.RestoreEnd{
		ID: id, State: backup.RestoreFailed, Error: "late", EndedAt: ended, DevReport: nil, LeaveMaintenance: false,
	})
	if !errors.Is(err, backup.ErrRestoreNotRunning) {
		t.Fatalf("second finish: error = %v, want ErrRestoreNotRunning", err)
	}

	mustLackStep(t, durableOps, held.ID, "finish-again")
}

// devReport returns a dev report that names n removed principals.
func devReport(n int64) backup.DevStripReport {
	return backup.DevStripReport{
		Principals: nil, OtherPrincipals: 0, OtherCounts: nil, Approvers: nil, Warnings: nil,
		PreBackupFound: nil, NotHeld: nil, ErrorCode: "",
		Classes: map[backup.DevRemovalClass]int64{backup.RemovedPrincipals: n},
	}
}

func testRestoreOpFenceRefused(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	cases := []struct {
		name  string
		fence func(t *testing.T, s durable.Store, held durable.Operation) durable.StepFence
		want  error
	}{
		{
			name: "a replaced claim",
			fence: func(t *testing.T, s durable.Store, held durable.Operation) durable.StepFence {
				t.Helper()
				replaceClaim(t, s, held)

				return held.Claim.Step(backup.RestoreStepPreBackup)
			},
			want: durable.ErrClaimLost,
		},
		{
			name: "a canceled operation",
			fence: func(t *testing.T, s durable.Store, held durable.Operation) durable.StepFence {
				t.Helper()
				cancelOnly(t, s, held)

				return held.Claim.Step(backup.RestoreStepPreBackup)
			},
			want: durable.ErrCanceled,
		},
		{
			name: "the live claim of another operation",
			fence: func(t *testing.T, s durable.Store, _ durable.Operation) durable.StepFence {
				t.Helper()

				mustStartDurable(t, s, "other")

				return mustClaimDurable(t, s).Claim.Step(backup.RestoreStepPreBackup)
			},
			want: durable.ErrClaimLost,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := newStore(t)
			ops := restorePortsOf(t, s).ops
			durableOps := s.DurableOperationsPort()
			id := mustStartRestoreOp(t, ops)
			held := mustClaimRestoreOp(t, durableOps, "instance-a")
			fence := tc.fence(t, durableOps, held)

			if err := ops.MarkRestoreApplying(t.Context(), fence, id, "bkp-pre", nil); !errors.Is(err, tc.want) {
				t.Fatalf("mark applying with %s: error = %v, want %v", tc.name, err, tc.want)
			}

			_, err := ops.FinishRestore(t.Context(), fence, backup.RestoreEnd{
				ID: id, State: backup.RestoreFailed, Error: "stale", EndedAt: now(), DevReport: nil,
				LeaveMaintenance: false,
			})
			if !errors.Is(err, tc.want) {
				t.Fatalf("finish with %s: error = %v, want %v", tc.name, err, tc.want)
			}

			if row := mustGetRestore(t, s, id); row.State != backup.RestoreChecking || row.PreBackupID != "" {
				t.Fatalf("restore after refused writes = %+v, want it checking and unchanged", row)
			}

			mustLackStep(t, durableOps, held.ID, backup.RestoreStepPreBackup)
		})
	}
}

func testRestoreOpFinishLeaves(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	cases := []struct {
		name     string
		operator bool
		leave    bool
		want     maintenance.State
	}{
		{name: "entered by the restore, leave", operator: false, leave: true, want: maintenance.StateNormal},
		{name: "entered by the restore, stay", operator: false, leave: false, want: maintenance.StateMaintenance},
		{name: "entered by an operator, leave", operator: true, leave: true, want: maintenance.StateMaintenance},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := newStore(t)
			ops := restorePortsOf(t, s).ops
			id := mustStartRestoreOp(t, ops)
			held := mustClaimRestoreOp(t, s.DurableOperationsPort(), "instance-a")

			if tc.operator {
				enterByOperator(t, s)
			} else {
				enterByRestore(t, ops, held, id)
			}

			_, err := ops.FinishRestore(t.Context(), held.Claim.Step(backup.RestoreStepFinish), backup.RestoreEnd{
				ID: id, State: backup.RestoreFailed, Error: "corrupt", EndedAt: now(), DevReport: nil,
				LeaveMaintenance: tc.leave,
			})
			if err != nil {
				t.Fatalf("finish failed: %v", err)
			}

			if got, stateErr := s.GetState(t.Context()); stateErr != nil || got != tc.want {
				t.Fatalf("installation state after the end = %s, %v, want %s", got, stateErr, tc.want)
			}
		})
	}
}

func testRestoreOpFailEnded(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	cases := []failEndedCase{
		{
			name: "canceled before maintenance mode", end: cancelAndRelease, enter: enterNone, leave: true,
			text: backup.RestoreCanceledText, state: maintenance.StateNormal,
		},
		{
			name: "canceled in its maintenance mode", end: cancelAndRelease, enter: enterRestore, leave: true,
			text: backup.RestoreCanceledText, state: maintenance.StateNormal,
		},
		{
			name: "canceled in its maintenance mode with the hold", end: cancelAndRelease, enter: enterRestore,
			leave: false, text: backup.RestoreCanceledText, state: maintenance.StateMaintenance,
		},
		{
			name: "canceled in the maintenance mode of an operator", end: cancelAndRelease, enter: enterOperator,
			leave: true, text: backup.RestoreCanceledText, state: maintenance.StateMaintenance,
		},
		{
			name: "failed with no end write", end: failFinal, enter: enterRestore, leave: true,
			text: backup.RestoreStoppedText, state: maintenance.StateNormal,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			runFailEnded(t, newStore(t), tc)
		})
	}
}

// failEndedCase is one case of testRestoreOpFailEnded: how the operation
// ends, who entered maintenance mode, whether the hold lets the fail leave
// it, and the error text and the state after the fail.
type failEndedCase struct {
	name  string
	end   func(t *testing.T, s durable.Store, held durable.Operation)
	enter enterBy
	leave bool
	text  string
	state maintenance.State
}

// enterBy names who moved the installation to maintenance mode in a case.
type enterBy int

// The parties of enterBy.
const (
	enterNone enterBy = iota
	enterRestore
	enterOperator
)

// runFailEnded starts and claims a restore on s, ends its operation as tc
// says, and checks what FailEndedRestores does to the row and the state.
func runFailEnded(t *testing.T, s RestoreStore, tc failEndedCase) {
	t.Helper()

	ops := restorePortsOf(t, s).ops
	durableOps := s.DurableOperationsPort()
	id := mustStartRestoreOp(t, ops)
	held := mustClaimRestoreOp(t, durableOps, "instance-a")

	enterAs(t, s, held, id, tc.enter)

	tc.end(t, durableOps, held)

	ended := now()

	failed, err := ops.FailEndedRestores(t.Context(), ended, tc.leave)
	if err != nil || len(failed) != 1 || failed[0].ID != id {
		t.Fatalf("fail ended restores = %+v, %v, want the row of %s", failed, err, id)
	}

	row := mustGetRestore(t, s, id)
	if row.State != backup.RestoreFailed || row.Error != tc.text || !row.EndedAt.Equal(ended) {
		t.Fatalf("restore after the fail = %+v, want failed with %q", row, tc.text)
	}

	if got, stateErr := s.GetState(t.Context()); stateErr != nil || got != tc.state {
		t.Fatalf("installation state after the fail = %s, %v, want %s", got, stateErr, tc.state)
	}

	again, err := ops.FailEndedRestores(t.Context(), ended, tc.leave)
	if err != nil || len(again) != 0 {
		t.Fatalf("second fail of ended restores = %+v, %v, want none", again, err)
	}
}

// enterAs moves the installation of s to maintenance mode as the party by
// does: the restore id under the fence of held, an operator, or nobody.
func enterAs(t *testing.T, s RestoreStore, held durable.Operation, id backup.RestoreID, by enterBy) {
	t.Helper()

	switch by {
	case enterRestore:
		enterByRestore(t, restorePortsOf(t, s).ops, held, id)
	case enterOperator:
		enterByOperator(t, s)
	case enterNone:
	default:
		t.Fatalf("unknown party %d", by)
	}
}

// enterByRestore moves the installation to maintenance mode under the fence
// of the enter-maintenance step of held, as the step of the restore id does
// before its confirm.
func enterByRestore(t *testing.T, ops backup.RestoreOperations, held durable.Operation, id backup.RestoreID) {
	t.Helper()

	enter := held.Claim.Step(backup.RestoreStepEnterMaintenance)

	err := ops.SetRestoreMaintenance(t.Context(), enter, id, maintenance.StateNormal, maintenance.StateMaintenance)
	if err != nil {
		t.Fatalf("enter maintenance mode under the fence: %v", err)
	}
}

// enterByOperator moves the installation to maintenance mode with no
// restore, as an operator does.
func enterByOperator(t *testing.T, s RestoreStore) {
	t.Helper()

	if err := s.SetState(t.Context(), maintenance.StateMaintenance); err != nil {
		t.Fatalf("enter maintenance mode as an operator: %v", err)
	}
}

// cancelAndRelease cancels the operation of held and releases its claim, so
// the operation ends canceled with no end write of the restore.
func cancelAndRelease(t *testing.T, s durable.Store, held durable.Operation) {
	t.Helper()

	if err := s.Cancel(t.Context(), held.ID); err != nil {
		t.Fatalf("cancel: %v", err)
	}

	if err := s.Release(t.Context(), held.Claim); err != nil {
		t.Fatalf("release: %v", err)
	}
}

// failFinal ends the operation of held failed, as the runner does after a
// step that panicked.
func failFinal(t *testing.T, s durable.Store, held durable.Operation) {
	t.Helper()

	failure := durable.Failure{Message: "panic", Final: true, RetryAfter: 0}
	if err := s.Fail(t.Context(), held.Claim, failure); err != nil {
		t.Fatalf("fail: %v", err)
	}
}

func testRestoreOpFailEndedLive(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	s := newStore(t)
	ops := restorePortsOf(t, s).ops
	durableOps := s.DurableOperationsPort()
	id := mustStartRestoreOp(t, ops)

	// A pending operation, then a running one with a live claim, then one
	// whose claim expired: no instance ended any of them.
	phases := []struct {
		name  string
		owner string
		lease time.Duration
	}{
		{name: "pending", owner: "", lease: 0},
		{name: "running", owner: "instance-a", lease: time.Hour},
		{name: "expired", owner: "instance-b", lease: time.Millisecond},
	}

	for _, phase := range phases {
		if phase.owner != "" {
			// The expired phase claims only after the live claim of the
			// running phase; a claim that finds no work leaves it live.
			_, err := durableOps.Claim(t.Context(), durable.ClaimRequest{
				Owner: phase.owner, Kinds: []durable.Kind{backup.RestoreOperationKind}, MaxVersions: nil,
				Lease: phase.lease, TakeCanceled: false,
			})
			if err != nil && !errors.Is(err, durable.ErrNoWork) {
				t.Fatalf("claim for the %s phase: %v", phase.name, err)
			}
		}

		failed, err := ops.FailEndedRestores(t.Context(), now(), true)
		if err != nil || len(failed) != 0 {
			t.Fatalf("fail ended restores with a %s operation = %+v, %v, want none", phase.name, failed, err)
		}

		if row := mustGetRestore(t, s, id); row.State != backup.RestoreChecking {
			t.Fatalf("restore with a %s operation = %+v, want checking", phase.name, row)
		}
	}
}

func testRestoreOpEnter(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	s := newStore(t)
	ops := restorePortsOf(t, s).ops
	durableOps := s.DurableOperationsPort()
	id := mustStartRestoreOp(t, ops)
	held := mustClaimRestoreOp(t, durableOps, "instance-a")
	ctx := t.Context()

	if entered, err := ops.RestoreMaintenanceEntered(ctx, id); err != nil || entered {
		t.Fatalf("maintenance flag before the enter = %t, %v, want false", entered, err)
	}

	enterByRestore(t, ops, held, id)

	if entered, err := ops.RestoreMaintenanceEntered(ctx, id); err != nil || !entered {
		t.Fatalf("maintenance flag after the enter = %t, %v, want true", entered, err)
	}

	_, err := ops.RestoreMaintenanceEntered(
		ctx,
		backup.MintRestoreID(),
	)
	if !errors.Is(err, backup.ErrRestoreNotFound) {
		t.Fatalf("maintenance flag of an unknown restore: error = %v, want ErrRestoreNotFound", err)
	}

	// The state change records no step: only the confirm does.
	mustLackStep(t, durableOps, held.ID, backup.RestoreStepEnterMaintenance)
	mustState(t, s, maintenance.StateMaintenance)

	enter := held.Claim.Step(backup.RestoreStepEnterMaintenance)

	_, err = ops.ConfirmRestoreMaintenance(ctx, enter, id, maintenance.StateNormal, nil)
	if !errors.Is(err, maintenance.ErrStateChanged) {
		t.Fatalf("confirm of a state that is not stored: error = %v, want ErrStateChanged", err)
	}

	mustLackStep(t, durableOps, held.ID, backup.RestoreStepEnterMaintenance)

	canceled := []string{"stack acme/web", "workspace acme/net"}

	entered, err := ops.ConfirmRestoreMaintenance(ctx, enter, id, maintenance.StateMaintenance, canceled)
	if err != nil || !entered {
		t.Fatalf("confirm after the enter = %t, %v, want entered", entered, err)
	}

	mustHaveStep(t, durableOps, held.ID, backup.RestoreStepEnterMaintenance)
	mustEnterResult(t, durableOps, held, canceled)

	// The leave clears the flag, and its confirm records the leave step.
	leave := held.Claim.Step(backup.RestoreStepLeaveMaintenance)

	err = ops.SetRestoreMaintenance(ctx, leave, id, maintenance.StateMaintenance, maintenance.StateNormal)
	if err != nil {
		t.Fatalf("leave under the fence: %v", err)
	}

	entered, err = ops.ConfirmRestoreMaintenance(
		ctx,
		leave,
		id,
		maintenance.StateNormal,
		nil,
	)
	if err != nil ||
		entered {
		t.Fatalf("confirm after the leave = %t, %v, want not entered", entered, err)
	}

	mustHaveStep(t, durableOps, held.ID, backup.RestoreStepLeaveMaintenance)
	mustState(t, s, maintenance.StateNormal)
}

// mustEnterResult proves that the enter step of held records the flag, the
// canceled units and the claim token, as EncodeRestoreMaintenance makes them.
func mustEnterResult(t *testing.T, ops durable.Store, held durable.Operation, canceled []string) {
	t.Helper()

	op, err := ops.Get(t.Context(), held.ID)
	if err != nil {
		t.Fatalf("get operation %s: %v", held.ID, err)
	}

	rec, ok := op.Step(backup.RestoreStepEnterMaintenance)
	if !ok {
		t.Fatalf("operation %s has no enter step", held.ID)
	}

	got, err := backup.DecodeRestoreMaintenance(rec.Result)
	if err != nil {
		t.Fatalf("decode the enter result %q: %v", rec.Result, err)
	}

	if !got.Entered || got.Token != held.Claim.Token || !slices.Equal(got.Canceled, canceled) {
		t.Fatalf("enter result = %+v, want entered, token %d and the units %v", got, held.Claim.Token, canceled)
	}
}

func testRestoreOpEnterRaced(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	s := newStore(t)
	ops := restorePortsOf(t, s).ops
	durableOps := s.DurableOperationsPort()
	id := mustStartRestoreOp(t, ops)
	held := mustClaimRestoreOp(t, durableOps, "instance-a")
	enter := held.Claim.Step(backup.RestoreStepEnterMaintenance)

	enterByRestore(t, ops, held, id)

	// Work raced the enter: the enter moves back to normal under the fence.
	err := ops.SetRestoreMaintenance(t.Context(), enter, id, maintenance.StateMaintenance, maintenance.StateNormal)
	if err != nil {
		t.Fatalf("move back to normal: %v", err)
	}

	mustLackStep(t, durableOps, held.ID, backup.RestoreStepEnterMaintenance)
	mustState(t, s, maintenance.StateNormal)

	// An end with the leave changes nothing: the flag is clear.
	enterByOperator(t, s)

	_, err = ops.FinishRestore(t.Context(), held.Claim.Step(backup.RestoreStepFinish), backup.RestoreEnd{
		ID: id, State: backup.RestoreFailed, Error: "raced", EndedAt: now(), DevReport: nil, LeaveMaintenance: true,
	})
	if err != nil {
		t.Fatalf("finish: %v", err)
	}

	mustState(t, s, maintenance.StateMaintenance)
}

func testRestoreOpEnterOperator(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	s := newStore(t)
	ops := restorePortsOf(t, s).ops
	durableOps := s.DurableOperationsPort()
	id := mustStartRestoreOp(t, ops)
	held := mustClaimRestoreOp(t, durableOps, "instance-a")

	enterByOperator(t, s)

	enter := held.Claim.Step(backup.RestoreStepEnterMaintenance)

	err := ops.SetRestoreMaintenance(t.Context(), enter, id, maintenance.StateNormal, maintenance.StateMaintenance)
	if !errors.Is(err, maintenance.ErrStateChanged) {
		t.Fatalf("enter in the maintenance mode of an operator: error = %v, want ErrStateChanged", err)
	}

	entered, err := ops.ConfirmRestoreMaintenance(t.Context(), enter, id, maintenance.StateMaintenance, nil)
	if err != nil || entered {
		t.Fatalf("confirm in the maintenance mode of an operator = %t, %v, want not entered", entered, err)
	}

	mustHaveStep(t, durableOps, held.ID, backup.RestoreStepEnterMaintenance)
}

func testRestoreOpEnterRefused(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	cases := []struct {
		name string
		end  func(t *testing.T, s durable.Store, held durable.Operation)
		want error
		// recorded is true when the enter step is recorded before the end.
		recorded bool
	}{
		{name: "replaced", end: replaceAndForget, want: durable.ErrClaimLost, recorded: false},
		{name: "canceled", end: cancelOnly, want: durable.ErrCanceled, recorded: false},
		{name: "canceled after the enter step", end: cancelOnly, want: durable.ErrCanceled, recorded: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := newStore(t)
			ops := restorePortsOf(t, s).ops
			durableOps := s.DurableOperationsPort()
			id := mustStartRestoreOp(t, ops)
			held := mustClaimRestoreOp(t, durableOps, "instance-a")

			if tc.recorded {
				recordEnterStep(t, durableOps, held)
			}

			tc.end(t, durableOps, held)

			enter := held.Claim.Step(backup.RestoreStepEnterMaintenance)

			err := ops.SetRestoreMaintenance(
				t.Context(), enter, id, maintenance.StateNormal, maintenance.StateMaintenance,
			)
			if !errors.Is(err, tc.want) {
				t.Fatalf("enter by a %s claim: error = %v, want %v", tc.name, err, tc.want)
			}

			_, err = ops.ConfirmRestoreMaintenance(
				t.Context(),
				enter,
				id,
				maintenance.StateNormal,
				nil,
			)
			if !errors.Is(err, tc.want) {
				t.Fatalf("confirm by a %s claim: error = %v, want %v", tc.name, err, tc.want)
			}

			mustState(t, s, maintenance.StateNormal)

			if tc.recorded {
				mustHaveStep(t, durableOps, held.ID, backup.RestoreStepEnterMaintenance)
			} else {
				mustLackStep(t, durableOps, held.ID, backup.RestoreStepEnterMaintenance)
			}
		})
	}
}

func testRestoreOpAudit(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	s := newStore(t)
	ops := restorePortsOf(t, s).ops
	durableOps := s.DurableOperationsPort()
	ctx := t.Context()
	id := mustStartRestoreOp(t, ops)
	held := mustClaimRestoreOp(t, durableOps, "instance-a")

	err := ops.MarkRestoreApplying(
		ctx,
		held.Claim.Step(backup.RestoreStepPreBackup),
		id,
		"bkp-pre",
		nil,
	)
	if err != nil {
		t.Fatalf("mark applying: %v", err)
	}

	after := held.Claim.Step(backup.RestoreStepAfterApply)
	entries := []audit.Entry{
		ServerChange(now(), "restore "+id.String()),
		ServerChange(now(), "restore "+id.String()+" removed dev principal"),
	}

	stored, err := ops.AppendRestoreAudit(ctx, after, id, entries)
	if err != nil || len(stored) != len(entries) || stored[1].Sequence != stored[0].Sequence+1 {
		t.Fatalf("append the audit entries = %+v, %v, want two entries in order", stored, err)
	}

	mustHaveStep(t, durableOps, held.ID, backup.RestoreStepAfterApply)

	last := stored[len(stored)-1]

	// A second append of the same step, as after a takeover, adds no entry.
	if _, err := ops.AppendRestoreAudit(ctx, after, id, entries); !errors.Is(err, durable.ErrStepDone) {
		t.Fatalf("second append: error = %v, want ErrStepDone", err)
	}

	if got := lastServerEntry(t, s); got.Sequence != last.Sequence || got.Hash != last.Hash {
		t.Fatalf("last server entry = %+v, want the last entry of the restore %+v", got, last)
	}

	// A stale holder appends nothing.
	replaceClaim(t, durableOps, held)

	stale := held.Claim.Step("after-apply-stale")
	if _, err := ops.AppendRestoreAudit(ctx, stale, id, entries); !errors.Is(err, durable.ErrClaimLost) {
		t.Fatalf("append by a stale holder: error = %v, want ErrClaimLost", err)
	}

	if again := lastServerEntry(t, s); again.Sequence != last.Sequence {
		t.Fatalf("last server entry after a stale append = %+v, want sequence %d", again, last.Sequence)
	}
}

// lastServerEntry returns the entry with the highest sequence of the
// server scope of s. A store with the restore ports reads its audit log, so
// the case fails when s has no reader.
func lastServerEntry(t *testing.T, s RestoreStore) audit.Entry {
	t.Helper()

	reader, ok := any(s).(audit.EntryReader)
	if !ok {
		t.Fatal("the store does not implement audit.EntryReader")
	}

	last, found, err := reader.LastEntry(t.Context(), audit.ServerScope)
	if err != nil || !found {
		t.Fatalf("last server entry: %t, %v", found, err)
	}

	return last
}

// mustState fails the test unless the installation state of s is want.
func mustState(t *testing.T, s RestoreStore, want maintenance.State) {
	t.Helper()

	if got, err := s.GetState(t.Context()); err != nil || got != want {
		t.Fatalf("installation state = %s, %v, want %s", got, err, want)
	}
}

// replaceClaim releases the claim of held, and a second instance claims the
// operation again with a new token. It returns the operation of the new
// claim.
func replaceClaim(t *testing.T, s durable.Store, held durable.Operation) durable.Operation {
	t.Helper()

	if err := s.Release(t.Context(), held.Claim); err != nil {
		t.Fatalf("release: %v", err)
	}

	return mustClaimRestoreOp(t, s, "instance-b")
}

// replaceAndForget runs replaceClaim for a case that needs no new claim.
func replaceAndForget(t *testing.T, s durable.Store, held durable.Operation) {
	t.Helper()
	replaceClaim(t, s, held)
}

// cancelOnly asks for the cancel of the operation of held.
func cancelOnly(t *testing.T, s durable.Store, held durable.Operation) {
	t.Helper()

	if err := s.Cancel(t.Context(), held.ID); err != nil {
		t.Fatalf("cancel: %v", err)
	}
}

// recordEnterStep records the enter-maintenance step of held with no result,
// so that the step is done before a later cancel.
func recordEnterStep(t *testing.T, s durable.Store, held durable.Operation) {
	t.Helper()

	if err := s.RecordStep(t.Context(), held.Claim.Step(backup.RestoreStepEnterMaintenance), nil); err != nil {
		t.Fatalf("record the enter step: %v", err)
	}
}
