//go:build e2e

package e2e

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/dustpan"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/postgres"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// errNoJobStore reports a store that keeps no jobs.
var errNoJobStore = errors.New("the store keeps no jobs")

// JobSeed names the rows that CreateJob made.
type JobSeed struct {
	JobID  string
	UnitID string
}

// jobStoreOptions returns the option that gives the in-process server the job
// store of st.
func jobStoreOptions(st serverStore) []dustpan.Option {
	if jobs, ok := jobsOf(st); ok {
		return []dustpan.Option{dustpan.WithJobStore(jobs)}
	}

	return nil
}

// jobsOf returns the job store of st, and false for a store that keeps none.
//
//nolint:ireturn // The harness takes the port of whichever store the run uses.
func jobsOf(st any) (job.Store, bool) {
	switch s := st.(type) {
	case *sqlite.Store:
		return s.Jobs(), true
	case *postgres.Store:
		return s.Jobs(), true
	default:
		return nil, false
	}
}

// CreateJob makes an OpenTofu workspace called name in the scenario namespace
// and a queued runner job that plans it, and returns their IDs. No route
// starts a job yet, so this is a direct store write. The binary server owns
// its database, so the write runs while the binary is stopped, on a handle
// that is closed before the binary starts again. The in-process server
// writes through its own store.
func (e *Env) CreateJob(ctx context.Context, tb testing.TB, name string) JobSeed {
	tb.Helper()

	var seed JobSeed

	write := func(st namespace.Store) error {
		var err error

		seed, err = seedJob(ctx, st, name)

		return err
	}

	if e.binPath == "" {
		if err := write(e.inprocess); err != nil {
			tb.Fatalf("create job: %v", err)
		}

		return seed
	}

	e.StopServer(ctx, tb)
	e.writeStopped(ctx, tb, write)
	e.StartServer(ctx, tb)

	return seed
}

// seedJob makes the workspace and the job in st.
func seedJob(ctx context.Context, st namespace.Store, name string) (JobSeed, error) {
	rows, ok := st.(interface {
		workspace.Store
		inventory.Reader
	})
	if !ok {
		return JobSeed{}, errNoJobStore
	}

	jobs, ok := jobsOf(st)
	if !ok {
		return JobSeed{}, errNoJobStore
	}

	ns, err := st.GetNamespace(ctx, Namespace)
	if err != nil {
		return JobSeed{}, fmt.Errorf("get namespace: %w", err)
	}

	unit, err := seedWorkspace(ctx, rows, ns.Fence(), name)
	if err != nil {
		return JobSeed{}, err
	}

	phases, err := job.DefaultPhases(job.ExecutorRunner, job.OperationPlan)
	if err != nil {
		return JobSeed{}, fmt.Errorf("default phases: %w", err)
	}

	j := job.Job{
		ID: job.ID(ident.NewID(job.IDPrefix)),
		Spec: job.Spec{
			Unit: unit, Operation: job.OperationPlan, Executor: job.ExecutorRunner,
			Location: job.Location{
				Connection: "", Repository: "acme/infra", Ref: "main", Directory: "", Revision: "",
			},
			Steps: []job.Step{{
				Name: "plan", Role: job.RoleTool, Phase: job.PhasePlan, Args: []string{"tofu", "plan"}, Env: nil,
				Dir: "", Timeout: 0, AllowFailure: false, ChangesResources: false, Outputs: nil,
			}},
			Pool: "default", Profile: "", Edits: nil, Delivery: "",
		},
		State: job.StateQueued, Wait: job.WaitNone, Reason: "", Phases: make([]job.Phase, len(phases)), Current: 0,
		StartedBy: "e2e", Agent: "", AgentMode: "", BreakGlass: nil, Delivery: nil, Closed: nil,
		Verification: job.VerificationNone, IdempotencyKey: "", Version: 0, CreatedAt: time.Time{},
		UpdatedAt: time.Time{},
	}

	for i, k := range phases {
		j.Phases[i].Kind = k
	}

	stored, _, err := jobs.Create(ctx, j, nil, ns.Fence(), []audit.Entry{jobEntry(unit)})
	if err != nil {
		return JobSeed{}, fmt.Errorf("create job: %w", err)
	}

	return JobSeed{JobID: string(stored.ID), UnitID: unit.UnitID()}, nil
}

// seedWorkspace makes the OpenTofu workspace name and returns its unit
// reference with the stable unit ID.
func seedWorkspace(
	ctx context.Context, rows interface {
		workspace.Store
		inventory.Reader
	}, fence namespace.Fence, name string,
) (identity.Object, error) {
	ref, err := workspace.ParseName(name)
	if err != nil {
		return identity.Object{}, fmt.Errorf("workspace name: %w", err)
	}

	now := time.Now().UTC()
	w := workspace.Workspace{
		CreatedAt: now, UpdatedAt: now, Lock: workspace.Lock{LockedAt: time.Time{}, ID: "", Holder: "", Reason: ""},
		ID: workspace.ID(ident.NewID("ws-")), Ref: workspace.Ref{Namespace: fence.Namespace, Name: ref}, Revision: 1,
		Overrides: namespace.Overrides{Forced: false, Unlocked: false},
	}

	if createErr := rows.CreateWorkspace(ctx, w, fence); createErr != nil {
		return identity.Object{}, fmt.Errorf("create workspace: %w", createErr)
	}

	entries, err := rows.ListUnits(ctx, fence.Namespace)
	if err != nil {
		return identity.Object{}, fmt.Errorf("list units: %w", err)
	}

	for i := range entries {
		if entries[i].Tool != "opentofu" || entries[i].Name != name {
			continue
		}

		o, objErr := identity.UnitObject(entries[i].Tool, fence.Namespace, name)
		if objErr != nil {
			return identity.Object{}, fmt.Errorf("unit object: %w", objErr)
		}

		return o.WithUnitID(entries[i].UnitID), nil
	}

	return identity.Object{}, fmt.Errorf("%w: unit %s is not in the inventory", errNoJobStore, name)
}

// jobEntry returns the audit entry of the start of a job on unit.
func jobEntry(unit identity.Object) audit.Entry {
	return audit.Entry{
		At: time.Now().UTC(), Namespace: unit.Namespace(), Subject: "e2e", IDPSubject: "", TokenID: "",
		Action: string(job.ActionUnitRunPlan), Object: unit.String(), Version: "", ValuePath: "", ValueCount: nil,
		Changes: nil, SourceIP: "", UserAgent: "", Channel: audit.ChannelAPI, Kind: audit.KindChange,
		Outcome: audit.OutcomeSuccess, RequestID: "", ObjectID: "", PrevHash: "", Hash: "", Sequence: 0,
		FenceVersion: 0, Reason: "", PrincipalKind: "", JobID: "",
	}
}
