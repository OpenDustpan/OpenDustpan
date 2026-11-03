package problem_test

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/errcode/errcodetest"
)

// apiDir is the directory internal/api, seen from this package.
const apiDir = ".."

// okStatuses returns the net/http status constants that a success answer may
// use in a WriteHeader call. Any other http.Status constant is an error status.
func okStatuses() []string {
	return []string{
		"StatusOK",
		"StatusCreated",
		"StatusAccepted",
		"StatusNoContent",
		"StatusPartialContent",
		"StatusMultiStatus",
		"StatusNotModified",
		"StatusSeeOther",
		"StatusFound",
		"StatusMovedPermanently",
		"StatusTemporaryRedirect",
		"StatusPermanentRedirect",
	}
}

// ownContractDirs returns the adapters that keep their own wire contract. The
// gate does not check them. Each value is the reason. The list only shrinks.
func ownContractDirs() map[string]string {
	return map[string]string{
		"pulumi":    "the Pulumi Cloud-compatible API keeps the error shape that the Pulumi CLI expects",
		"tfe":       "the TFE-compatible API keeps the JSON:API error documents that OpenTofu expects",
		"tofuhttp":  "the OpenTofu HTTP backend keeps the plain text errors and the 409 and 423 answers that OpenTofu expects",
		"runnerapi": "the runner protocol has its own codes and its own versions",
	}
}

// ownBodies returns, for each gated directory, the struct types with a JSON
// member code, errorCode or errors that are not written through problem.Writer.
// Each value is the reason. The list only shrinks.
func ownBodies() map[string]map[string]string {
	return map[string]map[string]string{
		"problem": {
			"Body": "the one error body of the tool-neutral APIs",
		},
		"server": {
			"errorResponse":        "an own error writer of the installation API that has not moved to problem.Writer yet",
			"codedResponse":        "an own error writer of the installation API that has not moved to problem.Writer yet",
			"enterRefusedResponse": "an own error writer of the installation API that has not moved to problem.Writer yet",
			"devCodeRequest":       "the request body of the dev sign-in; code is the sign-in code",
			"getRestoreResponse":   "a 200 row; errorCode repeats the stored catalog code of a restore",
			"restoreSummaryJSON":   "a 200 row; errorCode repeats the stored catalog code of a restore",
			"restoreWarningJSON":   "a warning in the report of a restore that succeeded; it is no error answer",
		},
		"settings": {
			"bulkError": "the error of one item inside a 200 bulk answer",
		},
	}
}

// writerViolations parses the non-test Go files of dir and returns the sorted
// violations of the writer rules, each as "<file>: <rule>".
//
// Defense in depth: the error code gate in internal/errcode checks that an
// error answer carries a catalog code. This gate checks who writes it. Neither
// one relies on the other.
func writerViolations(dir string) ([]string, error) {
	bodies, err := errcodetest.BodyTypes(dir)
	if err != nil {
		return nil, fmt.Errorf("read body types of %s: %w", dir, err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read directory %s: %w", dir, err)
	}

	owned := ownBodies()[filepath.Base(dir)]

	var out []string

	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}

		fileOut, err := fileViolations(filepath.Join(dir, name), bodies, owned)
		if err != nil {
			return nil, err
		}

		out = append(out, fileOut...)
	}

	slices.Sort(out)

	return out, nil
}

// fileViolations returns the violations of the Go file at path. bodies are the
// error body types of its directory, and owned are the body types that have a
// reason in ownBodies.
func fileViolations(path string, bodies []string, owned map[string]string) ([]string, error) {
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	name := filepath.Base(path)

	var out []string

	ast.Inspect(file, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			out = append(out, callViolations(name, call)...)
		}

		if ts, ok := n.(*ast.TypeSpec); ok {
			_, isOwnedType := owned[ts.Name.Name]
			if slices.Contains(bodies, ts.Name.Name) && !isOwnedType {
				out = append(out, fmt.Sprintf("%s: error body type %s", name, ts.Name.Name))
			}
		}

		return true
	})

	return out, nil
}

// callViolations returns the violations of one call expression in file.
func callViolations(file string, call *ast.CallExpr) []string {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return nil
	}

	if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "http" &&
		(sel.Sel.Name == "Error" || sel.Sel.Name == "NotFound") {
		return []string{file + ": http.Error"}
	}

	if sel.Sel.Name == "WriteHeader" && len(call.Args) == 1 {
		arg, ok := call.Args[0].(*ast.SelectorExpr)
		if !ok {
			return nil
		}

		if pkg, ok := arg.X.(*ast.Ident); ok && pkg.Name == "http" &&
			!slices.Contains(okStatuses(), arg.Sel.Name) {
			return []string{file + ": error status in WriteHeader"}
		}
	}

	return nil
}

// staleRows returns a message for each row of contract and bodies that no
// longer matches the source. dirs are the source directories, and bodyTypes
// returns the error body types of a directory. The result is sorted.
func staleRows(
	dirs []string,
	contract map[string]string,
	bodies map[string]map[string]string,
	bodyTypes func(string) ([]string, error),
) []string {
	var out []string

	for key := range contract {
		if !slices.Contains(dirs, key) {
			out = append(out, fmt.Sprintf("ownContractDirs row %q is not a source directory; remove the row", key))
		}
	}

	for dir, types := range bodies {
		if !slices.Contains(dirs, dir) {
			out = append(out, fmt.Sprintf("ownBodies row %q is not a source directory; remove the row", dir))

			continue
		}

		have, err := bodyTypes(dir)
		if err != nil {
			out = append(out, fmt.Sprintf("ownBodies row %q: read body types: %v", dir, err))

			continue
		}

		for name := range types {
			if !slices.Contains(have, name) {
				out = append(out, fmt.Sprintf(
					"ownBodies row %q lists %q, which is no longer an error body type; remove it",
					dir, name,
				))
			}
		}
	}

	slices.Sort(out)

	return out
}

// TestToolNeutralAdaptersUseProblemWriter fails when an adapter under
// internal/api writes an error body without problem.Writer, or when a row of
// the exception lists no longer matches the source.
func TestToolNeutralAdaptersUseProblemWriter(t *testing.T) {
	t.Parallel()

	dirs, err := errcodetest.SourceDirs(apiDir)
	if err != nil {
		t.Fatalf("list source directories: %v", err)
	}

	contract := ownContractDirs()

	for _, d := range dirs {
		first, _, _ := strings.Cut(filepath.ToSlash(d), "/")
		if _, ok := contract[d]; ok {
			continue
		}

		if _, ok := contract[first]; ok {
			continue
		}

		t.Run(d, func(t *testing.T) {
			t.Parallel()

			violations, err := writerViolations(filepath.Join(apiDir, d))
			if err != nil {
				t.Fatalf("check %s: %v", d, err)
			}

			for _, v := range violations {
				t.Errorf("%s: write the error through problem.Writer, or add the type to ownBodies with a reason", v)
			}
		})
	}

	t.Run("stale rows", func(t *testing.T) {
		t.Parallel()

		bodyTypes := func(d string) ([]string, error) {
			return errcodetest.BodyTypes(filepath.Join(apiDir, d))
		}

		for _, msg := range staleRows(dirs, contract, ownBodies(), bodyTypes) {
			t.Error(msg)
		}
	})
}

// checkFixture writes the files of one fixture package and compares the
// violations of its directory with want.
func checkFixture(t *testing.T, files map[string]string, want []string) {
	t.Helper()

	// The directory is named a, which has no entry in ownBodies.
	dir := filepath.Join(t.TempDir(), "a")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatalf("create fixture directory: %v", err)
	}

	for name, src := range files {
		body := "package a\n\n" + strings.ReplaceAll(src, "'", "`") + "\n"
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatalf("write fixture %s: %v", name, err)
		}
	}

	got, err := writerViolations(dir)
	if err != nil {
		t.Fatalf("writerViolations: %v", err)
	}

	if !slices.Equal(got, want) {
		t.Errorf("writerViolations = %q, want %q", got, want)
	}
}

// TestWriterViolations checks the writer rules on fixture packages in a
// temporary directory. Each fixture file is written with the package clause
// "package a" before its source. A backtick in a fixture is written as a single
// quote, because a Go raw string cannot hold a backtick.
func TestWriterViolations(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		files map[string]string
		want  []string
	}{
		{
			name: "clean",
			files: map[string]string{
				"a.go": `import "net/http"; func f(w http.ResponseWriter) { w.WriteHeader(http.StatusNoContent) }`,
			},
			want: nil,
		},
		{
			name: "http.Error",
			files: map[string]string{
				"a.go": `import "net/http"; func f(w http.ResponseWriter) { http.Error(w, "x", 400) }`,
			},
			want: []string{"a.go: http.Error"},
		},
		{
			name: "http.NotFound",
			files: map[string]string{
				"a.go": `import "net/http"; func f(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) }`,
			},
			want: []string{"a.go: http.Error"},
		},
		{
			name: "WriteHeader 400",
			files: map[string]string{
				"a.go": `import "net/http"; func f(w http.ResponseWriter) { w.WriteHeader(http.StatusBadRequest) }`,
			},
			want: []string{"a.go: error status in WriteHeader"},
		},
		{
			name: "WriteHeader 500",
			files: map[string]string{
				"a.go": `import "net/http"; func f(w http.ResponseWriter) { w.WriteHeader(http.StatusInternalServerError) }`,
			},
			want: []string{"a.go: error status in WriteHeader"},
		},
		{
			name: "WriteHeader variable",
			files: map[string]string{
				"a.go": `import "net/http"; func f(w http.ResponseWriter, s int) { w.WriteHeader(s) }`,
			},
			want: nil,
		},
		{
			name: "own error body",
			files: map[string]string{
				"a.go": `type errBody struct { Code int 'json:"code"'; Message string 'json:"message"' }`,
			},
			want: []string{"a.go: error body type errBody"},
		},
		{
			name: "own body with errorCode",
			files: map[string]string{
				"a.go": `type e2 struct { ErrorCode string 'json:"errorCode"' }`,
			},
			want: []string{"a.go: error body type e2"},
		},
		{
			name: "test file ignored",
			files: map[string]string{
				"a.go":      `import "net/http"; func f(w http.ResponseWriter) { w.WriteHeader(http.StatusNoContent) }`,
				"a_test.go": `import "net/http"; func f(w http.ResponseWriter) { http.Error(w, "x", 400) }`,
			},
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			checkFixture(t, tt.files, tt.want)
		})
	}
}

// errRead is the read failure of the fixture in TestStaleRows.
var errRead = errors.New("read failed")

// TestStaleRows checks that staleRows reports each row that no longer matches
// the source, and nothing for rows that match.
func TestStaleRows(t *testing.T) {
	t.Parallel()

	types := map[string][]string{
		"server": {"errorResponse"},
	}

	tests := []struct {
		name     string
		dirs     []string
		contract map[string]string
		bodies   map[string]map[string]string
		readErr  error
		want     int
	}{
		{
			name:     "matching rows",
			dirs:     []string{"server", "pulumi"},
			contract: map[string]string{"pulumi": "reason"},
			bodies:   map[string]map[string]string{"server": {"errorResponse": "reason"}},
			readErr:  nil,
			want:     0,
		},
		{
			name:     "contract key not in dirs",
			dirs:     []string{"server"},
			contract: map[string]string{"pulumi": "reason"},
			bodies:   nil,
			readErr:  nil,
			want:     1,
		},
		{
			name:     "body type not returned",
			dirs:     []string{"server"},
			contract: nil,
			bodies:   map[string]map[string]string{"server": {"gone": "reason"}},
			readErr:  nil,
			want:     1,
		},
		{
			name:     "body types cannot be read",
			dirs:     []string{"server"},
			contract: nil,
			bodies:   map[string]map[string]string{"server": {"errorResponse": "reason"}},
			readErr:  errRead,
			want:     1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			bodyTypes := func(d string) ([]string, error) {
				return types[d], tt.readErr
			}

			got := staleRows(tt.dirs, tt.contract, tt.bodies, bodyTypes)
			if len(got) != tt.want {
				t.Errorf("staleRows = %q, want %d message(s)", got, tt.want)
			}
		})
	}
}
