package drifttest

import (
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/drift"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
)

// CheckerFixture makes the rows that the cases of Checker need and reads the
// jobs that Checker starts. A harness that sets Checker sets each hook. The
// units are in the namespace acme, and acme has one runner pool that runs
// jobs.
type CheckerFixture struct {
	// Unit makes the unit name of tool in acme and returns its reference,
	// with no unit ID.
	Unit func(t *testing.T, tool, name string) identity.Object
	// Actor returns the actor of the checks: a person who holds every action
	// on the units of acme.
	Actor func(t *testing.T) job.Actor
	// Job reads the job id.
	Job func(t *testing.T, id job.ID) job.Job
	// Lock puts a manual lock on the unit u.
	Lock func(t *testing.T, u identity.Object)
	// SetNow sets the clock of Checker and of the job service to at.
	SetNow func(t *testing.T, at time.Time)
}

// complete reports whether each hook of f is set.
func (f CheckerFixture) complete() bool {
	return f.Unit != nil && f.Actor != nil && f.Job != nil && f.Lock != nil && f.SetNow != nil
}

func checkerCases() []suiteCase {
	return []suiteCase{
		{"checker: Check starts one job with the operation refresh and the purpose drift check", testCheckStarts},
		{"checker: Check needs no policy", testCheckNeedsNoPolicy},
		{"checker: Check twice in the same minute gives one job", testCheckSameMinute},
		{"checker: Check on a manually locked unit queues the job with the wait reason locked", testCheckLockedQueues},
		{
			"checker: Check on a unit of a tool with no refresh-only plan returns job.ErrUnsupported",
			testCheckNoRefreshOnlyPlan,
		},
		{"checker: Scheduled with the policy off returns ErrDisabled and starts no job", pendingScheduled},
		{"checker: Scheduled has the source kind schedule and never waits for an approval", pendingScheduled},
		{"checker: Scheduled twice for one slot gives one job", pendingScheduled},
		{"checker: a drift check never holds the unit for a change", testCheckChangesNothing},
		{"checker: a change freeze does not hold a drift check", pendingFreeze},
	}
}

// pendingScheduled skips a case of ScheduledChecker: no adapter implements
// that port yet, and the harness has no field for it. The case names are the
// contract that the slice of the drift schedule has to write.
func pendingScheduled(t *testing.T, _ Harness) {
	t.Helper()

	t.Skip("not implemented: no adapter serves drift.ScheduledChecker yet")
}

// pendingFreeze skips the case of a change freeze: the fixture has no hook
// that starts a freeze yet.
func pendingFreeze(t *testing.T, _ Harness) {
	t.Helper()

	t.Skip("not implemented: the checker fixture has no hook that starts a change freeze")
}

// codeLocation is the code location of every check of the cases. A unit of
// the fixture has no stored location, so each check names one.
func codeLocation() job.Location {
	return job.Location{Connection: "", Repository: "acme/infra", Ref: "main", Directory: "", Revision: ""}
}

// checkOf returns the request of a drift check of u.
func checkOf(u identity.Object) drift.CheckRequest {
	return drift.CheckRequest{
		Unit: u, Location: codeLocation(), Pool: "", Reason: "", Channel: "", SourceIP: "", UserAgent: "",
		RequestID: "",
	}
}

// startCheck makes the unit name of tool in acme at minute 0 of the fixtures
// and starts a drift check of it. It returns the unit, the job ID and the
// error of Check.
func startCheck(t *testing.T, h Harness, tool, name string) (identity.Object, job.ID, error) {
	t.Helper()

	h.Checks.SetNow(t, at(0))
	u := h.Checks.Unit(t, tool, name)

	id, err := h.Checker.Check(t.Context(), h.Checks.Actor(t), checkOf(u))

	return u, id, err
}

// testCheckStarts proves that Check starts one job of the unit
// with the operation refresh, the purpose drift check and the source kind
// api.
func testCheckStarts(t *testing.T, h Harness) {
	t.Helper()

	u, id, err := startCheck(t, h, "opentofu", "network")
	must(t, "check", err)

	j := h.Checks.Job(t, id)

	if j.ID != id || j.Spec.Operation != job.OperationRefresh || j.Spec.Purpose != job.PurposeDriftCheck ||
		j.Source != job.SourceAPI {
		t.Fatalf("job = %s %s %s from %s, want %s, a refresh with the purpose drift check from the api",
			j.ID, j.Spec.Operation, j.Spec.Purpose, j.Source, id)
	}

	if j.Spec.Unit.Tool() != u.Tool() || j.Spec.Unit.Namespace() != u.Namespace() ||
		j.Spec.Unit.UnitName() != u.UnitName() || j.Spec.Unit.UnitID() == "" {
		t.Fatalf("job unit = %s, want %s with its unit ID", j.Spec.Unit, u)
	}
}

// testCheckNeedsNoPolicy proves that a check that a person starts needs no
// drift policy: the harness stores none, and Check starts the job.
func testCheckNeedsNoPolicy(t *testing.T, h Harness) {
	t.Helper()

	_, id, err := startCheck(t, h, "opentofu", "network")
	if err != nil || id == "" {
		t.Fatalf("check with no policy = %q, %v, want a job", id, err)
	}

	if errors.Is(err, drift.ErrDisabled) {
		t.Fatalf("check with no policy = %v, want no ErrDisabled", err)
	}
}

// testCheckSameMinute proves that two checks of one unit in the same minute
// give one job, and that a check in the next minute gives a new job.
func testCheckSameMinute(t *testing.T, h Harness) {
	t.Helper()

	u, first, err := startCheck(t, h, "opentofu", "network")
	must(t, "first check", err)

	h.Checks.SetNow(t, at(0).Add(time.Minute-time.Second))

	second, err := h.Checker.Check(t.Context(), h.Checks.Actor(t), checkOf(u))
	must(t, "second check", err)

	if second != first {
		t.Fatalf("second check in the same minute = %s, want the job %s", second, first)
	}

	h.Checks.SetNow(t, at(1))

	next, err := h.Checker.Check(t.Context(), h.Checks.Actor(t), checkOf(u))
	must(t, "check in the next minute", err)

	if next == first {
		t.Fatalf("check in the next minute = %s, want a new job", next)
	}
}

// testCheckLockedQueues proves that a manual lock on the unit does not make
// Check fail: the job is queued with the wait reason locked.
func testCheckLockedQueues(t *testing.T, h Harness) {
	t.Helper()

	h.Checks.SetNow(t, at(0))
	u := h.Checks.Unit(t, "opentofu", "network")
	h.Checks.Lock(t, u)

	id, err := h.Checker.Check(t.Context(), h.Checks.Actor(t), checkOf(u))
	must(t, "check of a locked unit", err)

	j := h.Checks.Job(t, id)
	if j.State != job.StateQueued || j.Wait != job.WaitLocked {
		t.Fatalf("job of a locked unit = %s with wait %q, want %s with wait %q",
			j.State, j.Wait, job.StateQueued, job.WaitLocked)
	}
}

// testCheckNoRefreshOnlyPlan proves that a unit of a tool with no refresh-only
// plan gets job.ErrUnsupported and no job.
func testCheckNoRefreshOnlyPlan(t *testing.T, h Harness) {
	t.Helper()

	_, id, err := startCheck(t, h, "pulumi", "web/prod")
	if !errors.Is(err, job.ErrUnsupported) || id != "" {
		t.Fatalf("check of a Pulumi stack = %q, %v, want no job and job.ErrUnsupported", id, err)
	}
}

// testCheckChangesNothing proves that the job of a drift check changes no
// resource, so it never holds the unit for a change.
func testCheckChangesNothing(t *testing.T, h Harness) {
	t.Helper()

	_, id, err := startCheck(t, h, "opentofu", "network")
	must(t, "check", err)

	j := h.Checks.Job(t, id)
	if j.Spec.Changes() {
		t.Fatalf("job %s changes resources, want a drift check that changes nothing", id)
	}

	for i := range j.Spec.Steps {
		if j.Spec.Steps[i].ChangesResources {
			t.Fatalf("step %q of job %s changes resources, want none", j.Spec.Steps[i].Name, id)
		}
	}
}
