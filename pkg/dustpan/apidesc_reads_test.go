package dustpan_test

import (
	"fmt"
	"go/ast"
	"go/build"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/api/apidesc"
)

// undescribedQueryReads holds, for an adapter, the names that its routes
// outside the prefix read. Those routes are not in the description, so no label
// declares the names. A route that moves under the prefix declares its names,
// and the entry goes away. A name that is not here and that no label declares
// fails the test.
//
//nolint:gochecknoglobals // A fixed table; Go has no constant maps.
var undescribedQueryReads = map[string][]string{
	"opendustpan.org/opendustpan/internal/api/inventory": {"organization", "unit"},
	"opendustpan.org/opendustpan/internal/api/server": {
		"action", "code", "cursor", "error", "expand", "fresh", "from", "holder", "kind", "limit", "object",
		"prefix", "principal", "return", "role", "state", "status", "subject", "to", "tool", "type", "unit",
	},
}

// TestQueryReadsNameDeclaredQueries proves, from the source of each adapter
// that serves a route under the prefix, that every name that the code passes to
// Get, Has and All of apidesc.Query is a constant, and that a label of the
// package declares it. A typo or a missing WithQuery fails here, with no test
// request, and not only when a test reaches the branch of the read.
func TestQueryReadsNameDeclaredQueries(t *testing.T) {
	t.Parallel()

	srv := newEveryModuleServer(t)
	packages := srv.HandlerPackages()
	served := srv.Operations()

	checked := 0

	for _, source := range srv.RouteSources() {
		if !slices.ContainsFunc(source.Patterns, underPrefix) {
			continue
		}

		allowed := append(declaredQueryNames(served, source.Patterns), undescribedQueryReads[packages[source.Name]]...)
		pkgPath := packages[source.Name]

		t.Run(source.Name, func(t *testing.T) {
			t.Parallel()

			for _, read := range queryReads(t, pkgPath) {
				if problem := read.problem(allowed, pkgPath); problem != "" {
					t.Error(problem)
				}
			}
		})

		checked++
	}

	if checked == 0 {
		t.Fatal("no adapter serves a route under the prefix")
	}
}

// declaredQueryNames returns the query names that the served operations of the
// patterns declare.
func declaredQueryNames(served []apidesc.Operation, patterns []string) []string {
	var names []string

	for i := range served {
		o := &served[i]
		if !slices.Contains(patterns, o.Method+" "+o.Path) {
			continue
		}

		for _, p := range o.Query {
			names = append(names, p.Name)
		}
	}

	return names
}

// queryRead is one call of Get, Has or All on an apidesc.Query.
type queryRead struct {
	pos    string
	method string
	// name is the constant name, or nil when the argument is not a constant.
	name *string
}

// problem returns the text of the failure of the read, or "" when the name is
// one of the allowed names.
func (q queryRead) problem(allowed []string, pkgPath string) string {
	if q.name == nil {
		return fmt.Sprintf("%s: the name passed to %s is not a constant: use a constant, "+
			"so that this test can check it against the labels", q.pos, q.method)
	}

	if slices.Contains(allowed, *q.name) {
		return ""
	}

	return fmt.Sprintf("%s: %s(%q) reads a name that no label of the package %s declares: "+
		"add it with WithQuery, then run %s", q.pos, q.method, *q.name, pkgPath, apidesc.FixCommand)
}

// queryReads type-checks the non-test source of the package and returns the
// calls of Get, Has and All on apidesc.Query.
func queryReads(t *testing.T, pkgPath string) []queryRead {
	t.Helper()

	dir := filepath.Join("..", "..", strings.TrimPrefix(pkgPath, modulePath))

	bp, err := build.ImportDir(dir, 0)
	if err != nil {
		t.Fatalf("read the package %s: %v", pkgPath, err)
	}

	fset := token.NewFileSet()
	files := make([]*ast.File, 0, len(bp.GoFiles))

	for _, name := range bp.GoFiles {
		f, parseErr := parser.ParseFile(fset, filepath.Join(dir, name), nil, parser.SkipObjectResolution)
		if parseErr != nil {
			t.Fatalf("parse %s: %v", name, parseErr)
		}

		files = append(files, f)
	}

	//nolint:exhaustruct_v5 // go/types fills only the maps that the caller sets.
	info := &types.Info{
		Types:      map[ast.Expr]types.TypeAndValue{},
		Selections: map[*ast.SelectorExpr]*types.Selection{},
	}
	//nolint:exhaustruct_v5 // The other fields of the configuration keep their defaults.
	conf := types.Config{Importer: importer.ForCompiler(fset, "source", nil), Error: func(error) {}}

	if _, checkErr := conf.Check(pkgPath, fset, files, info); checkErr != nil {
		t.Fatalf("type-check %s: %v", pkgPath, checkErr)
	}

	var reads []queryRead

	for _, f := range files {
		ast.Inspect(f, func(n ast.Node) bool {
			if read, ok := queryReadOf(fset, info, n); ok {
				reads = append(reads, read)
			}

			return true
		})
	}

	return reads
}

// queryReadOf returns the read that the node is, when it is a call of Get,
// Has or All on an apidesc.Query.
func queryReadOf(fset *token.FileSet, info *types.Info, n ast.Node) (queryRead, bool) {
	var none queryRead

	call, ok := n.(*ast.CallExpr)
	if !ok || len(call.Args) != 1 {
		return none, false
	}

	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || !isQueryMethod(info.Selections[sel]) {
		return none, false
	}

	read := queryRead{pos: fset.Position(call.Pos()).String(), method: sel.Sel.Name, name: nil}
	if tv := info.Types[call.Args[0]]; tv.Value != nil {
		name := strings.Trim(tv.Value.ExactString(), `"`)
		read.name = &name
	}

	return read, true
}

// isQueryMethod reports whether the selection is Get, Has or All of
// apidesc.Query.
func isQueryMethod(sel *types.Selection) bool {
	if sel == nil {
		return false
	}

	fn, ok := sel.Obj().(*types.Func)
	if !ok || !slices.Contains([]string{"Get", "Has", "All"}, fn.Name()) {
		return false
	}

	return sel.Recv().String() == "opendustpan.org/opendustpan/internal/api/apidesc.Query"
}
