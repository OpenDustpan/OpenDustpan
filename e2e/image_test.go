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
