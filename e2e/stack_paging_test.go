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
	// pagingStackLetter starts the name of each stack of pagingProject, and
	// pagingOtherStackLetter that of each stack of pagingOtherProject. The
	// CLI output carries no project field, so the name tells the project.
	// pagingStackName makes the names, and the prefixes come from the same
	// letters, so the two cannot drift apart.
	pagingStackLetter      = "s"
	pagingOtherStackLetter = "o"
	// pagingStackPrefix starts the name of each stack of pagingProject.
	pagingStackPrefix = pagingNamespace + "/" + pagingStackLetter
	// pagingOtherStackPrefix starts the name of each stack of pagingOtherProject.
	pagingOtherStackPrefix = pagingNamespace + "/" + pagingOtherStackLetter
)

// pagingStackName returns the name of stack i of a project whose stacks start
// with letter. The prefixes above are built from the same letters.
func pagingStackName(letter string, i int) string {
	return fmt.Sprintf("%s%04d", letter, i)
}

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
		body := `{"stackName":` + strconv.Quote(pagingStackName(pagingStackLetter, i)) + `}`

		status, answer, err := env.APIStatus(ctx, http.MethodPost, path, body)
		if err != nil || status != http.StatusOK {
			t.Fatalf("create stack %d answers %d, %v (%s)", i, status, err, answer)
		}
	}

	for i := range pagingOtherStacks {
		path := "/api/stacks/" + pagingNamespace + "/" + pagingOtherProject
		body := `{"stackName":` + strconv.Quote(pagingStackName(pagingOtherStackLetter, i)) + `}`

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
// paging project once. The CLI output carries no project field, so the name
// tells the project: the other project's stacks are skipped here.
func pagingStacksListed(r e2e.Result) error {
	var stacks []struct {
		Name string `json:"name"`
	}

	if err := json.Unmarshal([]byte(r.Stdout), &stacks); err != nil {
		return fmt.Errorf("%w: decode stack list: %w", errUnexpected, err)
	}

	seen := map[string]bool{}

	for _, s := range stacks {
		// Defense in depth: the namespace and the name prefix both select the
		// stacks of this scenario, so a stack of another namespace does not count.
		if !strings.HasPrefix(s.Name, pagingNamespace+"/") || !strings.HasPrefix(s.Name, pagingStackPrefix) {
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

// pagingProjectStacksListed checks that a project-filtered stack ls lists each
// stack of pagingProject once, and no stack of another project. The CLI output
// carries no project field, so the scenario's naming is the discriminator: a
// stack of the paging project starts with pagingStackPrefix, and a stack of
// the other project starts with pagingOtherStackPrefix.
func pagingProjectStacksListed(r e2e.Result) error {
	var stacks []struct {
		Name string `json:"name"`
	}

	if err := json.Unmarshal([]byte(r.Stdout), &stacks); err != nil {
		return fmt.Errorf("%w: decode stack list: %w", errUnexpected, err)
	}

	seen := map[string]bool{}

	for _, s := range stacks {
		if !strings.HasPrefix(s.Name, pagingNamespace+"/") {
			return fmt.Errorf("%w: stack %q is outside namespace %q", errUnexpected, s.Name, pagingNamespace)
		}

		if strings.HasPrefix(s.Name, pagingOtherStackPrefix) {
			return fmt.Errorf("%w: stack %q of project %q is listed", errUnexpected, s.Name, pagingOtherProject)
		}

		if !strings.HasPrefix(s.Name, pagingStackPrefix) {
			return fmt.Errorf("%w: stack %q is not a stack of project %q", errUnexpected, s.Name, pagingProject)
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
