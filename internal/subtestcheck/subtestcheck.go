// Package subtestcheck finds a check in a parent test body that cannot see the
// result of a parallel subtest.
//
// A parallel subtest pauses at t.Parallel and continues only after the parent
// test function returns. A t.Error or t.Fatal that follows the t.Run call in the
// parent body therefore runs before the subtest body. When that call reads a
// variable that the subtest writes, it checks nothing.
//
// The analysis is syntactic (go/parser and go/ast). It reports a call to Error,
// Errorf, Fatal or Fatalf on the parent *testing.T when all of these are true:
//
//   - the call comes after a t.Run call whose function literal calls Parallel
//     on its own *testing.T parameter;
//   - the call, or the condition of an if statement that encloses it, names a
//     variable of the parent function; and
//   - the function literal of that t.Run writes the variable: an assignment
//     (which includes an append re-assignment), an increment, a write to a map
//     element, a slice element or a field, a delete call, or an atomic Store,
//     Add, Swap, CompareAndSwap, And or Or call on it.
//
// A check inside a function literal (for example t.Cleanup) is not reported:
// it runs after the subtests. A check that follows a t.Run of a group test,
// which does not call Parallel itself, is not reported either: that t.Run
// returns only after the parallel subtests inside it end.
//
// Known gaps. The check does not see a write through a method call (other than
// an atomic one), a subtest that is a named function and not a function
// literal, a helper function that calls t.Error, or a pointer that aliases the
// variable. Each gap gives a missed finding, not a false one.
//
// Variables are matched by name. A name that the subtest declares itself is a
// different variable and is not a write to the parent variable.
package subtestcheck

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
)

// Finding is one check that reads a variable that a parallel subtest writes.
type Finding struct {
	Pos      token.Position
	Variable string
}

// Message is the text that Run prints for the finding.
func (f Finding) Message() string {
	return fmt.Sprintf("%s: the check reads %q after a parallel t.Run that writes it, and runs before that subtest. "+
		"Move the check into t.Cleanup or into a group t.Run that wraps the subtests.", f.Pos, f.Variable)
}

// Exit codes of Run.
const (
	exitFinding = 1
	exitError   = 2
)

// Run checks the Go test files under each root in args (the current directory
// when args is empty), prints each finding, and returns the exit code: 0 for
// no finding, 1 for a finding and 2 for an error.
func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		args = []string{"."}
	}
	code := 0
	for _, root := range args {
		findings, err := CheckDir(root)
		if err != nil {
			if _, werr := fmt.Fprintf(stderr, "subtestcheck: %v\n", err); werr != nil {
				return exitError
			}
			return exitError
		}
		for _, f := range findings {
			if _, err := fmt.Fprintln(stdout, f.Message()); err != nil {
				return exitError
			}
			code = exitFinding
		}
	}
	return code
}

// CheckDir parses each _test.go file under root and returns the findings in
// path order. It skips testdata, vendor and hidden directories below root.
func CheckDir(root string) ([]Finding, error) {
	var findings []Finding
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && skippedDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
		findings = append(findings, CheckFile(fset, f)...)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk %s: %w", root, err)
	}
	return findings, nil
}

func skippedDir(name string) bool {
	return name == "testdata" || name == "vendor" || strings.HasPrefix(name, ".")
}

// CheckFile returns the findings in one parsed file, in source order.
func CheckFile(fset *token.FileSet, file *ast.File) []Finding {
	var findings []Finding
	ast.Inspect(file, func(n ast.Node) bool {
		ftype, body := funcParts(n)
		if body == nil {
			return true
		}
		if t := testingParam(ftype); t != "" {
			findings = append(findings, checkBody(fset, t, ftype, body)...)
		}
		return true
	})
	slices.SortStableFunc(findings, func(a, b Finding) int {
		return a.Pos.Offset - b.Pos.Offset
	})
	return findings
}

// funcParts returns the type and the body of a function declaration or literal.
func funcParts(n ast.Node) (*ast.FuncType, *ast.BlockStmt) {
	switch fn := n.(type) {
	case *ast.FuncDecl:
		return fn.Type, fn.Body
	case *ast.FuncLit:
		return fn.Type, fn.Body
	default:
		return nil, nil
	}
}

// subtest is a t.Run call with a parallel function literal, and the variables
// of the parent that the literal writes.
type subtest struct {
	end    token.Pos
	writes map[string]bool
}

func checkBody(fset *token.FileSet, t string, ftype *ast.FuncType, body *ast.BlockStmt) []Finding {
	subtests := parallelSubtests(t, declaredNames(ftype, body), body)
	if len(subtests) == 0 {
		return nil
	}
	r := &reader{fset: fset, t: t, subtests: subtests, conds: nil, findings: nil}
	for _, s := range body.List {
		ast.Inspect(s, r.visit)
	}
	return r.findings
}

// parallelSubtests returns the parallel t.Run calls of the body that write a
// variable that the parent function declares.
func parallelSubtests(t string, declared map[string]bool, body *ast.BlockStmt) []subtest {
	var subtests []subtest
	walkBody(body, func(n ast.Node) {
		call, ok := n.(*ast.CallExpr)
		if !ok || !isMethodCall(call, t, "Run") || len(call.Args) != 2 {
			return
		}
		lit, ok := call.Args[1].(*ast.FuncLit)
		if !ok || !callsParallel(lit) {
			return
		}
		writes := map[string]bool{}
		for name := range writtenNames(lit) {
			if declared[name] && name != t {
				writes[name] = true
			}
		}
		if len(writes) > 0 {
			subtests = append(subtests, subtest{end: call.End(), writes: writes})
		}
	})
	return subtests
}

// reader finds the checks in a parent body, outside function literals.
type reader struct {
	fset     *token.FileSet
	t        string
	subtests []subtest
	conds    []ast.Expr
	findings []Finding
}

func (r *reader) visit(n ast.Node) bool {
	switch n := n.(type) {
	case *ast.FuncLit:
		return false
	case *ast.IfStmt:
		r.visitIf(n)
		return false
	case *ast.CallExpr:
		return !r.visitCall(n)
	default:
		return true
	}
}

// visitIf tracks the condition of each enclosing if statement: a check in the
// body of the if reads the variables of that condition too.
func (r *reader) visitIf(n *ast.IfStmt) {
	if n.Init != nil {
		ast.Inspect(n.Init, r.visit)
	}
	r.conds = append(r.conds, n.Cond)
	ast.Inspect(n.Body, r.visit)
	r.conds = r.conds[:len(r.conds)-1]
	if n.Else != nil {
		ast.Inspect(n.Else, r.visit)
	}
}

// visitCall records a finding for a check call and reports whether it did.
func (r *reader) visitCall(n *ast.CallExpr) bool {
	sel, ok := n.Fun.(*ast.SelectorExpr)
	if !ok || !isCheckMethod(sel.Sel.Name) || !isIdent(sel.X, r.t) {
		return false
	}
	read := identNames(n.Args)
	for _, c := range r.conds {
		for name := range identNames([]ast.Expr{c}) {
			read[name] = true
		}
	}
	for _, st := range r.subtests {
		if n.Pos() < st.end {
			continue
		}
		for _, name := range sortedKeys(st.writes) {
			if read[name] {
				r.findings = append(r.findings, Finding{Pos: r.fset.Position(n.Pos()), Variable: name})
				return true
			}
		}
	}
	return false
}

func isCheckMethod(name string) bool {
	switch name {
	case "Error", "Errorf", "Fatal", "Fatalf":
		return true
	default:
		return false
	}
}

// testingParam returns the name of the *testing.T parameter, or "".
func testingParam(ft *ast.FuncType) string {
	if ft.Params == nil {
		return ""
	}
	for _, field := range ft.Params.List {
		if len(field.Names) == 0 || !isTestingT(field.Type) {
			continue
		}
		if name := field.Names[0].Name; name != "_" {
			return name
		}
	}
	return ""
}

func isTestingT(e ast.Expr) bool {
	star, ok := e.(*ast.StarExpr)
	if !ok {
		return false
	}
	sel, ok := star.X.(*ast.SelectorExpr)
	return ok && sel.Sel.Name == "T" && isIdent(sel.X, "testing")
}

// callsParallel reports whether the function literal calls Parallel on its own
// *testing.T parameter, outside any nested function literal.
func callsParallel(lit *ast.FuncLit) bool {
	t := testingParam(lit.Type)
	if t == "" {
		return false
	}
	found := false
	walkBody(lit.Body, func(n ast.Node) {
		if call, ok := n.(*ast.CallExpr); ok && isMethodCall(call, t, "Parallel") {
			found = true
		}
	})
	return found
}

// declaredNames returns the names that the function declares: parameters,
// results and the names that its own body (not a nested literal) defines.
func declaredNames(ft *ast.FuncType, body *ast.BlockStmt) map[string]bool {
	names := map[string]bool{}
	addFields(names, ft)
	collectDeclared(body, names)
	return names
}

func addFields(names map[string]bool, ft *ast.FuncType) {
	for _, fl := range []*ast.FieldList{ft.Params, ft.Results} {
		if fl == nil {
			continue
		}
		for _, f := range fl.List {
			for _, n := range f.Names {
				names[n.Name] = true
			}
		}
	}
}

// collectDeclared adds the names that n defines with :=, var or range,
// outside nested function literals.
func collectDeclared(n ast.Node, names map[string]bool) {
	walkBody(n, func(n ast.Node) {
		switch n := n.(type) {
		case *ast.AssignStmt:
			if n.Tok == token.DEFINE {
				addIdents(names, n.Lhs)
			}
		case *ast.ValueSpec:
			for _, id := range n.Names {
				names[id.Name] = true
			}
		case *ast.RangeStmt:
			if n.Tok == token.DEFINE {
				addIdents(names, []ast.Expr{n.Key, n.Value})
			}
		default:
		}
	})
}

// writtenNames returns the names that the literal writes and does not declare
// itself. A write inside a nested literal counts: it runs in the subtest.
func writtenNames(lit *ast.FuncLit) map[string]bool {
	local := declaredNames(lit.Type, lit.Body)
	// A nested literal declares names of its own. Treating them as local here
	// can hide a write, which is the safe direction for a check with no
	// false positive.
	ast.Inspect(lit.Body, func(n ast.Node) bool {
		if inner, ok := n.(*ast.FuncLit); ok {
			addFields(local, inner.Type)
			collectDeclared(inner.Body, local)
		}
		return true
	})

	written := map[string]bool{}
	add := func(e ast.Expr) {
		if name := rootName(e); name != "" && !local[name] {
			written[name] = true
		}
	}
	ast.Inspect(lit.Body, func(n ast.Node) bool {
		markWrites(n, add)
		return true
	})
	return written
}

// markWrites passes to add each expression that the node writes.
func markWrites(n ast.Node, add func(ast.Expr)) {
	switch n := n.(type) {
	case *ast.AssignStmt:
		if n.Tok != token.DEFINE {
			for _, l := range n.Lhs {
				add(l)
			}
		}
	case *ast.IncDecStmt:
		add(n.X)
	case *ast.CallExpr:
		markCallWrite(n, add)
	default:
	}
}

// markCallWrite passes to add the variable that a call mutates: delete(m, k),
// atomic.StoreInt64(&v, x) and v.Store(x).
func markCallWrite(call *ast.CallExpr, add func(ast.Expr)) {
	if id, ok := call.Fun.(*ast.Ident); ok && id.Name == "delete" && len(call.Args) > 0 {
		add(call.Args[0])
		return
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || !hasAtomicPrefix(sel.Sel.Name) {
		return
	}
	if !isIdent(sel.X, "atomic") {
		add(sel.X)
		return
	}
	if len(call.Args) == 0 {
		return
	}
	if u, ok := call.Args[0].(*ast.UnaryExpr); ok && u.Op == token.AND {
		add(u.X)
	}
}

func hasAtomicPrefix(name string) bool {
	for _, p := range [...]string{"Store", "Add", "Swap", "CompareAndSwap", "And", "Or"} {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// rootName returns the variable at the root of an assignment target such as
// m[k], s.f, *p or (v).
func rootName(e ast.Expr) string {
	for {
		switch x := e.(type) {
		case *ast.Ident:
			if x.Name == "_" {
				return ""
			}
			return x.Name
		case *ast.IndexExpr:
			e = x.X
		case *ast.SelectorExpr:
			e = x.X
		case *ast.StarExpr:
			e = x.X
		case *ast.ParenExpr:
			e = x.X
		default:
			return ""
		}
	}
}

// walkBody calls fn for n and each node below it, and does not enter a nested
// function literal.
func walkBody(n ast.Node, fn func(ast.Node)) {
	ast.Inspect(n, func(c ast.Node) bool {
		if c == nil {
			return false
		}
		if _, ok := c.(*ast.FuncLit); ok && c != n {
			return false
		}
		fn(c)
		return true
	})
}

// identNames returns the names of the identifiers in the expressions. It does
// not return the field name after a dot.
func identNames(exprs []ast.Expr) map[string]bool {
	names := map[string]bool{}
	for _, e := range exprs {
		ast.Inspect(e, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.SelectorExpr:
				for name := range identNames([]ast.Expr{n.X}) {
					names[name] = true
				}
				return false
			case *ast.Ident:
				names[n.Name] = true
			default:
			}
			return true
		})
	}
	return names
}

func addIdents(names map[string]bool, exprs []ast.Expr) {
	for _, e := range exprs {
		if id, ok := e.(*ast.Ident); ok {
			names[id.Name] = true
		}
	}
}

func isIdent(e ast.Expr, name string) bool {
	id, ok := e.(*ast.Ident)
	return ok && id.Name == name
}

func isMethodCall(call *ast.CallExpr, recv, method string) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	return ok && sel.Sel.Name == method && isIdent(sel.X, recv)
}

func sortedKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
