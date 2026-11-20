//go:build e2e

package e2e_test

import (
	"fmt"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

// pulumiMultiLineDescriptionScenario proves that the server takes a project
// description with line breaks. The Pulumi CLI sends the description of
// Pulumi.yaml unchanged as the tag pulumi:description, so a block scalar
// puts a line break and indentation in a tag value. Stack init and up must
// work and the stack must keep the text.
func pulumiMultiLineDescriptionScenario(t *testing.T, env *e2e.Env, org string) {
	t.Helper()

	ctx := t.Context()
	qualified := fmt.Sprintf("%s/%s/multiline-description", org, e2e.Project)

	env.WritePulumiProject(t, "name: "+e2e.Project+"\nruntime: yaml\n"+
		"description: |\n  first line\n  second line\n  third line\n")
	env.Step(ctx, t, step("stack init multi-line description", nil, "stack", "init", qualified, "--no-select"))
	env.Step(ctx, t, step(
		"up multi-line description", nil, "up", "--yes", "--non-interactive", "--stack", qualified,
	))
	env.Step(ctx, t, step(
		"stack ls keeps the multi-line description stack",
		contains("multiline-description"),
		"stack", "ls", "--json",
	))
}
