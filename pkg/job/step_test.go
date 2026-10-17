package job_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/job"
)

// planStep returns a valid step of the plan phase.
func planStep(name string) job.Step {
	return job.Step{
		Name: name, Role: job.RoleTool, Phase: job.PhasePlan, Args: []string{"tofu", "plan", "-out", "plan.bin"},
		Env: map[string]string{"TF_IN_AUTOMATION": "1"}, Dir: "", Timeout: 0, AllowFailure: false,
		ChangesResources: false, Outputs: []job.Output{{Kind: job.OutputPlanApply, Path: "plan.bin"}},
	}
}

func TestValidateSteps(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		change func(s []job.Step) []job.Step
		ok     bool
	}{
		{"valid", func(s []job.Step) []job.Step { return s }, true},
		{"no step", func([]job.Step) []job.Step { return nil }, false},
		{"too many steps", func([]job.Step) []job.Step {
			out := make([]job.Step, job.MaxSteps+1)
			for i := range out {
				out[i] = planStep("s" + strings.Repeat("a", i%10) + string(rune('a'+i%26)) + itoa(i))
			}

			return out
		}, false},
		{"duplicate name", func(s []job.Step) []job.Step { return append(s, planStep("plan")) }, false},
		{"upper case name", func(s []job.Step) []job.Step { s[0].Name = "Plan"; return s }, false},
		{"name ends with dash", func(s []job.Step) []job.Step { s[0].Name = "plan-"; return s }, false},
		{
			"long name",
			func(s []job.Step) []job.Step { s[0].Name = strings.Repeat("a", job.MaxStepName+1); return s },
			false,
		},
		{"unknown role", func(s []job.Step) []job.Step { s[0].Role = "script"; return s }, false},
		{"deliver phase", func(s []job.Step) []job.Step { s[0].Phase = job.PhaseDeliver; return s }, false},
		{
			"plan step changes resources",
			func(s []job.Step) []job.Step { s[0].ChangesResources = true; return s },
			false,
		},
		{"apply step changes resources", func(s []job.Step) []job.Step {
			s[0].Phase, s[0].ChangesResources = job.PhaseApply, true
			return s
		}, true},
		{"no argument", func(s []job.Step) []job.Step { s[0].Args = nil; return s }, false},
		{"empty program", func(s []job.Step) []job.Step { s[0].Args = []string{""}; return s }, false},
		{"relative program path", func(s []job.Step) []job.Step { s[0].Args[0] = "bin/tofu"; return s }, false},
		{
			"unclean absolute program",
			func(s []job.Step) []job.Step { s[0].Args[0] = "/usr/../bin/tofu"; return s },
			false,
		},
		{"absolute program", func(s []job.Step) []job.Step { s[0].Args[0] = "/usr/bin/tofu"; return s }, true},
		{"NUL in argument", func(s []job.Step) []job.Step { s[0].Args[1] = "pl\x00an"; return s }, false},
		{"shell text stays one argument", func(s []job.Step) []job.Step {
			s[0].Args = []string{"echo", "$HOME; rm -rf /"}
			return s
		}, true},
		{"too many arguments", func(s []job.Step) []job.Step {
			s[0].Args = append([]string{"tofu"}, make([]string, job.MaxArgs)...)
			return s
		}, false},
		{"arguments too large", func(s []job.Step) []job.Step {
			s[0].Args = []string{"tofu", strings.Repeat("a", job.MaxArgsBytes)}
			return s
		}, false},
		{
			"bad variable name",
			func(s []job.Step) []job.Step { s[0].Env = map[string]string{"1X": "a"}; return s },
			false,
		},
		{
			"reserved variable",
			func(s []job.Step) []job.Step { s[0].Env = map[string]string{"DUSTPAN_JOB": "a"}; return s },
			false,
		},
		{"reserved variable lower case", func(s []job.Step) []job.Step {
			s[0].Env = map[string]string{"dustpan_job": "a"}
			return s
		}, false},
		{
			"NUL in variable",
			func(s []job.Step) []job.Step { s[0].Env = map[string]string{"A": "\x00"}; return s },
			false,
		},
		{"absolute directory", func(s []job.Step) []job.Step { s[0].Dir = "/etc"; return s }, false},
		{"directory leaves source", func(s []job.Step) []job.Step { s[0].Dir = "../other"; return s }, false},
		{"unclean directory", func(s []job.Step) []job.Step { s[0].Dir = "a/../b"; return s }, false},
		{"sub directory", func(s []job.Step) []job.Step { s[0].Dir = "modules/net"; return s }, true},
		{"negative timeout", func(s []job.Step) []job.Step { s[0].Timeout = -time.Second; return s }, false},
		{
			"timeout above limit",
			func(s []job.Step) []job.Step { s[0].Timeout = job.MaxStepTimeout + 1; return s },
			false,
		},
		{"unknown output", func(s []job.Step) []job.Step { s[0].Outputs[0].Kind = "state"; return s }, false},
		{
			"output leaves source",
			func(s []job.Step) []job.Step { s[0].Outputs[0].Path = "../plan.bin"; return s },
			false,
		},
		{"empty output path", func(s []job.Step) []job.Step { s[0].Outputs[0].Path = ""; return s }, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := job.ValidateSteps(tt.change([]job.Step{planStep("plan")}))
			if tt.ok && err != nil {
				t.Fatalf("ValidateSteps() = %v, want nil", err)
			}

			if !tt.ok && !errors.Is(err, job.ErrInvalid) {
				t.Fatalf("ValidateSteps() = %v, want ErrInvalid", err)
			}
		})
	}
}

// itoa formats n for the test step names.
func itoa(n int) string {
	const digits = "0123456789"
	if n < 10 {
		return digits[n : n+1]
	}

	return itoa(n/10) + digits[n%10:n%10+1]
}
