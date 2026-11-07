package dustpan

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/job"
)

// describeSteps renders the steps in a fixed form: one line for each step.
func describeSteps(steps []job.Step) string {
	var b strings.Builder

	for i := range steps {
		s := &steps[i]
		fmt.Fprintf(&b, "%s/%s changes=%t env=%v outputs=%v: %s\n", s.Phase, s.Name, s.ChangesResources, s.Env,
			s.Outputs, strings.Join(s.Args, " "))
	}

	return b.String()
}

func TestOperationSteps(t *testing.T) {
	t.Parallel()

	const (
		tofuEnv   = "map[TF_IN_AUTOMATION:1]"
		pulumiEnv = "map[PULUMI_SKIP_UPDATE_CHECK:true]"
		pulumiExp = "map[PULUMI_EXPERIMENTAL:true PULUMI_SKIP_UPDATE_CHECK:true]"
		tofuOut   = "[{plan-apply tfplan}]"
		stack     = "--stack=acme/web/prod"
	)

	tests := []struct {
		name string
		tool string
		unit string
		op   job.Operation
		want string
	}{
		{
			"opentofu plan", "opentofu", "network", job.OperationPlan,
			"plan/init changes=false env=" + tofuEnv + " outputs=[]: tofu init -input=false\n" +
				"plan/plan changes=false env=" + tofuEnv + " outputs=" + tofuOut + ": tofu plan -input=false -out=tfplan\n",
		},
		{
			"opentofu apply", "opentofu", "network", job.OperationApply,
			"plan/init changes=false env=" + tofuEnv + " outputs=[]: tofu init -input=false\n" +
				"plan/plan changes=false env=" + tofuEnv + " outputs=" + tofuOut + ": tofu plan -input=false -out=tfplan\n" +
				"apply/apply-init changes=false env=" + tofuEnv + " outputs=[]: tofu init -input=false\n" +
				"apply/apply changes=true env=" + tofuEnv + " outputs=[]: tofu apply -input=false tfplan\n",
		},
		{
			"opentofu destroy", "opentofu", "network", job.OperationDestroy,
			"plan/init changes=false env=" + tofuEnv + " outputs=[]: tofu init -input=false\n" +
				"plan/plan changes=false env=" + tofuEnv + " outputs=" + tofuOut +
				": tofu plan -destroy -input=false -out=tfplan\n" +
				"apply/apply-init changes=false env=" + tofuEnv + " outputs=[]: tofu init -input=false\n" +
				"apply/apply changes=true env=" + tofuEnv + " outputs=[]: tofu apply -input=false tfplan\n",
		},
		{
			"pulumi plan",
			"pulumi",
			"web/prod",
			job.OperationPlan,
			"plan/preview changes=false env=" + pulumiEnv + " outputs=[]: pulumi preview --non-interactive " + stack + "\n",
		},
		{
			"pulumi apply", "pulumi", "web/prod", job.OperationApply,
			"plan/preview changes=false env=" + pulumiExp + " outputs=[{plan-apply pulumi-plan.json}]: " +
				"pulumi preview --non-interactive " + stack + " --save-plan=pulumi-plan.json\n" +
				"apply/up changes=true env=" + pulumiExp + " outputs=[]: " +
				"pulumi up --non-interactive " + stack + " --plan=pulumi-plan.json --yes --skip-preview\n",
		},
		{
			"pulumi destroy", "pulumi", "web/prod", job.OperationDestroy,
			"plan/preview changes=false env=" + pulumiEnv + " outputs=[]: " +
				"pulumi destroy --preview-only --non-interactive " + stack + "\n" +
				"apply/destroy changes=true env=" + pulumiEnv + " outputs=[]: " +
				"pulumi destroy --non-interactive " + stack + " --yes --skip-preview\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			steps, err := stepsOf(testUnit(t, tt.tool, tt.unit), tt.op, job.PurposeNone)
			if err != nil {
				t.Fatalf("stepsOf = %v", err)
			}

			if got := describeSteps(steps); got != tt.want {
				t.Errorf("steps =\n%s\nwant\n%s", got, tt.want)
			}

			err = job.ValidateSteps(steps)
			if err != nil {
				t.Errorf("ValidateSteps = %v, want nil", err)
			}
		})
	}
}

func TestOperationStepsRefuseOtherTools(t *testing.T) {
	t.Parallel()

	for _, op := range []job.Operation{job.OperationApply, job.OperationDestroy} {
		_, err := stepsOf(testUnit(t, "terraform", "network"), op, job.PurposeNone)
		if !errors.Is(err, job.ErrUnsupported) {
			t.Errorf("stepsOf(terraform, %s) = %v, want ErrUnsupported", op, err)
		}
	}
}

// TestApplyStepsRefuseHostilePulumiNames proves that the stack allowlist holds
// for the apply and destroy steps too, with no help from the other layers.
func TestApplyStepsRefuseHostilePulumiNames(t *testing.T) {
	t.Parallel()

	for _, op := range []job.Operation{job.OperationApply, job.OperationDestroy} {
		for _, name := range []string{"web/prod;rm", "web/$(id)", "web/-prod", "web/pr od"} {
			steps, err := stepsOf(testUnit(t, "pulumi", name), op, job.PurposeNone)
			if !errors.Is(err, job.ErrInvalid) || steps != nil {
				t.Errorf("stepsOf(%q, %s) = %+v, %v; want ErrInvalid and no step", name, op, steps, err)
			}
		}
	}
}
