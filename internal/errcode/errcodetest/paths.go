package errcodetest

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// errcodePath is the import path of the catalog.
const errcodePath = "opendustpan.org/opendustpan/internal/errcode"

// classifyPrefix starts the name of each classify function.
const classifyPrefix = "classify"

// The errors of UncodedPaths for an Adapter that does not match its source.
var (
	ErrBodyMissing = errors.New("the adapter has no such error body type")
	ErrSkipMissing = errors.New("the adapter has no such function to skip")
)

// Adapter names one HTTP adapter for the error code gate.
type Adapter struct {
	// Name is the directory of the adapter under internal/api.
	Name string
	// Bodies are the types of the error bodies that the adapter writes
	// outside its classify functions.
	Bodies []string
	// Skip are the functions that answer a fixed shape of another protocol,
	// which has its own codes. A method is "Type.method".
	Skip []string
}

// UncodedPaths returns the error paths of the adapter a in dir that name no
// code of the catalog, sorted. Test files are not read.
//
// An error path is one of these:
//   - a case of a switch in a classify function, when the switch has a
//     case that calls errors.Is, errors.As or errors.AsType: one path for
//     each error that the case names, or the case itself, or "default";
//   - a case of a type switch in a classify function;
//   - an if statement in a classify function whose condition calls errors.Is,
//     errors.As or errors.AsType;
//   - a composite literal of one of a.Bodies outside the classify functions.
//
// A path has a code when its case or its if statement, or its literal or the
// condition of an if statement around it, refers to the errcode package. A
// case or an if statement that calls another classify function hands its
// error on, and is no path. A path is "<function>: <error>", with " #n" for
// the n-th path of the same text.
func UncodedPaths(dir string, a Adapter) ([]string, error) {
	files, err := parseDir(dir)
	if err != nil {
		return nil, err
	}

	s := scan{bodies: a.Bodies, types: map[string]bool{}, funcs: map[string]bool{}, paths: nil}

	for _, f := range files {
		s.file(f, a.Skip)
	}

	for _, body := range a.Bodies {
		if !s.types[body] {
			return nil, fmt.Errorf("%w: %s.%s", ErrBodyMissing, a.Name, body)
		}
	}

	for _, skip := range a.Skip {
		if !s.funcs[skip] {
			return nil, fmt.Errorf("%w: %s.%s", ErrSkipMissing, a.Name, skip)
		}
	}

	return numbered(s.paths), nil
}

// parseDir parses the Go files of dir that are not tests, in the order of
// their names.
func parseDir(dir string) ([]*ast.File, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", dir, err)
	}

	fset := token.NewFileSet()

	var files []*ast.File

	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}

		f, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, parser.SkipObjectResolution)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", name, err)
		}

		files = append(files, f)
	}

	return files, nil
}

// scan collects the uncoded paths of one adapter.
type scan struct {
	bodies []string
	types  map[string]bool
	funcs  map[string]bool
	paths  []string
}

// file adds the paths of f, without the functions of skip.
func (s *scan) file(f *ast.File, skip []string) {
	local := errcodeName(f)

	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.GenDecl:
			s.typeNames(d)
		case *ast.FuncDecl:
			s.function(d, local, skip)
		default:
		}
	}
}

// typeNames records the types that d declares.
func (s *scan) typeNames(d *ast.GenDecl) {
	for _, spec := range d.Specs {
		if ts, ok := spec.(*ast.TypeSpec); ok {
			s.types[ts.Name.Name] = true
		}
	}
}

// function adds the paths of d, unless skip names it.
func (s *scan) function(d *ast.FuncDecl, local string, skip []string) {
	name := funcName(d)
	s.funcs[name] = true

	if d.Body == nil || slices.Contains(skip, name) {
		return
	}

	if strings.HasPrefix(d.Name.Name, classifyPrefix) {
		s.classify(name, d.Body, local)
	} else {
		s.literals(name, d.Body, local)
	}
}

// classify adds the uncoded branches of the classify function name.
func (s *scan) classify(name string, body *ast.BlockStmt, local string) {
	ast.Inspect(body, func(n ast.Node) bool {
		switch st := n.(type) {
		case *ast.SwitchStmt:
			if st.Tag == nil && hasErrorsCase(st.Body) {
				s.cases(name, st.Body, local, caseTargets)
			}
		case *ast.TypeSwitchStmt:
			s.cases(name, st.Body, local, typeTargets)
		case *ast.IfStmt:
			targets := append(errorTargets(st.Init), errorTargets(st.Cond)...)
			if len(targets) > 0 && !delegates(st.Body) &&
				!refers(st.Init, local) && !refers(st.Cond, local) && !refers(st.Body, local) {
				s.add(name, targets)
			}
		default:
		}

		return true
	})
}

// cases adds the uncoded clauses of a switch body. targets gives the errors
// that a clause names.
func (s *scan) cases(name string, body *ast.BlockStmt, local string, targets func(*ast.CaseClause) []string) {
	for _, stmt := range body.List {
		clause, ok := stmt.(*ast.CaseClause)
		if !ok || delegates(clause) || refers(clause, local) {
			continue
		}

		s.add(name, targets(clause))
	}
}

// literals adds the uncoded error bodies of the function name.
func (s *scan) literals(name string, body *ast.BlockStmt, local string) {
	var stack []ast.Node

	ast.Inspect(body, func(n ast.Node) bool {
		if n == nil {
			stack = stack[:len(stack)-1]

			return true
		}

		stack = append(stack, n)

		lit, ok := n.(*ast.CompositeLit)
		if !ok {
			return true
		}

		typ, ok := lit.Type.(*ast.Ident)
		if !ok || !slices.Contains(s.bodies, typ.Name) || refers(lit, local) || guarded(stack, local) {
			return true
		}

		s.add(name, []string{typ.Name + "{}"})

		return true
	})
}

// add adds one path for each target of the function name.
func (s *scan) add(name string, targets []string) {
	for _, t := range targets {
		s.paths = append(s.paths, name+": "+t)
	}
}

// guarded reports whether an if statement in stack has an init or a
// condition that refers to the errcode package.
func guarded(stack []ast.Node, local string) bool {
	for _, n := range stack {
		if st, ok := n.(*ast.IfStmt); ok && (refers(st.Init, local) || refers(st.Cond, local)) {
			return true
		}
	}

	return false
}

// hasErrorsCase reports whether a case of the switch body calls errors.Is,
// errors.As or errors.AsType.
func hasErrorsCase(body *ast.BlockStmt) bool {
	for _, stmt := range body.List {
		if clause, ok := stmt.(*ast.CaseClause); ok {
			for _, e := range clause.List {
				if len(errorTargets(e)) > 0 {
					return true
				}
			}
		}
	}

	return false
}

// caseTargets returns the errors that a case of an expression switch names:
// the errors of its errors calls, or the case expression when it has none.
func caseTargets(clause *ast.CaseClause) []string {
	if len(clause.List) == 0 {
		return []string{"default"}
	}

	var out []string

	for _, e := range clause.List {
		if t := errorTargets(e); len(t) > 0 {
			out = append(out, t...)
		} else {
			out = append(out, types.ExprString(e))
		}
	}

	return out
}

// typeTargets returns the types of a case of a type switch.
func typeTargets(clause *ast.CaseClause) []string {
	if len(clause.List) == 0 {
		return []string{"default"}
	}

	out := make([]string, 0, len(clause.List))
	for _, e := range clause.List {
		out = append(out, types.ExprString(e))
	}

	return out
}

// errorTargets returns the errors that the errors.Is, errors.As and
// errors.AsType calls in n name.
func errorTargets(n ast.Node) []string {
	if isNil(n) {
		return nil
	}

	var out []string

	ast.Inspect(n, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			if t := callTarget(call); t != "" {
				out = append(out, t)
			}
		}

		return true
	})

	return out
}

// callTarget returns the error that call names when it is errors.Is,
// errors.As or errors.AsType, and "" for any other call.
func callTarget(call *ast.CallExpr) string {
	switch fun := call.Fun.(type) {
	case *ast.SelectorExpr:
		if !isErrors(fun.X) || len(call.Args) != 2 {
			return ""
		}

		switch fun.Sel.Name {
		case "Is":
			return types.ExprString(call.Args[1])
		case "As":
			return "As(" + types.ExprString(call.Args[1]) + ")"
		default:
			return ""
		}
	case *ast.IndexExpr:
		if sel, ok := fun.X.(*ast.SelectorExpr); ok && isErrors(sel.X) && sel.Sel.Name == "AsType" {
			return "AsType[" + types.ExprString(fun.Index) + "]"
		}

		return ""
	default:
		return ""
	}
}

// isErrors reports whether e is the errors package.
func isErrors(e ast.Expr) bool {
	id, ok := e.(*ast.Ident)

	return ok && id.Name == "errors"
}

// delegates reports whether n calls a classify function.
func delegates(n ast.Node) bool {
	found := false

	ast.Inspect(n, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			if id, ok := call.Fun.(*ast.Ident); ok && strings.HasPrefix(id.Name, classifyPrefix) {
				found = true
			}
		}

		return !found
	})

	return found
}

// refers reports whether n names something of the errcode package, which the
// file imports as local.
func refers(n ast.Node, local string) bool {
	if local == "" || isNil(n) {
		return false
	}

	found := false

	ast.Inspect(n, func(n ast.Node) bool {
		if sel, ok := n.(*ast.SelectorExpr); ok {
			if id, ok := sel.X.(*ast.Ident); ok && id.Name == local {
				found = true
			}
		}

		return !found
	})

	return found
}

// isNil reports whether n is missing, as the init or the condition of an if
// statement can be. ast.Inspect does not take a missing node.
func isNil(n ast.Node) bool {
	return n == nil
}

// errcodeName returns the name under which f imports the catalog, or "" when
// f does not import it.
func errcodeName(f *ast.File) string {
	for _, imp := range f.Imports {
		p, err := strconv.Unquote(imp.Path.Value)
		if err != nil || p != errcodePath {
			continue
		}

		if imp.Name != nil {
			return imp.Name.Name
		}

		return filepath.Base(p)
	}

	return ""
}

// funcName returns the name of d, with the type of the receiver for a method.
func funcName(d *ast.FuncDecl) string {
	if d.Recv == nil || len(d.Recv.List) == 0 {
		return d.Name.Name
	}

	t := d.Recv.List[0].Type
	if star, ok := t.(*ast.StarExpr); ok {
		t = star.X
	}

	if idx, ok := t.(*ast.IndexExpr); ok {
		t = idx.X
	}

	return types.ExprString(t) + "." + d.Name.Name
}

// numbered gives the n-th copy of a path the suffix " #n", and sorts the
// paths.
func numbered(paths []string) []string {
	seen := map[string]int{}
	out := make([]string, 0, len(paths))

	for _, p := range paths {
		seen[p]++
		if seen[p] > 1 {
			p += " #" + strconv.Itoa(seen[p])
		}

		out = append(out, p)
	}

	slices.Sort(out)

	if len(out) == 0 {
		return nil
	}

	return out
}
