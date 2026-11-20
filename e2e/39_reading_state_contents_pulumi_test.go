//go:build e2e

package e2e_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

// readingStateSecret is the config secret of the Pulumi reading scenario. It
// is a test value only; no masked read may return it.
const readingStateSecret = "s3cr3t-reading-state"

// readingStateProgram returns the Pulumi.yaml of the reading scenario: one
// secret output from a secret config value.
func readingStateProgram() string {
	return "name: " + e2e.Project + "\nruntime: yaml\n" +
		"config:\n" +
		"  apiKey:\n" +
		"    type: string\n" +
		"    secret: true\n" +
		"outputs:\n" +
		"  apiKeyOut:\n" +
		"    fn::secret: ${apiKey}\n"
}

// pulumiReadingStateScenario proves the reads of the state with the real
// Pulumi CLI: one update with a secret output, then the existing resource
// list of the stack and the tool-neutral masked read of the same unit, each
// with an access token. No body holds the secret value.
func pulumiReadingStateScenario(t *testing.T, env *e2e.Env, org string) {
	t.Helper()

	ctx := t.Context()
	qualified := fmt.Sprintf("%s/%s/reading", org, e2e.Project)

	env.WritePulumiProject(t, readingStateProgram())
	env.Step(ctx, t, step("stack init reading", nil, "stack", "init", qualified, "--no-select"))
	env.Step(ctx, t, step(
		"config set secret for reading state", nil,
		"config", "set", "--secret", "--stack", qualified, "apiKey", readingStateSecret,
	))
	env.Step(ctx, t, step("up reading", nil, "up", "--yes", "--non-interactive", "--stack", qualified))
	env.Step(ctx, t, step(
		"reading state resources and masked read",
		checkReadingStatePulumi(ctx, env, org),
		"stack", "--show-name", "--stack", qualified,
	))
	env.Step(ctx, t, step("stack rm reading", nil, "stack", "rm", "--yes", "--force", "--stack", qualified))
}

// checkReadingStatePulumi returns the check that reads the resource list of
// the stack and the masked read of its unit after the update.
func checkReadingStatePulumi(ctx context.Context, env *e2e.Env, org string) func(e2e.Result) error {
	return func(e2e.Result) error {
		list := fmt.Sprintf("/api/stacks/%s/%s/reading/state/latest/resources", org, e2e.Project)

		body, err := maskedStateGet(ctx, env, list)
		if err != nil {
			return err
		}

		if strings.Contains(body, readingStateSecret) {
			return fmt.Errorf("%w: the resource list holds the secret value", errUnexpected)
		}

		ref := "pulumi:" + org + "/" + e2e.Project + "/reading"

		status, page, err := env.APIStatus(ctx, http.MethodGet, readingStatePath(ref, ""), "")
		if err != nil {
			return fmt.Errorf("%w: masked read: %w", errUnexpected, err)
		}

		if status != http.StatusOK {
			return fmt.Errorf(
				"%w: the masked read answers %d, want %d (%s)",
				errUnexpected,
				status,
				http.StatusOK,
				page,
			)
		}

		return checkReadingPage(ref, readingStateSecret)(page)
	}
}
