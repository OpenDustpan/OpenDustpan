package sqlite

import (
	"io/fs"
	"os"
	"strings"
	"testing"
)

// TestJobRowIsWrittenOnlyByUpdateJobRow proves that the one function that
// updates a job row is also the one that ends the claims of a job that moves
// to a terminal state: no other file of the package calls the generated
// UpdateJob query. A new write path of a job row that skips updateJobRow
// would let a claim outlive its job, so this test fails on it.
func TestJobRowIsWrittenOnlyByUpdateJobRow(t *testing.T) {
	t.Parallel()

	dir := os.DirFS(".")

	entries, err := fs.ReadDir(dir, ".")
	if err != nil {
		t.Fatalf("read the package directory: %v", err)
	}

	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}

		raw, readErr := fs.ReadFile(dir, name)
		if readErr != nil {
			t.Fatalf("read %s: %v", name, readErr)
		}

		if name != "state_close.go" && strings.Contains(string(raw), ".UpdateJob(ctx") {
			t.Errorf("%s calls the UpdateJob query: write a job row through updateJobRow", name)
		}
	}
}
