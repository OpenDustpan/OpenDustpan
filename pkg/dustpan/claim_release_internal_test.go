package dustpan

import (
	"context"
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/token"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/workspace"
	"opendustpan.org/opendustpan/pkg/workspace/workspacetest"
)

var errClaimReleaseInner = errors.New("the inner claimer failed")

// fakeReleaseClaimer is a job.Claimer whose Finish and Release return err.
type fakeReleaseClaimer struct {
	job.Claimer

	err error
}

func (f fakeReleaseClaimer) Finish(context.Context, job.PhaseClaim, job.Outcome) error { return f.err }

func (f fakeReleaseClaimer) Release(context.Context, job.PhaseClaim) error { return f.err }

// fakeReleaseJobs is a claimJobReader with one state for every job.
type fakeReleaseJobs struct {
	state job.State
	err   error
}

func (f fakeReleaseJobs) Get(_ context.Context, id job.ID) (job.Job, error) {
	if f.err != nil {
		return job.Job{}, f.err
	}

	var j job.Job
	j.ID = id
	j.State = f.state

	return j, nil
}

// releaseFixture registers one claim for job-1 and one for job-2 in acme.
func releaseFixture(t *testing.T) (*workspace.ClaimRegistry, namespace.Fence, workspace.ID) {
	t.Helper()

	now := time.UnixMilli(1_700_000_000_000).UTC()
	ns := namespace.Namespace{
		CreatedAt: now, ID: namespace.NewID(), Name: "acme", PrimaryInstallation: "local",
		Overrides: namespace.Overrides{Forced: false, Unlocked: false}, FailoverVersion: 1,
	}
	fence := ns.Fence()
	b := workspacetest.NewMemoryClaimBackend()

	if err := b.CreateNamespace(t.Context(), ns); err != nil {
		t.Fatalf("namespace: %v", err)
	}

	w := workspace.Workspace{
		NamespaceID: ns.ID, CreatedAt: now, UpdatedAt: now,
		Lock:        workspace.Lock{LockedAt: time.Time{}, ID: "", Holder: "", Reason: ""},
		LastRelease: workspace.NoRelease(), InCollection: false,
		Overrides: namespace.Overrides{Forced: false, Unlocked: false},
		Origin:    identity.StateOrigin{Job: "", Commit: ""}, ID: "ws-1",
		Ref: workspace.Ref{Namespace: ns.Name, Name: "network"}, Revision: 1,
	}
	if err := b.CreateWorkspace(t.Context(), w, fence); err != nil {
		t.Fatalf("workspace: %v", err)
	}

	sv := workspace.StateVersion{
		CreatedAt: time.Time{}, FinalizedAt: time.Time{}, UploadExpiresAt: time.Time{}, Upload: token.Digest{},
		Holder: "", LockID: "", ID: "sv-1", Workspace: "ws-1", Lineage: "lin", Status: workspace.StatusPending,
		Content: "", Outputs: nil, MD5: [16]byte{}, Serial: 1, Size: 0, Force: false, UnlockedWrite: false,
		Sealed: false, ClientEncrypted: workspace.ClientEncryptedUnknown, SensitiveMarks: nil, RestoredFrom: "",
		FenceBy: "", Origin: identity.StateOrigin{Job: "", Commit: ""},
	}
	if err := b.CreateStateVersion(t.Context(), sv, fence, workspace.DefaultMaxPendingVersions); err != nil {
		t.Fatalf("version: %v", err)
	}

	registry, err := workspace.NewClaimRegistry(workspace.ClaimRegistryConfig{
		Workspaces: b, Versions: b, Deleted: nil, Manual: nil, Store: b, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("registry: %v", err)
	}

	for _, jobID := range []string{"job-1", "job-2"} {
		_, err = registry.Register(t.Context(), &workspace.RegisterClaimRequest{
			WorkspaceID: "ws-1", Base: "sv-1", Addresses: []string{"aws_instance." + jobID}, Replace: false,
			Principal: "ada", JobID: jobID, Trigger: "",
			Audit: audit.Entry{
				At: now, Namespace: ns.Name, Subject: "ada", IDPSubject: "", TokenID: "",
				Action: string(identity.ActionUnitClaimRegister), Object: "unit:network", Version: "", ValuePath: "",
				ValueCount: nil, Changes: nil, SourceIP: "", UserAgent: "", Channel: audit.ChannelAPI,
				Kind: audit.KindChange, Outcome: audit.OutcomeSuccess, RequestID: "", ObjectID: "", PrevHash: "",
				Hash: "", Sequence: 0, FenceVersion: 0, PrincipalKind: "", JobID: "", Reason: "", Origin: nil,
			},
		}, fence)
		if err != nil {
			t.Fatalf("register %s: %v", jobID, err)
		}
	}

	return registry, fence, "ws-1"
}

// states returns the state of the claim of each job of the workspace.
func releaseStates(t *testing.T, r *workspace.ClaimRegistry, ws workspace.ID) map[string]workspace.ClaimState {
	t.Helper()

	list, err := r.List(t.Context(), ws)
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	out := make(map[string]workspace.ClaimState, len(list))
	for i := range list {
		out[list[i].JobID] = list[i].State
	}

	return out
}

// TestClaimReleasingClaimer proves that the claims of a job end when its job
// reaches a terminal state, through Finish and through Release, and in no
// other case.
func TestClaimReleasingClaimer(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		inner    error
		jobs     fakeReleaseJobs
		useFin   bool
		wantErr  bool
		wantJob1 workspace.ClaimState
	}{
		{
			"finish of a terminal job",
			nil,
			fakeReleaseJobs{state: job.StateSucceeded, err: nil},
			true,
			false,
			workspace.ClaimEnded,
		},
		{
			"release of a terminal job",
			nil,
			fakeReleaseJobs{state: job.StateFailed, err: nil},
			false,
			false,
			workspace.ClaimEnded,
		},
		{
			"finish of a running job",
			nil,
			fakeReleaseJobs{state: job.StateRunning, err: nil},
			true,
			false,
			workspace.ClaimAccepted,
		},
		{
			"inner error ends nothing", errClaimReleaseInner,
			fakeReleaseJobs{state: job.StateSucceeded, err: nil},
			true, true,
			workspace.ClaimAccepted,
		},
		{
			"job that cannot be read", nil,
			fakeReleaseJobs{state: job.StateSucceeded, err: job.ErrNotFound},
			true, true,
			workspace.ClaimAccepted,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			registry, fence, ws := releaseFixture(t)
			claimer := claimReleasingClaimer{
				Claimer: fakeReleaseClaimer{Claimer: nil, err: c.inner},
				jobs:    c.jobs, claims: registry,
				now: func() time.Time { return time.UnixMilli(1_700_000_060_000).UTC() },
			}
			var noClaim durable.Claim // The fake claimer ignores it.

			pc := job.PhaseClaim{Job: "job-1", Phase: 0, Claim: noClaim, Fence: fence}

			var err error
			if c.useFin {
				err = claimer.Finish(t.Context(), pc, job.Outcome{Succeeded: true, Message: ""})
			} else {
				err = claimer.Release(t.Context(), pc)
			}

			if (err != nil) != c.wantErr {
				t.Fatalf("call = %v, want error %v", err, c.wantErr)
			}

			got := releaseStates(t, registry, ws)
			if got["job-1"] != c.wantJob1 || got["job-2"] != workspace.ClaimAccepted {
				t.Fatalf("claim states = %v, want job-1 %s and job-2 accepted", got, c.wantJob1)
			}
		})
	}
}
