//go:build e2e

package e2e_test

import (
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
)

// TestInstallGuideCommands reads the install guide and runs the command of
// the page that needs no registry, no cluster and no repository setting:
// `helm template` of the chart with the --set values of the page. It checks
// that the command below passes the same --set values as the helm install
// command of the page. Each other command of the page (docker pull, kubectl
// create secret, helm install against a cluster, gh secret set) is a step of
// the usability check. It names no route and no credential kind.
func TestInstallGuideCommands(t *testing.T) {
	t.Parallel()

	template := exec.CommandContext(t.Context(), "helm",
		"template", "opendustpan", "../deploy/helm/opendustpan",
		"--kube-version", "1.35.0",
		"--set", "database.existingSecret.name=opendustpan-database",
		"--set", "keyEncryptionKey.existingSecret.name=opendustpan-key-encryption-key")

	// The arguments of the command are fixed, because gosec refuses an
	// argument of a process that is not a literal. The check below keeps
	// them equal to the helm install command of the page.
	command := firstHelmInstallCommand(t, readInstallGuide(t))
	got, want := helmSetValues(command), helmSetValues(template.Args)
	if !slices.Equal(got, want) {
		t.Fatalf("the helm install command of the install guide passes --set values %v, want %v", got, want)
	}

	// The check above needs no helm binary. The render below needs one, so
	// that step skips on a host without helm.
	checkHelm(t)

	rendered := runHelmTemplate(t, "the install guide", template)
	for _, name := range helmSecretNames(want) {
		if !strings.Contains(rendered, name) {
			t.Errorf("the render of the chart does not hold the Secret name %q", name)
		}
	}
}

// readInstallGuide answers the install guide as text.
func readInstallGuide(t *testing.T) string {
	t.Helper()

	root, err := os.OpenRoot("..")
	if err != nil {
		t.Fatalf("open the repository: %v", err)
	}

	raw, readErr := root.ReadFile("docs/guides/install.md")
	if closeErr := root.Close(); readErr != nil || closeErr != nil {
		t.Fatalf("read the install guide: %v %v", readErr, closeErr)
	}

	return string(raw)
}

// firstHelmInstallCommand answers the lines of the first `helm install`
// command of the page. A command starts at a line that holds "helm install"
// and continues while the line before it ends with a backslash.
func firstHelmInstallCommand(t *testing.T, page string) []string {
	t.Helper()

	var command []string
	for line := range strings.Lines(page) {
		trimmed := strings.TrimSpace(line)
		if len(command) == 0 && !strings.HasPrefix(trimmed, "helm install") {
			continue
		}

		command = append(command, trimmed)
		if strings.HasSuffix(trimmed, "\\") {
			continue
		}

		return command
	}

	t.Fatal("the install guide holds no helm install command")
	return nil
}

// helmSetValues answers the value of each --set argument of a helm command.
func helmSetValues(command []string) []string {
	var values []string

	fields := strings.Fields(strings.Join(command, " "))
	for i, field := range fields {
		if field == "--set" && i+1 < len(fields) {
			values = append(values, fields[i+1])
		}
	}

	return values
}

// helmSecretNames answers the Secret names of the --set values: the name of
// each value of the form <key>.existingSecret.name=<name>.
func helmSecretNames(values []string) []string {
	var names []string

	for _, value := range values {
		key, name, found := strings.Cut(value, "=")
		if found && strings.HasSuffix(key, ".existingSecret.name") && name != "" {
			names = append(names, name)
		}
	}

	return names
}
