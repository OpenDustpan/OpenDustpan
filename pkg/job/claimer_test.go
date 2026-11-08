package job_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// oneNamespace is a job.NamespaceReader that answers ns for every name.
type oneNamespace struct {
	ns namespace.Namespace
}

// GetNamespace implements job.NamespaceReader.
func (o oneNamespace) GetNamespace(context.Context, namespace.Name) (namespace.Namespace, error) {
	return o.ns, nil
}

// errReachedWrite is the error of a write that a claimer makes after a check
// that did not refuse the job. A test that expects the refusal then fails
// with this error, not with a panic.
var errReachedWrite = errors.New("the claimer reached a write")

// oneJob is a job.Store that answers Get with j, and Apply with
// errReachedWrite. Any other method of the port panics on the nil embedded
// interface, which proves the claimer reads nothing else.
type oneJob struct {
	job.Store

	j job.Job
}

// Get implements job.Store.
func (o *oneJob) Get(context.Context, job.ID) (job.Job, error) {
	return o.j, nil
}

// Apply implements job.Store.
func (o *oneJob) Apply(context.Context, job.Change, namespace.Fence, []audit.Entry) (job.Job, error) {
	return job.Job{}, errReachedWrite
}

// noOperations is a durable.Store whose Extend returns errReachedWrite. Any
// other method of the port panics on the nil embedded interface.
type noOperations struct {
	durable.Store
}

// Extend implements durable.Store.
func (noOperations) Extend(context.Context, durable.Claim, time.Duration) (durable.Claim, error) {
	return durable.Claim{}, errReachedWrite
}

// claimerUnit returns the unit of the tests and the namespace that holds it,
// with its own ID.
func claimerUnit(t *testing.T) (identity.Object, namespace.Namespace) {
	t.Helper()

	unit, err := identity.UnitObject("opentofu", "acme", "network")
	if err != nil {
		t.Fatalf("UnitObject: %v", err)
	}

	return unit, namespace.Namespace{
		ID: namespace.NewID(), CreatedAt: time.Time{}, Name: "acme", PrimaryInstallation: "",
		FailoverVersion: 1, Overrides: namespace.Overrides{Forced: false, Unlocked: false},
	}
}

// claimerClock returns the time of the injected clock of the claimer.
func claimerClock() time.Time {
	return time.Date(2031, time.March, 4, 5, 6, 7, 0, time.UTC)
}

// claimerJob returns a running job of unit that names the namespace id.
func claimerJob(unit identity.Object, id namespace.ID) job.Job {
	j := phaseJob(unit, job.StateRunning, 0, time.Time{})
	j.NamespaceID = id

	return j
}

// claimerClaim returns the claim of the first phase of j, held by runnerOne.
func claimerClaim(j *job.Job, at time.Time) job.PhaseClaim {
	return job.PhaseClaim{
		Job: j.ID, Phase: 0,
		Claim: durable.Claim{Operation: "op-1", Owner: "runner-1", Token: 1, ExpiresAt: at.Add(time.Hour)},
		Fence: namespace.Fence{Namespace: "acme", FailoverVersion: 1},
	}
}

// claimerID is one ID of a job that does not name the namespace that holds
// its unit.
type claimerID struct {
	name string
	id   namespace.ID
}

// claimerIDs returns the IDs of a job that does not name the namespace that
// holds its unit, and the empty ID.
func claimerIDs() []claimerID {
	return []claimerID{
		{name: "another namespace ID", id: namespace.NewID()},
		{name: "no namespace ID", id: ""},
	}
}

// TestRunningRefusesAnotherNamespaceID proves that the end path refuses a job
// that names another namespace ID than the namespace that holds the name of
// its namespace, or no ID at all: the end of its phase is a lost claim. The
// claim-time check of fence, the check of checkFence and the fence check of
// the store are the other layers.
func TestRunningRefusesAnotherNamespaceID(t *testing.T) {
	t.Parallel()

	unit, held := claimerUnit(t)
	at := claimerClock()

	for _, tt := range claimerIDs() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			j := claimerJob(unit, tt.id)

			c := job.NewClaimer(
				&oneJob{Store: nil, j: j}, noOperations{Store: nil}, oneNamespace{ns: held}, nil,
				job.WithClaimerClock(func() time.Time { return at }),
			)

			err := c.Release(t.Context(), claimerClaim(&j, at))
			if !errors.Is(err, job.ErrClaimLost) || !errors.Is(err, durable.ErrClaimLost) {
				t.Fatalf("Release = %v, want ErrClaimLost", err)
			}
		})
	}
}

// TestRenewRefusesAnotherNamespaceID proves that the renewal path refuses a
// job that names another namespace ID than the namespace that holds the name
// of its namespace, or no ID at all: the renewal of its claim is a lost
// claim. The claim-time check of fence, the check of running and the fence
// check of the store are the other layers.
func TestRenewRefusesAnotherNamespaceID(t *testing.T) {
	t.Parallel()

	unit, held := claimerUnit(t)
	at := claimerClock()

	for _, tt := range claimerIDs() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			j := claimerJob(unit, tt.id)

			c := job.NewClaimer(
				&oneJob{Store: nil, j: j}, noOperations{Store: nil}, oneNamespace{ns: held}, nil,
				job.WithClaimerClock(func() time.Time { return at }),
			)

			if _, err := c.Renew(t.Context(), claimerClaim(&j, at), time.Minute); !errors.Is(err, job.ErrClaimLost) ||
				!errors.Is(err, durable.ErrClaimLost) {
				t.Fatalf("Renew = %v, want ErrClaimLost", err)
			}
		})
	}
}
