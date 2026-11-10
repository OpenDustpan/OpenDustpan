package sqlite_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/history"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// TestPortableRestoreMakesPlanStale proves that a portable restore derives
// the state version of a unit from the restored rows: an apply job whose plan
// read the empty state of the unit cannot dispatch after a restore that
// brings a state version, and refuses with job.ErrPlanStale.
func TestPortableRestoreMakesPlanStale(t *testing.T) {
	t.Parallel()

	s := open(t)
	ctx := t.Context()

	fence, unit := stateWriteUnit(t, s)
	planned := plannedApplyJob(t, s, fence, unit, "")

	unitStateTofuFinalize(t, s, fence, unit)
	restoredVersion := unitStateTofuRelease(t, s, fence, unit)

	ps, ok := any(s).(storetest.PortableRestoreStore)
	if !ok {
		t.Fatal("the SQLite store does not export and stage the portable format")
	}

	files, sum, descs := storetest.PortableFiles(t, ps)

	_, stageErr := ps.StagePortable(
		ctx, storetest.PortableOpener(files), sum, descs, storetest.StageAs(t, ps),
	)
	if stageErr != nil {
		t.Fatalf("stage portable backup = %v", stageErr)
	}

	t.Cleanup(func() {
		if discardErr := ps.DiscardStaging(context.WithoutCancel(ctx)); discardErr != nil {
			t.Errorf("discard staging = %v", discardErr)
		}
	})

	if err := ps.ApplyStaging(ctx); err != nil {
		t.Fatalf("apply staged backup = %v", err)
	}

	if got := unitStateVersion(t, s, fence.Namespace, unit.UnitID()); got != restoredVersion {
		t.Fatalf("state version after the restore = %q, want the restored %q", got, restoredVersion)
	}

	ns, err := s.GetNamespace(ctx, fence.Namespace)
	if err != nil {
		t.Fatalf("GetNamespace = %v", err)
	}

	stored, err := s.Jobs().Get(ctx, planned.ID)
	if err != nil {
		t.Fatalf("Get = %v", err)
	}

	_, err = s.Jobs().Apply(ctx, job.Change{
		Job: stored.ID, Version: stored.Version, Event: job.EventDispatch, Wait: job.WaitNone, Reason: "",
		Dispatch: &durable.StartRequest{
			Key: job.PhaseKey(stored.ID, stored.Current), Kind: job.RunnerKind(stored.Spec.Pool),
			Version: 1, Input: nil,
		},
		PlanNotBefore: time.Time{}, Executor: "", Claim: nil, Delivery: nil, MergedAt: time.Time{},
		Approval: nil, End: nil,
	}, ns.Fence(), []audit.Entry{stateWriteEntry(unit)})
	if !errors.Is(err, job.ErrPlanStale) {
		t.Fatalf("dispatch after the restore = %v, want ErrPlanStale", err)
	}
}

// TestPortableRestoreIsNotAFinalVersion proves that a portable restore sets
// the current state version of a unit from the restored rows and leaves no
// final version: the restored state predates the delivery of any waiting job,
// so a write-back job that waited before the restore still waits.
func TestPortableRestoreIsNotAFinalVersion(t *testing.T) {
	t.Parallel()

	s := open(t)
	ctx := t.Context()

	fence, unit := stateWriteUnit(t, s)

	restoredVersion := unitStateTofuFinalize(t, s, fence, unit)
	delivered := awaitingWriteBackJob(t, s, fence, unit)

	ps, ok := any(s).(storetest.PortableRestoreStore)
	if !ok {
		t.Fatal("the SQLite store does not export and stage the portable format")
	}

	files, sum, descs := storetest.PortableFiles(t, ps)

	_, stageErr := ps.StagePortable(
		ctx, storetest.PortableOpener(files), sum, descs, storetest.StageAs(t, ps),
	)
	if stageErr != nil {
		t.Fatalf("stage portable backup = %v", stageErr)
	}

	t.Cleanup(func() {
		if discardErr := ps.DiscardStaging(context.WithoutCancel(ctx)); discardErr != nil {
			t.Errorf("discard staging = %v", discardErr)
		}
	})

	if err := ps.ApplyStaging(ctx); err != nil {
		t.Fatalf("apply staged backup = %v", err)
	}

	if got := unitStateVersion(t, s, fence.Namespace, unit.UnitID()); got != restoredVersion {
		t.Fatalf("state version after the restore = %q, want the restored %q", got, restoredVersion)
	}

	assertNoFinalVersion(t, s, fence.Namespace, unit.UnitID())
	assertWriteBackWaiting(t, s, delivered.ID)
}

// plannedApplyJob stores a queued apply job whose plan phase made a plan that
// read the state version sv of unit, and that waits to dispatch its apply
// phase.
func plannedApplyJob(
	t *testing.T, s *sqlite.Store, fence namespace.Fence, unit identity.Object, sv string,
) job.Job {
	t.Helper()

	phases, err := job.DefaultPhases(job.ExecutorRunner, job.OperationApply)
	if err != nil {
		t.Fatalf("DefaultPhases = %v", err)
	}

	at := time.UnixMilli(unitStateWriteAtMs).UTC()

	j := job.Job{
		ID: job.ID(ident.NewID(job.IDPrefix)), State: job.StateQueued, Wait: job.WaitNone, Reason: "", Current: 1,
		StartedBy: "al", Source: job.SourceAPI, Agent: "", AgentMode: "", BreakGlass: nil, Trigger: nil,
		Delivery: nil, Closed: nil, Verification: job.VerificationNone, IdempotencyKey: "", RequestHash: "",
		Version: 0, CreatedAt: time.Time{}, UpdatedAt: time.Time{}, StateChangedAt: time.Time{},
		Spec: job.Spec{
			Unit: unit, Operation: job.OperationApply, Purpose: job.PurposeNone, Executor: job.ExecutorRunner,
			Location: job.Location{
				Connection: "github", Repository: "acme/infra", Ref: "main", Directory: "network",
				Revision: stateWriteRevision,
			},
			Steps: []job.Step{{
				Name: "plan", Role: job.RoleTool, Phase: job.PhasePlan, Args: []string{"tofu", "plan"},
				Env: nil, Dir: "", Timeout: 0, AllowFailure: false, ChangesResources: false, Outputs: nil,
			}},
			Pool: "rpl-default", Profile: "prf-default", Edits: nil, Delivery: "", Defaulted: nil,
		},
		Phases: make([]job.Phase, len(phases)),
	}

	for i, kind := range phases {
		j.Phases[i].Kind = kind
	}

	j.Phases[0].StateVersion = sv
	j.Phases[0].Plan = &job.PlanRef{
		ID: "pln-" + string(j.ID), ItemKey: secret.NewItemKeyID(), StateVersion: sv,
		Parts: []job.PlanPart{
			{Kind: job.OutputPlanDisplay, Size: 2, SHA256: "d1", Secret: false, StoredSize: 0, StoredMD5: ""},
			{Kind: job.OutputPlanApply, Size: 2, SHA256: "a1", Secret: true, StoredSize: 0, StoredMD5: ""},
		},
		CreatedAt: at, ExpiresAt: history.ExpiresAt(at, history.DefaultRetention), RedactedAt: time.Time{},
	}

	stored, created, err := s.Jobs().Create(t.Context(), j, nil, fence, []audit.Entry{stateWriteEntry(unit)})
	if err != nil || !created {
		t.Fatalf("Create = created %v, %v; want a new job", created, err)
	}

	return stored
}
