package storetest

import (
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
)

// RunJobRestoreReconcile proves that a restore which leaves jobs with no
// durable operation gets reconciled. The backup holds the jobs of a namespace
// and none of the control data, so the store that the backup restores into has
// a dispatched job whose operation it does not know. Orphans names that job
// with Missing set, the reconcile ends it as failed with the reason of a
// restore, and a queued job of the same unit then dispatches in the same run,
// because the unit is free.
func RunJobRestoreReconcile(t *testing.T, newPair func(t *testing.T) (src, dst JobBackup)) {
	t.Helper()

	src, dst := newPair(t)
	seeded := seedRestoreJobs(t, src)

	files, sum, descs := PortableFiles(t, src.Store)

	staged := StageAs(t, dst.Store)

	_, err := dst.Store.StagePortable(t.Context(), PortableOpener(files), sum, descs, staged)
	if err != nil {
		t.Fatalf("stage the portable backup: %v", err)
	}

	if err = dst.Store.ApplyStaging(t.Context()); err != nil {
		t.Fatalf("apply the staged backup: %v", err)
	}

	orphans, damaged, err := dst.Jobs.Orphans(t.Context(), restoreOrphanPage)
	if err != nil || len(orphans) != 1 || orphans[0].Job.ID != seeded.lost || !orphans[0].Missing || len(damaged) != 0 {
		t.Fatalf(
			"Orphans after the restore = %+v, %v; want only %s with a missing operation",
			orphans,
			err,
			seeded.lost,
		)
	}

	reconcileRestored(t, dst, seeded)
}

// restoreOrphanPage is the page size of the orphan reads of the restore case.
const restoreOrphanPage = 10

// restoredJobs names the two jobs that the restore case makes: the dispatched
// job that holds the unit, and the queued job that waits for it.
type restoredJobs struct {
	lost    job.ID
	waiting job.ID
}

// seedRestoreJobs makes the namespace, the unit, a dispatched job that holds the
// unit and a queued job of the same unit in b, and returns their IDs.
func seedRestoreJobs(t *testing.T, b JobBackup) restoredJobs {
	t.Helper()

	ctx := t.Context()

	fence, err := b.Domain.CreateNamespace(ctx, "acme")
	if err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	unit, err := b.Domain.CreateUnit(ctx, fence, "opentofu", "network")
	if err != nil {
		t.Fatalf("create unit: %v", err)
	}

	first := restoreJob(unit)
	dispatch := durable.StartRequest{
		Key: job.PhaseKey(first.ID, 0), Kind: job.RunnerKind(first.Spec.Pool), Version: job.PhaseVersion, Input: nil,
	}

	stored, created, err := b.Jobs.Create(ctx, first, &dispatch, fence, []audit.Entry{restoreEntry(unit)})
	if err != nil || !created {
		t.Fatalf("create the dispatched job: created %v, error %v", created, err)
	}

	queued, created, err := b.Jobs.Create(ctx, restoreJob(unit), nil, fence, []audit.Entry{restoreEntry(unit)})
	if err != nil || !created {
		t.Fatalf("create the queued job: created %v, error %v", created, err)
	}

	return restoredJobs{lost: stored.ID, waiting: queued.ID}
}

// reconcileRestored runs the reconcile over the restored store b and checks
// that it ended the job lost, which has no operation, and dispatched the job
// waiting.
func reconcileRestored(t *testing.T, b JobBackup, seeded restoredJobs) {
	t.Helper()

	r, err := job.NewReconciler(job.ReconcilerConfig{
		Store: b.Jobs, Namespaces: b.Namespaces, QueueTimeout: 0, DispatchTimeout: 0, StateTimeout: 0,
		MaxPlanAge: 0, Approvers: gate.ApproverPolicy{Production: false, IsDev: nil}, Batch: 0, Now: nil, Log: nil,
	})
	if err != nil {
		t.Fatalf("new reconciler: %v", err)
	}

	res, err := r.Reconcile(t.Context())
	if err != nil || res.Ended != 1 || res.Dispatched != 1 {
		t.Fatalf("Reconcile after the restore = %+v, %v; want one ended job and one dispatch", res, err)
	}

	got, err := b.Jobs.Get(t.Context(), seeded.lost)
	if err != nil || got.State != job.StateFailed || got.Reason != job.ReasonRestored {
		t.Fatalf("job without an operation after the reconcile = %s, %q, %v; want failed with the reason %q",
			got.State, got.Reason, err, job.ReasonRestored)
	}

	got, err = b.Jobs.Get(t.Context(), seeded.waiting)
	if err != nil || got.State != job.StateDispatched {
		t.Fatalf("queued job after the reconcile = %s, %v; want dispatched", got.State, err)
	}

	checkNoOrphans(t, b)
}

// checkNoOrphans fails the test unless the restored store b holds no orphan
// and no damaged row.
func checkNoOrphans(t *testing.T, b JobBackup) {
	t.Helper()

	orphans, damaged, err := b.Jobs.Orphans(t.Context(), restoreOrphanPage)
	if err != nil || len(orphans) != 0 || len(damaged) != 0 {
		t.Fatalf("Orphans after the reconcile = %+v, damaged %+v, %v; want none", orphans, damaged, err)
	}
}

// restoreJob returns a queued runner job that refreshes unit. Its one phase
// changes state, so a dispatched job holds the unit.
func restoreJob(unit identity.Object) job.Job {
	kinds, err := job.DefaultPhases(job.ExecutorRunner, job.OperationRefresh)
	if err != nil {
		panic(err)
	}

	phases := make([]job.Phase, len(kinds))
	for i, k := range kinds {
		phases[i] = job.Phase{
			Kind: k, Operation: "", Claims: 0, Executor: "", Steps: nil, StateVersion: "", Plan: nil, Gates: nil,
			Approvals: nil, StartedAt: time.Time{}, FinishedAt: time.Time{},
		}
	}

	return job.Job{
		ID:          job.ID(ident.NewID(job.IDPrefix)),
		NamespaceID: "",
		Spec: job.Spec{
			Unit: unit, Operation: job.OperationRefresh, Purpose: "", Executor: job.ExecutorRunner,
			Location: job.Location{
				Connection: "github", Repository: "acme/infra", Ref: "main", Directory: "network", Revision: "abc123",
			},
			LocationSource: job.LocationSourceRequest,
			Steps: []job.Step{{
				Name: "refresh", Role: job.RoleTool, Phase: job.PhaseApply, Args: []string{"tofu", "apply"}, Env: nil,
				Dir: "", Timeout: 0, AllowFailure: false, ChangesResources: true, Outputs: nil,
			}},
			Pool: "rpl-default", Profile: "prf-default", Edits: nil, Delivery: "", Defaulted: nil,
		},
		State: job.StateQueued, Wait: job.WaitNone, Reason: "", Phases: phases, Current: 0, StartedBy: "al",
		Source: job.SourceAPI, Agent: "", AgentMode: "", BreakGlass: nil, Delivery: nil, Closed: nil,
		Verification: job.VerificationNone, IdempotencyKey: "", RequestHash: "", Version: 0, Trigger: nil,
		CreatedAt: time.Time{}, UpdatedAt: time.Time{}, StateChangedAt: time.Time{},
	}
}

// restoreEntry returns the audit entry of the start of a job on unit.
func restoreEntry(unit identity.Object) audit.Entry {
	return audit.Entry{
		At: time.Now().UTC(), Namespace: unit.Namespace(), Subject: "al", IDPSubject: "", TokenID: "",
		Action: string(job.ActionUnitRunApply), Object: unit.String(), Version: "", ValuePath: "", ValueCount: nil,
		Changes: nil, SourceIP: "", UserAgent: "", Channel: audit.ChannelAPI, Kind: audit.KindChange,
		Outcome: audit.OutcomeSuccess, RequestID: "", ObjectID: "", PrevHash: "", Hash: "", Sequence: 0,
		FenceVersion: 0, Reason: "", PrincipalKind: "", JobID: "",
	}
}
