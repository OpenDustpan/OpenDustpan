//go:build e2e

package e2e_test

import (
	"os"
	"strings"
	"testing"
)

// TestInstallPageListsBothPullCommands reads the install guide as text and
// checks that it lists the pull command for the forge container registry
// and the pull command for Docker Hub. It also checks the digest form of
// the reference and the two places that name the digest of a release, and
// that the page does not name the release notes, which hold no digest. It
// names no route and no credential kind.
func TestInstallPageListsBothPullCommands(t *testing.T) {
	t.Parallel()

	root, err := os.OpenRoot("..")
	if err != nil {
		t.Fatalf("open the repository: %v", err)
	}

	raw, readErr := root.ReadFile("docs/guides/install.md")
	if closeErr := root.Close(); readErr != nil || closeErr != nil {
		t.Fatalf("read the install guide: %v %v", readErr, closeErr)
	}
	page := string(raw)

	for _, want := range []string{
		"docker pull ghcr.io/opendustpan/opendustpan:",
		"docker pull docker.io/opendustpan/opendustpan:",
		"ghcr.io/opendustpan/opendustpan@sha256:<digest>",
		"docker buildx imagetools inspect",
		"workflow run",
		"DOCKERHUB_USERNAME",
		"DOCKERHUB_TOKEN",
		"gh secret set DOCKERHUB_TOKEN --repo OpenDustpan/OpenDustpan",
		"--set database.existingSecret.name=",
		"--set keyEncryptionKey.existingSecret.name=",
		"kubectl create secret generic opendustpan-database",
		"kubectl create secret generic opendustpan-key-encryption-key",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("the install guide lacks %q", want)
		}
	}

	if strings.Contains(page, "release notes") {
		t.Error("the install guide names the release notes, which hold no digest")
	}
}
