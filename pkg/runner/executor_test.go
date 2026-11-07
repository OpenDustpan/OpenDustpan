package runner_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/runner"
)

var errPoolDriver = errors.New("driver: connection reset")

type poolReader struct {
	pools map[string]runner.Pool
	err   error
}

func (p poolReader) Pool(_ context.Context, id string) (runner.Pool, error) {
	if p.err != nil {
		return runner.Pool{}, p.err
	}

	pool, ok := p.pools[id]
	if !ok {
		return runner.Pool{}, runner.ErrNotFound
	}

	return pool, nil
}

func planSpec(t *testing.T) job.Spec {
	t.Helper()

	unit, err := identity.UnitObject("opentofu", "acme", "web")
	if err != nil {
		t.Fatalf("UnitObject = %v", err)
	}

	return job.Spec{
		Unit: unit.WithUnitID("unt-web"), Operation: job.OperationPlan, Purpose: "", Executor: job.ExecutorRunner,
		Location: job.Location{
			Connection: "", Repository: "acme/infra", Ref: "main", Directory: "", Revision: "",
		},
		LocationSource: job.LocationSourceRequest,
		Steps:          []job.Step{toolStep("plan", job.PhasePlan, "plan", false)},
		Pool:           "rpl-1",
		Profile:        "",
		Edits:          nil,
		Delivery:       "",
		Defaulted:      nil,
	}
}

// toolStep is a tofu step of the phase.
func toolStep(name string, phase job.PhaseKind, verb string, changes bool) job.Step {
	return job.Step{
		Name: name, Role: job.RoleTool, Phase: phase, Args: []string{"tofu", verb},
		Env: nil, Dir: "", Timeout: 0, AllowFailure: false, ChangesResources: changes, Outputs: nil,
	}
}

func testPool(id string, ns namespace.Name, purpose runner.Purpose) runner.Pool {
	var noCredentials runner.GitCredentials

	return runner.Pool{
		ID: id, Namespace: ns, Name: "", Purpose: purpose, Lease: 0, Concurrency: 0, Isolation: nil,
		GitCredentials: noCredentials, CreatedAt: time.Time{}, UpdatedAt: time.Time{},
	}
}

type phaseCase struct {
	name    string
	mutate  func(*job.Spec)
	reader  poolReader
	want    []job.PhaseKind
	wantErr error
}

func phaseCases() []phaseCase {
	pools := map[string]runner.Pool{
		"rpl-1": testPool("rpl-1", "acme", runner.PurposeJobs),
		"rpl-2": testPool("rpl-2", "other", runner.PurposeJobs),
		"rpl-3": testPool("rpl-3", "acme", runner.PurposeSweeps),
	}
	ok := poolReader{pools: pools, err: nil}
	plan := []job.PhaseKind{job.PhasePlan}
	invalid := func(name string, mutate func(*job.Spec)) phaseCase {
		return phaseCase{name, mutate, ok, nil, job.ErrInvalid}
	}

	return append([]phaseCase{
		{"a plan", func(*job.Spec) {}, ok, plan, nil},
		{"an apply has two phases", func(s *job.Spec) {
			s.Operation = job.OperationApply
			s.Steps = append(s.Steps, toolStep("apply", job.PhaseApply, "apply", true))
		}, ok, []job.PhaseKind{job.PhasePlan, job.PhaseApply}, nil},
		invalid("a step in a phase the job lacks", func(s *job.Spec) {
			s.Steps[0].Phase = job.PhaseApply
			s.Steps[0].ChangesResources = true
		}),
		invalid("no code location", func(s *job.Spec) {
			var none job.Location

			s.Location = none
		}),
		invalid("a directory above the checkout", func(s *job.Spec) { s.Location.Directory = "../x" }),
		invalid("no steps", func(s *job.Spec) { s.Steps = nil }),
		invalid("a shell in the program", func(s *job.Spec) { s.Steps[0].Args = []string{"../tofu"} }),
		invalid("a credential in the environment", func(s *job.Spec) {
			s.Steps[0].Env = map[string]string{"AWS_SESSION_TOKEN": "x"}
		}),
		invalid("no pool", func(s *job.Spec) { s.Pool = "" }),
		invalid("an unknown pool", func(s *job.Spec) { s.Pool = "rpl-9" }),
		invalid("a pool of another namespace", func(s *job.Spec) { s.Pool = "rpl-2" }),
		{"a pool for sweeps", func(s *job.Spec) { s.Pool = "rpl-3" }, ok, nil, runner.ErrPurpose},
		{"the pool store fails", func(*job.Spec) {}, poolReader{pools: nil, err: errPoolDriver}, nil, errPoolDriver},
		invalid("another executor", func(s *job.Spec) { s.Executor = job.ExecutorWriteBack }),
		invalid("an unknown operation", func(s *job.Spec) { s.Operation = "reboot" }),
	}, driftCases(ok)...)
}

func TestExecutorPhases(t *testing.T) {
	t.Parallel()

	for _, tt := range phaseCases() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			e, err := runner.NewExecutor(tt.reader)
			if err != nil {
				t.Fatalf("NewExecutor = %v", err)
			}

			spec := planSpec(t)
			tt.mutate(&spec)

			got, err := e.Phases(t.Context(), spec)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Phases = %v, want %v", err, tt.wantErr)
			}

			if !slices.Equal(got, tt.want) {
				t.Fatalf("Phases = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestExecutorBasics(t *testing.T) {
	t.Parallel()

	if _, err := runner.NewExecutor(nil); !errors.Is(err, runner.ErrInvalid) {
		t.Fatalf("NewExecutor(nil) = %v, want ErrInvalid", err)
	}

	e, err := runner.NewExecutor(poolReader{pools: nil, err: nil})
	if err != nil {
		t.Fatalf("NewExecutor = %v", err)
	}

	if e.Kind() != job.ExecutorRunner {
		t.Fatalf("Kind = %q, want runner", e.Kind())
	}

	var zeroJob job.Job

	if err := e.Abandon(t.Context(), zeroJob); err != nil {
		t.Fatalf("Abandon = %v, want nil", err)
	}
}

// driftSpec turns a plan spec into a drift check: the operation refresh with
// the purpose, and the plan steps of a refresh-only plan.
func driftSpec(s *job.Spec) {
	s.Operation, s.Purpose = job.OperationRefresh, job.PurposeDriftCheck
	s.Steps = []job.Step{
		toolStep("init", job.PhasePlan, "init", false), toolStep("plan", job.PhasePlan, "plan", false),
	}
	s.Steps[0].Args = []string{"tofu", "init", "-input=false"}
	s.Steps[1].Args = []string{"tofu", "plan", "-refresh-only", "-input=false", "-out=drift.tfplan"}

	for i := range s.Steps {
		s.Steps[i].Env = map[string]string{"TF_IN_AUTOMATION": "1"}
	}
}

// driftCases returns the cases of a drift check. A drift check has the plan
// phase alone, and no step may add an argument or change resources.
func driftCases(ok poolReader) []phaseCase {
	plan := []job.PhaseKind{job.PhasePlan}
	invalid := func(name string, mutate func(*job.Spec)) phaseCase {
		return phaseCase{name, func(s *job.Spec) { driftSpec(s); mutate(s) }, ok, nil, job.ErrInvalid}
	}

	return []phaseCase{
		{"a drift check is one plan phase", driftSpec, ok, plan, nil},
		invalid("a drift check with a step in the apply phase", func(s *job.Spec) {
			s.Steps = append(s.Steps, toolStep("apply", job.PhaseApply, "apply", true))
		}),
		invalid("a drift check with a hook", func(s *job.Spec) { s.Steps[0].Role = job.RoleHook }),
		invalid("a drift check with TF_CLI_ARGS", func(s *job.Spec) { s.Steps[0].Env["TF_CLI_ARGS"] = "-lock=false" }),
		invalid("a drift check with a plan for an apply", func(s *job.Spec) {
			s.Steps[0].Outputs = []job.Output{{Kind: job.OutputPlanApply, Path: "tfplan"}}
		}),
		invalid("a drift check of a plan", func(s *job.Spec) { s.Operation = job.OperationPlan }),
		invalid("a drift check of an apply", func(s *job.Spec) { s.Operation = job.OperationApply }),
		invalid("a drift check through write-back", func(s *job.Spec) { s.Executor = job.ExecutorWriteBack }),
		invalid("an unknown purpose", func(s *job.Spec) { s.Purpose = "scan" }),
		{
			"a refresh with no purpose keeps its apply phase", func(s *job.Spec) {
				s.Operation = job.OperationRefresh
				s.Steps = []job.Step{toolStep("refresh", job.PhaseApply, "apply", true)}
			}, ok, []job.PhaseKind{job.PhaseApply}, nil,
		},
	}
}
