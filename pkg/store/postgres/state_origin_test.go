package postgres_test

import (
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/postgres"
)

// TestStateWriteOriginClosesWriteBack proves that a final write of another
// credential, which names no job and no commit, still closes a write-back job
// that waits for a state version, and leaves the job unverified.
func TestStateWriteOriginClosesWriteBack(t *testing.T) {
	t.Parallel()

	s := openUnitStateStore(t)

	fence, unit := unitStateTofuUnit(t, s)
	delivered := awaitingWriteBackJob(t, s, fence, unit)

	w := unlockedForWrites(t, s, fence, unit)
	sv := unitStatePending(t, w)

	if err := s.CreateStateVersion(t.Context(), sv, fence, unitStatePendingCap); err != nil {
		t.Fatalf("CreateStateVersion = %v", err)
	}

	unitStateFinalize(t, s, sv, fence, "")

	closed, err := s.Jobs().Get(t.Context(), delivered.ID)
	if err != nil {
		t.Fatalf("Get = %v", err)
	}

	if closed.State != job.StateSucceeded || closed.Closed == nil ||
		closed.Closed.VersionID != sv.ID.String() {
		t.Fatalf("closed job = %s, %+v; want succeeded, closed by %s", closed.State, closed.Closed, sv.ID)
	}

	if closed.Closed.Job != "" || closed.Closed.Commit != "" {
		t.Fatalf("origin of the close = job %q, commit %q; want an empty origin",
			closed.Closed.Job, closed.Closed.Commit)
	}

	if closed.Verification == job.VerificationVerified {
		t.Fatalf("verification of the closed job = %q, want not verified", closed.Verification)
	}
}

// awaitingWriteBackJob stores a write-back job of unit that delivered a direct
// push and waits for a state version.
func awaitingWriteBackJob(
	t *testing.T, s *postgres.Store, fence namespace.Fence, unit identity.Object,
) job.Job {
	t.Helper()

	phases, err := job.DefaultPhases(job.ExecutorWriteBack, job.OperationApply)
	if err != nil {
		t.Fatalf("DefaultPhases = %v", err)
	}

	j := job.Job{
		ID: job.ID(ident.NewID(job.IDPrefix)), State: job.StateQueued, Wait: job.WaitNone, Reason: "", Current: 0,
		StartedBy: "al", Source: job.SourceAPI, Agent: "", AgentMode: "", BreakGlass: nil, Trigger: nil,
		Delivery: nil, Closed: nil, Verification: job.VerificationNone, IdempotencyKey: "", RequestHash: "",
		Version: 0, CreatedAt: time.Time{}, UpdatedAt: time.Time{}, StateChangedAt: time.Time{}, NamespaceID: "",
		Spec: job.Spec{
			Unit: unit, Operation: job.OperationApply, Purpose: job.PurposeNone, Executor: job.ExecutorWriteBack,
			Location: job.Location{
				Connection: "github", Repository: "acme/infra", Ref: "main", Directory: "network",
				Revision: unitStateWriteRevision,
			},
			LocationSource: job.LocationSourceRequest,
			Steps:          nil,
			Pool:           "", Profile: "", Edits: []job.FileEdit{{
				Path: "network.tfvars.json", Keys: map[string][]byte{"size": []byte(`"large"`)},
			}},
			Delivery: job.DeliveryDirectPush, Defaulted: nil,
		},
		Phases: make([]job.Phase, len(phases)),
	}

	for i, kind := range phases {
		j.Phases[i].Kind = kind
	}

	start := &durable.StartRequest{
		Key: job.PhaseKey(j.ID, j.Current), Kind: job.KindWriteBack, Version: 1, Input: nil,
	}

	stored, created, err := s.Jobs().Create(t.Context(), j, start, fence, []audit.Entry{stateOriginEntry(unit)})
	if err != nil || !created {
		t.Fatalf("Create = created %v, %v; want a new job", created, err)
	}

	_, err = s.DurableOperationsPort().Claim(t.Context(), durable.ClaimRequest{
		Owner: "instance-a", Kinds: []durable.Kind{job.KindWriteBack}, MaxVersions: nil,
		Lease: time.Minute, TakeCanceled: false,
	})
	if err != nil {
		t.Fatalf("durable Claim = %v", err)
	}

	claimed, err := s.Jobs().Apply(t.Context(), job.Change{
		Job: stored.ID, Version: stored.Version, Event: job.EventClaim, Wait: job.WaitNone, Reason: "",
		Dispatch: nil, PlanNotBefore: time.Time{}, Executor: "instance-a", Claim: nil, Delivery: nil,
		MergedAt: time.Time{}, Approval: nil, End: nil,
	}, fence, []audit.Entry{stateOriginEntry(unit)})
	if err != nil {
		t.Fatalf("Apply(claim) = %v", err)
	}

	delivered, err := s.Jobs().Apply(t.Context(), job.Change{
		Job: claimed.ID, Version: claimed.Version, Event: job.EventDelivered, Wait: job.WaitNone, Reason: "",
		Dispatch: nil, PlanNotBefore: time.Time{}, Executor: "", Claim: nil,
		Delivery: &job.DeliveryResult{
			Delivery: job.DeliveryDirectPush, Commit: unitStateWriteRevision, Branch: "main", ReviewRequest: "",
			MergedAt: time.Time{}, DeliveredAt: time.UnixMilli(unitStateWriteAtMs).UTC(),
		},
		MergedAt: time.Time{}, Approval: nil, End: nil,
	}, fence, []audit.Entry{stateOriginEntry(unit)})
	if err != nil {
		t.Fatalf("Apply(delivered) = %v", err)
	}

	if delivered.State != job.StateAwaitingState {
		t.Fatalf("delivered job = %s, want awaiting-state", delivered.State)
	}

	return delivered
}

// unitStateWriteRevision is the commit that the spec of the job of these
// tests names.
const unitStateWriteRevision = "abc123"

// stateOriginEntry returns a valid audit entry of a job change on unit.
func stateOriginEntry(unit identity.Object) audit.Entry {
	return audit.Entry{
		At: time.UnixMilli(unitStateWriteAtMs).UTC(), Namespace: unit.Namespace(), Subject: "al", IDPSubject: "",
		TokenID: "", Action: string(job.ActionUnitRunApply), Object: unit.String(), Version: "", ValuePath: "",
		ValueCount: nil, Changes: nil, SourceIP: "", UserAgent: "", Channel: audit.ChannelAPI,
		Kind: audit.KindChange, Outcome: audit.OutcomeSuccess, RequestID: "", ObjectID: "", PrevHash: "",
		Hash: "", Sequence: 0, FenceVersion: 0, PrincipalKind: "", JobID: "", Reason: "",
	}
}
