package storetest

import (
	"context"
	"slices"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/job/jobtest"
)

// JobBackup is what a SQL store gives the job backup case: the store that
// exports and stages the portable format, its job store, and the way to make
// a namespace and a unit.
type JobBackup struct {
	Store interface {
		PortableStore
		// ApplyStaging swaps the staged data into the live data.
		ApplyStaging(ctx context.Context) error
	}
	Jobs   job.Store
	Domain jobtest.Domain
	// Namespaces reads the namespaces of the database, for the fence of a
	// reconcile of its jobs.
	Namespaces job.NamespaceReader
}

// RunJobBackup proves that the values a job took by default survive a
// portable export, the stage of that export and the apply of the stage. The
// store keeps them in the body of the job row, which the backup copies as it
// is, so a restore must give back the list that the start wrote.
func RunJobBackup(t *testing.T, newBackup func(t *testing.T) JobBackup) {
	t.Helper()

	b := newBackup(t)
	ctx := t.Context()

	fence, err := b.Domain.CreateNamespace(ctx, "acme")
	if err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	unit, err := b.Domain.CreateUnit(ctx, fence, "opentofu", "network")
	if err != nil {
		t.Fatalf("create unit: %v", err)
	}

	want := map[job.ID][]string{}

	for _, list := range [][]string{nil, {"pool"}, {"pool", "location", "profile"}} {
		j := job.Job{
			ID:          job.ID(ident.NewID(job.IDPrefix)),
			NamespaceID: "",
			Spec: job.Spec{
				Unit: unit, Operation: job.OperationPlan, Purpose: "", Executor: job.ExecutorRunner,
				Location: job.Location{
					Connection: "github", Repository: "acme/infra", Ref: "main", Directory: "network",
					Revision: "abc123",
				},
				Steps: []job.Step{{
					Name: "plan", Role: job.RoleTool, Phase: job.PhasePlan, Args: []string{"tofu", "plan"}, Env: nil,
					Dir: "", Timeout: 0, AllowFailure: false, ChangesResources: false, Outputs: nil,
				}},
				Pool: "rpl-default", Profile: "prf-default", Edits: nil, Delivery: "", Defaulted: list,
			},
			State: job.StateQueued, Wait: job.WaitNone, Reason: "",
			Phases: []job.Phase{{
				Kind: job.PhasePlan, Operation: "", Claims: 0, Executor: "", Steps: nil, StateVersion: "",
				Plan: nil, Gates: nil, Approvals: nil, StartedAt: time.Time{}, FinishedAt: time.Time{},
			}},
			Current: 0, StartedBy: "al", Source: job.SourceAPI, Agent: "", AgentMode: "", BreakGlass: nil,
			Delivery: nil, Closed: nil, Verification: job.VerificationNone, IdempotencyKey: "", RequestHash: "",
			Version: 0, Trigger: nil, CreatedAt: time.Time{}, UpdatedAt: time.Time{},
			StateChangedAt: time.Time{},
		}

		entry := audit.Entry{
			At: time.Now().UTC(), Namespace: unit.Namespace(), Subject: "al", IDPSubject: "", TokenID: "",
			Action: string(job.ActionUnitRunPlan), Object: unit.String(), Version: "", ValuePath: "",
			ValueCount: nil, Changes: nil, SourceIP: "", UserAgent: "", Channel: audit.ChannelAPI,
			Kind: audit.KindChange, Outcome: audit.OutcomeSuccess, RequestID: "", ObjectID: "", PrevHash: "",
			Hash: "", Sequence: 0, FenceVersion: 0, Reason: "", PrincipalKind: "", JobID: "",
		}

		stored, created, createErr := b.Jobs.Create(ctx, j, nil, fence, []audit.Entry{entry})
		if createErr != nil || !created {
			t.Fatalf("create job: created %v, error %v", created, createErr)
		}

		want[stored.ID] = list
	}

	files, sum, descs := PortableFiles(t, b.Store)

	_, err = b.Store.StagePortable(ctx, PortableOpener(files), sum, descs, StageAs(t, b.Store))
	if err != nil {
		t.Fatalf("stage the portable backup: %v", err)
	}

	err = b.Store.ApplyStaging(ctx)
	if err != nil {
		t.Fatalf("apply the staged backup: %v", err)
	}

	for id, list := range want {
		got, getErr := b.Jobs.Get(ctx, id)
		if getErr != nil {
			t.Fatalf("get job %s after the restore: %v", id, getErr)
		}

		if !slices.Equal(got.Spec.Defaulted, list) {
			t.Errorf("job %s: Spec.Defaulted after the restore = %v, want %v", id, got.Spec.Defaulted, list)
		}
	}
}
