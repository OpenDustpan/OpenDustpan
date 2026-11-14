//go:build e2e

package e2e_test

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// helmTemplate runs `helm template` for the chart with the default values
// and answers its output. It skips the test when the helm binary is
// missing, the way the image trial test skips when its helper is missing.
func helmTemplate(t *testing.T) string {
	t.Helper()

	checkHelm(t)

	template := exec.CommandContext(t.Context(), "helm",
		"template", "opendustpan", "../deploy/helm/opendustpan",
		"--kube-version", "1.35.0",
		"--values", "../deploy/helm/opendustpan/ci/minimal-values.yaml",
	)

	return runHelmTemplate(t, "defaults", template)
}

// helmTemplateOverride runs `helm template` for the chart with the Docker
// Hub repository override and answers its output.
func helmTemplateOverride(t *testing.T) string {
	t.Helper()

	checkHelm(t)

	template := exec.CommandContext(t.Context(), "helm",
		"template", "opendustpan", "../deploy/helm/opendustpan",
		"--kube-version", "1.35.0",
		"--values", "../deploy/helm/opendustpan/ci/minimal-values.yaml",
		"--set", "image.repository=docker.io/opendustpan/opendustpan",
	)

	return runHelmTemplate(t, "override", template)
}

// checkHelm skips the test when the helm binary is missing.
func checkHelm(t *testing.T) {
	t.Helper()

	if _, err := exec.LookPath("helm"); err != nil {
		t.Skip("the chart e2e step needs the helm binary")
	}
}

// runHelmTemplate runs the helm command and answers its output.
func runHelmTemplate(t *testing.T, what string, template *exec.Cmd) string {
	t.Helper()

	home := t.TempDir()
	template.Env = append(os.Environ(), "HELM_CONFIG_HOME="+home, "HELM_CACHE_HOME="+home)

	out, err := template.CombinedOutput()
	if err != nil {
		t.Fatalf("helm template with %s: %v\n%s", what, err, out)
	}

	return string(out)
}

// renderedImage answers the image line of the server Deployment in the
// rendered chart output.
func renderedImage(t *testing.T, output string) string {
	t.Helper()

	for line := range strings.Lines(output) {
		if trimmed := strings.TrimSpace(line); strings.HasPrefix(trimmed, "image:") {
			return trimmed
		}
	}

	t.Fatal("the rendered chart has no image line")
	return ""
}

// TestChartDefaultRegistryAndOverride runs `helm template` with the
// defaults and with the Docker Hub override value, and checks that the
// default image names the forge container registry and the override
// names Docker Hub. It names no route and no credential kind.
func TestChartDefaultRegistryAndOverride(t *testing.T) {
	t.Parallel()

	def := renderedImage(t, helmTemplate(t))
	if !strings.Contains(def, "ghcr.io/opendustpan/opendustpan") {
		t.Errorf("the chart default image is %q, want the forge container registry", def)
	}

	hub := renderedImage(t, helmTemplateOverride(t))
	if !strings.Contains(hub, "docker.io/opendustpan/opendustpan") {
		t.Errorf("the chart override image is %q, want Docker Hub", hub)
	}
}
