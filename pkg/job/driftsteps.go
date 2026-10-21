package job

import (
	"fmt"
	"slices"
	"strings"
)

// driftEnvPrefixes start the names of variables that change what a tool does
// with its own arguments or its own files. A drift check never sets them: they
// could add an argument that the allowlist of the server did not name.
//
//nolint:gochecknoglobals // A fixed list, read only.
var driftEnvPrefixes = []string{"TF_CLI_ARGS", "TF_CLI_CONFIG_FILE", "TF_PLUGIN_CACHE", "TF_REATTACH_PROVIDERS"}

// ValidateDriftSteps checks the steps of a drift check, after ValidateSteps.
// A drift check reads the real resources and changes nothing, so every step
// runs in the plan phase with the role tool, marks no change, sets no variable
// that a tool reads as extra arguments (TF_CLI_ARGS and the like), and leaves
// no plan for an apply phase. It returns an error that wraps ErrInvalid and
// names the first step that fails.
//
// The server gives the steps of a drift check itself and a caller never does.
// Defense in depth: the planner builds fixed steps, and this check stays so
// that a planner that reads custom steps never lets one into a drift check.
func ValidateDriftSteps(steps []Step) error {
	if err := ValidateSteps(steps); err != nil {
		return err
	}

	for i := range steps {
		if err := validateDriftStep(&steps[i]); err != nil {
			return err
		}
	}

	return nil
}

// validateDriftStep checks one step of a drift check.
func validateDriftStep(s *Step) error {
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

	for name := range s.Env {
		if isDriftEnv(name) {
			return fmt.Errorf("%w: step %q of a drift check sets %s, which changes the arguments of the tool",
				ErrInvalid, s.Name, name)
		}
	}

	return nil
}

// isDriftEnv reports whether name is a variable that a drift check never sets.
func isDriftEnv(name string) bool {
	upper := strings.ToUpper(name)

	return slices.ContainsFunc(driftEnvPrefixes, func(prefix string) bool { return strings.HasPrefix(upper, prefix) })
}
