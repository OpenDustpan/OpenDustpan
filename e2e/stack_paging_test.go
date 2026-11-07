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
	// pagingOtherProject is a second project in the same namespace. A
	// filtered stack ls must not list its stacks.
	pagingOtherProject = "other"
	// pagingOtherStacks is the number of stacks in pagingOtherProject.
	pagingOtherStacks = 3
	// pagingStacks is one more than a page of GET /api/user/stacks, so the
	// CLI must read a second page with the continuationToken.
	pagingStacks = 501
)

// pulumiStackPagingScenario creates pagingStacks stacks in one project of a
// namespace of their own, and a few stacks in a second project of the same
// namespace, through the API. It then proves that stack ls of the real CLI
// lists each stack of the first project once: the CLI follows the
// continuationToken of the stack list to the last page. It runs with the
// organization and without it, and from the project directory without --all:
// a client that does not follow the token lists only the first page of 500
// stacks, with no error, so each supported version must show that it follows
// it. The step without --all also proves that the project filter leaves out
// the second project.
func pulumiStackPagingScenario(t *testing.T, env *e2e.Env, clis []e2e.Install) {
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

	for i := range pagingOtherStacks {
		path := "/api/stacks/" + pagingNamespace + "/" + pagingOtherProject
		body := `{"stackName":` + strconv.Quote(fmt.Sprintf("o%04d", i)) + `}`

		status, answer, err := env.APIStatus(ctx, http.MethodPost, path, body)
		if err != nil || status != http.StatusOK {
			t.Fatalf("create stack %d of %q answers %d, %v (%s)", i, pagingOtherProject, status, err, answer)
		}
	}

	env.WritePulumiProject(t, "name: "+pagingProject+"\nruntime: yaml\n")

	for _, cli := range clis {
		env.SetCLI(cli)

		for _, s := range []e2e.StepSpec{
			step("login "+cli.Version, nil, "login"),
			step("stack ls of one organization over two pages with "+cli.Version, pagingStacksListed,
				"stack", "ls", "--json", "--all", "--organization", pagingNamespace),
			step("stack ls of every organization over two pages with "+cli.Version, pagingStacksListed,
				"stack", "ls", "--json", "--all"),
		} {
			env.Step(ctx, t, s)
		}

		env.StepIn(ctx, t, env.ProjectDir(), step(
			"stack ls of one project with "+cli.Version, pagingProjectStacksListed,
			"stack", "ls", "--json", "--organization", pagingNamespace,
		))
	}
}

// pagingStacksListed checks that stack ls --json lists each stack of the
// paging project once.
func pagingStacksListed(r e2e.Result) error {
	var stacks []struct {
		Name        string `json:"name"`
		ProjectName string `json:"projectName"`
	}

	if err := json.Unmarshal([]byte(r.Stdout), &stacks); err != nil {
		return fmt.Errorf("%w: decode stack list: %w", errUnexpected, err)
	}

	seen := map[string]bool{}

	for _, s := range stacks {
		// Defense in depth: the namespace and the project both select the stacks
		// of this scenario, so a project of the same name in another namespace
		// does not count.
		if !strings.HasPrefix(s.Name, pagingNamespace+"/") || s.ProjectName != pagingProject {
			continue
		}

		if seen[s.Name] {
			return fmt.Errorf("%w: stack %q is listed two times", errUnexpected, s.Name)
		}

		seen[s.Name] = true
	}

	if len(seen) != pagingStacks {
		return fmt.Errorf(
			"%w: %d stacks of project %q listed, want %d",
			errUnexpected,
			len(seen),
			pagingProject,
			pagingStacks,
		)
	}

	return nil
}

// pagingProjectStacksListed checks that stack ls lists each stack of
// pagingProject once, and no stack of another project.
func pagingProjectStacksListed(r e2e.Result) error {
	var stacks []struct {
		Name        string `json:"name"`
		ProjectName string `json:"projectName"`
	}

	if err := json.Unmarshal([]byte(r.Stdout), &stacks); err != nil {
		return fmt.Errorf("%w: decode stack list: %w", errUnexpected, err)
	}

	seen := map[string]bool{}

	for _, s := range stacks {
		if s.ProjectName != pagingProject {
			return fmt.Errorf(
				"%w: stack %q is in project %q, want %q",
				errUnexpected,
				s.Name,
				s.ProjectName,
				pagingProject,
			)
		}

		if seen[s.Name] {
			return fmt.Errorf("%w: stack %q is listed two times", errUnexpected, s.Name)
		}

		seen[s.Name] = true
	}

	if len(seen) != pagingStacks {
		return fmt.Errorf(
			"%w: %d stacks of project %q listed, want %d",
			errUnexpected,
			len(seen),
			pagingProject,
			pagingStacks,
		)
	}

	return nil
}
