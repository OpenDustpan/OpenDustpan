//go:build e2e

package e2e_test

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// unmanagedSweepStoreDocsPage is the guide that this step runs.
const unmanagedSweepStoreDocsPage = "docs/guides/unmanaged-sweep-store.md"

// unmanagedSweepStoreDocsCommand is the command of the page.
const unmanagedSweepStoreDocsCommand = "go test -count=1 -run TestUnmanagedScanStoreConformance ./internal/store/sqlite/"

// TestE2EUnmanagedSweepStoreDocsPage runs each command of the guide as
// written, from the root of the repository. The sweep store has no route in
// this part, so the test calls no route of it.
func TestE2EUnmanagedSweepStoreDocsPage(t *testing.T) {
	t.Parallel()

	commands := unmanagedSweepStoreDocsCommands(t)
	if len(commands) == 0 {
		t.Fatalf("%s holds no command", unmanagedSweepStoreDocsPage)
	}

	for _, command := range commands {
		// The test runs only the command that it knows. A new command on the
		// page needs a new case here.
		if command != unmanagedSweepStoreDocsCommand {
			t.Fatalf("the page holds the command %q, want %q", command, unmanagedSweepStoreDocsCommand)
		}

		cmd := exec.CommandContext(t.Context(), "go", "test", "-count=1", "-run", "TestUnmanagedScanStoreConformance",
			"./internal/store/sqlite/")
		cmd.Dir = ".."

		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("the command %q failed: %v: %s", command, err, out)
		}

		if !strings.Contains(string(out), "ok") {
			t.Fatalf("the command %q did not report a passing package: %s", command, out)
		}
	}
}

// unmanagedSweepStoreDocsCommands returns the lines of the sh blocks of the
// page.
func unmanagedSweepStoreDocsCommands(t *testing.T) []string {
	t.Helper()

	root, err := os.OpenRoot("..")
	if err != nil {
		t.Fatalf("open the repository: %v", err)
	}

	data, readErr := root.ReadFile(unmanagedSweepStoreDocsPage)
	if closeErr := root.Close(); readErr != nil || closeErr != nil {
		t.Fatalf("read %s: %v %v", unmanagedSweepStoreDocsPage, readErr, closeErr)
	}

	var commands []string

	rest := string(data)

	for {
		_, after, ok := strings.Cut(rest, "```sh\n")
		if !ok {
			return commands
		}

		body, next, ok := strings.Cut(after, "```")
		if !ok {
			t.Fatalf("a command block of %s has no end", unmanagedSweepStoreDocsPage)
		}

		for line := range strings.SplitSeq(body, "\n") {
			if line = strings.TrimSpace(line); line != "" {
				commands = append(commands, line)
			}
		}

		rest = next
	}
}
