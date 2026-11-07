package job_test

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
)

func TestPurposeValid(t *testing.T) {
	t.Parallel()

	for purpose, want := range map[job.Purpose]bool{
		job.PurposeNone: true, job.PurposeDriftCheck: true, "drift": false, "Drift-Check": false, "DRIFT-CHECK": false,
	} {
		if got := purpose.Valid(); got != want {
			t.Errorf("Purpose(%q).Valid() = %t, want %t", purpose, got, want)
		}
	}
}

// specOf returns a spec with the given operation, purpose and executor. The
// fields that no case reads stay empty.
func specOf(op job.Operation, purpose job.Purpose, executor job.ExecutorKind) job.Spec {
	return job.Spec{
		Unit: identity.Object{}, Operation: op, Purpose: purpose, Executor: executor,
		Location: emptyLocation(), LocationSource: "", Steps: nil, Pool: "", Profile: "", Edits: nil,
		Delivery: "", Defaulted: nil,
	}
}

func TestSpecChangesForADriftCheck(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		spec job.Spec
		want bool
	}{
		{"refresh without a purpose writes state", specOf(job.OperationRefresh, "", job.ExecutorRunner), true},
		{
			"a drift check changes nothing",
			specOf(job.OperationRefresh, job.PurposeDriftCheck, job.ExecutorRunner), false,
		},
		{"a plan changes nothing", specOf(job.OperationPlan, "", job.ExecutorRunner), false},
		{"a write-back always changes", specOf(job.OperationApply, "", job.ExecutorWriteBack), true},
	}

	for _, tt := range tests {
		if got := tt.spec.Changes(); got != tt.want {
			t.Errorf("%s: Changes() = %t, want %t", tt.name, got, tt.want)
		}
	}
}

func TestPhasesForADriftCheck(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		executor job.ExecutorKind
		op       job.Operation
		purpose  job.Purpose
		want     []job.PhaseKind
		err      error
	}{
		{
			"a drift check is one plan phase",
			job.ExecutorRunner,
			job.OperationRefresh,
			job.PurposeDriftCheck,
			[]job.PhaseKind{job.PhasePlan},
			nil,
		},
		{
			"no purpose keeps the table",
			job.ExecutorRunner,
			job.OperationRefresh,
			job.PurposeNone,
			[]job.PhaseKind{job.PhaseApply},
			nil,
		},
		{
			"a drift check needs refresh: plan",
			job.ExecutorRunner,
			job.OperationPlan,
			job.PurposeDriftCheck,
			nil,
			job.ErrInvalid,
		},
		{
			"a drift check needs refresh: apply",
			job.ExecutorRunner,
			job.OperationApply,
			job.PurposeDriftCheck,
			nil,
			job.ErrInvalid,
		},
		{
			"a drift check needs refresh: destroy",
			job.ExecutorRunner,
			job.OperationDestroy,
			job.PurposeDriftCheck,
			nil,
			job.ErrInvalid,
		},
		{
			"a drift check never goes through write-back",
			job.ExecutorWriteBack,
			job.OperationApply,
			job.PurposeDriftCheck,
			nil,
			job.ErrUnsupported,
		},
		{
			"a drift check through write-back refresh",
			job.ExecutorWriteBack,
			job.OperationRefresh,
			job.PurposeDriftCheck,
			nil,
			job.ErrUnsupported,
		},
		{"unknown purpose", job.ExecutorRunner, job.OperationRefresh, "scan", nil, job.ErrInvalid},
		{"unknown executor", "ci", job.OperationRefresh, job.PurposeDriftCheck, nil, job.ErrInvalid},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := job.PhasesFor(tt.executor, tt.op, tt.purpose)
			if !errors.Is(err, tt.err) {
				t.Fatalf("PhasesFor() error = %v, want %v", err, tt.err)
			}

			if !slices.Equal(got, tt.want) {
				t.Fatalf("PhasesFor() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestStartRequirementsOfADriftCheck(t *testing.T) {
	t.Parallel()

	unit := mustUnit("acme", "web").WithUnitID("unt-web")

	spec := specOf(job.OperationRefresh, job.PurposeDriftCheck, job.ExecutorRunner)
	spec.Unit = unit

	reqs, err := job.StartRequirements(spec, nil)
	if err != nil || len(reqs) != 1 || reqs[0].Action != job.ActionUnitRunPlan ||
		reqs[0].Object.String() != unit.String() {
		t.Fatalf("StartRequirements = %+v, %v; want unit:run-plan on the unit alone", reqs, err)
	}

	// The same refresh with no purpose needs the apply action.
	spec.Purpose = job.PurposeNone

	reqs, err = job.StartRequirements(spec, nil)
	if err != nil || len(reqs) != 1 || reqs[0].Action != job.ActionUnitRunApply {
		t.Fatalf("StartRequirements of a refresh = %+v, %v; want unit:run-apply", reqs, err)
	}

	// A drift check is never a write-back.
	spec.Purpose, spec.Executor = job.PurposeDriftCheck, job.ExecutorWriteBack
	if _, err = job.StartRequirements(spec, nil); !errors.Is(err, job.ErrUnsupported) {
		t.Fatalf("StartRequirements through write-back = %v, want ErrUnsupported", err)
	}
}

// driftStep returns a valid plan step of a drift check.
func driftStep(name string, args ...string) job.Step {
	return job.Step{
		Name:             name,
		Role:             job.RoleTool,
		Phase:            job.PhasePlan,
		Args:             args,
		Env:              map[string]string{"TF_IN_AUTOMATION": "1"},
		Dir:              "",
		Timeout:          0,
		AllowFailure:     false,
		ChangesResources: false,
		Outputs:          nil,
	}
}

func TestValidateDriftSteps(t *testing.T) {
	t.Parallel()

	good := func() []job.Step {
		return []job.Step{
			driftStep("init", "tofu", "init", "-input=false"),
			driftStep("plan", "tofu", "plan", "-refresh-only", "-input=false", "-out=drift.tfplan"),
		}
	}

	if err := job.ValidateDriftSteps(good()); err != nil {
		t.Fatalf("ValidateDriftSteps of the fixed steps = %v", err)
	}

	tests := []struct {
		name   string
		change func(steps []job.Step) []job.Step
	}{
		{"no step", func([]job.Step) []job.Step { return nil }},
		{"a step in the apply phase", func(s []job.Step) []job.Step { s[1].Phase = job.PhaseApply; return s }},
		{"a hook", func(s []job.Step) []job.Step { s[0].Role = job.RoleHook; return s }},
		{"a step that marks a change", func(s []job.Step) []job.Step { s[1].ChangesResources = true; return s }},
		{"a plan for an apply phase", func(s []job.Step) []job.Step {
			s[1].Outputs = []job.Output{{Kind: job.OutputPlanApply, Path: "tfplan"}}

			return s
		}},
		{"a readable plan output", func(s []job.Step) []job.Step {
			s[1].Outputs = []job.Output{{Kind: job.OutputPlanDisplay, Path: "drift.json"}}

			return s
		}},
		{"AllowFailure", func(s []job.Step) []job.Step { s[0].AllowFailure = true; return s }},
		{"a timeout", func(s []job.Step) []job.Step { s[1].Timeout = time.Minute; return s }},
		{"the longest timeout", func(s []job.Step) []job.Step { s[1].Timeout = job.MaxStepTimeout; return s }},
		{"TF_CLI_ARGS", func(s []job.Step) []job.Step { s[1].Env["TF_CLI_ARGS"] = "-lock=false"; return s }},
		{"tf_cli_args in lower case", func(s []job.Step) []job.Step { s[1].Env["tf_cli_args_plan"] = "-x"; return s }},
		{"TF_DATA_DIR", func(s []job.Step) []job.Step { s[1].Env["TF_DATA_DIR"] = "/tmp/x"; return s }},
		{"TF_WORKSPACE", func(s []job.Step) []job.Step { s[1].Env["TF_WORKSPACE"] = "other"; return s }},
		{"TF_VAR_x", func(s []job.Step) []job.Step { s[1].Env["TF_VAR_x"] = "1"; return s }},
		{"TF_LOG_PATH", func(s []job.Step) []job.Step { s[0].Env["TF_LOG_PATH"] = "/tmp/x"; return s }},
		{"TF_CLI_CONFIG_FILE", func(s []job.Step) []job.Step { s[1].Env["TF_CLI_CONFIG_FILE"] = "/tmp/x"; return s }},
		{
			"a changed value of TF_IN_AUTOMATION",
			func(s []job.Step) []job.Step { s[1].Env["TF_IN_AUTOMATION"] = "0"; return s },
		},
		{"no environment", func(s []job.Step) []job.Step { s[1].Env = nil; return s }},
		{"a Dir", func(s []job.Step) []job.Step { s[1].Dir = "sub"; return s }},
		{"a Dir that leaves the checkout", func(s []job.Step) []job.Step { s[0].Dir = "../.."; return s }},
		{"apply", func(s []job.Step) []job.Step { s[1].Args = []string{"tofu", "apply", "-auto-approve"}; return s }},
		{
			"destroy",
			func(s []job.Step) []job.Step { s[1].Args = []string{"tofu", "destroy", "-auto-approve"}; return s },
		},
		{"plan with -destroy", func(s []job.Step) []job.Step { s[1].Args = append(s[1].Args, "-destroy"); return s }},
		{
			"plan with -lock=false",
			func(s []job.Step) []job.Step { s[1].Args = append(s[1].Args, "-lock=false"); return s },
		},
		{"plan with -chdir", func(s []job.Step) []job.Step {
			s[1].Args = []string{"tofu", "-chdir=sub", "plan", "-refresh-only", "-input=false", "-out=drift.tfplan"}

			return s
		}},
		{"plan without -refresh-only", func(s []job.Step) []job.Step {
			s[1].Args = []string{"tofu", "plan", "-input=false", "-out=drift.tfplan"}

			return s
		}},
		{"another program", func(s []job.Step) []job.Step { s[0].Args = []string{"sh", "-c", "tofu apply"}; return s }},
		{
			"init with an extra flag",
			func(s []job.Step) []job.Step { s[0].Args = append(s[0].Args, "-upgrade"); return s },
		},
		{"the steps in the other order", func(s []job.Step) []job.Step { return []job.Step{s[1], s[0]} }},
		{
			"a third step",
			func(s []job.Step) []job.Step { return append(s, driftStep("extra", "tofu", "init", "-input=false")) },
		},
		{"one step", func(s []job.Step) []job.Step { return s[:1] }},
		{"a step that ValidateSteps refuses", func(s []job.Step) []job.Step { s[0].Args = nil; return s }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if err := job.ValidateDriftSteps(tt.change(good())); !errors.Is(err, job.ErrInvalid) {
				t.Fatalf("ValidateDriftSteps = %v, want ErrInvalid", err)
			}
		})
	}
}

// driftCheck returns the request of a drift check of the unit of the rig.
func (r *rig) driftCheck(key string) job.StartRequest {
	req := r.plan(key)
	req.Operation, req.Purpose = job.OperationRefresh, job.PurposeDriftCheck

	return req
}

// onlyRunPlan limits the principal ada to unit:run-plan and the read action.
func onlyRunPlan(_ *job.ServiceConfig, r *rig) {
	delete(r.authz.allow, string(job.ActionUnitRunApply)+" "+r.unit.String())
}

func TestStartDriftCheck(t *testing.T) {
	t.Parallel()

	r := newRig(t, onlyRunPlan)

	j, created, err := r.svc.StartOnce(t.Context(), actorOf("ada"), r.driftCheck("client-key"))
	if err != nil || !created {
		t.Fatalf("StartOnce = created %v, error %v; want a new drift check with unit:run-plan alone", created, err)
	}

	c := r.store.creates[0]

	if c.dispatch == nil {
		t.Error("a drift check start makes no dispatch")
	}

	checkDriftJob(t, &j)
	checkDriftAudit(t, &c.entries[0])

	// The decider sees the purpose, so a policy can tell a drift check apart.
	if len(r.decider.inputs) != 1 || r.decider.inputs[0].Spec.Purpose != job.PurposeDriftCheck {
		t.Errorf("decider inputs = %+v, want the spec of the drift check", r.decider.inputs)
	}
}

// checkDriftJob checks the job that a start of a drift check made.
func checkDriftJob(t *testing.T, j *job.Job) {
	t.Helper()

	if j.Spec.Purpose != job.PurposeDriftCheck || j.Spec.Operation != job.OperationRefresh {
		t.Errorf("job = purpose %q, operation %q; want drift-check and refresh", j.Spec.Purpose, j.Spec.Operation)
	}

	if len(j.Phases) != 1 || j.Phases[0].Kind != job.PhasePlan || len(j.Phases[0].Gates) != 0 {
		t.Errorf("phases = %+v, want the plan phase alone, with no gate", j.Phases)
	}

	if j.State != job.StateDispatched {
		t.Errorf("state = %q, want a dispatched job", j.State)
	}

	if j.Spec.Changes() {
		t.Error("a drift check job says that it changes resources")
	}
}

// checkDriftAudit checks the audit entry of the start of a drift check.
func checkDriftAudit(t *testing.T, e *audit.Entry) {
	t.Helper()

	got := map[string]string{}
	for _, ch := range e.Changes {
		got[ch.Key] = ch.New
	}

	if e.Action != string(job.ActionUnitRunPlan) || got["purpose"] != "drift-check" || got["operation"] != "refresh" {
		t.Errorf("audit entry = action %q, changes %v; want unit:run-plan, purpose drift-check, operation refresh",
			e.Action, got)
	}
}

// TestScheduledDriftCheckNeverWaitsForApproval proves that a drift check from a
// schedule is never held for an approval, also when the policy gates every
// phase that changes something: a drift check has the plan phase alone.
func TestScheduledDriftCheckNeverWaitsForApproval(t *testing.T) {
	t.Parallel()

	needsApproval := func(_ *job.ServiceConfig, r *rig) {
		r.decider.decision = job.Decision{
			Allow: true, Reason: "", PolicyVersion: "v1",
			Gates: map[job.PhaseKind]job.Gate{
				job.PhaseApply: {
					Type:      job.GateApproval,
					Approvals: 1,
					Groups:    nil,
					GroupIDs:  nil,
					AllowSelf: false,
					Timeout:   0,
					BindPlan:  true,
				},
			},
		}
		r.authz.allow[string(job.ActionUnitRunApply)+" "+r.unit.String()] = true
	}

	r := newRig(t, needsApproval)

	req := r.driftCheck("")
	req.Source = job.SourceSchedule

	j, err := r.svc.Start(t.Context(), actorOf("ada"), req)
	if err != nil {
		t.Fatalf("Start = %v", err)
	}

	if j.State == job.StateAwaitingApproval || r.store.creates[0].dispatch == nil || j.Source != job.SourceSchedule {
		t.Fatalf("job = state %q, source %q, dispatch %+v; want a dispatched schedule job that waits for nobody",
			j.State, j.Source, r.store.creates[0].dispatch)
	}

	if len(r.store.applies) != 0 {
		t.Errorf("Apply calls = %d, want none: no event of approval", len(r.store.applies))
	}
}

func TestStartDriftCheckRefusals(t *testing.T) {
	t.Parallel()

	runRefusals(t, []refusal{
		{"unknown purpose", "ada", job.ErrInvalid, 0, func(req *job.StartRequest) {
			req.Operation, req.Purpose = job.OperationRefresh, "scan"
		}, nil},
		{"a drift check of a plan", "ada", job.ErrInvalid, 0, func(req *job.StartRequest) {
			req.Purpose = job.PurposeDriftCheck
		}, nil},
		{"a drift check of an apply", "ada", job.ErrInvalid, 0, func(req *job.StartRequest) {
			req.Operation, req.Purpose = job.OperationApply, job.PurposeDriftCheck
		}, nil},
		{"a drift check through write-back", "ada", job.ErrUnsupported, 0, func(req *job.StartRequest) {
			req.Operation, req.Purpose, req.Executor = job.OperationRefresh, job.PurposeDriftCheck, job.ExecutorWriteBack
		}, nil},
		{"a person without unit:run-plan", "bob", identity.ErrDenied, 0, func(req *job.StartRequest) {
			req.Operation, req.Purpose = job.OperationRefresh, job.PurposeDriftCheck
		}, nil},
		{
			"a person with unit:run-apply and not unit:run-plan", "ada", identity.ErrDenied, 0,
			func(req *job.StartRequest) { req.Operation, req.Purpose = job.OperationRefresh, job.PurposeDriftCheck },
			func(_ *job.ServiceConfig, r *rig) {
				delete(r.authz.allow, string(job.ActionUnitRunPlan)+" "+r.unit.String())
				r.authz.allow[string(job.ActionUnitRunApply)+" "+r.unit.String()] = true
			},
		},
		{
			"a planner that drops the purpose", "ada", job.ErrInvalid, 1,
			func(req *job.StartRequest) { req.Operation, req.Purpose = job.OperationRefresh, job.PurposeDriftCheck },
			func(_ *job.ServiceConfig, r *rig) {
				inner := r.planner.spec
				r.planner.spec = func(u identity.Object, req *job.StartRequest) job.Spec {
					spec := inner(u, req)
					spec.Purpose = job.PurposeNone

					return spec
				}
			},
		},
		{
			"a planner that adds the purpose", "ada", job.ErrInvalid, 1, nil,
			func(_ *job.ServiceConfig, r *rig) {
				inner := r.planner.spec
				r.planner.spec = func(u identity.Object, req *job.StartRequest) job.Spec {
					spec := inner(u, req)
					spec.Purpose = job.PurposeDriftCheck

					return spec
				}
			},
		},
	})
}

func TestRequestHashHoldsThePurpose(t *testing.T) {
	t.Parallel()

	r := newRig(t)

	first, _, err := r.svc.StartOnce(t.Context(), actorOf("ada"), r.driftCheck("same-key"))
	if err != nil {
		t.Fatalf("first Start = %v", err)
	}

	other := r.driftCheck("same-key")
	other.Purpose = job.PurposeNone

	// A refresh with no purpose is another request, so its hash differs.
	r2 := newRig(t, func(_ *job.ServiceConfig, r *rig) {
		r.authz.allow[string(job.ActionUnitRunApply)+" "+r.unit.String()] = true
	})

	second, _, err := r2.svc.StartOnce(t.Context(), actorOf("ada"), other)
	if err != nil {
		t.Fatalf("second Start = %v", err)
	}

	if first.RequestHash == "" || first.RequestHash == second.RequestHash ||
		strings.Contains(first.RequestHash, "drift") {
		t.Fatalf("request hashes %q and %q; want two different hashes", first.RequestHash, second.RequestHash)
	}
}
