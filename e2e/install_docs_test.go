//go:build e2e

package e2e_test

import (
	"os"
	"strings"
	"testing"
)

// TestInstallPageListsBothPullCommands reads the install page as text and
// checks that it lists the pull command for the forge container registry
// and the pull command for Docker Hub. It names no route and no credential
// kind.
func TestInstallPageListsBothPullCommands(t *testing.T) {
	t.Parallel()

	root, err := os.OpenRoot("..")
	if err != nil {
		t.Fatalf("open the repository: %v", err)
	}

	raw, readErr := root.ReadFile("docs/install.md")
	if closeErr := root.Close(); readErr != nil || closeErr != nil {
		t.Fatalf("read the install page: %v %v", readErr, closeErr)
	}
	page := string(raw)

	for _, want := range []string{
		"docker pull ghcr.io/opendustpan/opendustpan:",
		"docker pull docker.io/opendustpan/opendustpan:",
		"DOCKERHUB_USERNAME",
		"DOCKERHUB_TOKEN",
		"gh secret set DOCKERHUB_TOKEN --repo OpenDustpan/OpenDustpan",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("the install page lacks %q", want)
		}
	}
}
