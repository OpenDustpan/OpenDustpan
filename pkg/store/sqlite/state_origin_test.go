package sqlite_test

import (
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/internal/token"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
	"opendustpan.org/opendustpan/pkg/workspace"
)

const (
	// stateWriteAtMs is the time that the writes of these tests carry.
	stateWriteAtMs = 1_700_000_000_000
	// stateWriteRevision is the commit that the spec of the job of these
	// tests names.
	stateWriteRevision = "abc123"
	// stateWriteContent is the content of the state version of these tests.
	stateWriteContent = "v1"
)

// stateWriteOriginCase is one row of TestStateWriteOrigin: the name of the
// case, and whether the write declares a job token.
type stateWriteOriginCase struct {
	name string
	job  bool
}

// TestStateWriteOrigin proves that the origin of a state write travels from
// the write to the last final state version of the unit: a write that a job
// token declared names its job and takes the commit of the spec of that job,
// and a write of any other credential names neither.
func TestStateWriteOrigin(t *testing.T) {
	t.Parallel()

	tests := []stateWriteOriginCase{
		{"a job token", true},
		{"another credential", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			checkStateWriteOrigin(t, tc)
		})
	}
}

// checkStateWriteOrigin writes one state version of a fresh unit under the
// credential of tc and checks the origin of the final version: the job of the
// write and the commit of the spec of that job for a job token, and an empty
// origin for any other credential.
func checkStateWriteOrigin(t *testing.T, tc stateWriteOriginCase) {
	t.Helper()

	s := open(t)

	fence, unit := stateWriteUnit(t, s)
	w := unlockedForWrites(t, s, fence, unit)

	want := identity.StateOrigin{Job: "", Commit: ""}

	if tc.job {
		want = identity.StateOrigin{
			Job: string(stateWriteJob(t, s, fence, unit)), Commit: stateWriteRevision,
		}
	}

	sv := pendingStateWrite(t, w, want.Job)
	if err := s.CreateStateVersion(t.Context(), sv, fence, stateWriteCap); err != nil {
		t.Fatalf("CreateStateVersion = %v", err)
	}

	final := finalizeStateWrite(t, s, sv, fence)

	got, ok, err := s.Jobs().FinalVersion(t.Context(), fence.Namespace, unit.UnitID())
	if err != nil || !ok {
		t.Fatalf("FinalVersion = %+v, %v, %v; want the version of the finalize", got, ok, err)
	}

	if got.VersionID != final.ID.String() {
		t.Fatalf("final version = %s, want the finalized write %s", got.VersionID, final.ID)
	}

	if got.Job != job.ID(want.Job) || got.Commit != want.Commit {
		t.Fatalf("origin of the final version = job %q, commit %q; want job %q, commit %q",
			got.Job, got.Commit, want.Job, want.Commit)
	}
}

// TestStateWriteOriginClosesWriteBack proves that a final write of another
// credential, which names no job and no commit, still closes a write-back job
// that waits for a state version, and leaves the job unverified.
func TestStateWriteOriginClosesWriteBack(t *testing.T) {
	t.Parallel()

	s := open(t)

	fence, unit := stateWriteUnit(t, s)
	delivered := awaitingWriteBackJob(t, s, fence, unit)

	w := unlockedForWrites(t, s, fence, unit)
	sv := pendingStateWrite(t, w, "")

	if err := s.CreateStateVersion(t.Context(), sv, fence, stateWriteCap); err != nil {
		t.Fatalf("CreateStateVersion = %v", err)
	}

	finalizeStateWrite(t, s, sv, fence)

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
	t *testing.T, s *sqlite.Store, fence namespace.Fence, unit identity.Object,
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
		Version: 0, CreatedAt: time.Time{}, UpdatedAt: time.Time{}, StateChangedAt: time.Time{},
		Spec: job.Spec{
			Unit: unit, Operation: job.OperationApply, Purpose: job.PurposeNone, Executor: job.ExecutorWriteBack,
			Location: job.Location{
				Connection: "github", Repository: "acme/infra", Ref: "main", Directory: "network",
				Revision: stateWriteRevision,
			},
			Steps: nil,
			Pool:  "", Profile: "", Edits: []job.FileEdit{{
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

	stored, created, err := s.Jobs().Create(t.Context(), j, start, fence, []audit.Entry{stateWriteEntry(unit)})
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
	}, fence, []audit.Entry{stateWriteEntry(unit)})
	if err != nil {
		t.Fatalf("Apply(claim) = %v", err)
	}

	delivered, err := s.Jobs().Apply(t.Context(), job.Change{
		Job: claimed.ID, Version: claimed.Version, Event: job.EventDelivered, Wait: job.WaitNone, Reason: "",
		Dispatch: nil, PlanNotBefore: time.Time{}, Executor: "", Claim: nil,
		Delivery: &job.DeliveryResult{
			Delivery: job.DeliveryDirectPush, Commit: stateWriteRevision, Branch: "main", ReviewRequest: "",
			MergedAt: time.Time{}, DeliveredAt: time.UnixMilli(stateWriteAtMs).UTC(),
		},
		MergedAt: time.Time{}, Approval: nil, End: nil,
	}, fence, []audit.Entry{stateWriteEntry(unit)})
	if err != nil {
		t.Fatalf("Apply(delivered) = %v", err)
	}

	if delivered.State != job.StateAwaitingState {
		t.Fatalf("delivered job = %s, want awaiting-state", delivered.State)
	}

	return delivered
}

// stateWriteCap bounds the pending versions of these tests, which declare one.
const stateWriteCap = 10

// stateWriteUnit creates the namespace acme with the workspace network, and
// returns the fence of the namespace and its unit.
func stateWriteUnit(t *testing.T, s *sqlite.Store) (namespace.Fence, identity.Object) {
	t.Helper()

	d := &jobDomain{s: s}

	fence, err := d.CreateNamespace(t.Context(), "acme")
	if err != nil {
		t.Fatalf("CreateNamespace = %v", err)
	}

	unit, err := d.CreateUnit(t.Context(), fence, "opentofu", "network")
	if err != nil {
		t.Fatalf("CreateUnit = %v", err)
	}

	return fence, unit
}

// unlockedForWrites turns on the unlocked-write switch of the namespace and of
// the unit, and returns the unit as stored.
func unlockedForWrites(
	t *testing.T, s *sqlite.Store, fence namespace.Fence, unit identity.Object,
) workspace.Workspace {
	t.Helper()

	ov := namespace.Overrides{Forced: false, Unlocked: true}

	if err := s.SetOverrides(t.Context(), fence.Namespace, ov, fence); err != nil {
		t.Fatalf("SetOverrides = %v", err)
	}

	w, err := s.GetWorkspaceByID(t.Context(), workspace.ID(unit.UnitID()))
	if err != nil {
		t.Fatalf("GetWorkspaceByID = %v", err)
	}

	w.Overrides = ov
	w.Revision++
	w.UpdatedAt = time.UnixMilli(stateWriteAtMs).UTC()

	if err := s.SaveWorkspace(t.Context(), w, fence); err != nil {
		t.Fatalf("SaveWorkspace = %v", err)
	}

	return w
}

// stateWriteJob stores a queued plan job of the unit whose spec names
// stateWriteRevision, and returns its ID.
func stateWriteJob(t *testing.T, s *sqlite.Store, fence namespace.Fence, unit identity.Object) job.ID {
	t.Helper()

	phases, err := job.DefaultPhases(job.ExecutorRunner, job.OperationPlan)
	if err != nil {
		t.Fatalf("DefaultPhases = %v", err)
	}

	j := job.Job{
		ID: job.ID(ident.NewID(job.IDPrefix)), State: job.StateQueued, Wait: job.WaitNone, Reason: "", Current: 0,
		StartedBy: "al", Source: job.SourceAPI, Agent: "", AgentMode: "", BreakGlass: nil, Trigger: nil,
		Delivery: nil, Closed: nil, Verification: job.VerificationNone, IdempotencyKey: "", RequestHash: "",
		Version: 0, CreatedAt: time.Time{}, UpdatedAt: time.Time{}, StateChangedAt: time.Time{},
		Spec: job.Spec{
			Unit: unit, Operation: job.OperationPlan, Purpose: job.PurposeNone, Executor: job.ExecutorRunner,
			Location: job.Location{
				Connection: "", Repository: "", Ref: "", Directory: "", Revision: stateWriteRevision,
			},
			Steps: []job.Step{{
				Name: "plan", Role: job.RoleTool, Phase: job.PhasePlan, Args: []string{"tofu", "plan"}, Env: nil,
				Dir: "", Timeout: 0, AllowFailure: false, ChangesResources: false, Outputs: nil,
			}},
			Pool: "rpl-default", Profile: "prf-default", Edits: nil, Delivery: "", Defaulted: nil,
		},
		Phases: make([]job.Phase, len(phases)),
	}

	for i, kind := range phases {
		j.Phases[i].Kind = kind
	}

	stored, created, err := s.Jobs().Create(t.Context(), j, nil, fence, []audit.Entry{stateWriteEntry(unit)})
	if err != nil || !created {
		t.Fatalf("Create = created %v, %v; want a new job", created, err)
	}

	return stored.ID
}

// stateWriteEntry returns a valid audit entry of a job start on unit.
func stateWriteEntry(unit identity.Object) audit.Entry {
	return audit.Entry{
		At: time.UnixMilli(stateWriteAtMs).UTC(), Namespace: unit.Namespace(), Subject: "al", IDPSubject: "",
		TokenID: "", Action: string(job.ActionUnitRunPlan), Object: unit.String(), Version: "", ValuePath: "",
		ValueCount: nil, Changes: nil, SourceIP: "", UserAgent: "", Channel: audit.ChannelAPI,
		Kind: audit.KindChange, Outcome: audit.OutcomeSuccess, RequestID: "", ObjectID: "", PrevHash: "",
		Hash: "", Sequence: 0, FenceVersion: 0, PrincipalKind: "", JobID: "", Reason: "",
	}
}

// pendingStateWrite returns the state version that an unlocked write of the
// workspace declares, under the job jobID, or under no job when jobID is
// empty.
func pendingStateWrite(t *testing.T, w workspace.Workspace, jobID string) workspace.StateVersion {
	t.Helper()

	tok, err := token.New()
	if err != nil {
		t.Fatalf("new upload capability: %v", err)
	}

	at := time.UnixMilli(stateWriteAtMs).UTC()

	return workspace.StateVersion{
		CreatedAt: at, FinalizedAt: time.Time{}, UploadExpiresAt: at.Add(workspace.UploadWindow),
		Upload: tok.Digest(), ID: workspace.StateVersionID(ident.NewID("sv-")), Workspace: w.ID,
		Holder: "ci", LockID: "", Lineage: "l1", Status: workspace.StatusPending, Content: "", Outputs: nil,
		// The store does not check the MD5 of a state version, so a fixed value is enough.
		MD5: [16]byte{1, 2, 3}, Serial: 1, Size: 0, Force: false, UnlockedWrite: true,
		Sealed: true, SensitiveMarks: nil, RestoredFrom: "", FenceBy: "",
		Origin: identity.StateOrigin{Job: jobID, Commit: ""},
	}
}

// finalizeStateWrite records the attempt of sv and finalizes it, so the
// version becomes the current and the last final version of its unit.
func finalizeStateWrite(
	t *testing.T, s *sqlite.Store, sv workspace.StateVersion, fence namespace.Fence,
) workspace.StateVersion {
	t.Helper()

	ns, err := s.GetNamespace(t.Context(), fence.Namespace)
	if err != nil {
		t.Fatalf("GetNamespace = %v", err)
	}

	sv.Status = workspace.StatusFinalized
	sv.FinalizedAt = time.UnixMilli(stateWriteAtMs + 1000).UTC()
	sv.Content = blob.Key("namespaces/" + ns.ID.String() + "/tofu/" + sv.Workspace.String() + "/" +
		sv.ID.String() + "/a1")
	sv.Size = int64(len(stateWriteContent))

	if _, err := s.RecordUploadAttempt(t.Context(), sv.ID, sv.Content, fence); err != nil {
		t.Fatalf("RecordUploadAttempt = %v", err)
	}

	if err := s.FinalizeStateVersion(t.Context(), sv, fence); err != nil {
		t.Fatalf("FinalizeStateVersion = %v", err)
	}

	return sv
}
