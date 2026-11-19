package releaserecord_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// imageScript is one of the scripts that build the container image. Each takes
// the version from IMAGE_VERSION and each has its own default.
type imageScript struct {
	name           string
	defaultVersion string
}

func imageScripts() []imageScript {
	return []imageScript{
		{"build-image-local.sh", "dev"},
		{"smoke-image.sh", "smoke"},
	}
}

// fakeDockerScript records the arguments of each call. A call is the line
// "call" and one line for each argument. It fails each call, so that a script
// never goes on to a real build step.
const fakeDockerScript = `#!/bin/sh
echo call >>"$DOCKER_LOG"
for arg in "$@"; do
  printf 'arg:%s\n' "$arg" >>"$DOCKER_LOG"
done
exit 1
`

// scriptRun is the result of one run of an image script.
type scriptRun struct {
	err   error
	out   string
	calls []string
}

// dockerCalls counts the calls in the lines of the fake docker log.
func (r scriptRun) dockerCalls() int {
	return strings.Count("\n"+strings.Join(r.calls, "\n"), "\ncall")
}

// runImageScript copies one image script into a temporary tree, so that the
// script writes its console tarball there and not in the work tree, and runs
// it with a fake docker first in PATH. imageVersion is nil when the variable
// must be unset.
func runImageScript(t *testing.T, script string, imageVersion *string) scriptRun {
	t.Helper()

	tree := t.TempDir()

	repo := openRootOf(t, "../..")
	defer closeRoot(t, repo)

	src, err := repo.ReadFile("scripts/" + script)
	if err != nil {
		t.Fatal(err)
	}

	root := openRootOf(t, tree)
	defer closeRoot(t, root)

	for _, dir := range []string{"scripts", "bin"} {
		if err = root.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}

	if err = root.WriteFile("scripts/"+script, src, 0o700); err != nil {
		t.Fatal(err)
	}

	if err = root.WriteFile("bin/docker", []byte(fakeDockerScript), 0o700); err != nil {
		t.Fatal(err)
	}

	env := []string{
		"PATH=" + filepath.Join(tree, "bin") + ":" + os.Getenv("PATH"),
		"HOME=" + tree,
		"DOCKER_LOG=" + filepath.Join(tree, "docker.log"),
		"CONSOLE_URL=", "CONSOLE_SHA256=",
	}
	if imageVersion != nil {
		env = append(env, "IMAGE_VERSION="+*imageVersion)
	}

	// The command takes an argument list, so no argument passes through a shell.
	cmd := exec.CommandContext(t.Context(), "bash", filepath.Join(tree, "scripts", script))
	cmd.Dir = tree
	cmd.Env = env

	var combined bytes.Buffer

	cmd.Stdout = &combined
	cmd.Stderr = &combined

	result := scriptRun{err: cmd.Run(), out: combined.String(), calls: nil}

	if raw, readErr := root.ReadFile("docker.log"); readErr == nil {
		result.calls = strings.Split(strings.TrimSpace(string(raw)), "\n")
	}

	return result
}

// badImageVersions are the values that the allowlist must refuse.
func badImageVersions() map[string]string {
	return map[string]string{
		"empty but set":    "",
		"space":            "v0.1 0",
		"double quote":     `v0.1.0"`,
		"single quote":     `v0.1.0'`,
		"command":          "$(id)",
		"variable":         "${HOME}",
		"backtick":         "`id`",
		"semicolon":        "v0.1.0;id",
		"newline":          "v0.1.0\nid",
		"leading dash":     "-x",
		"leading dot":      ".x",
		"leading plus":     "+x",
		"65 characters":    "v" + strings.Repeat("1", 64),
		"slash":            "v0/1",
		"backslash":        `v0\1`,
		"non ASCII letter": "v0.1.0é",
	}
}

// refuseImageVersion runs one script with a bad version and checks the refusal.
func refuseImageVersion(t *testing.T, script, value string) {
	t.Helper()

	run := runImageScript(t, script, &value)
	if run.err == nil {
		t.Errorf("%s accepted IMAGE_VERSION %q\n%s", script, value, run.out)
	}

	if n := run.dockerCalls(); n != 0 {
		t.Errorf("%s made %d docker calls for IMAGE_VERSION %q, want 0:\n%v", script, n, value, run.calls)
	}

	if !strings.Contains(run.out, "IMAGE_VERSION") || !strings.Contains(run.out, "letters, digits") {
		t.Errorf("%s did not name IMAGE_VERSION and the allowed characters for %q:\n%s", script, value, run.out)
	}
}

// TestImageScriptsRefuseABadVersion sends a version that is outside the
// allowlist to each image script. The script refuses it, names the allowed
// characters and makes no docker call. The fake docker fails each call, so a
// script that did not check the version would still show its call in the log.
func TestImageScriptsRefuseABadVersion(t *testing.T) {
	t.Parallel()

	for _, script := range imageScripts() {
		for label, value := range badImageVersions() {
			t.Run(script.name+"/"+label, func(t *testing.T) {
				t.Parallel()

				refuseImageVersion(t, script.name, value)
			})
		}
	}
}

// TestBuildImageLocalPassesTheVersion checks the good values and the default
// of each script: the first docker call is a build, and it carries the build
// argument VERSION with the value of IMAGE_VERSION, or the default of the
// script when the variable is not set.
func TestBuildImageLocalPassesTheVersion(t *testing.T) {
	t.Parallel()

	good := []string{"v0.1.0", "v0.1.0-3-gabc123-dirty", "v0.1.0+meta", "0", strings.Repeat("a", 64)}

	for _, script := range imageScripts() {
		for _, value := range good {
			t.Run(script.name+"/"+value, func(t *testing.T) {
				t.Parallel()

				run := runImageScript(t, script.name, &value)
				if !slices.Contains(run.calls, "arg:VERSION="+value) {
					t.Errorf("%s did not pass VERSION=%s to docker:\n%v\n%s", script.name, value, run.calls, run.out)
				}
			})
		}

		t.Run(script.name+"/default", func(t *testing.T) {
			t.Parallel()

			run := runImageScript(t, script.name, nil)
			if !slices.Contains(run.calls, "arg:VERSION="+script.defaultVersion) {
				t.Errorf(
					"%s did not pass VERSION=%s by default:\n%v\n%s",
					script.name,
					script.defaultVersion,
					run.calls,
					run.out,
				)
			}
		})
	}
}
