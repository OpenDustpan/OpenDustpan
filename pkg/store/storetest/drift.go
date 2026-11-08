package storetest

import (
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/drift"
	"opendustpan.org/opendustpan/pkg/drift/drifttest"
	"opendustpan.org/opendustpan/pkg/graph"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// DriftBackend is what a SQL store gives the drift suite: its drift store, its
// job store, a way to make a namespace, and a way to set the state and the
// times of a job with direct SQL. The suite needs the last because a job that
// the store makes has the time of the store, and the suite checks the time
// window of a job at fixed times.
type DriftBackend struct {
	Drift drift.Store
	Jobs  job.Store
	// Namespace makes the namespace name when it does not exist, and returns
	// its current fence.
	Namespace func(t *testing.T, name string) namespace.Fence
	// SetJob sets the state, the creation time and the last change time of
	// the job id, in Unix milliseconds, with direct SQL.
	SetJob func(t *testing.T, id job.ID, state job.State, createdMs, updatedMs int64)
}

// RunDrift runs the conformance suite of package drifttest over the stores of
// newBackend. Each case gets a fresh backend.
func RunDrift(t *testing.T, newBackend func(t *testing.T) DriftBackend) {
	t.Helper()

	drifttest.Run(t, func(t *testing.T) drifttest.Harness {
		t.Helper()

		return driftHarness(newBackend(t))
	})
}

// driftHarness returns the harness of the suite over b.
func driftHarness(b DriftBackend) drifttest.Harness {
	window := drifttest.JobWindow()

	mint := func(t *testing.T, u graph.Unit, purpose job.Purpose, state job.State) job.ID {
		t.Helper()

		id := storeDriftJob(t, b, u, purpose)

		end := window.End
		if state == job.StateRunning {
			end = window.Start
		}

		b.SetJob(t, id, state, window.Start.UnixMilli(), end.UnixMilli())

		return id
	}

	return drifttest.Harness{
		Store:    b.Drift,
		Policies: nil,
		// The store has no job service. The SDK runs the cases of Checker with
		// the job service of the server, on the SQLite and the PostgreSQL store.
		Checker:   nil,
		Checks:    drifttest.CheckerFixture{Unit: nil, Actor: nil, Job: nil, Lock: nil, SetNow: nil},
		Namespace: b.Namespace,
		Jobs: drifttest.Jobs{
			DriftCheck: func(t *testing.T, u graph.Unit) job.ID {
				t.Helper()

				return mint(t, u, job.PurposeDriftCheck, job.StateSucceeded)
			},
			Running: func(t *testing.T, u graph.Unit) job.ID {
				t.Helper()

				return mint(t, u, job.PurposeDriftCheck, job.StateRunning)
			},
			Failed: func(t *testing.T, u graph.Unit) job.ID {
				t.Helper()

				return mint(t, u, job.PurposeDriftCheck, job.StateFailed)
			},
			Canceled: func(t *testing.T, u graph.Unit) job.ID {
				t.Helper()

				return mint(t, u, job.PurposeDriftCheck, job.StateCanceled)
			},
			Plain: func(t *testing.T, u graph.Unit) job.ID {
				t.Helper()

				return mint(t, u, job.PurposeNone, job.StateSucceeded)
			},
		},
		Projection: nil,
		Notified:   nil,
		Audited:    nil,
	}
}

// storeDriftJob stores a queued job on u and returns its ID. A drift check is
// the operation refresh with the one phase plan, a plain job is a plan.
func storeDriftJob(t *testing.T, b DriftBackend, u graph.Unit, purpose job.Purpose) job.ID {
	t.Helper()

	fence := b.Namespace(t, u.Namespace.String())

	obj, err := identity.UnitObject(u.Tool, u.Namespace, u.ID)
	if err != nil {
		t.Fatalf("unit object: %v", err)
	}

	op := job.OperationPlan
	if purpose == job.PurposeDriftCheck {
		op = job.OperationRefresh
	}

	j := job.Job{
		ID:          job.ID(ident.NewID(job.IDPrefix)),
		NamespaceID: "",
		Spec: job.Spec{
			Unit: obj.WithUnitID(u.ID), Operation: op, Purpose: purpose, Executor: job.ExecutorRunner,
			Location: job.Location{
				Connection: "github", Repository: "acme/infra", Ref: "main", Directory: "network", Revision: "abc123",
			},
			Steps: []job.Step{{
				Name: "plan", Role: job.RoleTool, Phase: job.PhasePlan, Args: []string{"tofu", "plan"}, Env: nil,
				Dir: "", Timeout: 0, AllowFailure: false, ChangesResources: false, Outputs: nil,
			}},
			Pool: "rpl-default", Profile: "prf-default", Edits: nil, Delivery: "",
			Defaulted: nil,
		},
		State: job.StateQueued, Wait: job.WaitNone, Reason: "",
		Phases: []job.Phase{{
			Kind: job.PhasePlan, Operation: "", Claims: 0, Executor: "", Steps: nil, StateVersion: "", Plan: nil,
			Gates: nil, Approvals: nil, StartedAt: time.Time{}, FinishedAt: time.Time{},
		}},
		Current: 0, StartedBy: "al", Source: job.SourceAPI, Agent: "", AgentMode: "", BreakGlass: nil, Delivery: nil,
		Closed: nil, Verification: job.VerificationNone, IdempotencyKey: "", RequestHash: "", Version: 0,
		Trigger:   nil,
		CreatedAt: time.Time{}, UpdatedAt: time.Time{},
		StateChangedAt: time.Time{},
	}

	entry := audit.Entry{
		At: time.Now().UTC(), Namespace: u.Namespace, Subject: "al", IDPSubject: "", TokenID: "",
		Action: string(job.ActionUnitRunPlan), Object: obj.String(), Version: "", ValuePath: "", ValueCount: nil,
		Changes: nil, SourceIP: "", UserAgent: "", Channel: audit.ChannelAPI, Kind: audit.KindChange,
		Outcome: audit.OutcomeSuccess, RequestID: "", ObjectID: "", PrevHash: "", Hash: "", Sequence: 0,
		FenceVersion: 0, Reason: "", PrincipalKind: "", JobID: "",
	}

	stored, created, err := b.Jobs.Create(t.Context(), j, nil, fence, []audit.Entry{entry})
	if err != nil || !created {
		t.Fatalf("create job: created %v, error %v", created, err)
	}

	return stored.ID
}

// FixtureCheck makes a succeeded drift check job of u and returns the result
// that the job makes, with one changed resource. The result is checked inside
// the run of the job, so Record accepts it. A test of a feature that follows
// the result of a unit uses it: the purge of the unit, for example.
func (b DriftBackend) FixtureCheck(t *testing.T, u graph.Unit, address string) drift.Result {
	t.Helper()

	window := drifttest.JobWindow()

	id := storeDriftJob(t, b, u, job.PurposeDriftCheck)
	b.SetJob(t, id, job.StateSucceeded, window.Start.UnixMilli(), window.End.UnixMilli())

	return drift.Result{
		CheckedAt: window.Start.Add(time.Minute), Unit: u, JobID: id,
		Resources: []drift.Resource{{Address: address, Kind: drift.KindChanged}}, Truncated: false,
	}
}
