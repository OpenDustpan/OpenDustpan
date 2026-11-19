//go:build e2e

package e2e_test

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

// ciVersionSources are the files that set the tool versions of a job that runs
// on a pull request, a merge group or a push: the CI workflow and the Makefile
// targets that it calls. The nightly workflow is not here. Only its latest rows
// may fetch a published checksum file.
func ciVersionSources() []string {
	return []string{".github/workflows/ci.yml", "Makefile"}
}

// ciVersionPattern finds a version setting in a workflow ("E2E_X_VERSIONS: v")
// or in a Makefile recipe ("E2E_X_VERSIONS=v"). The value runs to the first
// space, so a value with a space needs the check for "${{" below.
var ciVersionPattern = regexp.MustCompile(`\b(E2E_[A-Z]+_VERSIONS)[:=][ \t]*([^\s'"]+)`)

// ciTools lists every tool of the e2e suite, so a tool that no CI file sets
// still has its defaults checked.
func ciTools() []e2e.Tool {
	return []e2e.Tool{e2e.Pulumi(), e2e.OpenTofu(), e2e.Terragrunt()}
}

// TestCIVersionsHavePinnedSums fails for each tool version that a CI job runs
// and that has no pinned sum for a CI platform. Without the sum, the fetcher
// downloads the published checksum file before the cache check, and an outage
// of that host fails the run. The test reads the same files that CI reads and
// expands each value as the e2e run does. It also checks the defaults of each
// tool, because a job that leaves the variable unset runs the defaults.
func TestCIVersionsHavePinnedSums(t *testing.T) {
	t.Parallel()

	root, err := os.OpenRoot("..")
	if err != nil {
		t.Fatalf("open the repository root: %v", err)
	}

	t.Cleanup(func() {
		if err := root.Close(); err != nil {
			t.Errorf("close the repository root: %v", err)
		}
	})

	byEnv := map[string]e2e.Tool{}
	for _, tool := range ciTools() {
		byEnv[e2e.VersionsEnv(tool)] = tool
		// The variable is unset: the run uses the defaults.
		checkCIVersions(t, tool, "the defaults of "+e2e.VersionsEnv(tool), "")
	}

	found := 0

	for _, source := range ciVersionSources() {
		found += checkCISource(t, root, byEnv, source)
	}

	if found == 0 {
		t.Fatalf("found no CI tool version in %s: the pattern is stale", strings.Join(ciVersionSources(), ", "))
	}
}

// checkCISource checks each version setting in source and returns the number
// of settings that it checked.
func checkCISource(t *testing.T, root *os.Root, byEnv map[string]e2e.Tool, source string) int {
	t.Helper()

	body, err := root.ReadFile(source)
	if err != nil {
		t.Fatalf("read %s: %v", source, err)
	}

	found := 0

	for _, match := range ciVersionPattern.FindAllStringSubmatch(string(body), -1) {
		env, value := match[1], match[2]
		where := source + " sets " + env

		tool, ok := byEnv[env]
		if !ok {
			t.Errorf("%s, which this test does not know: add the tool to ciTools", where)

			continue
		}

		if strings.Contains(value, "${{") {
			t.Errorf("%s to %q: this test cannot expand an expression, so set a literal value", where, value)

			continue
		}

		checkCIVersions(t, tool, where+" to "+value, value)

		found++
	}

	return found
}

// checkCIVersions checks each version that value selects for tool. The label
// says where the value comes from.
func checkCIVersions(t *testing.T, tool e2e.Tool, label, value string) {
	t.Helper()

	sums := e2e.PinnedSums(tool)

	for _, version := range e2e.ExpandVersions(tool, value) {
		if version == e2e.Latest {
			t.Errorf("%s: only the nightly run may use latest", label)

			continue
		}

		for _, p := range [][2]string{{"linux", "amd64"}, {"linux", "arm64"}, {"darwin", "amd64"}, {"darwin", "arm64"}} {
			archive := e2e.ArchiveNameFor(tool, version, p[0], p[1])
			if sums[archive] == "" {
				t.Errorf("%s: version %s runs in CI, and %s has no pinned sum", label, version, archive)
			}
		}
	}
}
