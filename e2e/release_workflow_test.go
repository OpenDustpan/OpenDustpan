//go:build e2e

package e2e_test

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// releaseWorkflow reads the release workflow file as text. It names no
// route and no credential kind.
func releaseWorkflow(t *testing.T) string {
	t.Helper()

	root, err := os.OpenRoot("..")
	if err != nil {
		t.Fatalf("open the repository: %v", err)
	}

	raw, readErr := root.ReadFile(".github/workflows/release.yml")
	if closeErr := root.Close(); readErr != nil || closeErr != nil {
		t.Fatalf("read the release workflow: %v %v", readErr, closeErr)
	}

	return string(raw)
}

// stepIndex answers the index of the step with the name in the workflow,
// or -1 when the workflow has no such step. It matches the step
// definition only, not comments.
func stepIndex(workflow, name string) int {
	return strings.Index(workflow, "\n      - name: "+name+"\n")
}

// requireSteps fails the test when the workflow lacks a named step.
func requireSteps(t *testing.T, workflow string, names ...string) []int {
	t.Helper()

	indexes := make([]int, len(names))
	for i, name := range names {
		indexes[i] = stepIndex(workflow, name)
		if indexes[i] < 0 {
			t.Fatalf("the release workflow lacks the step %q", name)
		}
	}

	return indexes
}

// TestReleasePushesOneDigestToBothRegistries checks the structure of the
// release workflow: the untrusted-run guard, the permission and allowlist
// checks before the first login, the two-registry push with the forge
// registry first, the re-run rules, the digest comparison and the secret
// error paths. It reads a text file only.
func TestReleasePushesOneDigestToBothRegistries(t *testing.T) {
	t.Parallel()

	workflow := releaseWorkflow(t)

	checkJobGuard(t, workflow)
	checkImageRefs(t, workflow)
	checkTagAllowlist(t, workflow)
	checkChecksBeforeLogin(t, workflow)
	checkPushOrder(t, workflow)
	checkRerunRules(t, workflow)
	checkErrorPaths(t, workflow)
	checkChartPush(t, workflow)
	checkActionPins(t, workflow)
}

// checkJobGuard checks that the job guard limits the publish job to tag
// events in the main repository, so fork runs and pull_request runs stop
// before any secret read.
func checkJobGuard(t *testing.T, workflow string) {
	t.Helper()

	for _, want := range []string{
		"github.event_name == 'push'",
		"startsWith(github.ref, 'refs/tags/v')",
		"github.repository == 'OpenDustpan/OpenDustpan'",
	} {
		if !strings.Contains(workflow, want) {
			t.Errorf("the release workflow lacks the job guard %q", want)
		}
	}
}

// checkImageRefs checks that the workflow defines both image references
// once as named top-level values and repeats no literal registry path.
func checkImageRefs(t *testing.T, workflow string) {
	t.Helper()

	for _, want := range []string{
		"FORGE_IMAGE: ghcr.io/opendustpan/opendustpan",
		"HUB_IMAGE: docker.io/opendustpan/opendustpan",
	} {
		if !strings.Contains(workflow, want) {
			t.Errorf("the release workflow lacks the named image reference %q", want)
		}
	}
	if count := strings.Count(workflow, "ghcr.io/opendustpan/opendustpan"); count != 1 {
		t.Errorf("the release workflow repeats the forge image reference %d times, want 1", count)
	}
	if count := strings.Count(workflow, "docker.io/opendustpan/opendustpan"); count != 1 {
		t.Errorf("the release workflow repeats the Docker Hub image reference %d times, want 1", count)
	}
}

// checkTagAllowlist checks that the allowlist accepts only final version
// tags of the form vX.Y.Z with no suffix, and that it reads the tag
// through the environment so that a tag name cannot inject shell
// metacharacters.
func checkTagAllowlist(t *testing.T, workflow string) {
	t.Helper()

	if !strings.Contains(workflow, `^v[0-9]+\.[0-9]+\.[0-9]+$`) {
		t.Error("the release workflow lacks the final version tag pattern")
	}
	if !strings.Contains(workflow, "TAG: ${{ github.ref_name }}") {
		t.Error("the release workflow does not pass the tag through env")
	}
}

// checkChecksBeforeLogin checks that the permission check and the
// allowlist run before the first registry login, that the secret check
// runs before the Docker Hub login, and that the permission step passes
// its token through the environment.
func checkChecksBeforeLogin(t *testing.T, workflow string) {
	t.Helper()

	indexes := requireSteps(t, workflow,
		"Check that the actor has release permission",
		"Check that the tag is a final version tag",
		"Check that the Docker Hub secrets exist",
		"Log in to Docker Hub",
	)
	permission, allowlist, secrets, hubLogin := indexes[0], indexes[1], indexes[2], indexes[3]
	firstLogin := strings.Index(workflow, "docker/login")
	if firstLogin < 0 {
		firstLogin = strings.Index(workflow, "docker login")
	}
	if firstLogin < 0 {
		t.Fatal("the release workflow has no registry login")
	}

	if permission > firstLogin || allowlist > firstLogin || secrets > hubLogin {
		t.Error("the release workflow runs a check after its login:")
		t.Errorf("permission %d, allowlist %d, secrets %d, first login %d, hub login %d",
			permission, allowlist, secrets, firstLogin, hubLogin)
	}
	if !strings.Contains(workflow, "collaborators") {
		t.Error("the release workflow has no repository permission check")
	}
	if !strings.Contains(workflow, "GH_TOKEN: ${{ secrets.GITHUB_TOKEN }}") {
		t.Error("the release workflow does not pass the token to the permission check through env")
	}
}

// checkPushOrder checks that the secret check and the Docker Hub login
// run before any push, that the forge push runs before the Docker Hub
// push, that one build feeds both registries, that the comparison runs
// after the pushes, and that the chart push runs last.
func checkPushOrder(t *testing.T, workflow string) {
	t.Helper()

	indexes := requireSteps(t, workflow,
		"Check that the Docker Hub secrets exist",
		"Log in to Docker Hub",
		"Build once and push to the forge container registry",
		"Push the same digest to Docker Hub",
		"Compare the digests of both registries",
		"Push the chart as an OCI artifact to the forge registry",
	)
	secrets, hubLogin := indexes[0], indexes[1]
	forgePush, hubPush, compare, chart := indexes[2], indexes[3], indexes[4], indexes[5]

	if secrets > hubLogin || hubLogin > forgePush {
		t.Errorf("the release workflow orders its checks after its logins: secrets %d, hub login %d, forge push %d",
			secrets, hubLogin, forgePush)
	}
	if forgePush > hubPush || hubPush > compare || compare > chart {
		t.Errorf("the release workflow has the wrong step order: forge %d, hub %d, compare %d, chart %d",
			forgePush, hubPush, compare, chart)
	}
	if !strings.Contains(workflow, "--platform linux/amd64,linux/arm64") {
		t.Error("the release workflow does not build both architectures")
	}
}

// checkRerunRules checks that the re-run rules inspect both registries
// before the build: refuse when both hold the same digest, refuse on
// different digests, and otherwise push only to the missing registry.
func checkRerunRules(t *testing.T, workflow string) {
	t.Helper()

	for _, want := range []string{
		"apply the re-run rules",
		"Both registries already hold the same digest",
		"refuses to overwrite",
		"MODE=complete-hub",
		"MODE=complete-forge",
		"MODE=build",
		"imagetools inspect",
		"imagetools create",
	} {
		if !strings.Contains(workflow, want) {
			t.Errorf("the release workflow lacks the re-run rule %q", want)
		}
	}
}

// checkErrorPaths checks that each failure announces itself and stops the
// run: no dead `$?` check, and the one setup command for each secret
// error.
func checkErrorPaths(t *testing.T, workflow string) {
	t.Helper()

	if strings.Contains(workflow, "$?") {
		t.Error("the release workflow reads $?, which is dead code under bash -e")
	}
	if !strings.Contains(workflow, "DOCKERHUB_TOKEN") || !strings.Contains(workflow, "DOCKERHUB_USERNAME") {
		t.Error("the release workflow does not name both Docker Hub secrets")
	}
	for _, want := range []string{
		"gh secret set DOCKERHUB_TOKEN --repo OpenDustpan/OpenDustpan",
		"gh secret set DOCKERHUB_USERNAME --repo OpenDustpan/OpenDustpan",
		"docker buildx imagetools inspect <image>:<tag>",
	} {
		if !strings.Contains(workflow, want) {
			t.Errorf("the release workflow lacks the error text %q", want)
		}
	}
}

// checkChartPush checks that the chart goes to the forge registry as a
// packaged OCI artifact.
func checkChartPush(t *testing.T, workflow string) {
	t.Helper()

	for _, want := range []string{
		"helm package deploy/helm/opendustpan",
		"helm registry login ghcr.io",
		"helm push \"opendustpan-$VERSION.tgz\" oci://ghcr.io/opendustpan/charts",
	} {
		if !strings.Contains(workflow, want) {
			t.Errorf("the release workflow lacks the chart push step %q", want)
		}
	}
}

// checkActionPins checks that each action pin is a full 40-character
// hexadecimal SHA. The checkout pin is a SHA known from this repository.
func checkActionPins(t *testing.T, workflow string) {
	t.Helper()

	pins := regexp.MustCompile(`(?m)^\s*-? ?uses: [^@\s]+@(\S+)`).FindAllStringSubmatch(workflow, -1)
	if len(pins) == 0 {
		t.Fatal("the release workflow has no pinned action")
	}
	hex := regexp.MustCompile(`^[0-9a-fA-F]+$`)
	for _, match := range pins {
		if len(match[1]) != 40 || !hex.MatchString(match[1]) {
			t.Errorf("the release workflow has a pin that is not a full SHA: %q", match[0])
		}
	}
	if !strings.Contains(workflow, "actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1") {
		t.Error("the release workflow does not pin checkout by the SHA known from this repository")
	}
}
