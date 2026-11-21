package durable_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/token"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/durable/durabletest"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// TestRunnerRunsAnOperationStartedWithItsClaim proves that an operation that
// a start claimed for the runner runs on the caller with RunClaimed, and that
// no other claimer can take it while it runs.
func TestRunnerRunsAnOperationStartedWithItsClaim(t *testing.T) {
	t.Parallel()

	mem := durabletest.NewMem()
	store := mem.Store()
	rec := newRecorder()
	other := newTestRunner(t, store, "worker", rec.step("other", nil))

	var claimedByOther error

	step := rec.step("one", func(ctx context.Context, _ durable.StepContext) ([]byte, error) {
		claimedByOther = other.RunOnce(ctx)

		return nil, nil
	})

	r := newTestRunner(t, store, "starter", step)

	claimedAt := time.Now()

	op, err := mem.StartClaimedWith(t.Context(),
		durable.StartRequest{Key: "k", Kind: testKind, Version: 1, Input: nil}, r.StartClaim(), durabletest.Hooks{
			Before: nil, After: nil,
		})
	if err != nil {
		t.Fatalf("start with the claim of the runner: %v", err)
	}

	if op.State != durable.StateRunning || op.Claim.Owner != "starter" {
		t.Fatalf("started operation = %s, owner %q; want running with the claim of the starter", op.State,
			op.Claim.Owner)
	}

	if err := r.RunClaimed(t.Context(), op, claimedAt); err != nil {
		t.Fatalf("RunClaimed: %v", err)
	}

	if !errors.Is(claimedByOther, durable.ErrNoWork) {
		t.Fatalf("claim of another runner while the step ran = %v, want ErrNoWork", claimedByOther)
	}

	waitFor(t, store, op.ID, "succeeded", inState(durable.StateSucceeded))

	if n := rec.count("one"); n != 1 {
		t.Fatalf("step runs = %d, want 1", n)
	}
}

// TestRunClaimedRefusesAnOperationWithoutItsClaim proves that RunClaimed runs
// only a running operation of a kind of the runner with a claim of its owner,
// and changes nothing otherwise.
func TestRunClaimedRefusesAnOperationWithoutItsClaim(t *testing.T) {
	t.Parallel()

	store := durabletest.NewMem().Store()
	rec := newRecorder()
	r := newTestRunner(t, store, "starter", rec.step("one", nil))
	start(t, store, "claimed")

	claimed, err := store.Claim(t.Context(), durable.ClaimRequest{
		Owner: "worker", Kinds: []durable.Kind{testKind}, MaxVersions: nil, Lease: time.Minute, TakeCanceled: false,
	})
	if err != nil {
		t.Fatalf("claim for the worker: %v", err)
	}

	pending := start(t, store, "pending")

	unknown := claimed
	unknown.Kind = "other"
	unknown.Claim.Owner = "starter"

	for name, op := range map[string]durable.Operation{
		"a pending operation":          pending,
		"a claim of another owner":     claimed,
		"a kind that the runner lacks": unknown,
	} {
		if err := r.RunClaimed(t.Context(), op, time.Now()); !errors.Is(err, durable.ErrInvalid) {
			t.Errorf("RunClaimed of %s = %v, want ErrInvalid", name, err)
		}
	}

	if n := rec.count("one"); n != 0 {
		t.Fatalf("step runs = %d, want 0", n)
	}

	if got := mustGet(t, store, claimed.ID); got.State != durable.StateRunning || got.Claim.Owner != "worker" {
		t.Fatalf("operation after the refusals = %s, owner %q; want the claim of the worker", got.State,
			got.Claim.Owner)
	}
}

// claimEndWorkspaces is a workspace.ClaimWorkspaces fake with one live
// workspace.
type claimEndWorkspaces struct {
	ws workspace.Workspace
}

func (f claimEndWorkspaces) GetWorkspaceByID(
	_ context.Context, id workspace.ID,
) (workspace.Workspace, error) {
	if id != f.ws.ID {
		return workspace.Workspace{}, workspace.ErrNotFound
	}

	return f.ws, nil
}

// claimEndVersions is a workspace.ClaimVersions fake that knows one base
// version.
type claimEndVersions struct {
	ws   workspace.ID
	base workspace.StateVersionID
}

func (f claimEndVersions) GetWorkspaceStateVersion(
	_ context.Context, _ namespace.Name, ws workspace.ID, id workspace.StateVersionID,
) (workspace.StateVersion, error) {
	if ws != f.ws || id != f.base {
		return workspace.StateVersion{}, workspace.ErrStateVersionNotFound
	}

	return claimEndVersion(id, ws), nil
}

// claimEndVersion returns a finalized base state version with every field
// set explicitly.
func claimEndVersion(id workspace.StateVersionID, ws workspace.ID) workspace.StateVersion {
	return workspace.StateVersion{
		CreatedAt:       time.Time{},
		FinalizedAt:     time.Time{},
		UploadExpiresAt: time.Time{},
		Upload:          claimEndDigest(),
		Holder:          "",
		LockID:          "",
		ID:              id,
		Workspace:       ws,
		Lineage:         "",
		Status:          workspace.StatusFinalized,
		Content:         "",
		Outputs:         nil,
		MD5:             [16]byte{},
		Serial:          0,
		Size:            0,
		Force:           false,
		UnlockedWrite:   false,
		Sealed:          false,
		ClientEncrypted: workspace.ClientEncryptedUnknown,
		SensitiveMarks:  nil,
		RestoredFrom:    "",
		FenceBy:         "",
		Origin:          identity.StateOrigin{Job: "", Commit: ""},
	}
}

// claimEndFixture holds the registry and the IDs of the claim release
// test: two accepted claims on ws-1, for job-1 and job-2.
type claimEndFixture struct {
	registry *workspace.ClaimRegistry
	wsID     workspace.ID
	base     workspace.StateVersionID
	fence    namespace.Fence
	now      time.Time
}

// newClaimEndFixture registers one accepted claim for job-1 and one for
// job-2.
func newClaimEndFixture(t *testing.T) *claimEndFixture {
	t.Helper()

	now := time.UnixMilli(1_700_000_000_000).UTC()
	ns := namespace.Name("acme")
	wsID := workspace.ID("ws-1")
	base := workspace.StateVersionID("sv-1")
	fence := namespace.Fence{Namespace: ns, FailoverVersion: 1}

	registry, err := workspace.NewClaimRegistry(workspace.ClaimRegistryConfig{
		Workspaces: claimEndWorkspaces{ws: workspace.Workspace{
			ID:           wsID,
			NamespaceID:  namespace.ID("ns-acme"),
			CreatedAt:    now,
			UpdatedAt:    now,
			Lock:         workspace.Lock{LockedAt: time.Time{}, ID: "", Holder: "", Reason: ""},
			LastRelease:  workspace.NoRelease(),
			InCollection: false,
			Overrides:    namespace.Overrides{Forced: false, Unlocked: false},
			Origin:       identity.StateOrigin{Job: "", Commit: ""},
			Ref:          workspace.Ref{Namespace: ns, Name: "network"},
			Revision:     1,
		}},
		Versions: claimEndVersions{ws: wsID, base: base},
		Deleted:  nil,
		Manual:   nil,
		OnAccept: nil,
		Now:      func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("registry: %v", err)
	}

	f := &claimEndFixture{registry: registry, wsID: wsID, base: base, fence: fence, now: now}

	for _, jobID := range []string{"job-1", "job-2"} {
		f.register(t, jobID)
	}

	return f
}

// register registers one accepted end-fixture claim for jobID and fails
// the test on any error.
func (f *claimEndFixture) register(t *testing.T, jobID string) {
	t.Helper()

	_, err := f.registry.Register(t.Context(), workspace.RegisterClaimRequest{
		WorkspaceID: f.wsID,
		Base:        f.base,
		Addresses:   []string{"aws_instance." + jobID},
		Replace:     false,
		Principal:   "ada",
		JobID:       jobID,
		Trigger:     "",
	}, f.fence)
	if err != nil {
		t.Fatalf("register %s: %v", jobID, err)
	}
}

// endJobOne runs the durable operation of job-1 to success: the end of
// the work of the job.
func endJobOne(t *testing.T, now time.Time) {
	t.Helper()

	ctx := t.Context()

	mem := durabletest.NewMem()
	store := mem.Store()
	rec := newRecorder()
	r := newTestRunner(t, store, "starter", rec.step("one", nil))

	op, err := mem.StartClaimedWith(ctx,
		durable.StartRequest{Key: "job-1/0", Kind: testKind, Version: 1, Input: nil}, r.StartClaim(),
		durabletest.Hooks{Before: nil, After: nil})
	if err != nil {
		t.Fatalf("start the operation of job-1: %v", err)
	}

	if err := r.RunClaimed(ctx, op, now); err != nil {
		t.Fatalf("RunClaimed: %v", err)
	}

	waitFor(t, store, op.ID, "succeeded", inState(durable.StateSucceeded))
}

// checkClaimsEnded proves that the claim of job-1 is ended with an end
// time, while the claim of job-2 stays accepted.
func checkClaimsEnded(t *testing.T, f *claimEndFixture) {
	t.Helper()

	got := f.registry.List(t.Context(), f.wsID)
	if len(got) != 2 {
		t.Fatalf("list holds %d claims, want 2", len(got))
	}

	for i := range got {
		claim := &got[i]

		switch claim.JobID {
		case "job-1":
			if claim.State != workspace.ClaimEnded || claim.EndedAt.IsZero() {
				t.Errorf("claim of the ended job = %+v, want ended with an end time", claim)
			}
		case "job-2":
			if claim.State != workspace.ClaimAccepted || !claim.EndedAt.IsZero() {
				t.Errorf("claim of the running job = %+v, want accepted with no end time", claim)
			}
		default:
			t.Errorf("claim holds job %q, want job-1 or job-2", claim.JobID)
		}
	}
}

// claimEndDigest returns the zero upload digest of a fixture version.
func claimEndDigest() token.Digest {
	return token.Digest{}
}

// TestClaimReleaseAtJobEnd proves that ReleaseByJob marks each accepted
// claim of the ended job ended with an end time, while the claim of a job
// that still runs stays accepted.
func TestClaimReleaseAtJobEnd(t *testing.T) {
	t.Parallel()

	f := newClaimEndFixture(t)
	endJobOne(t, f.now)

	// ReleaseByJob ends the accepted claims of job-1 with an end time.
	if n := f.registry.ReleaseByJob(t.Context(), "job-1", f.now.Add(time.Minute)); n != 1 {
		t.Fatalf("released claims of job-1 = %d, want 1", n)
	}

	if n := f.registry.ReleaseByJob(t.Context(), "job-unknown", f.now.Add(time.Minute)); n != 0 {
		t.Fatalf("released claims of an unknown job = %d, want 0", n)
	}

	checkClaimsEnded(t, f)
}
