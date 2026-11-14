//go:build e2e

package e2e_test

import (
	"errors"
	"os/exec"
	"strings"
	"testing"
)

// workflowStubs defines shell functions that shadow the docker, gh and
// helm tools for the workflow branch tests. Each function answers from
// the environment, so the tests run no real tool.
const workflowStubs = `
docker() {
  if [ "$1" = "buildx" ] && [ "$2" = "imagetools" ] && [ "$3" = "inspect" ]; then
    shift 3
    format=""
    ref=""
    while [ "$#" -gt 0 ]; do
      case "$1" in
        --format) format="$2"; shift 2 ;;
        *) ref="$1"; shift ;;
      esac
    done
    case "$format" in
      *Platform*) echo "linux/amd64 linux/arm64 "; return 0 ;;
    esac
    case "$ref" in
      *ghcr.io*) result="$STUB_FORGE" ;;
      *docker.io*) result="$STUB_HUB" ;;
      *) echo "stub: unexpected reference $ref" >&2; return 99 ;;
    esac
    case "$result" in
      ""|NOTFOUND) echo "Error: manifest unknown" >&2; return 1 ;;
      MISSING) echo "Error: not found" >&2; return 1 ;;
      ERROR:*) echo "${result#ERROR:}" >&2; return 1 ;;
      *) echo "\"$result\""; return 0 ;;
    esac
  elif [ "$1" = "buildx" ] && [ "$2" = "imagetools" ] && [ "$3" = "create" ]; then
    if [ -n "$STUB_CREATE_FAIL" ]; then echo "authentication denied" >&2; return 1; fi
    return 0
  elif [ "$1" = "buildx" ] && [ "$2" = "build" ]; then
    return 0
  elif [ "$1" = "login" ]; then
    if [ -n "$STUB_LOGIN_FAIL" ]; then echo "authentication denied" >&2; return 1; fi
    return 0
  fi
  echo "stub: unexpected docker call: $*" >&2; return 99
}
gh() {
  if [ -z "$GH_TOKEN" ]; then echo "stub: GH_TOKEN is empty" >&2; return 1; fi
  if [ -n "$STUB_GH_FAIL" ]; then echo "stub: api error" >&2; return 1; fi
  echo "$STUB_PERMISSION"
}
helm() {
  if [ -n "$STUB_HELM_FAIL" ]; then echo "stub: helm failed" >&2; return 1; fi
  return 0
}
`

// workflowStepScript answers the shell script of the named step of the
// workflow. It names no route and no credential kind.
func workflowStepScript(t *testing.T, workflow, name string) string {
	t.Helper()

	start := strings.Index(workflow, "\n      - name: "+name+"\n")
	if start < 0 {
		t.Fatalf("the release workflow lacks the step %q", name)
	}
	rest := workflow[start:]

	run := strings.Index(rest, "run: |")
	if run < 0 {
		t.Fatalf("the release workflow step %q has no run script", name)
	}
	rest = rest[run+len("run: |"):]

	var out strings.Builder
	for line := range strings.Lines(rest) {
		if strings.TrimSpace(line) == "" {
			out.WriteByte('\n')
			continue
		}
		if !strings.HasPrefix(line, "          ") {
			break
		}
		out.WriteString(strings.TrimPrefix(line, "          ") + "\n")
	}

	script := out.String()
	if strings.TrimSpace(script) == "" {
		t.Fatalf("the release workflow step %q has an empty run script", name)
	}

	return script
}

// runWorkflowScript runs the workflow script with bash, the stub tools
// shadowing the real tools, and the environment. It answers the exit
// code and the combined output, which holds the GITHUB_ENV and summary
// writes.
func runWorkflowScript(t *testing.T, script string, env map[string]string) (int, string) {
	t.Helper()

	full := make([]string, 0, len(env)+5)
	full = append(full,
		"GITHUB_ENV=/dev/stdout",
		"GITHUB_STEP_SUMMARY=/dev/stdout",
		"RUNNER_TEMP="+t.TempDir(),
		"FORGE_IMAGE=ghcr.io/opendustpan/opendustpan",
		"HUB_IMAGE=docker.io/opendustpan/opendustpan",
	)
	for key, value := range env {
		full = append(full, key+"="+value)
	}

	command := exec.CommandContext(t.Context(), "bash", "--noprofile", "--norc", "-eo", "pipefail", "-s")
	command.Stdin = strings.NewReader(workflowStubs + "\n" + script)
	command.Env = full
	command.Dir = t.TempDir()

	raw, err := command.CombinedOutput()
	code := 0
	if err != nil {
		failed, ok := errors.AsType[*exec.ExitError](err)
		if !ok {
			t.Fatalf("run the workflow script: %v", err)
		}
		code = failed.ExitCode()
	}

	return code, string(raw)
}

// branchCase is one behavior case of a workflow step: the environment for
// the stubs, the wanted exit code, and the wanted text in the output.
type branchCase struct {
	name    string
	env     map[string]string
	want    int
	wantOut string
}

// checkBranch runs the script with the environment of the case and checks
// the exit code and the output.
func checkBranch(t *testing.T, script string, tc branchCase) {
	t.Helper()

	code, out := runWorkflowScript(t, script, tc.env)
	if code != tc.want {
		t.Errorf("%s: exit code = %d, want %d\n%s", tc.name, code, tc.want, out)
	}
	if tc.wantOut != "" && !strings.Contains(out, tc.wantOut) {
		t.Errorf("%s: output lacks %q\n%s", tc.name, tc.wantOut, out)
	}
}

// checkBranchTable runs each case of the table against the script.
func checkBranchTable(t *testing.T, script string, cases []branchCase) {
	t.Helper()

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			checkBranch(t, script, tc)
		})
	}
}

// TestReleaseWorkflowRerun runs the re-run decision of the workflow with
// stubbed tools: same digest refuses without change, different digests
// refuse overwrite, a partial push completes the missing registry, and a
// transient inspect error fails instead of pushing over unknown state.
func TestReleaseWorkflowRerun(t *testing.T) {
	t.Parallel()

	workflow := releaseWorkflow(t)
	script := workflowStepScript(t, workflow, "Inspect both registries and apply the re-run rules")

	checkBranchTable(t, script, []branchCase{
		{
			name:    "both hold the same digest",
			env:     map[string]string{"TAG": "v0.1.0", "STUB_FORGE": "sha256:aaa", "STUB_HUB": "sha256:aaa"},
			want:    1,
			wantOut: "changes nothing",
		},
		{
			name:    "different digests refuse",
			env:     map[string]string{"TAG": "v0.1.0", "STUB_FORGE": "sha256:aaa", "STUB_HUB": "sha256:bbb"},
			want:    1,
			wantOut: "refuses to overwrite",
		},
		{
			name:    "forge only completes the hub",
			env:     map[string]string{"TAG": "v0.1.0", "STUB_FORGE": "sha256:aaa", "STUB_HUB": "NOTFOUND"},
			want:    0,
			wantOut: "MODE=complete-hub",
		},
		{
			name:    "hub only completes the forge",
			env:     map[string]string{"TAG": "v0.1.0", "STUB_FORGE": "NOTFOUND", "STUB_HUB": "sha256:aaa"},
			want:    0,
			wantOut: "MODE=complete-forge",
		},
		{
			name:    "neither builds",
			env:     map[string]string{"TAG": "v0.1.0", "STUB_FORGE": "NOTFOUND", "STUB_HUB": "MISSING"},
			want:    0,
			wantOut: "MODE=build",
		},
		{
			name: "transient forge error fails",
			env: map[string]string{
				"TAG":        "v0.1.0",
				"STUB_FORGE": "ERROR:connection refused",
				"STUB_HUB":   "NOTFOUND",
			},
			want:    1,
			wantOut: "cannot inspect",
		},
		{
			name: "transient hub error fails",
			env: map[string]string{
				"TAG":        "v0.1.0",
				"STUB_FORGE": "NOTFOUND",
				"STUB_HUB":   "ERROR:connection reset",
			},
			want:    1,
			wantOut: "cannot inspect",
		},
		{
			name: "tls unknown authority fails closed",
			env: map[string]string{
				"TAG":        "v0.1.0",
				"STUB_FORGE": "ERROR:x509: certificate signed by unknown authority",
				"STUB_HUB":   "NOTFOUND",
			},
			want:    1,
			wantOut: "cannot inspect",
		},
		{
			name: "unknown host fails closed",
			env: map[string]string{
				"TAG":        "v0.1.0",
				"STUB_FORGE": "NOTFOUND",
				"STUB_HUB":   "ERROR:unknown host registry.example",
			},
			want:    1,
			wantOut: "cannot inspect",
		},
	})
}

// TestReleaseWorkflowAllowlist runs the tag allowlist with hostile tags:
// a suffix fails, a branch name fails, and shell metacharacters in the
// tag fail without executing.
func TestReleaseWorkflowAllowlist(t *testing.T) {
	t.Parallel()

	workflow := releaseWorkflow(t)
	script := workflowStepScript(t, workflow, "Check that the tag is a final version tag")

	checkBranchTable(t, script, []branchCase{
		{
			name:    "final tag passes",
			env:     map[string]string{"TAG": "v0.1.0"},
			want:    0,
			wantOut: "",
		},
		{
			name:    "pre-release suffix fails",
			env:     map[string]string{"TAG": "v0.1.0-rc1"},
			want:    1,
			wantOut: "no suffix",
		},
		{
			name:    "branch push fails",
			env:     map[string]string{"TAG": "main"},
			want:    1,
			wantOut: "no suffix",
		},
		{
			name:    "substitution fails without executing",
			env:     map[string]string{"TAG": "v0.1.0$(echo INJECTED_MARKER)"},
			want:    1,
			wantOut: "no suffix",
		},
	})

	code, out := runWorkflowScript(t, script, map[string]string{"TAG": "v0.1.0; echo INJECTED_MARKER"})
	if code == 0 || strings.Contains(out, "INJECTED_MARKER") {
		t.Errorf("metacharacters: exit code = %d, want a failure without the marker\n%s", code, out)
	}
}

// TestReleaseWorkflowSecrets runs the secret check: both secrets pass,
// and each missing secret fails with its one setup step.
func TestReleaseWorkflowSecrets(t *testing.T) {
	t.Parallel()

	workflow := releaseWorkflow(t)
	script := workflowStepScript(t, workflow, "Check that the Docker Hub secrets exist")

	checkBranchTable(t, script, []branchCase{
		{
			name:    "both secrets pass",
			env:     map[string]string{"DOCKERHUB_USERNAME": "owner", "DOCKERHUB_TOKEN": "token"},
			want:    0,
			wantOut: "",
		},
		{
			name:    "missing token fails with the setup step",
			env:     map[string]string{"DOCKERHUB_USERNAME": "owner", "DOCKERHUB_TOKEN": ""},
			want:    1,
			wantOut: "gh secret set DOCKERHUB_TOKEN --repo OpenDustpan/OpenDustpan",
		},
		{
			name:    "missing username fails with the setup step",
			env:     map[string]string{"DOCKERHUB_USERNAME": "", "DOCKERHUB_TOKEN": "token"},
			want:    1,
			wantOut: "gh secret set DOCKERHUB_USERNAME --repo OpenDustpan/OpenDustpan",
		},
	})
}

// TestReleaseWorkflowPermission runs the permission check: maintain and
// admin pass, read fails, and an API error fails closed.
func TestReleaseWorkflowPermission(t *testing.T) {
	t.Parallel()

	workflow := releaseWorkflow(t)
	script := workflowStepScript(t, workflow, "Check that the actor has release permission")

	checkBranchTable(t, script, []branchCase{
		{
			name:    "maintain passes",
			env:     map[string]string{"GH_TOKEN": "token", "STUB_PERMISSION": "maintain"},
			want:    0,
			wantOut: "",
		},
		{
			name:    "admin passes",
			env:     map[string]string{"GH_TOKEN": "token", "STUB_PERMISSION": "admin"},
			want:    0,
			wantOut: "",
		},
		{
			name:    "read fails",
			env:     map[string]string{"GH_TOKEN": "token", "STUB_PERMISSION": "read"},
			want:    1,
			wantOut: "not release permission",
		},
		{
			name:    "api error fails closed",
			env:     map[string]string{"GH_TOKEN": "token", "STUB_GH_FAIL": "1"},
			want:    1,
			wantOut: "must start the tag",
		},
	})
}

// TestReleaseWorkflowCompare runs the digest comparison: the same digest
// passes with an unquoted digest in the summary, different digests fail,
// and an inspect error fails.
func TestReleaseWorkflowCompare(t *testing.T) {
	t.Parallel()

	workflow := releaseWorkflow(t)
	script := workflowStepScript(t, workflow, "Compare the digests of both registries")

	checkBranchTable(t, script, []branchCase{
		{
			name:    "same digest passes with the summary",
			env:     map[string]string{"TAG": "v0.1.0", "STUB_FORGE": "sha256:aaa", "STUB_HUB": "sha256:aaa"},
			want:    0,
			wantOut: "digest=sha256:aaa",
		},
		{
			name: "different digests fail",
			env: map[string]string{
				"TAG":        "v0.1.0",
				"STUB_FORGE": "sha256:aaa",
				"STUB_HUB":   "sha256:bbb",
			},
			want:    1,
			wantOut: "Digest mismatch",
		},
		{
			name: "inspect error fails",
			env: map[string]string{
				"TAG":        "v0.1.0",
				"STUB_FORGE": "ERROR:connection refused",
				"STUB_HUB":   "sha256:aaa",
			},
			want:    1,
			wantOut: "cannot inspect",
		},
	})
}

// TestReleaseWorkflowPushFailure runs the Docker Hub push with a failing
// registry and checks that it names the rotation step.
func TestReleaseWorkflowPushFailure(t *testing.T) {
	t.Parallel()

	workflow := releaseWorkflow(t)
	script := workflowStepScript(t, workflow, "Push the same digest to Docker Hub")

	checkBranch(t, script, branchCase{
		name:    "push failure names the rotation step",
		env:     map[string]string{"TAG": "v0.1.0", "STUB_CREATE_FAIL": "1"},
		want:    1,
		wantOut: "gh secret set DOCKERHUB_TOKEN --repo OpenDustpan/OpenDustpan",
	})
}

// TestReleaseWorkflowChartFailure runs the chart push with a failing helm
// and checks that the step fails instead of announcing the tag.
func TestReleaseWorkflowChartFailure(t *testing.T) {
	t.Parallel()

	workflow := releaseWorkflow(t)
	script := workflowStepScript(t, workflow, "Push the chart as an OCI artifact to the forge registry")

	code, out := runWorkflowScript(t, script, map[string]string{
		"TAG": "v0.1.0", "GHCR_TOKEN": "token", "GHCR_ACTOR": "owner", "STUB_HELM_FAIL": "1",
	})
	if code == 0 {
		t.Errorf("chart push failure: exit code = 0, want a failure\n%s", out)
	}
}
