package charttest_test

import (
	"bytes"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// update rewrites the golden files.
//
//nolint:gochecknoglobals // A test flag is read once; passing it as a parameter adds nothing.
var update = flag.Bool("update", false, "write the golden files of testdata")

const chartDir = "../opendustpan"

// kubeVersion is the Kubernetes version of the renders. The default of helm
// template is older than the kubeVersion of Chart.yaml.
const kubeVersion = "1.35.0"

// result holds the output of one helm run.
type result struct {
	stdout string
	stderr string
	err    error
}

// TestTemplateGolden compares the output of helm template with the golden
// file of each values profile. Run `go test ./deploy/helm/charttest -update`
// to write the golden files after a change to the chart.
func TestTemplateGolden(t *testing.T) {
	t.Parallel()

	helm := requireHelm(t)

	tests := []struct {
		name   string
		values string // path from the chart directory
	}{
		{name: "minimal", values: "ci/minimal-values.yaml"},
		{name: "s3", values: "ci/s3-values.yaml"},
		{name: "tls-dir-backup", values: "ci/tls-dir-backup-values.yaml"},
		{name: "numeric-secret-key", values: "ci/numeric-secret-key-values.yaml"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			args := []string{
				"template",
				"release",
				chartDir,
				"--kube-version",
				kubeVersion,
				"--namespace",
				"dustpan",
				"--values",
				filepath.Join(chartDir, tc.values),
			}

			res := run(t, helm, args)
			if res.err != nil {
				t.Fatalf("helm template: %v\n%s", res.err, res.stderr)
			}

			compareGolden(t, filepath.Join("testdata", tc.name+".golden.yaml"), res.stdout)
		})
	}
}

// TestTemplateRefused renders values that the chart refuses, and expects the
// helm error to name each setting that is wrong. The default values are one
// case: an install with no Secret names fails. Helm versions write the path
// of a schema error in different forms, so each wanted text is a group of
// forms, and one match of the group is enough.
func TestTemplateRefused(t *testing.T) {
	t.Parallel()

	helm := requireHelm(t)

	// A valid base: the two required Secret names.
	base := []string{
		"--set", "database.existingSecret.name=db",
		"--set", "keyEncryptionKey.existingSecret.name=kek",
	}

	tests := []struct {
		name     string
		useBase  bool
		set      []string
		wantText [][]string
	}{
		{
			name:    "default values",
			useBase: false,
			set:     nil,
			wantText: [][]string{
				// Helm v3.18.6 and later write the path as a JSON pointer,
				// earlier versions write it with dots.
				{"/database/existingSecret/name", "database.existingSecret.name:"},
				{"/keyEncryptionKey/existingSecret/name", "keyEncryptionKey.existingSecret.name:"},
			},
		},
		{
			name:    "database and key in one Secret and key",
			useBase: true,
			set: []string{
				"--set", "keyEncryptionKey.existingSecret.name=db",
				"--set", "keyEncryptionKey.existingSecret.key=database-url",
			},
			wantText: [][]string{{"name the same Secret and key"}},
		},
		{
			name:    "backup token is the admin token",
			useBase: true,
			set: []string{
				"--set", "backup.target.type=s3",
				"--set", "backup.target.s3.bucket=backup-bucket",
				"--set", "adminToken.existingSecret.name=adm",
				"--set", "backup.schedule.enabled=true",
				"--set", "backup.schedule.tokenSecret.name=adm",
				"--set", "backup.schedule.tokenSecret.key=admin-token",
			},
			wantText: [][]string{{"backup.schedule.tokenSecret names the same Secret and key"}},
		},
		{
			name:    "metrics token is the admin token",
			useBase: true,
			set: []string{
				"--set", "adminToken.existingSecret.name=adm",
				"--set", "metrics.serviceMonitor.enabled=true",
				"--set", "metrics.serviceMonitor.tokenSecret.name=adm",
				"--set", "metrics.serviceMonitor.tokenSecret.key=admin-token",
			},
			wantText: [][]string{{"metrics.serviceMonitor.tokenSecret names the same Secret and key"}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			args := []string{"template", "release", chartDir, "--kube-version", kubeVersion}
			if tc.useBase {
				args = append(args, base...)
			}

			args = append(args, tc.set...)

			res := run(t, helm, args)
			if res.err == nil {
				t.Fatalf("helm template succeeded, want an error with %q", tc.wantText)
			}

			for _, forms := range tc.wantText {
				if !containsAny(res.stderr, forms) {
					t.Errorf("helm error = %q, want one of %q", res.stderr, forms)
				}
			}
		})
	}
}

// containsAny reports whether s contains at least one of texts.
func containsAny(s string, texts []string) bool {
	for _, text := range texts {
		if strings.Contains(s, text) {
			return true
		}
	}

	return false
}

func requireHelm(t *testing.T) string {
	t.Helper()

	helm, err := exec.LookPath("helm")
	if err != nil {
		t.Skip("helm is not on PATH: install it (https://helm.sh/docs/intro/install/) to run the chart tests")
	}

	//nolint:gosec // The binary is helm from PATH; the arguments are literals.
	out, err := exec.CommandContext(t.Context(), helm, "version", "--short").Output()
	if err != nil {
		t.Skipf("helm version failed: %v: install Helm 3 to run the chart tests", err)
	}

	if !strings.HasPrefix(strings.TrimSpace(string(out)), "v3.") {
		t.Skipf("helm version is %q: the chart tests need Helm 3 (v3.x)", strings.TrimSpace(string(out)))
	}

	return helm
}

func run(t *testing.T, bin string, args []string) result {
	t.Helper()

	var outBuf, errBuf bytes.Buffer

	//nolint:gosec // The binary is helm from PATH; the arguments are literals of this test.
	cmd := exec.CommandContext(t.Context(), bin, args...)
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	err := cmd.Run()

	return result{stdout: outBuf.String(), stderr: errBuf.String(), err: err}
}

func compareGolden(t *testing.T, path, got string) {
	t.Helper()

	if *update {
		if err := os.WriteFile(path, []byte(got), 0o600); err != nil {
			t.Fatalf("write golden file: %v", err)
		}

		return
	}

	want, err := os.ReadFile(path) //nolint:gosec // The path is a literal name in testdata.
	if err != nil {
		t.Fatalf("read golden file %s: %v (run with -update to write it)", path, err)
	}

	if string(want) != got {
		t.Fatalf(
			"helm template output differs from %s: run `go test ./deploy/helm/charttest -update` and review the diff",
			path,
		)
	}
}
