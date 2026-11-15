package runnerapi_test

import (
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/api/runnerapi"
	"opendustpan.org/opendustpan/pkg/job"
)

// TestOutputsNeededByALaterPhase checks the needed flag of the declared
// outputs of a step: a plan-apply output is needed when a later phase of the
// job is an apply phase. An output of another kind, and an output with no
// later apply phase, are not needed.
func TestOutputsNeededByALaterPhase(t *testing.T) {
	t.Parallel()

	planStep := func(kind job.OutputKind) job.Step {
		return job.Step{
			Name: "plan", Role: job.RoleTool, Phase: job.PhasePlan, Args: []string{"tofu", "plan"}, Env: nil,
			Dir: "", Timeout: 0, AllowFailure: false, ChangesResources: false,
			Outputs: []job.Output{{Kind: kind, Path: "tfplan"}},
		}
	}
	applyStep := job.Step{
		Name: "apply", Role: job.RoleTool, Phase: job.PhaseApply, Args: []string{"tofu", "apply"}, Env: nil,
		Dir: "", Timeout: 0, AllowFailure: false, ChangesResources: true,
		Outputs: []job.Output{{Kind: job.OutputPlanApply, Path: "tfplan"}},
	}
	phase := func(kind job.PhaseKind) job.Phase {
		return job.Phase{
			Kind: kind, Operation: "", Claims: 0, Executor: "", Steps: nil, StateVersion: "", Plan: nil,
			Gates: nil, Approvals: nil, StartedAt: time.Time{}, FinishedAt: time.Time{},
		}
	}

	planThenApply := []job.Phase{phase(job.PhasePlan), phase(job.PhaseApply)}
	planOnly := []job.Phase{phase(job.PhasePlan)}

	cases := []struct {
		name    string
		steps   []job.Step
		kind    job.PhaseKind
		phases  []job.Phase
		current int
		needed  bool
	}{
		{
			"a plan-apply output of the plan phase of a plan-then-apply job",
			[]job.Step{planStep(job.OutputPlanApply)},
			job.PhasePlan, planThenApply, 0, true,
		},
		{
			"a plan-apply output of the plan phase of a plan-only job",
			[]job.Step{planStep(job.OutputPlanApply)},
			job.PhasePlan, planOnly, 0, false,
		},
		{
			"a plan-display output of the plan phase of a plan-then-apply job",
			[]job.Step{planStep(job.OutputPlanDisplay)},
			job.PhasePlan, planThenApply, 0, false,
		},
		{
			"a plan-apply output of the apply phase itself",
			[]job.Step{applyStep},
			job.PhaseApply, planThenApply, 1, false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			steps := runnerapi.OutputStepsOf(tc.steps, tc.kind, tc.phases, tc.current)
			if len(steps) != 1 || len(steps[0].Outputs) != 1 {
				t.Fatalf("steps = %+v, want one step with one output", steps)
			}

			if got := steps[0].Outputs[0].Needed; got != tc.needed {
				t.Fatalf("needed = %t, want %t", got, tc.needed)
			}
		})
	}
}
