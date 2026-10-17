package job_test

import (
	"errors"
	"slices"
	"testing"

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
		want         job.Event
	}{
		{"nothing started", nil, nil, job.EventRelease},
		{"safe step started", []string{"init"}, nil, job.EventRelease},
		{"safe step done", []string{"init"}, []string{"init"}, job.EventRelease},
		{"change started", []string{"init", "apply"}, []string{"init"}, job.EventPhaseFailed},
		{"change done", []string{"init", "apply"}, []string{"init", "apply"}, job.EventRelease},
		{
			"after change, safe step started",
			[]string{"init", "apply", "notify"},
			[]string{"init", "apply"},
			job.EventRelease,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, reason := job.AfterLostClaim(steps, tt.begun, tt.ended)
			if got != tt.want {
				t.Fatalf("AfterLostClaim() = %s, want %s", got, tt.want)
			}

			if (got == job.EventPhaseFailed) != (reason != "") {
				t.Fatalf("AfterLostClaim() reason = %q for %s", reason, got)
			}
		})
	}
}

func TestDispatchKeyIsUniquePerDispatch(t *testing.T) {
	t.Parallel()

	seen := map[string]bool{}

	for phase := range 3 {
		for n := 1; n <= 3; n++ {
			k := job.DispatchKey("job-a", phase, n)
			if seen[k] {
				t.Fatalf("DispatchKey(job-a, %d, %d) = %q, used before", phase, n, k)
			}

			seen[k] = true
		}
	}

	if got := job.RunnerKind("rpl-1"); got != "job.runner.rpl-1" {
		t.Fatalf("RunnerKind() = %q", got)
	}
}

// applyStep returns a step of the apply phase.
func applyStep(name string, changes bool) job.Step {
	return job.Step{
		Name: name, Role: job.RoleTool, Phase: job.PhaseApply, Args: []string{"tofu", name}, Env: nil, Dir: "",
		Timeout: 0, AllowFailure: false, ChangesResources: changes, Outputs: nil,
	}
}
