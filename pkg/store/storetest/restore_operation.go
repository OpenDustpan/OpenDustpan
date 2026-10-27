package storetest

import (
	"errors"
	"slices"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/maintenance"
)

// restoreOperationCases are the cases of the ports of a restore that runs as
// a durable operation: backup.RestoreOperations, backup.RestoreStaging and
// maintenance.FencedStore. A store that does not implement a port skips its
// cases.
func restoreOperationCases() []restoreCase {
	cases := make([]restoreCase, 0, 14+4*len(backup.RestoreSteps()))
	cases = append(cases, []restoreCase{
		{name: "a restore start stores the row and the operation together", test: testRestoreOpStart},
		{name: "a restore start refuses a second running restore", test: testRestoreOpStartRunning},
		{name: "a restore start refuses a bad ID or a foreign operation", test: testRestoreOpStartRefused},
		{name: "fenced restore writes record their steps", test: testRestoreOpFencedWrites},
		{name: "a stale, canceled or foreign fence cannot write a restore row", test: testRestoreOpFenceRefused},
		{name: "a failed restore end can leave maintenance mode", test: testRestoreOpFinishLeaves},
		{name: "ended operations fail their running restore rows", test: testRestoreOpFailEnded},
		{name: "live operations keep their running restore rows", test: testRestoreOpFailEndedLive},
		{name: "a fenced state change records its step", test: testFencedStateChange},
		{name: "a stale or canceled fence changes no state", test: testFencedStateRefused},
		{name: "a staging area has the name of its restore and token", test: testRestoreAreaStage},
		{name: "a staging area refuses a name that is not valid", test: testRestoreAreaRefusesBadName},
		{name: "the swap of a staging area checks the fence in its transaction", test: testRestoreAreaSwapFenced},
		{name: "cleanup drops the areas of its restore and of ended restores", test: testRestoreAreaCleanup},
	}...)

	return append(cases, restoreWorkerCases()...)
}

// restoreWorkerCases are the cases of a whole restore that the workers of
// two instances run on one database: a takeover in each step, a start of
// another instance during each step, a stop during the swap, and a cancel
// before and after the swap. Each case runs the restore service with the
// ports of the store, so the cases wait for the steps of the worker.
func restoreWorkerCases() []restoreCase {
	steps := backup.RestoreSteps()
	cases := make([]restoreCase, 0, 2*len(steps)+4)

	for _, step := range steps {
		cases = append(cases,
			restoreCase{name: "a takeover in the " + step + " step ends the restore once", test: pendingRestoreWorker},
			restoreCase{
				name: "a start of another instance during the " + step + " step changes nothing",
				test: pendingRestoreWorker,
			},
		)
	}

	return append(cases,
		restoreCase{
			name: "a holder stopped in the swap transaction cannot swap after a takeover", test: pendingRestoreWorker,
		},
		restoreCase{
			name: "a cancel before the swap ends the restore failed and leaves maintenance mode",
			test: pendingRestoreWorker,
		},
		restoreCase{name: "a cancel after the swap is refused and the restore succeeds", test: pendingRestoreWorker},
		restoreCase{
			name: "the cleanup of the next restore drops an area of an ended restore", test: pendingRestoreWorker,
		},
	)
}

// pendingRestoreWorker skips a case of restoreWorkerCases until the restore
// service runs its steps on a worker.
func pendingRestoreWorker(t *testing.T, _ RestoreFactory) {
	t.Helper()
	t.Skip("the restore service does not run its steps on a worker yet")
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
	fenced  maintenance.FencedStore
}

// restorePortsOf returns the restore operation ports of s, and skips the
// case when s does not implement each of them: the ports land together.
func restorePortsOf(t *testing.T, s RestoreStore) restorePorts {
	t.Helper()

	// RestoreStore still holds backup.RestoreRegistry, whose methods have the
	// same names with other signatures, so the assertions start from any.
	ops, opsOK := any(s).(backup.RestoreOperations)
	staging, stagingOK := any(s).(backup.RestoreStaging)
	fenced, fencedOK := any(s).(maintenance.FencedStore)

	if !opsOK || !stagingOK || !fencedOK {
		t.Skip("the store does not implement the restore operation ports")
	}

	return restorePorts{ops: ops, staging: staging, fenced: fenced}
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

func testRestoreOpFencedWrites(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	s := newStore(t)
	ops := restorePortsOf(t, s).ops
	durableOps := s.DurableOperationsPort()
	ctx := t.Context()
	id := mustStartRestoreOp(t, ops)
	held := mustClaimRestoreOp(t, durableOps, "instance-a")

	if err := ops.MarkRestoreApplying(ctx, held.Claim.Step(backup.RestoreStepPreBackup), id, "bkp-pre"); err != nil {
		t.Fatalf("mark applying: %v", err)
	}

	mustHaveStep(t, durableOps, held.ID, backup.RestoreStepPreBackup)

	report := backup.DevStripReport{
		Principals: nil, OtherPrincipals: 0, OtherCounts: nil, Approvers: nil, Warnings: nil,
		Classes: map[backup.DevRemovalClass]int64{backup.RemovedPrincipals: 2},
	}
	if err := ops.RecordRestoreReport(ctx, held.Claim.Step(backup.RestoreStepAfterApply), id, report); err != nil {
		t.Fatalf("record the report: %v", err)
	}

	mustHaveStep(t, durableOps, held.ID, backup.RestoreStepAfterApply)

	ended := now()

	row, err := ops.FinishRestore(ctx, held.Claim.Step(backup.RestoreStepFinish), backup.RestoreEnd{
		ID: id, State: backup.RestoreSucceeded, Error: "", EndedAt: ended, LeaveMaintenance: false,
	})
	if err != nil {
		t.Fatalf("finish: %v", err)
	}

	mustHaveStep(t, durableOps, held.ID, backup.RestoreStepFinish)

	if row.State != backup.RestoreSucceeded || row.PreBackupID != "bkp-pre" || !row.EndedAt.Equal(ended) ||
		row.DevReport == nil || row.DevReport.Classes[backup.RemovedPrincipals] != 2 {
		t.Fatalf("finished restore = %+v, want succeeded with the pre-backup, the report and the end time", row)
	}

	_, err = ops.FinishRestore(ctx, held.Claim.Step("finish-again"), backup.RestoreEnd{
		ID: id, State: backup.RestoreFailed, Error: "late", EndedAt: ended, LeaveMaintenance: false,
	})
	if !errors.Is(err, backup.ErrRestoreNotRunning) {
		t.Fatalf("second finish: error = %v, want ErrRestoreNotRunning", err)
	}

	mustLackStep(t, durableOps, held.ID, "finish-again")
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

			if err := ops.MarkRestoreApplying(t.Context(), fence, id, "bkp-pre"); !errors.Is(err, tc.want) {
				t.Fatalf("mark applying with %s: error = %v, want %v", tc.name, err, tc.want)
			}

			_, err := ops.FinishRestore(t.Context(), fence, backup.RestoreEnd{
				ID: id, State: backup.RestoreFailed, Error: "stale", EndedAt: now(), LeaveMaintenance: false,
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

	for _, leave := range []bool{true, false} {
		t.Run(map[bool]string{true: "leave", false: "stay"}[leave], func(t *testing.T) {
			t.Parallel()

			s := newStore(t)
			ops := restorePortsOf(t, s).ops
			id := mustStartRestoreOp(t, ops)
			held := mustClaimRestoreOp(t, s.DurableOperationsPort(), "instance-a")

			if err := s.SetState(t.Context(), maintenance.StateMaintenance); err != nil {
				t.Fatalf("enter maintenance mode: %v", err)
			}

			_, err := ops.FinishRestore(t.Context(), held.Claim.Step(backup.RestoreStepFinish), backup.RestoreEnd{
				ID: id, State: backup.RestoreFailed, Error: "corrupt", EndedAt: now(), LeaveMaintenance: leave,
			})
			if err != nil {
				t.Fatalf("finish failed: %v", err)
			}

			want := maintenance.StateMaintenance
			if leave {
				want = maintenance.StateNormal
			}

			if got, err := s.GetState(t.Context()); err != nil || got != want {
				t.Fatalf("installation state after the end = %s, %v, want %s", got, err, want)
			}
		})
	}
}

func testRestoreOpFailEnded(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	cases := []failEndedCase{
		{
			name: "canceled before maintenance mode", end: cancelAndRelease, entered: false, leave: true,
			text: backup.RestoreCanceledText, state: maintenance.StateNormal,
		},
		{
			name: "canceled in maintenance mode", end: cancelAndRelease, entered: true, leave: true,
			text: backup.RestoreCanceledText, state: maintenance.StateNormal,
		},
		{
			name: "canceled in maintenance mode with the hold", end: cancelAndRelease, entered: true, leave: false,
			text: backup.RestoreCanceledText, state: maintenance.StateMaintenance,
		},
		{
			name: "failed with no end write", end: failFinal, entered: true, leave: true,
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
// ends, whether it entered maintenance mode, whether the hold lets the fail
// leave it, and the error text and the state after the fail.
type failEndedCase struct {
	name    string
	end     func(t *testing.T, s durable.Store, held durable.Operation)
	entered bool
	leave   bool
	text    string
	state   maintenance.State
}

// runFailEnded starts and claims a restore on s, ends its operation as tc
// says, and checks what FailEndedRestores does to the row and the state.
func runFailEnded(t *testing.T, s RestoreStore, tc failEndedCase) {
	t.Helper()

	ops := restorePortsOf(t, s).ops
	durableOps := s.DurableOperationsPort()
	id := mustStartRestoreOp(t, ops)
	held := mustClaimRestoreOp(t, durableOps, "instance-a")

	if tc.entered {
		enterRecorded(t, s, held)
	}

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

// enterRecorded moves the installation of s to maintenance mode and records
// the enter-maintenance step of held, as the step of the restore does.
func enterRecorded(t *testing.T, s RestoreStore, held durable.Operation) {
	t.Helper()

	if err := s.SetState(t.Context(), maintenance.StateMaintenance); err != nil {
		t.Fatalf("enter maintenance mode: %v", err)
	}

	enter := held.Claim.Step(backup.RestoreStepEnterMaintenance)
	if err := s.DurableOperationsPort().RecordStep(t.Context(), enter, nil); err != nil {
		t.Fatalf("record enter-maintenance: %v", err)
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

func testFencedStateChange(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	s := newStore(t)
	fenced := restorePortsOf(t, s).fenced
	ops := restorePortsOf(t, s).ops
	durableOps := s.DurableOperationsPort()
	mustStartRestoreOp(t, ops)
	held := mustClaimRestoreOp(t, durableOps, "instance-a")
	ctx := t.Context()

	enter := held.Claim.Step(backup.RestoreStepEnterMaintenance)
	if err := fenced.SetStateIfFenced(ctx, maintenance.StateNormal, maintenance.StateMaintenance, enter); err != nil {
		t.Fatalf("fenced enter: %v", err)
	}

	mustHaveStep(t, durableOps, held.ID, backup.RestoreStepEnterMaintenance)

	if got, err := s.GetState(ctx); err != nil || got != maintenance.StateMaintenance {
		t.Fatalf("state after the fenced enter = %s, %v, want maintenance", got, err)
	}

	// A changed state records no step.
	leave := held.Claim.Step(backup.RestoreStepLeaveMaintenance)

	err := fenced.SetStateIfFenced(ctx, maintenance.StateNormal, maintenance.StateMaintenance, leave)
	if !errors.Is(err, maintenance.ErrStateChanged) {
		t.Fatalf("fenced change from a state that is not stored: error = %v, want ErrStateChanged", err)
	}

	mustLackStep(t, durableOps, held.ID, backup.RestoreStepLeaveMaintenance)

	if err := fenced.SetStateIfFenced(ctx, maintenance.StateMaintenance, maintenance.StateNormal, leave); err != nil {
		t.Fatalf("fenced leave: %v", err)
	}

	mustHaveStep(t, durableOps, held.ID, backup.RestoreStepLeaveMaintenance)

	if got, err := s.GetState(ctx); err != nil || got != maintenance.StateNormal {
		t.Fatalf("state after the fenced leave = %s, %v, want normal", got, err)
	}
}

func testFencedStateRefused(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	cases := []struct {
		name string
		end  func(t *testing.T, s durable.Store, held durable.Operation)
		want error
	}{
		{name: "replaced", end: replaceClaim, want: durable.ErrClaimLost},
		{name: "canceled", end: cancelOnly, want: durable.ErrCanceled},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := newStore(t)
			fenced := restorePortsOf(t, s).fenced
			durableOps := s.DurableOperationsPort()
			mustStartRestoreOp(t, restorePortsOf(t, s).ops)
			held := mustClaimRestoreOp(t, durableOps, "instance-a")
			tc.end(t, durableOps, held)

			enter := held.Claim.Step(backup.RestoreStepEnterMaintenance)

			err := fenced.SetStateIfFenced(t.Context(), maintenance.StateNormal, maintenance.StateMaintenance, enter)
			if !errors.Is(err, tc.want) {
				t.Fatalf("fenced enter of a %s claim: error = %v, want %v", tc.name, err, tc.want)
			}

			if got, stateErr := s.GetState(t.Context()); stateErr != nil || got != maintenance.StateNormal {
				t.Fatalf("state after a refused enter = %s, %v, want normal", got, stateErr)
			}

			mustLackStep(t, durableOps, held.ID, backup.RestoreStepEnterMaintenance)
		})
	}
}

// replaceClaim releases the claim of held, and a second instance claims the
// operation again with a new token.
func replaceClaim(t *testing.T, s durable.Store, held durable.Operation) {
	t.Helper()

	if err := s.Release(t.Context(), held.Claim); err != nil {
		t.Fatalf("release: %v", err)
	}

	mustClaimRestoreOp(t, s, "instance-b")
}

// cancelOnly asks for the cancel of the operation of held.
func cancelOnly(t *testing.T, s durable.Store, held durable.Operation) {
	t.Helper()

	if err := s.Cancel(t.Context(), held.ID); err != nil {
		t.Fatalf("cancel: %v", err)
	}
}

// stagedRestore is one restore of the staging cases: a destination store
// with its ports, a backup of a source store, and a started restore with a
// claim.
type stagedRestore struct {
	dst     PortableRestoreStore
	ops     backup.RestoreOperations
	staging backup.RestoreStaging
	sink    *portableSink
	sum     backup.Summary
	files   []backup.PortableFile
	id      backup.RestoreID
	held    durable.Operation
}

// newStagedRestore exports a seeded source store and starts a restore with
// a claim on a fresh destination store.
func newStagedRestore(t *testing.T, newStore RestoreFactory) *stagedRestore {
	t.Helper()

	src, dst := newStore(t), newStore(t)
	ops := restorePortsOf(t, dst).ops
	staging := restorePortsOf(t, dst).staging
	ps := portableRestoreStores(t, src, dst)

	seedBackupStore(t, ps.src)
	sink, sum, files := exportPortable(t, ps.src)

	id := mustStartRestoreOp(t, ops)
	held := mustClaimRestoreOp(t, dst.DurableOperationsPort(), "instance-a")

	return &stagedRestore{
		dst: ps.dst, ops: ops, staging: staging, sink: sink, sum: sum, files: files, id: id, held: held,
	}
}

// stage stages the backup into the area of the restore and token.
func (r *stagedRestore) stage(t *testing.T, token int64) backup.StagingArea {
	t.Helper()

	area, err := backup.NewStagingArea(r.id, token)
	if err != nil {
		t.Fatalf("staging area: %v", err)
	}

	if _, err := r.staging.StageRestore(t.Context(), area, r.sink.read, r.sum, r.files, StageAs(t, r.dst)); err != nil {
		t.Fatalf("stage into %s: %v", area, err)
	}

	return area
}

// areas lists the staging areas of the destination.
func (r *stagedRestore) areas(t *testing.T) []backup.StagingArea {
	t.Helper()

	got, err := r.staging.ListRestoreAreas(t.Context())
	if err != nil {
		t.Fatalf("list staging areas: %v", err)
	}

	return got
}

func testRestoreAreaStage(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	r := newStagedRestore(t, newStore)
	first := r.stage(t, r.held.Claim.Token)

	// A second stage into the same area replaces it, and a stage of another
	// token makes a second area.
	r.stage(t, r.held.Claim.Token)
	second := r.stage(t, r.held.Claim.Token+1)

	got := r.areas(t)
	if len(got) != 2 || !slices.Contains(got, first) || !slices.Contains(got, second) {
		t.Fatalf("staging areas = %+v, want %s and %s", got, first, second)
	}

	if rows := mustListRestores(t, r.dst); len(rows) != 1 || rows[0].State != backup.RestoreChecking {
		t.Fatalf("restores after the stages = %+v, want the one checking restore", rows)
	}
}

func testRestoreAreaRefusesBadName(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	r := newStagedRestore(t, newStore)

	// The service builds areas with NewStagingArea. Each adapter checks the
	// area again, so these values never reach a schema name or a path.
	bad := []backup.StagingArea{
		{Restore: "rst-../../escape", Token: 1},
		{Restore: "rst-aaaaaaaaaaaaaaaaaaaa\"; DROP SCHEMA public; --", Token: 1},
		{Restore: r.id, Token: 0},
		{Restore: r.id, Token: -1},
		{Restore: "", Token: 1},
	}

	for _, area := range bad {
		_, err := r.staging.StageRestore(t.Context(), area, r.sink.read, r.sum, r.files, StageAs(t, r.dst))
		if !errors.Is(err, backup.ErrInvalidRestoreID) {
			t.Fatalf("stage into %+v: error = %v, want ErrInvalidRestoreID", area, err)
		}

		_, err = r.staging.ApplyRestoreArea(t.Context(), r.held.Claim.Step(backup.RestoreStepSwap), area, false)
		if !errors.Is(err, backup.ErrInvalidRestoreID) {
			t.Fatalf("swap of %+v: error = %v, want ErrInvalidRestoreID", area, err)
		}
	}

	if got := r.areas(t); len(got) != 0 {
		t.Fatalf("staging areas after refused names = %+v, want none", got)
	}

	mustLackStep(t, r.dst.DurableOperationsPort(), r.held.ID, backup.RestoreStepSwap)
}

func testRestoreAreaSwapFenced(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	r := newStagedRestore(t, newStore)
	durableOps := r.dst.DurableOperationsPort()
	area := r.stage(t, r.held.Claim.Token)

	// A takeover: the first holder releases, and a second holder claims.
	if err := durableOps.Release(t.Context(), r.held.Claim); err != nil {
		t.Fatalf("release: %v", err)
	}

	taken := mustClaimRestoreOp(t, durableOps, "instance-b")

	_, err := r.staging.ApplyRestoreArea(t.Context(), r.held.Claim.Step(backup.RestoreStepSwap), area, false)
	if !errors.Is(err, durable.ErrClaimLost) {
		t.Fatalf("swap by the replaced holder: error = %v, want ErrClaimLost", err)
	}

	if got := r.areas(t); !slices.Contains(got, area) {
		t.Fatalf("staging areas after a refused swap = %+v, want %s kept", got, area)
	}

	mustLackStep(t, durableOps, taken.ID, backup.RestoreStepSwap)

	swap := taken.Claim.Step(backup.RestoreStepSwap)
	if _, err := r.staging.ApplyRestoreArea(t.Context(), swap, area, false); err != nil {
		t.Fatalf("swap by the live holder: %v", err)
	}

	mustHaveStep(t, durableOps, taken.ID, backup.RestoreStepSwap)

	if got := r.areas(t); slices.Contains(got, area) {
		t.Fatalf("staging areas after the swap = %+v, want %s gone", got, area)
	}

	// The restore row is in the control tables, so it stays across the swap.
	if row := mustGetRestore(t, r.dst, r.id); row.State != backup.RestoreChecking {
		t.Fatalf("restore after the swap = %+v, want checking", row)
	}
}

func testRestoreAreaCleanup(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	r := newStagedRestore(t, newStore)
	durableOps := r.dst.DurableOperationsPort()
	ctx := t.Context()

	// An area of an earlier holder of this restore, and the area that the
	// stage step of the live holder names.
	early := r.stage(t, r.held.Claim.Token)
	own := r.stage(t, r.held.Claim.Token+1)

	// An area of a restore that has no row here: another installation can
	// share the database, so cleanup keeps it.
	foreign, err := backup.NewStagingArea(backup.MintRestoreID(), 1)
	if err != nil {
		t.Fatalf("foreign area: %v", err)
	}

	opts := StageAs(t, r.dst)
	if _, stageErr := r.staging.StageRestore(ctx, foreign, r.sink.read, r.sum, r.files, opts); stageErr != nil {
		t.Fatalf("stage the foreign area: %v", stageErr)
	}

	cleanup := r.held.Claim.Step(backup.RestoreStepCleanup)

	dropped, err := r.staging.CleanRestoreAreas(ctx, cleanup, r.id, time.Second)
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}

	if len(dropped) != 2 || !slices.Contains(dropped, early) || !slices.Contains(dropped, own) {
		t.Fatalf("cleanup dropped %+v, want %s and %s", dropped, early, own)
	}

	if got := r.areas(t); len(got) != 1 || got[0] != foreign {
		t.Fatalf("staging areas after the cleanup = %+v, want only %s", got, foreign)
	}

	mustHaveStep(t, durableOps, r.held.ID, backup.RestoreStepCleanup)

	// A second cleanup of the same step changes nothing.
	if _, err := r.staging.CleanRestoreAreas(ctx, cleanup, r.id, time.Second); !errors.Is(err, durable.ErrStepDone) {
		t.Fatalf("second cleanup: error = %v, want ErrStepDone", err)
	}

	r.finishAndCleanNext(t, foreign)
}

// finishAndCleanNext ends the restore of r with an area left behind, starts
// the next restore, and proves that its cleanup drops the area of the ended
// restore and keeps foreign.
func (r *stagedRestore) finishAndCleanNext(t *testing.T, foreign backup.StagingArea) {
	t.Helper()

	durableOps := r.dst.DurableOperationsPort()
	ctx := t.Context()
	left := r.stage(t, r.held.Claim.Token+2)

	end := backup.RestoreEnd{
		ID: r.id, State: backup.RestoreFailed, Error: "stopped", EndedAt: now(), LeaveMaintenance: false,
	}

	if _, err := r.ops.FinishRestore(ctx, r.held.Claim.Step(backup.RestoreStepFinish), end); err != nil {
		t.Fatalf("finish: %v", err)
	}

	if err := durableOps.Complete(ctx, r.held.Claim); err != nil {
		t.Fatalf("complete: %v", err)
	}

	next := mustStartRestoreOp(t, r.ops)
	held := mustClaimRestoreOp(t, durableOps, "instance-a")

	dropped, err := r.staging.CleanRestoreAreas(ctx, held.Claim.Step(backup.RestoreStepCleanup), next, time.Second)
	if err != nil {
		t.Fatalf("cleanup of the next restore: %v", err)
	}

	if len(dropped) != 1 || dropped[0] != left {
		t.Fatalf("cleanup of the next restore dropped %+v, want %s of the ended restore", dropped, left)
	}

	if got := r.areas(t); len(got) != 1 || got[0] != foreign {
		t.Fatalf("staging areas after the next cleanup = %+v, want only %s", got, foreign)
	}
}
