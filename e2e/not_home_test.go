//go:build e2e

package e2e_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

// foreignNamespace is a namespace that another installation owns. The server reads
// it but refuses every write to it.
const foreignNamespace = "e2e-foreign"

// foreignInstallation is the primary installation of foreignNamespace. The server under test
// never runs in it.
const foreignInstallation = "e2e-other"

// notHomeMessage is the text of the server refusal of a write to a namespace
// that another installation owns.
const notHomeMessage = "this installation is not the primary installation of the namespace"

// notHomeLockError is the error the http backend of OpenTofu prints when the
// server refuses its LOCK with 421 Misdirected Request.
const notHomeLockError = "Unexpected HTTP response code 421"

// tofuCloudConfig is the cloud block configuration for one organization and
// one workspace, with no providers so nothing is downloaded.
const tofuCloudConfig = `terraform {
  cloud {
    hostname     = %q
    organization = %q
    workspaces {
      name = %q
    }
  }
}

output "greeting" {
  value = "hello"
}
`

// tofuNotHomeScenario proves with the real OpenTofu CLI that a write to a
// namespace that another installation owns fails with 421, not with an internal
// error: init with the cloud block cannot create the workspace, and apply
// through the http backend cannot take the lock.
func tofuNotHomeScenario(t *testing.T, env *e2e.Env, backendEnv []string) {
	t.Helper()

	ctx := t.Context()
	env.CreateForeignNamespace(ctx, t, foreignNamespace, foreignInstallation)

	cloudDir := t.TempDir()
	writeProjectFile(t, cloudDir, fmt.Sprintf(tofuCloudConfig, env.Host(), foreignNamespace, e2e.Workspace))

	env.StepIn(ctx, t, cloudDir, failStep("init in a namespace of another installation",
		outputContains(notHomeMessage), "init", "-input=false", "-no-color"))

	httpDir := t.TempDir()
	address := env.URL + "/tofu/state/" + foreignNamespace + "/" + e2e.Workspace
	writeTofuHTTPConfig(t, httpDir, address)

	env.StepIn(ctx, t, httpDir, e2e.StepSpec{
		Check: nil, Name: "init http backend in a namespace of another installation",
		Args: []string{"init", "-input=false", "-no-color"},
		Env:  backendEnv, Stdin: "", Fail: false, Redact: false,
	})

	// The http backend prints the status of a refused LOCK, not its body.
	env.StepIn(ctx, t, httpDir, e2e.StepSpec{
		Check: outputContains(notHomeLockError), Name: "apply in a namespace of another installation",
		Args: []string{"apply", "-input=false", "-auto-approve", "-no-color"},
		Env:  backendEnv, Stdin: "", Fail: true, Redact: false,
	})
}

// writeProjectFile writes content as main.tf of the project directory dir.
func writeProjectFile(t *testing.T, dir, content string) {
	t.Helper()

	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(content), 0o600); err != nil {
		t.Fatalf("write main.tf: %v", err)
	}
}
