package job_test

import (
	"errors"
	"slices"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
)

func TestDefaultPhases(t *testing.T) {
	t.Parallel()

	tests := []struct {
		executor job.ExecutorKind
		op       job.Operation
		want     []job.PhaseKind
		err      error
	}{
		{job.ExecutorRunner, job.OperationPlan, []job.PhaseKind{job.PhasePlan}, nil},
		{job.ExecutorRunner, job.OperationApply, []job.PhaseKind{job.PhasePlan, job.PhaseApply}, nil},
		{job.ExecutorRunner, job.OperationDestroy, []job.PhaseKind{job.PhasePlan, job.PhaseApply}, nil},
		{job.ExecutorRunner, job.OperationRefresh, []job.PhaseKind{job.PhaseApply}, nil},
		{job.ExecutorWriteBack, job.OperationApply, []job.PhaseKind{job.PhaseDeliver}, nil},
		{job.ExecutorWriteBack, job.OperationPlan, nil, job.ErrUnsupported},
		{job.ExecutorWriteBack, job.OperationDestroy, nil, job.ErrUnsupported},
		{job.ExecutorWriteBack, job.OperationRefresh, nil, job.ErrUnsupported},
		{job.ExecutorRunner, "import", nil, job.ErrInvalid},
		{"ci", job.OperationApply, nil, job.ErrInvalid},
	}

	for _, tt := range tests {
		t.Run(string(tt.executor)+"/"+string(tt.op), func(t *testing.T) {
			t.Parallel()

			got, err := job.DefaultPhases(tt.executor, tt.op)
			if !errors.Is(err, tt.err) {
				t.Fatalf("DefaultPhases() error = %v, want %v", err, tt.err)
			}

			if !slices.Equal(got, tt.want) {
				t.Fatalf("DefaultPhases() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPhaseKindChanges(t *testing.T) {
	t.Parallel()

	for kind, want := range map[job.PhaseKind]bool{job.PhasePlan: false, job.PhaseApply: true, job.PhaseDeliver: true} {
		if got := kind.Changes(); got != want {
			t.Errorf("%s.Changes() = %t, want %t", kind, got, want)
		}
	}
}

func TestFinishEvent(t *testing.T) {
	t.Parallel()

	ok := job.Outcome{Succeeded: true, Message: ""}
	failed := job.Outcome{Succeeded: false, Message: "exit code 1"}

	tests := []struct {
		name string
		kind job.PhaseKind
		last bool
		o    job.Outcome
		want job.Event
	}{
		{"plan with more phases", job.PhasePlan, false, ok, job.EventPhaseDone},
		{"last plan", job.PhasePlan, true, ok, job.EventLastPhaseDone},
		{"last apply", job.PhaseApply, true, ok, job.EventLastPhaseDone},
		{"deliver waits for state", job.PhaseDeliver, true, ok, job.EventDelivered},
		{"failed plan", job.PhasePlan, false, failed, job.EventPhaseFailed},
		{"failed apply", job.PhaseApply, true, failed, job.EventPhaseFailed},
		{"failed deliver", job.PhaseDeliver, true, failed, job.EventPhaseFailed},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := job.FinishEvent(tt.kind, tt.last, tt.o); got != tt.want {
				t.Fatalf("FinishEvent() = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestAfterLostClaim(t *testing.T) {
	t.Parallel()

	steps := []job.Step{applyStep("init", false), applyStep("apply", true), applyStep("notify", false)}

	tests := []struct {
		name    string
		records []job.StepRecord
		ev      job.Event
		want    job.Event
	}{
		{"takeover, nothing started", nil, job.EventClaim, job.EventClaim},
		{"takeover, safe step started", []job.StepRecord{begun("init", 1)}, job.EventClaim, job.EventClaim},
		{
			"takeover, safe step done",
			[]job.StepRecord{ended("init", 1)},
			job.EventClaim, job.EventClaim,
		},
		{
			"takeover, change started",
			[]job.StepRecord{ended("init", 1), begun("apply", 1)},
			job.EventClaim, job.EventPhaseFailed,
		},
		{
			"takeover, change done",
			[]job.StepRecord{ended("init", 1), ended("apply", 1)},
			job.EventClaim, job.EventPhaseFailed,
		},
		{
			"takeover, change done under the first of two claims",
			[]job.StepRecord{ended("init", 1), ended("apply", 1), ended("init", 2)},
			job.EventClaim, job.EventPhaseFailed,
		},
		{"release, nothing started", nil, job.EventRelease, job.EventRelease},
		{"release, safe step done", []job.StepRecord{ended("init", 1)}, job.EventRelease, job.EventRelease},
		{
			"release, change started",
			[]job.StepRecord{ended("init", 1), begun("apply", 1)},
			job.EventRelease, job.EventPhaseFailed,
		},
		{
			"release after change, safe step started",
			[]job.StepRecord{ended("init", 1), ended("apply", 1), begun("notify", 1)},
			job.EventRelease, job.EventPhaseFailed,
		},
		{
			"a record of an unknown step does not count",
			[]job.StepRecord{begun("other", 1)},
			job.EventRelease, job.EventRelease,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, reason := job.AfterLostClaim(steps, tt.records, tt.ev)
			if got != tt.want {
				t.Fatalf("AfterLostClaim() = %s, want %s", got, tt.want)
			}

			if (got == job.EventPhaseFailed) != (reason != "") {
				t.Fatalf("AfterLostClaim() reason = %q for %s", reason, got)
			}

			checkLostClaimEvent(t, tt.ev, got)
		})
	}
}

// checkLostClaimEvent fails t when got, the event of AfterLostClaim for
// ev, is not allowed in the states where it applies, or when a failure
// does not end the job failed.
func checkLostClaimEvent(t *testing.T, ev, got job.Event) {
	t.Helper()

	// A takeover during a cancel never happens: the durable store ends a
	// canceled operation whose claim expired, and the reconcile schedule
	// applies AfterOperationEnded.
	from := []job.State{job.StateRunning}
	if ev == job.EventRelease {
		from = append(from, job.StateCanceling)
	}

	for _, s := range from {
		to, err := job.Next(s, got)
		if err != nil {
			t.Fatalf("Next(%s, %s) = %v", s, got, err)
		}

		if got == job.EventPhaseFailed && to != job.StateFailed {
			t.Fatalf("Next(%s, %s) = %s, want failed", s, got, to)
		}
	}
}

func TestAfterOperationEnded(t *testing.T) {
	t.Parallel()

	changeBegun := []job.StepRecord{ended("init", 1), begun("apply", 1)}
	changeDone := []job.StepRecord{ended("init", 1), ended("apply", 1)}

	tests := []struct {
		name    string
		state   job.State
		records []job.StepRecord
		missing bool
		want    job.Event
		end     job.State
		reason  string
	}{
		{"dispatched, ended", job.StateDispatched, nil, false, job.EventPhaseFailed, job.StateFailed, ""},
		{
			"dispatched, restored", job.StateDispatched, nil, true, job.EventPhaseFailed, job.StateFailed,
			job.ReasonRestored,
		},
		{"running, no change", job.StateRunning, nil, false, job.EventPhaseFailed, job.StateFailed, ""},
		{"running, change began", job.StateRunning, changeBegun, false, job.EventPhaseFailed, job.StateFailed, ""},
		{"canceling, no change", job.StateCanceling, nil, false, job.EventRelease, job.StateCanceled, ""},
		{
			"canceling, safe step done", job.StateCanceling,
			[]job.StepRecord{ended("init", 1)},
			false,
			job.EventRelease, job.StateCanceled, "",
		},
		{
			"canceling, change began", job.StateCanceling, changeBegun, false, job.EventPhaseFailed,
			job.StateFailed, "",
		},
		{
			"canceling, change done", job.StateCanceling, changeDone, false, job.EventPhaseFailed,
			job.StateFailed, "",
		},
		{
			"canceling, restored, no change", job.StateCanceling, nil, true, job.EventRelease,
			job.StateCanceled, job.ReasonRestored,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			j := jobIn(tt.state, nil)
			j.Spec = newSpec(identity.NamespaceObject("acme"), job.OperationRefresh, job.ExecutorRunner)
			j.Spec.Steps = []job.Step{applyStep("init", false), applyStep("apply", true)}
			j.Phases = []job.Phase{phaseOf(job.PhaseApply, 1, tt.records)}

			got, reason, err := job.AfterOperationEnded(&job.Orphan{Job: j, Missing: tt.missing})
			if err != nil || got != tt.want || reason == "" {
				t.Fatalf("AfterOperationEnded() = %s, %q, %v; want %s with a reason", got, reason, err, tt.want)
			}

			if tt.reason != "" && reason != tt.reason {
				t.Fatalf("AfterOperationEnded() reason = %q, want %q", reason, tt.reason)
			}

			if to, err := job.Next(tt.state, got); err != nil || to != tt.end {
				t.Fatalf("Next(%s, %s) = %s, %v; want %s", tt.state, got, to, err, tt.end)
			}
		})
	}

	queued := job.Orphan{Job: jobIn(job.StateQueued, nil), Missing: false}
	if _, _, err := job.AfterOperationEnded(&queued); !errors.Is(err, job.ErrTransition) {
		t.Fatalf("AfterOperationEnded(queued) = %v, want ErrTransition", err)
	}
}

func TestNeedsCommitLookup(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		claims int
		steps  []job.StepRecord
		want   bool
	}{
		{"first claim, nothing recorded", 1, nil, false},
		{"first claim, commit began", 1, []job.StepRecord{begun(job.WriteBackStepCommit, 1)}, true},
		{"takeover, nothing recorded", 2, nil, true},
		{"takeover, commit ended", 2, []job.StepRecord{ended(job.WriteBackStepCommit, 1)}, true},
		{"third claim", 3, nil, true},
		{"first claim, other step", 1, []job.StepRecord{begun(job.WriteBackStepReview, 1)}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			p := phaseOf(job.PhaseDeliver, tt.claims, tt.steps)
			if got := job.NeedsCommitLookup(&p); got != tt.want {
				t.Fatalf("NeedsCommitLookup() = %t, want %t", got, tt.want)
			}
		})
	}
}

// phaseOf returns a phase of kind k with the count of claims and the step
// records steps.
func phaseOf(k job.PhaseKind, claims int, steps []job.StepRecord) job.Phase {
	return job.Phase{
		Kind: k, Operation: "op-1", Claims: claims, Executor: "runner-1", Steps: steps, StateVersion: "", Plan: nil,
		Gates: nil, Approvals: nil, StartedAt: time.Time{}, FinishedAt: time.Time{},
	}
}

// begun returns the record of the step name that began under the claim
// token and did not end.
func begun(name string, token int64) job.StepRecord {
	return job.StepRecord{
		Name: name, Token: token, ExitCode: 0, StartedAt: time.Unix(10, 0), FinishedAt: time.Time{}, Log: nil,
	}
}

// ended returns the record of the step name that began and ended under the
// claim token.
func ended(name string, token int64) job.StepRecord {
	r := begun(name, token)
	r.FinishedAt = time.Unix(20, 0)

	return r
}

func TestCheckCancel(t *testing.T) {
	t.Parallel()

	merged := time.Unix(100, 0)

	tests := []struct {
		name  string
		state job.State
		d     *job.DeliveryResult
		want  error
	}{
		{"queued", job.StateQueued, nil, nil},
		{"running", job.StateRunning, nil, nil},
		{"ended", job.StateSucceeded, nil, job.ErrFinished},
		{"review request open", job.StateAwaitingState, delivery(job.DeliveryReviewRequest, time.Time{}), nil},
		{
			"review request merged",
			job.StateAwaitingState, delivery(job.DeliveryReviewRequest, merged), job.ErrDelivered,
		},
		{"direct push", job.StateAwaitingState, delivery(job.DeliveryDirectPush, time.Time{}), job.ErrDelivered},
		{
			"direct push during a cancel",
			job.StateCanceling, delivery(job.DeliveryDirectPush, time.Time{}), job.ErrDelivered,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			j := jobIn(tt.state, tt.d)

			err := job.CheckCancel(&j)
			if (tt.want == nil && err != nil) || (tt.want != nil && !errors.Is(err, tt.want)) {
				t.Fatalf("CheckCancel() = %v, want %v", err, tt.want)
			}
		})
	}
}

// jobIn returns a job in state s with the delivery d.
func jobIn(s job.State, d *job.DeliveryResult) job.Job {
	return job.Job{
		ID:             "job-a",
		NamespaceID:    "",
		Spec:           newSpec(identity.NamespaceObject("acme"), job.OperationApply, job.ExecutorWriteBack),
		State:          s,
		Wait:           job.WaitNone,
		Reason:         "",
		Phases:         nil,
		Current:        0,
		StartedBy:      "al",
		Source:         job.SourceAPI,
		Agent:          "",
		AgentMode:      "",
		BreakGlass:     nil,
		Trigger:        nil,
		Delivery:       d,
		Closed:         nil,
		Verification:   job.VerificationNone,
		IdempotencyKey: "",
		RequestHash:    "",
		Version:        1,
		CreatedAt:      time.Time{},
		UpdatedAt:      time.Time{},
		StateChangedAt: time.Time{},
	}
}

// delivery returns a delivery result of d, merged at merged.
func delivery(d job.Delivery, merged time.Time) *job.DeliveryResult {
	return &job.DeliveryResult{
		Delivery: d, Commit: "def456", Branch: "main", ReviewRequest: "", MergedAt: merged,
		DeliveredAt: time.Unix(50, 0),
	}
}

func TestKeys(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, got, want string
	}{
		{"phase 0", job.PhaseKey("job-a", 0), "job-a/0"},
		{"phase 1", job.PhaseKey("job-a", 1), "job-a/1"},
		{"abandon", job.FollowUpKey("job-a", job.KindAbandon), "job-a/job.abandon"},
		{"runner kind", string(job.RunnerKind("rpl-1")), "job.runner.rpl-1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if tt.got != tt.want {
				t.Fatalf("got %q, want %q", tt.got, tt.want)
			}
		})
	}
}

// applyStep returns a step of the apply phase.
func applyStep(name string, changes bool) job.Step {
	return job.Step{
		Name: name, Role: job.RoleTool, Phase: job.PhaseApply, Args: []string{"tofu", name}, Env: nil, Dir: "",
		Timeout: 0, AllowFailure: false, ChangesResources: changes, Outputs: nil,
	}
}
