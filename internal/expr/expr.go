// Package expr is the built-in Expr engine. It implements catalog.Evaluator
// with github.com/expr-lang/expr, and it is the only package that imports
// that library (depguard holds this rule).
//
// An expression is untrusted text that a person wrote. Each layer below stops
// a hostile expression alone, and each has its own test:
//
//   - Size: Compile refuses source above catalog.MaxExprBytes before the
//     parser reads it, and a tree above catalog.MaxExprNodes. A run refuses an
//     environment above MaxEnvBytes.
//   - Names: the environment is catalog.Env, a type with no method, and the
//     compiler checks every name against it. The builtins are exactly
//     catalog.ExprBuiltins. No function of Go, no reflection and no
//     environment variable is reachable.
//   - Syntax: Compile refuses let, the range operator, a closure inside a
//     closure and a matches pattern that is not a string literal.
//   - Memory: the virtual machine has the budget catalog.ExprMemoryBudget,
//     and the operator + refuses a string above catalog.MaxConcatBytes.
//   - Work: each string function and each operator that reads a string
//     counts the bytes that it reads, with the budget MaxWorkUnits for one
//     run. This bounds a loop over the groups that reads a long string in
//     each turn, and it ends a run at its next call after the time limit.
//   - Time: a run that passes catalog.ExprTimeoutMillis, or whose context
//     ends, returns catalog.ErrRuleFailed to the caller at that time. The
//     virtual machine of Expr cannot be stopped from outside, so the
//     goroutine of such a run ends at its next counted call. An operation
//     that no counter covers, for example the compare of two strings, runs
//     to its end; the environment limit and the node limit bound it.
//   - Runs: at most MaxConcurrentRuns runs of one Evaluator have not ended,
//     and a run that finds no room fails with catalog.ErrRuleFailed. A run
//     that passed its time limit counts until its goroutine ends.
//   - Result: a run that gives another type than the expression compiled to,
//     or that fails in any way, never gives true.
package expr

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"runtime/debug"
	"slices"
	"time"

	exprlang "github.com/expr-lang/expr"
	"github.com/expr-lang/expr/ast"
	"github.com/expr-lang/expr/vm"
	exprruntime "github.com/expr-lang/expr/vm/runtime"

	"opendustpan.org/opendustpan/internal/catalog"
)

var (
	errConcatArity = errors.New("concat needs 2 arguments")
	errConcatLimit = errors.New("a string above the limit of the operator +")
)

const (
	// concatArgs is the count of operands of the operator +.
	concatArgs = 2

	library = "github.com/expr-lang/expr"
	// concatName is the name of the function that replaces the operator +.
	// It holds a space, so no expression can write the name.
	concatName = "dustpan +"
)

// Evaluator is the catalog.Evaluator of the built-in Expr engine. It has no
// state: use one value for all goroutines.
type Evaluator struct {
	// timeout is the time limit of one run. New sets it to
	// catalog.ExprTimeoutMillis.
	timeout time.Duration
	// slots holds one item for each run that has not ended.
	slots chan struct{}
}

var _ catalog.Evaluator = (*Evaluator)(nil)

// New returns the Evaluator.
func New() *Evaluator {
	return &Evaluator{
		timeout: catalog.ExprTimeoutMillis * time.Millisecond,
		slots:   make(chan struct{}, MaxConcurrentRuns),
	}
}

// Version returns the version of the Expr library in the build, for example
// "v1.17.8". It is empty only for a binary built with no module information.
func (*Evaluator) Version() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}

	for _, dep := range info.Deps {
		if dep.Path != library {
			continue
		}

		if dep.Replace != nil {
			return dep.Replace.Version
		}

		return dep.Version
	}

	return ""
}

// Compile implements catalog.Evaluator.
func (*Evaluator) Compile(source string, kind catalog.ResultKind) error {
	_, err := compile(source, kind, true, newMeter(func() error { return nil }))

	return err
}

// Bool implements catalog.Evaluator.
func (e *Evaluator) Bool(ctx context.Context, source string, env *catalog.Env) (bool, error) {
	out, err := e.run(ctx, source, catalog.ResultBool, env)
	if err != nil {
		return false, err
	}

	b, ok := out.(bool)
	if !ok {
		return false, fmt.Errorf("%w: the result is %T, want a boolean", catalog.ErrRuleFailed, out)
	}

	return b, nil
}

// String implements catalog.Evaluator.
func (e *Evaluator) String(ctx context.Context, source string, env *catalog.Env) (string, error) {
	out, err := e.run(ctx, source, catalog.ResultString, env)
	if err != nil {
		return "", err
	}

	s, ok := out.(string)
	if !ok {
		return "", fmt.Errorf("%w: the result is %T, want a string", catalog.ErrRuleFailed, out)
	}

	return s, nil
}

type runResult struct {
	out any
	err error
}

func (e *Evaluator) run(ctx context.Context, source string, kind catalog.ResultKind, env *catalog.Env) (any, error) {
	if env == nil {
		return nil, fmt.Errorf("%w: there is no environment", catalog.ErrRuleFailed)
	}

	// Defense in depth: the caller bounds the groups of a requester too; this
	// check stays so that a new caller cannot skip it.
	if n := len(env.Request.Requester.Groups); n > catalog.MaxEnvGroups {
		return nil, fmt.Errorf("%w: the requester is in %d groups, the limit is %d",
			catalog.ErrRuleFailed, n, catalog.MaxEnvGroups)
	}

	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("%w: %w", catalog.ErrRuleFailed, err)
	}

	if err := checkEnvSize(env); err != nil {
		return nil, fmt.Errorf("%w: %w", catalog.ErrRuleFailed, err)
	}

	ctx, cancel := context.WithTimeout(ctx, e.timeout)
	defer cancel()

	// The run compiles with no result type. The compiler casts a nil to false
	// for a boolean, and a rule that gives no value must fail, not pass or
	// deny. run checks the type of the result.
	program, err := compile(source, kind, false, newMeter(ctx.Err))
	if err != nil {
		// An expression that does not compile cannot run. The caller never
		// counts this as a pass.
		return nil, fmt.Errorf("%w: %w", catalog.ErrRuleFailed, err)
	}

	select {
	case e.slots <- struct{}{}:
	default:
		return nil, fmt.Errorf("%w: %w", catalog.ErrRuleFailed, errBusy)
	}

	// The virtual machine of Expr has no way to stop a run from outside. The
	// channel has room for the result, so the goroutine always ends: at the
	// end of the program, or at its next counted call after the time limit
	// (see the package comment). It gives its slot back when it ends, not
	// when the caller stops waiting.
	done := make(chan runResult, 1)

	go func() {
		defer func() { <-e.slots }()

		machine := vm.VM{Stack: nil, Scopes: nil, Variables: nil, MemoryBudget: catalog.ExprMemoryBudget}
		out, err := machine.Run(program, *env)
		done <- runResult{out: out, err: err}
	}()

	select {
	case r := <-done:
		if r.err != nil {
			return nil, fmt.Errorf("%w: %w", catalog.ErrRuleFailed, r.err)
		}

		return r.out, nil
	case <-ctx.Done():
		return nil, fmt.Errorf("%w: %w", catalog.ErrRuleFailed, ctx.Err())
	}
}

// compile checks the limits and the syntax, and compiles source. With typed it
// also checks that source gives kind.
func compile(source string, kind catalog.ResultKind, typed bool, m *meter) (*vm.Program, error) {
	if source == "" || len(source) > catalog.MaxExprBytes {
		return nil, fmt.Errorf("%w: an expression has 1 to %d bytes, this one has %d",
			catalog.ErrLimit, catalog.MaxExprBytes, len(source))
	}

	options := []exprlang.Option{
		exprlang.Env(schema()),
		exprlang.MaxNodes(catalog.MaxExprNodes),
		exprlang.DisableAllBuiltins(),
		exprlang.Function(concatName, concat, concatTypes()...),
		exprlang.Operator("+", concatName),
	}

	options = append(options, m.stringFunctions()...)

	for _, name := range catalog.ExprBuiltins() {
		// The meter gives these as functions that count their work.
		if !isMetered(name) {
			options = append(options, exprlang.EnableBuiltin(name))
		}
	}

	switch {
	case kind != catalog.ResultBool && kind != catalog.ResultString:
		return nil, fmt.Errorf("%w: the result kind %q is not known", catalog.ErrInvalid, kind)
	case !typed:
		options = append(options, exprlang.AsAny())
	case kind == catalog.ResultBool:
		options = append(options, exprlang.AsBool())
	default:
		options = append(options, exprlang.AsKind(reflect.String))
	}

	refused := &refusals{found: nil}
	rewrite := &rewriter{regexps: m.regexps, failed: nil}
	options = append(options, exprlang.Patch(refused), exprlang.Patch(rewrite))

	prog, err := safeCompile(source, options)

	if found := slices.Concat(refused.found, rewrite.failed); len(found) > 0 {
		return nil, fmt.Errorf("%w: %s", catalog.ErrInvalid, found[0])
	}

	if err != nil {
		return nil, err
	}

	return prog, nil
}

// safeCompile compiles source. The library reports most errors as values and
// some as panics; both are a refusal of the expression.
func safeCompile(source string, options []exprlang.Option) (*vm.Program, error) {
	var (
		prog *vm.Program
		err  error
	)

	func() {
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("%w: %v", catalog.ErrInvalid, r)
			}
		}()

		prog, err = exprlang.Compile(source, options...)
		if err != nil {
			err = classify(err)
		}
	}()

	return prog, err
}

// schema returns the value that the compiler reads the types of the
// environment from. Env has the same fields at each run.
func schema() catalog.Env {
	return catalog.Env{
		Value:  nil,
		Inputs: nil,
		Usage:  nil,
		Input:  nil,
		Request: catalog.RequestEnv{
			Requester: catalog.Requester{Subject: "", Agent: "", Groups: nil},
			Namespace: "",
			Name:      "",
		},
	}
}

// classify turns an error of the compiler into ErrLimit for the node limit
// and ErrInvalid for the rest.
func classify(err error) error {
	if isNodeLimit(err.Error()) {
		return fmt.Errorf("%w: an expression has at most %d nodes", catalog.ErrLimit, catalog.MaxExprNodes)
	}

	return fmt.Errorf("%w: %s", catalog.ErrInvalid, firstLine(err.Error()))
}

// concat is the function behind the operator +. It gives a string only when
// the result has at most catalog.MaxConcatBytes bytes, because the memory
// budget of Expr does not count strings.
func concat(params ...any) (any, error) {
	if len(params) != concatArgs {
		return nil, errConcatArity
	}

	a, aok := params[0].(string)
	b, bok := params[1].(string)

	if aok && bok {
		if len(a)+len(b) > catalog.MaxConcatBytes {
			return nil, fmt.Errorf("%w: %d bytes, the limit is %d",
				errConcatLimit, len(a)+len(b), catalog.MaxConcatBytes)
		}

		return a + b, nil
	}

	// A string and another type is an error in Expr. A sum of numbers, and
	// the other cases of +, are the work of the library.
	return exprruntime.Add(params[0], params[1]), nil
}

// concatTypes lists the types that the operator + takes. The compiler picks
// the first that fits, so the exact types come before any.
func concatTypes() []any {
	return []any{
		func(string, string) string { return "" },
		func(int, int) int { return 0 },
		func(float64, float64) float64 { return 0 },
		func(any, any) any { return nil },
	}
}

// refusals collects the syntax that the engine refuses. It is a visitor of
// the compiler: it changes no node.
type refusals struct {
	found []string
}

// nodeRef is the parameter type of ast.Visitor.
type nodeRef = *ast.Node

// Visit implements ast.Visitor.
func (r *refusals) Visit(node nodeRef) {
	switch n := (*node).(type) {
	case *ast.VariableDeclaratorNode:
		r.add("let is not allowed")
	case *ast.BuiltinNode:
		// The parser of the library reads a builtin that takes a closure
		// (reduce, sortBy, groupBy and others) even when it is disabled, so
		// the list of builtins is checked here too.
		if !slices.Contains(catalog.ExprBuiltins(), n.Name) {
			r.add("the builtin " + n.Name + " is not allowed")
		}
	case *ast.BinaryNode:
		r.visitBinary(n)
	case *ast.PredicateNode:
		var depth closureCheck

		ast.Walk(&n.Node, &depth)

		if depth.nested {
			r.add(fmt.Sprintf("a closure inside a closure is not allowed, the depth limit is %d",
				catalog.MaxClosureDepth))
		}
	default:
	}
}

func (r *refusals) visitBinary(n *ast.BinaryNode) {
	switch n.Operator {
	case "..":
		r.add("the range operator .. is not allowed")
	case opMatches:
		// A pattern from the environment would compile at each run, and a
		// requester writes the environment.
		if _, ok := n.Right.(*ast.StringNode); !ok {
			r.add("matches needs a string literal as its pattern")
		}
	default:
	}
}

func (r *refusals) add(msg string) {
	r.found = append(r.found, msg)
}

// closureCheck finds a closure inside the body of a closure.
type closureCheck struct{ nested bool }

// Visit implements ast.Visitor.
func (c *closureCheck) Visit(node nodeRef) {
	if _, ok := (*node).(*ast.PredicateNode); ok {
		c.nested = true
	}
}
