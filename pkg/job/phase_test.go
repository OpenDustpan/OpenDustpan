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
		name         string
		begun, ended []string
		ev           job.Event
		want         job.Event
	}{
		{"takeover, nothing started", nil, nil, job.EventClaim, job.EventClaim},
		{"takeover, safe step started", []string{"init"}, nil, job.EventClaim, job.EventClaim},
		{
			"takeover, change started",
			[]string{"init", "apply"},
			[]string{"init"},
			job.EventClaim, job.EventPhaseFailed,
		},
		{
			"takeover, change done",
			[]string{"init", "apply"},
			[]string{"init", "apply"},
			job.EventClaim, job.EventClaim,
		},
		{"release, nothing started", nil, nil, job.EventRelease, job.EventRelease},
		{"release, safe step done", []string{"init"}, []string{"init"}, job.EventRelease, job.EventRelease},
		{
			"release, change started",
			[]string{"init", "apply"},
			[]string{"init"},
			job.EventRelease, job.EventPhaseFailed,
		},
		{
			"release after change, safe step started",
			[]string{"init", "apply", "notify"},
			[]string{"init", "apply"},
			job.EventRelease,
			job.EventRelease,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, reason := job.AfterLostClaim(steps, tt.begun, tt.ended, tt.ev)
			if got != tt.want {
				t.Fatalf("AfterLostClaim() = %s, want %s", got, tt.want)
			}

			if (got == job.EventPhaseFailed) != (reason != "") {
				t.Fatalf("AfterLostClaim() reason = %q for %s", reason, got)
			}

			if got != tt.ev {
				if _, err := job.Next(job.StateRunning, got); err != nil {
					t.Fatalf("Next(running, %s) = %v", got, err)
				}
			}
		})
	}
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
		Spec:           newSpec(identity.NamespaceObject("acme"), job.OperationApply, job.ExecutorWriteBack),
		State:          s,
		Wait:           job.WaitNone,
		Reason:         "",
		Phases:         nil,
		Current:        0,
		StartedBy:      "al",
		Agent:          "",
		BreakGlass:     nil,
		Delivery:       d,
		Closed:         nil,
		Verification:   job.VerificationNone,
		IdempotencyKey: "",
		Version:        1,
		CreatedAt:      time.Time{},
		UpdatedAt:      time.Time{},
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
