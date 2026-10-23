package job

import (
	"fmt"
	"slices"
)

// driftArgv holds the one argument list that each step of a drift check may
// have, in order: the fixed steps that the server plans for OpenTofu.
//
//nolint:gochecknoglobals // A fixed list, read only.
var driftArgv = [][]string{
	{"tofu", "init", "-input=false"},
	{"tofu", "plan", "-refresh-only", "-input=false", "-out=drift.tfplan"},
}

// driftEnvName is the one variable that a step of a drift check sets, with its
// one value.
const (
	driftEnvName  = "TF_IN_AUTOMATION"
	driftEnvValue = "1"
)

// ValidateDriftSteps checks the steps of a drift check, after ValidateSteps.
// A drift check reads the real resources and changes nothing, so every step
// runs in the plan phase with the role tool, marks no change, and leaves no
// plan for an apply phase. The check is an allowlist: the steps must be the
// fixed steps of driftArgv in order, each with the environment
// TF_IN_AUTOMATION=1 only and no Dir. Any other argument, variable or
// directory is refused. It returns an error that wraps ErrInvalid and
// names the first step that fails.
//
// The server gives the steps of a drift check itself and a caller never does.
// Defense in depth: the planner builds fixed steps, and this check stays so
// that a planner that reads custom steps never lets one into a drift check.
func ValidateDriftSteps(steps []Step) error {
	if err := ValidateSteps(steps); err != nil {
		return err
	}

	if len(steps) != len(driftArgv) {
		return fmt.Errorf("%w: a drift check has %d steps, not %d", ErrInvalid, len(steps), len(driftArgv))
	}

	for i := range steps {
		if err := validateDriftStep(&steps[i], driftArgv[i]); err != nil {
			return err
		}
	}

	return nil
}

// validateDriftStep checks one step of a drift check.
func validateDriftStep(s *Step, argv []string) error {
	switch {
	case s.Phase != PhasePlan:
		return fmt.Errorf("%w: step %q of a drift check runs in the %s phase, not plan", ErrInvalid, s.Name, s.Phase)
	case s.Role != RoleTool:
		return fmt.Errorf("%w: step %q of a drift check has the role %s: a drift check has no hook", ErrInvalid,
			s.Name, s.Role)
	case s.ChangesResources:
		return fmt.Errorf("%w: step %q of a drift check marks a change", ErrInvalid, s.Name)
	case slices.ContainsFunc(s.Outputs, func(o Output) bool { return o.Kind == OutputPlanApply }):
		return fmt.Errorf("%w: step %q of a drift check leaves a plan for an apply phase", ErrInvalid, s.Name)
	default:
	}

	if !slices.Equal(s.Args, argv) {
		return fmt.Errorf("%w: step %q of a drift check runs %q, not %q", ErrInvalid, s.Name, s.Args, argv)
	}

	if s.Dir != "" {
		return fmt.Errorf("%w: step %q of a drift check sets the directory %q", ErrInvalid, s.Name, s.Dir)
	}

	if len(s.Env) != 1 || s.Env[driftEnvName] != driftEnvValue {
		return fmt.Errorf("%w: step %q of a drift check sets a variable other than %s=%s", ErrInvalid, s.Name,
			driftEnvName, driftEnvValue)
	}

	return nil
}
