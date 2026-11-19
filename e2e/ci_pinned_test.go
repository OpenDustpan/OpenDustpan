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
	return []string{"../.github/workflows/ci.yml", "../Makefile"}
}

// ciVersionPattern finds a version setting in a workflow ("E2E_X_VERSIONS: v")
// or in a Makefile recipe ("E2E_X_VERSIONS=v").
var ciVersionPattern = regexp.MustCompile(`\b(E2E_[A-Z]+_VERSIONS)[:=][ \t]*([^\s'"]+)`)

// TestCIVersionsHavePinnedSums fails for each tool version that a CI job runs
// and that has no pinned sum for a CI platform. Without the sum, the fetcher
// downloads the published checksum file before the cache check, and an outage
// of that host fails the run. The test reads the same files that CI reads and
// expands each value with Tool.Versions, as the e2e run does.
//
//nolint:paralleltest // t.Setenv sets the variable that Tool.Versions reads.
func TestCIVersionsHavePinnedSums(t *testing.T) {
	found := 0

	for _, source := range ciVersionSources() {
		body, err := os.ReadFile(source) //nolint:gosec // The paths are the fixed list above.
		if err != nil {
			t.Fatalf("read %s: %v", source, err)
		}

		for _, match := range ciVersionPattern.FindAllStringSubmatch(string(body), -1) {
			found += checkCIVersions(t, source, match[1], match[2])
		}
	}

	if found == 0 {
		t.Fatalf("found no CI tool version in %s: the pattern is stale", strings.Join(ciVersionSources(), ", "))
	}
}

// checkCIVersions checks each version that value selects for env, and returns
// the number of versions that it checked.
func checkCIVersions(t *testing.T, source, env, value string) int {
	t.Helper()

	tools := map[string]func() e2e.Tool{
		"E2E_PULUMI_VERSIONS":     e2e.Pulumi,
		"E2E_TOFU_VERSIONS":       e2e.OpenTofu,
		"E2E_TERRAGRUNT_VERSIONS": e2e.Terragrunt,
	}

	newTool, ok := tools[env]
	if !ok {
		t.Errorf("%s sets %s, which this test does not know: add the tool to this test", source, env)

		return 0
	}

	t.Setenv(env, value)

	tool := newTool()
	sums := e2e.PinnedSums(tool)
	versions := tool.Versions()

	for _, version := range versions {
		if version == e2e.Latest {
			t.Errorf("%s sets %s to %q: only the nightly run may use latest", source, env, value)

			continue
		}

		for _, p := range [][2]string{{"linux", "amd64"}, {"linux", "arm64"}, {"darwin", "amd64"}, {"darwin", "arm64"}} {
			archive := e2e.ArchiveNameFor(tool, version, p[0], p[1])
			if sums[archive] == "" {
				t.Errorf("%s runs %s version %s in CI, and %s has no pinned sum", source, env, version, archive)
			}
		}
	}

	return len(versions)
}
