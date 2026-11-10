package sqlite_test

import (
	"bytes"
	_ "embed"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"regexp"
	"strings"
	"testing"
)

//go:embed queries.sql
var unitStateQueries string

// unitStateWritePattern matches a statement that writes to the unit_state
// table, in any case, with any spacing, and with or without a schema prefix.
var unitStateWritePattern = regexp.MustCompile(
	`(?i)\b(?:INSERT\s+INTO|UPDATE|DELETE\s+FROM)\s+(?:[^\n;]*?\.)?unit_state\b`,
)

// queryNamePattern matches the name line that starts a query of queries.sql.
var queryNamePattern = regexp.MustCompile(`(?m)^-- name: ([A-Za-z0-9_]+)`)

// generatedFilePattern matches the marker of a generated Go file.
var generatedFilePattern = regexp.MustCompile(`(?m)^// Code generated .* DO NOT EDIT\.$`)

// unitStateWriteQueries returns the queries that write unit_state.
func unitStateWriteQueries() []string {
	return []string{
		"FinalizeReleasedUnitState",
		"FinalizeUnitState",
		"PublishUnitState",
	}
}

const (
	// unitStateFile is the file that holds the one entry point of the writes.
	unitStateFile = "unit_state.go"
	// unitStateWriter is the function that writes unit_state.
	unitStateWriter = "publishUnitState"
	// unitStateRestoreHelper writes the versions of a restore derive. Only
	// publishUnitState calls it.
	unitStateRestoreHelper = "restoreUnitState"
)

// TestQueriesWriteUnitStateOnlyInPublishUnitState proves that the state write
// queries and the writes of the unit_state table live only in publishUnitState,
// which calls the restore helper that writes the restored versions.
func TestQueriesWriteUnitStateOnlyInPublishUnitState(t *testing.T) {
	t.Parallel()

	t.Run("queries.sql", func(t *testing.T) {
		t.Parallel()

		checkQueriesWriteUnitStateOnlyInPublishUnitState(t)
	})

	t.Run("Go files", func(t *testing.T) {
		t.Parallel()

		checkGoFilesWriteUnitStateOnlyInPublishUnitState(t)
	})
}

// checkQueriesWriteUnitStateOnlyInPublishUnitState reads the queries of the
// dialect and fails when a query other than a state write query writes
// unit_state.
func checkQueriesWriteUnitStateOnlyInPublishUnitState(t *testing.T) {
	t.Helper()

	names := queryNamePattern.FindAllStringSubmatchIndex(unitStateQueries, -1)
	if len(names) == 0 {
		t.Fatal("queries.sql holds no query")
	}

	writers := make(map[string]bool, len(unitStateWriteQueries()))
	for _, name := range unitStateWriteQueries() {
		writers[name] = false
	}

	for i, loc := range names {
		end := len(unitStateQueries)
		if i+1 < len(names) {
			end = names[i+1][0]
		}

		name := unitStateQueries[loc[2]:loc[3]]
		query := unitStateQueries[loc[0]:end]

		if _, ok := writers[name]; ok {
			writers[name] = true

			if !unitStateWritePattern.MatchString(query) {
				t.Fatalf("%s does not write unit_state", name)
			}

			continue
		}

		if unitStateWritePattern.MatchString(query) {
			t.Fatalf("query %s writes unit_state, want one of the state write queries", name)
		}
	}

	for name, wrote := range writers {
		if !wrote {
			t.Fatalf("queries.sql holds no %s query", name)
		}
	}
}

// checkGoFilesWriteUnitStateOnlyInPublishUnitState reads every non-generated
// Go file of the dialect package, tests included, and fails when one writes
// unit_state outside the writer or calls a state write query or the restore
// helper outside publishUnitState.
func checkGoFilesWriteUnitStateOnlyInPublishUnitState(t *testing.T) {
	t.Helper()

	dir := os.DirFS(".")

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read the package directory: %v", err)
	}

	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") {
			continue
		}

		src, readErr := fs.ReadFile(dir, name)
		if readErr != nil {
			t.Fatalf("read %s: %v", name, readErr)
		}

		if generatedFilePattern.Match(src) {
			continue
		}

		checkGoFileUnitStateWrites(t, name, src)
	}
}

// checkGoFileUnitStateWrites fails when name writes unit_state outside the
// writer or its helper, or calls a state write query or the helper outside
// publishUnitState.
func checkGoFileUnitStateWrites(t *testing.T, name string, src []byte) {
	t.Helper()

	fset := token.NewFileSet()

	file, err := parser.ParseFile(fset, name, src, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}

	ranges := unitStateWriteRanges(fset, name, file)

	for _, loc := range unitStateWritePattern.FindAllIndex(src, -1) {
		if unitStateInRanges(ranges, loc[0]) {
			continue
		}

		t.Fatalf("%s:%d writes unit_state, want only %s and %s in %s",
			name, lineNumber(src, loc[0]), unitStateWriter, unitStateRestoreHelper, unitStateFile)
	}

	checkGoFileUnitStateCalls(t, name, fset, file)
	checkGoFileUnitStateHelperCalls(t, name, fset, file)
}

// unitStateRange is a byte range of a source file.
type unitStateRange struct {
	start int
	end   int
}

// unitStateWriteRanges returns the byte ranges of the bodies of the writer and
// its helper in name, or no range in any other file.
func unitStateWriteRanges(fset *token.FileSet, name string, file *ast.File) []unitStateRange {
	if name != unitStateFile {
		return nil
	}

	var out []unitStateRange

	for _, fnName := range []string{unitStateWriter, unitStateRestoreHelper} {
		r := unitStateFunctionRange(fset, name, file, fnName)
		if r.end > r.start {
			out = append(out, r)
		}
	}

	return out
}

// unitStateFunctionRange returns the byte range of the body of fnName in
// unit_state.go, or a range that holds no offset.
func unitStateFunctionRange(fset *token.FileSet, name string, file *ast.File, fnName string) unitStateRange {
	if name != unitStateFile {
		return unitStateRange{start: 0, end: 0}
	}

	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != fnName || fn.Body == nil {
			continue
		}

		return unitStateRange{
			start: fset.Position(fn.Body.Pos()).Offset,
			end:   fset.Position(fn.Body.End()).Offset,
		}
	}

	return unitStateRange{start: 0, end: 0}
}

// unitStateInRanges reports whether offset falls in one of ranges.
func unitStateInRanges(ranges []unitStateRange, offset int) bool {
	for _, r := range ranges {
		if r.start <= offset && offset < r.end {
			return true
		}
	}

	return false
}

// unitStateAllowedCalls returns the positions of the state write query calls
// that publishUnitState makes in unit_state.go.
func unitStateAllowedCalls(name string, file *ast.File) map[token.Pos]bool {
	allowed := make(map[token.Pos]bool)

	if name != unitStateFile {
		return allowed
	}

	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != unitStateWriter || fn.Body == nil {
			continue
		}

		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if ok && unitStateQueryCall(call) != "" {
				allowed[call.Pos()] = true
			}

			return true
		})
	}

	return allowed
}

// checkGoFileUnitStateCalls fails when a state write query is called outside
// publishUnitState in unit_state.go, or in any other file.
func checkGoFileUnitStateCalls(t *testing.T, name string, fset *token.FileSet, file *ast.File) {
	t.Helper()

	allowed := unitStateAllowedCalls(name, file)

	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}

		query := unitStateQueryCall(call)
		if query == "" || allowed[call.Pos()] {
			return true
		}

		pos := fset.Position(call.Pos())
		t.Fatalf("%s:%d calls %s, want only %s in %s", name, pos.Line, query, unitStateWriter, unitStateFile)

		return false
	})
}

// checkGoFileUnitStateHelperCalls fails when a function other than
// publishUnitState calls the restore helper.
func checkGoFileUnitStateHelperCalls(t *testing.T, name string, fset *token.FileSet, file *ast.File) {
	t.Helper()

	writer := unitStateFunctionRange(fset, name, file, unitStateWriter)

	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}

		ident, ok := call.Fun.(*ast.Ident)
		if !ok || ident.Name != unitStateRestoreHelper {
			return true
		}

		offset := fset.Position(call.Pos()).Offset
		if writer.start <= offset && offset < writer.end {
			return true
		}

		pos := fset.Position(call.Pos())
		t.Fatalf("%s:%d calls %s, want only %s in %s",
			name, pos.Line, unitStateRestoreHelper, unitStateWriter, unitStateFile)

		return false
	})
}

// unitStateQueryCall returns the state write query that call calls, or an
// empty string when it calls none.
func unitStateQueryCall(call *ast.CallExpr) string {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return ""
	}

	for _, name := range unitStateWriteQueries() {
		if selector.Sel.Name == name {
			return name
		}
	}

	return ""
}

// lineNumber returns the 1-based line of the byte offset in src.
func lineNumber(src []byte, offset int) int {
	return 1 + bytes.Count(src[:offset], []byte("\n"))
}
