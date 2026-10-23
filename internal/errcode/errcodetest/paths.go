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

	"opendustpan.org/opendustpan/internal/errcode"
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
	// Name is the name of the known list of the adapter.
	Name string
	// Dir is the directory of the adapter.
	Dir string
	// Bodies are the types of the error bodies that the adapter writes
	// outside its mapping functions.
	Bodies []string
	// Skip are the functions that answer a fixed shape of another protocol,
	// which has its own codes. A method is "Type.method".
	Skip []string
}

// UncodedPaths returns the error paths of the adapter a that name no code of
// the catalog, sorted. coded are the names in the errcode package that give a
// path a code (see CodedNames). Test files are not read.
//
// A mapping function is a classify function, or a function that calls
// errors.Is, errors.As or errors.AsType and names an HTTP status of 400 or
// more. An error path is one of these:
//   - a case of a switch in a mapping function, when the switch has a case
//     that calls errors.Is, errors.As or errors.AsType: one path for each
//     error that the case names, or the case itself, or "default";
//   - a case of a type switch in a mapping function;
//   - an if statement in a mapping function whose init or condition calls
//     errors.Is, errors.As or errors.AsType;
//   - an element of a table, a composite literal whose elements each hold a
//     sentinel error and an HTTP status of 400 or more;
//   - a composite literal of one of a.Bodies outside these cases and if
//     statements, also in the value of a variable;
//   - a string literal that holds the JSON member "code" or "errors";
//   - an anonymous struct literal with a member whose JSON name is code,
//     errorCode or errors, and a map literal with such a key;
//   - a call of http.Error or http.NotFound.
//
// Outside a classify function, a case or an if statement is a path only when
// its body names an HTTP status of 400 or more. A path has a code when its
// case, its if statement, its element or its literal names one of coded. A
// literal in the body of an if statement whose init or condition names one of
// coded has a code; a literal in its else branch does not. A case or an if
// statement that calls another classify function hands its error on, and is
// no path. A path is "<function>: <error>", with " #n" for the n-th path of
// the same text.
func UncodedPaths(a Adapter, coded []string) ([]string, error) {
	files, err := parseDir(a.Dir)
	if err != nil {
		return nil, err
	}

	s := scan{
		bodies: a.Bodies, coded: coded, local: "", classify: false, answers: nil,
		types: map[string]bool{}, funcs: map[string]bool{}, branches: map[ast.Node]bool{}, paths: nil,
	}

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

// CodedNames returns the names in the errcode package that give an error
// path a code: New, Forbidden, and each constant of the source in dir whose
// value is a code of the catalog. Any other name of the package, for example
// Valid or KindIdentifier, gives no code.
func CodedNames(dir string) ([]string, error) {
	files, err := parseDir(dir)
	if err != nil {
		return nil, err
	}

	names := []string{"New", "Forbidden"}

	for _, f := range files {
		for _, decl := range f.Decls {
			if d, ok := decl.(*ast.GenDecl); ok && d.Tok == token.CONST {
				names = append(names, codeConstants(d)...)
			}
		}
	}

	slices.Sort(names)

	return names, nil
}

// codeConstants returns the constants of d whose value is a code of the
// catalog.
func codeConstants(d *ast.GenDecl) []string {
	var out []string

	for _, spec := range d.Specs {
		vs, ok := spec.(*ast.ValueSpec)
		if !ok || len(vs.Names) != len(vs.Values) {
			continue
		}

		for i, name := range vs.Names {
			lit, ok := vs.Values[i].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				continue
			}

			value, err := strconv.Unquote(lit.Value)
			if _, known := errcode.Lookup(value); err == nil && known {
				out = append(out, name.Name)
			}
		}
	}

	return out
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
	coded  []string
	// local is the name under which the current file imports the catalog.
	local string
	// classify is true in a classify function, where each branch that tests
	// an error is a path.
	classify bool
	// answers are the names in the current function that have the type
	// errcode.Answer: a parameter, a result or a variable.
	answers  map[string]bool
	types    map[string]bool
	funcs    map[string]bool
	branches map[ast.Node]bool
	paths    []string
}

// file adds the paths of f, without the functions of skip.
func (s *scan) file(f *ast.File, skip []string) {
	s.local = errcodeName(f)

	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.GenDecl:
			s.typeNames(d)
			s.varTables(d)
		case *ast.FuncDecl:
			s.function(d, skip)
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

// varTables adds the uncoded elements of the tables that d declares.
func (s *scan) varTables(d *ast.GenDecl) {
	for _, spec := range d.Specs {
		vs, ok := spec.(*ast.ValueSpec)
		if !ok {
			continue
		}

		for i, value := range vs.Values {
			name := "_"
			if i < len(vs.Names) {
				name = vs.Names[i].Name
			}

			s.tables(name, value)
			s.literals(name, value)
		}
	}
}

// function adds the paths of d, unless skip names it.
func (s *scan) function(d *ast.FuncDecl, skip []string) {
	name := funcName(d)
	s.funcs[name] = true

	if d.Body == nil || slices.Contains(skip, name) {
		return
	}

	s.answers = s.answerNames(d)
	s.classify = strings.HasPrefix(d.Name.Name, classifyPrefix)
	if s.classify || (len(errorTargets(d.Body)) > 0 && errorStatus(d.Body)) {
		s.branchPaths(name, d.Body)
	}

	s.tables(name, d.Body)
	s.literals(name, d.Body)
}

// branchPaths adds the uncoded branches of the mapping function name. Outside
// a classify function, a branch counts only when its body names an error
// status.
func (s *scan) branchPaths(name string, body *ast.BlockStmt) {
	ast.Inspect(body, func(n ast.Node) bool {
		switch st := n.(type) {
		case *ast.SwitchStmt:
			if st.Tag == nil && hasErrorsCase(st.Body) {
				s.cases(name, st.Body, caseTargets)
			}
		case *ast.TypeSwitchStmt:
			s.cases(name, st.Body, typeTargets)
		case *ast.IfStmt:
			s.ifPath(name, st)
		default:
		}

		return true
	})
}

// ifPath adds the if statement st of name when it tests an error and has no
// code.
func (s *scan) ifPath(name string, st *ast.IfStmt) {
	targets := append(errorTargets(st.Init), errorTargets(st.Cond)...)
	if len(targets) == 0 || delegates(st.Body) || (!s.classify && !errorStatus(st.Body)) {
		return
	}

	s.branches[st.Body] = true

	if !s.refers(st.Init) && !s.refers(st.Cond) && !s.refers(st.Body) {
		s.add(name, targets)
	}
}

// cases adds the uncoded clauses of a switch body. targets gives the errors
// that a clause names.
func (s *scan) cases(name string, body *ast.BlockStmt, targets func(*ast.CaseClause) []string) {
	for _, stmt := range body.List {
		clause, ok := stmt.(*ast.CaseClause)
		if !ok || delegates(clause) || (!s.classify && !errorStatus(clause)) {
			continue
		}

		s.branches[clause] = true

		if !s.refers(clause) {
			s.add(name, targets(clause))
		}
	}
}

// tables adds the uncoded elements of each table in n, under the name owner.
func (s *scan) tables(owner string, n ast.Node) {
	ast.Inspect(n, func(n ast.Node) bool {
		lit, ok := n.(*ast.CompositeLit)
		if !ok || len(lit.Elts) == 0 {
			return true
		}

		elems := make([]*ast.CompositeLit, 0, len(lit.Elts))

		for _, e := range lit.Elts {
			elem, ok := e.(*ast.CompositeLit)
			if !ok || !hasSentinel(elem) || !errorStatus(elem) {
				return true
			}

			elems = append(elems, elem)
		}

		for _, elem := range elems {
			s.branches[elem] = true

			if !s.refers(elem) {
				s.add(owner, []string{elementName(elem)})
			}
		}

		return false
	})
}

// literals adds the uncoded error bodies, raw JSON error bodies and plain
// text errors in body, the body of the function name or the value of the
// variable name, that no branch or table holds.
func (s *scan) literals(name string, body ast.Node) {
	var stack []ast.Node

	ast.Inspect(body, func(n ast.Node) bool {
		if n == nil {
			stack = stack[:len(stack)-1]

			return true
		}

		stack = append(stack, n)

		if s.inBranch(stack) || s.guarded(stack) {
			return true
		}

		if path := s.literal(n); path != "" {
			s.add(name, []string{path})
		}

		return true
	})
}

// literal returns the path of n when n is an uncoded error body, a raw JSON
// error body or a plain text error, and "" when it is none of these.
func (s *scan) literal(n ast.Node) string {
	switch x := n.(type) {
	case *ast.CompositeLit:
		return s.compositeLit(x)
	case *ast.BasicLit:
		if rawJSONError(x) {
			return "raw JSON"
		}
	case *ast.CallExpr:
		return plainTextError(x)
	default:
	}

	return ""
}

// compositeLit returns the path of lit when it is an uncoded error body: a
// literal of one of the bodies, an anonymous struct with a member that
// bodyMembers names, or a map with such a key. It returns "" otherwise.
func (s *scan) compositeLit(lit *ast.CompositeLit) string {
	if s.refers(lit) || s.copiesCode(lit) {
		return ""
	}

	switch typ := lit.Type.(type) {
	case *ast.Ident:
		if slices.Contains(s.bodies, typ.Name) {
			return typ.Name + "{}"
		}
	case *ast.StructType:
		if hasBodyMember(typ) {
			return "anonymous struct"
		}
	case *ast.MapType:
		if hasBodyKey(lit) {
			return "map literal"
		}
	default:
	}

	return ""
}

// copiesCode reports whether the code member of the body literal lit
// (ErrorCode or Code) holds the Code method of an errcode.Answer. New is the
// only way to make an Answer with a code, so a body that copies the code of an
// Answer has a code of the catalog. No other call counts, and no other place:
// a condition, a case clause or a table element that calls Code is not coded.
func (s *scan) copiesCode(lit *ast.CompositeLit) bool {
	return slices.ContainsFunc(lit.Elts, func(e ast.Expr) bool {
		kv, ok := e.(*ast.KeyValueExpr)
		if !ok {
			return false
		}

		var key string

		switch k := kv.Key.(type) {
		case *ast.Ident:
			key = k.Name
		case *ast.BasicLit:
			key, _ = strconv.Unquote(k.Value)
		default:
			return false
		}

		return (key == "ErrorCode" || key == "Code" || key == "errorCode" || key == "code") && s.answerCode(kv.Value)
	})
}

// answerCode reports whether e is a call of the Code method, with no
// argument, of an errcode.Answer: a name that answerNames found, or a
// selector that ends in .Answer.
func (s *scan) answerCode(e ast.Expr) bool {
	call, ok := e.(*ast.CallExpr)
	if !ok || len(call.Args) != 0 {
		return false
	}

	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Code" {
		return false
	}

	switch x := sel.X.(type) {
	case *ast.Ident:
		return s.answers[x.Name]
	case *ast.SelectorExpr:
		return x.Sel.Name == "Answer"
	default:
		return false
	}
}

// answerNames returns the names in d, in its parameters, results and var
// declarations, that have the type Answer of the errcode package.
func (s *scan) answerNames(d *ast.FuncDecl) map[string]bool {
	out := map[string]bool{}
	if s.local == "" {
		return out
	}

	fields := func(l *ast.FieldList) {
		if l == nil {
			return
		}

		for _, f := range l.List {
			if s.isAnswerType(f.Type) {
				for _, n := range f.Names {
					out[n.Name] = true
				}
			}
		}
	}

	fields(d.Type.Params)
	fields(d.Type.Results)
	fields(d.Recv)

	ast.Inspect(d.Body, func(n ast.Node) bool {
		if vs, ok := n.(*ast.ValueSpec); ok && s.isAnswerType(vs.Type) {
			for _, name := range vs.Names {
				out[name.Name] = true
			}
		}

		return true
	})

	return out
}

// isAnswerType reports whether t is errcode.Answer, with the local name of
// the import.
func (s *scan) isAnswerType(t ast.Expr) bool {
	sel, ok := t.(*ast.SelectorExpr)

	return ok && isPackage(sel.X, s.local) && sel.Sel.Name == "Answer"
}

// hasBodyKey reports whether a key of the map literal lit is a string that
// bodyMembers names.
func hasBodyKey(lit *ast.CompositeLit) bool {
	return slices.ContainsFunc(lit.Elts, func(e ast.Expr) bool {
		kv, ok := e.(*ast.KeyValueExpr)
		if !ok {
			return false
		}

		key, ok := kv.Key.(*ast.BasicLit)
		if !ok || key.Kind != token.STRING {
			return false
		}

		name, err := strconv.Unquote(key.Value)

		return err == nil && slices.Contains(bodyMembers(), name)
	})
}

// plainTextError returns the path of call when it is http.Error or
// http.NotFound, and "" for any other call.
func plainTextError(call *ast.CallExpr) string {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || !isPackage(sel.X, "http") || (sel.Sel.Name != "Error" && sel.Sel.Name != "NotFound") {
		return ""
	}

	return "http." + sel.Sel.Name
}

// rawJSONError reports whether lit is a string that holds the JSON member
// "code" or "errors" of an error body.
func rawJSONError(lit *ast.BasicLit) bool {
	if lit.Kind != token.STRING {
		return false
	}

	value, err := strconv.Unquote(lit.Value)
	if err != nil {
		return false
	}

	return strings.Contains(value, `"code"`) || strings.Contains(value, `"errors"`)
}

// add adds one path for each target of the function name.
func (s *scan) add(name string, targets []string) {
	for _, t := range targets {
		s.paths = append(s.paths, name+": "+t)
	}
}

// inBranch reports whether a branch or a table element in stack is a path
// already, coded or not.
func (s *scan) inBranch(stack []ast.Node) bool {
	return slices.ContainsFunc(stack, func(n ast.Node) bool { return s.branches[n] })
}

// guarded reports whether stack is in the body of an if statement whose init
// or condition names a code. The else branch of that if statement is not
// guarded.
func (s *scan) guarded(stack []ast.Node) bool {
	for i, n := range stack {
		st, ok := n.(*ast.IfStmt)
		if !ok || i+1 >= len(stack) || stack[i+1] != st.Body {
			continue
		}

		if s.refers(st.Init) || s.refers(st.Cond) {
			return true
		}
	}

	return false
}

// refers reports whether n names one of the coded names of the errcode
// package.
func (s *scan) refers(n ast.Node) bool {
	if s.local == "" || isNil(n) {
		return false
	}

	found := false

	ast.Inspect(n, func(n ast.Node) bool {
		if sel, ok := n.(*ast.SelectorExpr); ok && isPackage(sel.X, s.local) && slices.Contains(s.coded, sel.Sel.Name) {
			found = true
		}

		return !found
	})

	return found
}

// errorStatus reports whether n names an HTTP status of 400 or more, as a
// constant of net/http.
func errorStatus(n ast.Node) bool {
	if isNil(n) {
		return false
	}

	found := false

	ast.Inspect(n, func(n ast.Node) bool {
		if sel, ok := n.(*ast.SelectorExpr); ok && isPackage(sel.X, "http") && isErrorStatus(sel.Sel.Name) {
			found = true
		}

		return !found
	})

	return found
}

// isErrorStatus reports whether name is a status constant of net/http of 400
// or more. The constants below 400 are few and fixed, so the check lists them.
func isErrorStatus(name string) bool {
	below := []string{
		"StatusContinue", "StatusSwitchingProtocols", "StatusProcessing", "StatusEarlyHints",
		"StatusOK", "StatusCreated", "StatusAccepted", "StatusNonAuthoritativeInfo", "StatusNoContent",
		"StatusResetContent", "StatusPartialContent", "StatusMultiStatus", "StatusAlreadyReported", "StatusIMUsed",
		"StatusMultipleChoices", "StatusMovedPermanently", "StatusFound", "StatusSeeOther", "StatusNotModified",
		"StatusUseProxy", "StatusTemporaryRedirect", "StatusPermanentRedirect", "StatusText",
	}

	return strings.HasPrefix(name, "Status") && !slices.Contains(below, name)
}

// hasSentinel reports whether n names a sentinel error, a name that starts
// with "Err" or "err" followed by a capital letter.
func hasSentinel(n ast.Node) bool {
	found := false

	ast.Inspect(n, func(n ast.Node) bool {
		var name string

		switch x := n.(type) {
		case *ast.SelectorExpr:
			name = x.Sel.Name
		case *ast.Ident:
			name = x.Name
		default:
			return !found
		}

		if sentinelName(name) {
			found = true
		}

		return !found
	})

	return found
}

// sentinelName reports whether name has the form of a sentinel error.
func sentinelName(name string) bool {
	rest, ok := strings.CutPrefix(name, "Err")
	if !ok {
		rest, ok = strings.CutPrefix(name, "err")
	}

	return ok && rest != "" && rest[0] >= 'A' && rest[0] <= 'Z'
}

// elementName returns the error that a table element names: its first
// element.
func elementName(elem *ast.CompositeLit) string {
	first := elem.Elts[0]
	if kv, ok := first.(*ast.KeyValueExpr); ok {
		first = kv.Value
	}

	return types.ExprString(first)
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
		if !isPackage(fun.X, "errors") || len(call.Args) != 2 {
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
		if sel, ok := fun.X.(*ast.SelectorExpr); ok && isPackage(sel.X, "errors") && sel.Sel.Name == "AsType" {
			return "AsType[" + types.ExprString(fun.Index) + "]"
		}

		return ""
	default:
		return ""
	}
}

// isPackage reports whether e is the package that the file names as name.
func isPackage(e ast.Expr, name string) bool {
	id, ok := e.(*ast.Ident)

	return ok && id.Name == name
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
