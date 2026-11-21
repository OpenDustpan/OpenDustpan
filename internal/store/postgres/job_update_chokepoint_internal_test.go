package postgres

import (
	"io/fs"
	"os"
	"regexp"
	"strings"
	"testing"
)

// updateJobsSQL matches a statement that updates the jobs table, in any case
// and with any white space.
var updateJobsSQL = regexp.MustCompile(`(?i)update\s+"?jobs"?\b`)

// TestJobRowIsWrittenOnlyByUpdateJobRow proves that the one function that
// updates a job row is also the one that ends the claims of a job that moves
// to a terminal state. Three scans guard it: no other file of the package calls
// the generated UpdateJob query, no query of queries.sql except UpdateJob
// updates the jobs table, and no Go file holds a statement that updates it. A
// new write path of a job row that skips updateJobRow would let a claim outlive
// its job, so this test fails on it.
func TestJobRowIsWrittenOnlyByUpdateJobRow(t *testing.T) {
	t.Parallel()

	dir := os.DirFS(".")

	scanGoFilesForJobWrites(t, dir)

	raw, err := fs.ReadFile(dir, "queries.sql")
	if err != nil {
		t.Fatalf("read queries.sql: %v", err)
	}

	for _, name := range queriesThatUpdateJobs(string(raw)) {
		if name != "UpdateJob" {
			t.Errorf("query %s updates the jobs table: write a job row through the UpdateJob query", name)
		}
	}
}

// scanGoFilesForJobWrites fails the test for each non-test Go file of dir,
// except the one of updateJobRow, that calls the UpdateJob query or holds a
// statement that updates the jobs table.
func scanGoFilesForJobWrites(t *testing.T, dir fs.FS) {
	t.Helper()

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

		if updateJobsSQL.Match(raw) {
			t.Errorf("%s holds a statement that updates the jobs table: write a job row through updateJobRow", name)
		}
	}
}

// queriesThatUpdateJobs returns the name of each query of a queries file whose
// text updates the jobs table. A query starts at its "-- name:" line.
func queriesThatUpdateJobs(file string) []string {
	var names []string

	for _, block := range strings.Split("\n"+file, "\n-- name: ")[1:] {
		name, _, _ := strings.Cut(block, " ")
		if updateJobsSQL.MatchString(block) {
			names = append(names, name)
		}
	}

	return names
}

// TestQueriesThatUpdateJobsFindsAHiddenWrite proves the scan of queries.sql: a
// second query that updates the jobs table is found, in any case and layout.
func TestQueriesThatUpdateJobsFindsAHiddenWrite(t *testing.T) {
	t.Parallel()

	file := "-- name: UpdateJob :execrows\nUPDATE jobs SET state = 1;\n\n" +
		"-- name: Other :exec\nSELECT 1;\n\n" +
		"-- name: Sneaky :exec\n-- comment\nupdate   jobs\nSET state = 2;\n\n" +
		"-- name: Quoted :exec\nUPDATE \"jobs\" SET state = 3;\n"

	got := queriesThatUpdateJobs(file)
	if len(got) != 3 || got[0] != "UpdateJob" || got[1] != "Sneaky" || got[2] != "Quoted" {
		t.Fatalf("queries that update jobs = %v, want [UpdateJob Sneaky Quoted]", got)
	}
}
