//go:build e2e

package e2e_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

const (
	// pagingNamespace holds the stacks of the paging scenario, so no other
	// step counts them.
	pagingNamespace = "pages"
	// pagingProject is the project of those stacks.
	pagingProject = "paging"
	// pagingStacks is one more than a page of GET /api/user/stacks, so the
	// CLI must read a second page with the continuationToken.
	pagingStacks = 501
)

// pulumiStackPagingScenario creates pagingStacks stacks in a namespace of
// their own through the API, then proves that stack ls of the real CLI
// lists each of them once: the CLI follows the continuationToken of the
// stack list to the last page. It runs with the organization and without it.
func pulumiStackPagingScenario(t *testing.T, env *e2e.Env) {
	t.Helper()

	ctx := t.Context()

	createSecondNamespaceThroughAPI(t, env, pagingNamespace)

	for i := range pagingStacks {
		path := "/api/stacks/" + pagingNamespace + "/" + pagingProject
		body := `{"stackName":` + strconv.Quote(fmt.Sprintf("s%04d", i)) + `}`

		status, answer, err := env.APIStatus(ctx, http.MethodPost, path, body)
		if err != nil || status != http.StatusOK {
			t.Fatalf("create stack %d answers %d, %v (%s)", i, status, err, answer)
		}
	}

	for _, s := range []e2e.StepSpec{
		step("stack ls of one organization over two pages", pagingStacksListed,
			"stack", "ls", "--json", "--all", "--organization", pagingNamespace),
		step("stack ls of every organization over two pages", pagingStacksListed, "stack", "ls", "--json", "--all"),
	} {
		env.Step(ctx, t, s)
	}
}

// pagingStacksListed checks that stack ls --json lists each stack of the
// paging namespace once.
func pagingStacksListed(r e2e.Result) error {
	var stacks []struct {
		Name string `json:"name"`
	}

	if err := json.Unmarshal([]byte(r.Stdout), &stacks); err != nil {
		return fmt.Errorf("%w: decode stack list: %w", errUnexpected, err)
	}

	seen := map[string]bool{}

	for _, s := range stacks {
		if !strings.HasPrefix(s.Name, pagingNamespace+"/") {
			continue
		}

		if seen[s.Name] {
			return fmt.Errorf("%w: stack %q is listed two times", errUnexpected, s.Name)
		}

		seen[s.Name] = true
	}

	if len(seen) != pagingStacks {
		return fmt.Errorf(
			"%w: %d stacks of %q listed, want %d",
			errUnexpected,
			len(seen),
			pagingNamespace,
			pagingStacks,
		)
	}

	return nil
}
