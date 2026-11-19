//go:build e2e

package e2e_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

// dockerfileLines reads the Dockerfile of the repository and answers its lines.
func dockerfileLines(t *testing.T) []string {
	t.Helper()

	root, err := os.OpenRoot("..")
	if err != nil {
		t.Fatalf("open the repository: %v", err)
	}

	raw, readErr := root.ReadFile("Dockerfile")
	if closeErr := root.Close(); readErr != nil || closeErr != nil {
		t.Fatalf("read the Dockerfile: %v %v", readErr, closeErr)
	}

	return strings.Split(string(raw), "\n")
}

// dockerfileCommand answers the command of the image: the arguments of the
// CMD line of the Dockerfile that runs serve. The image has a second CMD
// line for its health check, so the helper takes the line whose first
// argument is serve.
func dockerfileCommand(t *testing.T) []string {
	t.Helper()

	for _, line := range dockerfileLines(t) {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "CMD [") {
			continue
		}

		var command []string
		if err := json.Unmarshal([]byte(strings.TrimPrefix(trimmed, "CMD ")), &command); err != nil {
			t.Fatalf("decode the CMD line %q of the Dockerfile: %v", trimmed, err)
		}

		if len(command) == 0 {
			t.Fatalf("the CMD line %q of the Dockerfile is empty", trimmed)
		}

		if command[0] == "serve" {
			return command
		}
	}

	t.Fatal("the Dockerfile has no CMD line that runs serve")
	return nil
}

// TestImageFailsClosed proves what the Dockerfile sets for a container that
// has no key: the command is serve with the database in /data, the image sets
// DUSTPAN_DATA_DIR=/data, the volume is /data and the user nonroot owns it,
// and the image sets no key and no admin token. It reads the file. The
// next test runs the binary with the command of the image.
func TestImageFailsClosed(t *testing.T) {
	t.Parallel()

	lines := dockerfileLines(t)

	want := []string{
		`ENV DUSTPAN_DATA_DIR=/data`,
		`VOLUME /data`,
		`COPY --from=build --chown=65532:65532 /out/data /data`,
		`ENTRYPOINT ["/opendustpan"]`,
		`CMD ["serve", "--database", "sqlite:/data/dustpan.db", "--blob-dir", "/data/blobs", "--listen", ":8080", ` +
			`"--console-dir", "/console"]`,
	}

	for _, line := range want {
		if !slicesContainsLine(lines, line) {
			t.Errorf("the Dockerfile lacks the line %q", line)
		}
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			continue
		}

		for _, secret := range []string{"DUSTPAN_KEY_ENCRYPTION_KEY", "DUSTPAN_ADMIN_TOKEN", "--generate-key"} {
			if strings.Contains(trimmed, secret) {
				t.Errorf("the Dockerfile sets %s in %q: the image must fail closed", secret, line)
			}
		}
	}
}

// slicesContainsLine reports whether lines holds line.
func slicesContainsLine(lines []string, line string) bool {
	for _, candidate := range lines {
		if strings.TrimSpace(candidate) == line {
			return true
		}
	}

	return false
}

// TestImageCommandWithoutKey runs the binary with the command of the image and
// no key, as docker run does with no key: serve stops with exit code 1 and
// the key error, and it opens no database.
func TestImageCommandWithoutKey(t *testing.T) {
	t.Parallel()

	bin := e2e.BuildBinaryWithVersion(t.Context(), t, t.TempDir(), "")
	data := t.TempDir()

	args := dockerfileCommand(t)
	for i, arg := range args {
		switch {
		case strings.HasPrefix(arg, "sqlite:/data"):
			args[i] = "sqlite:" + filepath.Join(data, strings.TrimPrefix(arg, "sqlite:/data"))
		case strings.HasPrefix(arg, "/data"):
			args[i] = filepath.Join(data, strings.TrimPrefix(arg, "/data"))
		case arg == "/console":
			args[i] = consoleStub(t)
		default:
		}
	}

	out, err := runBinaryWithEnv(t, bin, []string{"DUSTPAN_DATA_DIR=" + data}, args...)

	exit, isExit := errors.AsType[*exec.ExitError](err)
	if !isExit || exit.ExitCode() != 1 || !strings.Contains(out, "opendustpan: no key-encryption key is set") {
		t.Fatalf("serve with no key: err = %v, output:\n%s", err, out)
	}

	if entries, readErr := os.ReadDir(data); readErr != nil || len(entries) != 0 {
		t.Fatalf("serve with no key wrote files in the data directory (read err %v): %v", readErr, entries)
	}
}

// TestImageTrialCommand runs dev the way the trial command of the image does:
// DUSTPAN_DATA_DIR names the data volume and --listen names a wildcard
// address. dev keeps its data in the directory dev of the volume, and the
// server answers.
func TestImageTrialCommand(t *testing.T) {
	t.Parallel()

	if runtime.GOOS != "linux" {
		t.Skipf("the dev e2e step needs Linux (a pseudo terminal), not %s", runtime.GOOS)
	}

	if _, err := exec.LookPath("script"); err != nil {
		t.Skip("the dev e2e step needs the script utility for a pseudo terminal")
	}

	bin := e2e.BuildBinaryWithVersion(t.Context(), t, t.TempDir(), "")
	volume := t.TempDir()
	dir := filepath.Join(volume, "dev")

	server := startDevServer(t, bin, dir, []string{"DUSTPAN_DATA_DIR=" + volume},
		"--listen", "0.0.0.0:0", "--console-dir", consoleStub(t))

	if code, body := get(t, server.url, "/health/ready"); code != http.StatusOK {
		t.Fatalf("GET /health/ready on the trial server = %d, want 200\n%s", code, body)
	}

	if _, err := os.Stat(filepath.Join(dir, "dev.token")); err != nil {
		t.Fatalf("dev did not keep its data in the directory dev of the volume: %v", err)
	}
}

// lineIndex answers the index of the first line that holds want, or -1.
func lineIndex(lines []string, want string) int {
	for i, line := range lines {
		if strings.Contains(line, want) {
			return i
		}
	}

	return -1
}

// TestImageDockerfileChecksVersion reads the Dockerfile and checks that the Go
// stage tests VERSION against the allowlist before it runs go build, and that
// the message names the build argument. The allowlist is the one of the image
// scripts. The test reads the file only: the image smoke test runs the build.
func TestImageDockerfileChecksVersion(t *testing.T) {
	t.Parallel()

	lines := dockerfileLines(t)

	check := lineIndex(lines, `[!0-9A-Za-z._+-]`)
	build := lineIndex(lines, "go build -trimpath")

	if check < 0 {
		t.Fatal("the Dockerfile has no test of VERSION against the allowlist of letters, digits, dot, plus and dash")
	}

	if build < 0 || check > build {
		t.Fatalf("the VERSION test is at line %d and go build at line %d: the test must come first", check+1, build+1)
	}

	for _, want := range []string{`case "${VERSION}" in`, `-gt 64`, "build argument VERSION must be", "exit 1"} {
		if lineIndex(lines[:build], want) < 0 {
			t.Errorf("the Dockerfile has no %q before go build", want)
		}
	}
}

// versionCheckBody answers the shell text of the RUN step that tests VERSION:
// the lines from the RUN line that holds the case test up to the first line
// that does not end in a backslash, with the word RUN and the trailing
// backslashes removed.
func versionCheckBody(t *testing.T, lines []string) string {
	t.Helper()

	start := lineIndex(lines, `RUN case "${VERSION}" in`)
	if start < 0 {
		t.Fatal(`the Dockerfile has no RUN step that starts with case "${VERSION}" in`)
	}

	var body []string

	for i := start; i < len(lines); i++ {
		line := lines[i]
		if i == start {
			line = strings.TrimPrefix(line, "RUN ")
		}

		continued := strings.HasSuffix(line, "\\")
		body = append(body, strings.TrimSuffix(line, "\\"))

		if !continued {
			break
		}
	}

	return strings.Join(body, "\n")
}

// runVersionCheck runs the body with sh and answers the exit code and the
// output. VERSION is in the environment, as the build argument is in a RUN
// step. The command takes an argument list: no shell parses the value.
func runVersionCheck(t *testing.T, body, version string) (int, string) {
	t.Helper()

	cmd := exec.CommandContext( //nolint:gosec // The body is the Dockerfile RUN text; the hostile value is in the environment.
		t.Context(),
		"sh",
		"-c",
		body,
	)
	cmd.Env = []string{"LC_ALL=C", "PATH=" + os.Getenv("PATH"), "VERSION=" + version}

	out, err := cmd.CombinedOutput()
	if err == nil {
		return 0, string(out)
	}

	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("run the VERSION check: %v", err)
	}

	return exitErr.ExitCode(), string(out)
}

// TestImageDockerfileVersionCheckRuns takes the shell text of the VERSION test
// out of the Dockerfile and runs it with sh over hostile and good values. The
// test fails when a part of the allowlist is removed from the Dockerfile.
func TestImageDockerfileVersionCheckRuns(t *testing.T) {
	t.Parallel()

	body := versionCheckBody(t, dockerfileLines(t))

	bad := map[string]string{
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
		"leading underbar": "_x",
		"65 characters":    "v" + strings.Repeat("1", 64),
		"slash":            "v0/1",
		"backslash":        `v0\1`,
		"non ASCII letter": "v0.1.0\u00e9",
	}

	for label, value := range bad {
		t.Run("bad/"+label, func(t *testing.T) {
			t.Parallel()

			code, out := runVersionCheck(t, body, value)
			if code != 1 {
				t.Errorf("VERSION %q: exit code %d, want 1\n%s", value, code, out)
			}

			if !strings.Contains(out, "build argument VERSION must be") {
				t.Errorf("VERSION %q: the message does not name the build argument:\n%s", value, out)
			}
		})
	}

	good := map[string]string{
		"release":       "v0.1.0",
		"dev":           "dev",
		"git describe":  "v0.1.0-3-gabc123-dirty",
		"build meta":    "v0.1.0+meta",
		"digit":         "0",
		"64 characters": strings.Repeat("a", 64),
	}

	for label, value := range good {
		t.Run("good/"+label, func(t *testing.T) {
			t.Parallel()

			if code, out := runVersionCheck(t, body, value); code != 0 {
				t.Errorf("VERSION %q: exit code %d, want 0\n%s", value, code, out)
			}
		})
	}
}

// TestImageDockerfileRefusesPlaceholderPin reads the Dockerfile and checks that
// the console stage tests CONSOLE_SHA256 for 64 zeros before the download, and
// that the message names the Dockerfile pin and the two build arguments.
func TestImageDockerfileRefusesPlaceholderPin(t *testing.T) {
	t.Parallel()

	lines := dockerfileLines(t)

	stage := lineIndex(lines, "AS console")
	download := lineIndex(lines[stage+1:], "curl -fsSL")
	zeros := strings.Repeat("0", 64)

	if stage < 0 || download < 0 {
		t.Fatal("the Dockerfile has no console stage with a curl download")
	}

	download += stage + 1

	test := lineIndex(lines[stage+1:download], `case "${CONSOLE_SHA256}" in`)
	if test < 0 {
		t.Fatal("the console stage has no test of CONSOLE_SHA256 before the download")
	}

	// The default of the argument is the placeholder, so the zeros appear on
	// the ARG line too; the refusal needs its own pattern line.
	body := strings.Join(lines[stage+1+test:download], "\n")
	for _, want := range []string{zeros + ")", "Pin the console tag and SHA-256 in the Dockerfile", "CONSOLE_URL", "CONSOLE_SHA256", "exit 1"} {
		if !strings.Contains(body, want) {
			t.Errorf("the placeholder test of the console stage has no %q before the download", want)
		}
	}
}

// TestReleaseWorkflowStampsTheImage reads the release workflow and checks that
// the docker buildx build line passes the tag as the build argument VERSION and
// holds no console pin of its own: the Dockerfile is the one source of the pin.
func TestReleaseWorkflowStampsTheImage(t *testing.T) {
	t.Parallel()

	root, err := os.OpenRoot("..")
	if err != nil {
		t.Fatalf("open the repository: %v", err)
	}

	raw, readErr := root.ReadFile(".github/workflows/release.yml")
	if closeErr := root.Close(); readErr != nil || closeErr != nil {
		t.Fatalf("read the release workflow: %v %v", readErr, closeErr)
	}

	var build string

	for line := range strings.SplitSeq(string(raw), "\n") {
		if strings.Contains(line, "docker buildx build") {
			build = line
		}
	}

	if build == "" {
		t.Fatal("the release workflow has no docker buildx build line")
	}

	if !strings.Contains(build, `--build-arg VERSION="${TAG}"`) {
		t.Errorf("the docker buildx build line does not pass the tag as VERSION: %s", build)
	}

	if strings.Contains(build, strings.Repeat("0", 64)) {
		t.Errorf("the docker buildx build line holds the placeholder console pin: %s", build)
	}
}
