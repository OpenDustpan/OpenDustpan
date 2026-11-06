package selfservice

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// RecheckPoint names a point where the server checks a revision, because a
// value that the check reads can change between two points.
type RecheckPoint string

// The recheck points.
const (
	// PointSubmit is the submit through the API. A failure stores no
	// revision.
	PointSubmit RecheckPoint = "submit"
	// PointApproval is before the job of the revision records an approval.
	// A failure rejects the job.
	PointApproval RecheckPoint = "approval"
	// PointApply is before the dispatch of a phase that changes, and each
	// render for the team's CI. A failure rejects the job, or refuses the
	// render.
	PointApply RecheckPoint = "apply"
)

// FailureCode names why an answer or a revision failed a check.
type FailureCode string

// The codes of a failure.
const (
	FailureUnknownInput FailureCode = "unknown-input"
	FailureRequired     FailureCode = "required"
	FailureType         FailureCode = "type"
	FailureRule         FailureCode = "rule"
	FailureRuleFailed   FailureCode = "rule-failed"
	FailureChoice       FailureCode = "choice"
	FailurePolicy       FailureCode = "policy"
	FailureQuota        FailureCode = "quota"
	FailureOneHome      FailureCode = "one-home"
	FailureRetired      FailureCode = "retired"
	FailureExprVersion  FailureCode = "expr-version"
)

// Failure is one failure of a check. Input names the input that failed, or
// is empty for a failure of the whole revision, such as a policy denial.
// Message is the text that a form shows next to the input.
type Failure struct {
	Input   string      `json:"input,omitempty"`
	Code    FailureCode `json:"code"`
	Message string      `json:"message"`
}

// CheckError is the error of a check that found failures. It matches
// ErrCheckFailed with errors.Is.
type CheckError struct {
	Point    RecheckPoint
	Failures []Failure
}

// Error names the point and each failure.
func (e *CheckError) Error() string {
	parts := make([]string, 0, len(e.Failures))
	for _, f := range e.Failures {
		if f.Input == "" {
			parts = append(parts, string(f.Code)+": "+f.Message)

			continue
		}

		parts = append(parts, f.Input+": "+string(f.Code)+": "+f.Message)
	}

	return fmt.Sprintf("%s at %s: %s", ErrCheckFailed, e.Point, strings.Join(parts, "; "))
}

// Is reports whether target is ErrCheckFailed.
func (*CheckError) Is(target error) bool {
	return target == ErrCheckFailed
}

// CheckAnswers returns the failures of the answers of d against the inputs
// of b, with no engine: an answer to an unknown input, a required input
// with no answer, and an answer of the wrong type. The failures are in the
// order of the inputs of b, then the unknown inputs in sorted order. The
// Expr rules, the choices, the policy and the quotas come after, in the
// order of the recheck.
func CheckAnswers(b *Blueprint, d *Document) []Failure {
	var out []Failure

	for i := range b.Inputs {
		in := &b.Inputs[i]

		raw, ok := d.Inputs[in.Name]

		switch {
		case !ok && in.Required:
			out = append(out, Failure{Input: in.Name, Code: FailureRequired, Message: "Give an answer."})
		case ok && !in.Type.accepts(raw):
			out = append(out, Failure{
				Input: in.Name, Code: FailureType, Message: "Give a value of the type " + string(in.Type) + ".",
			})
		default:
		}
	}

	var unknown []string

	for name := range d.Inputs {
		if _, ok := b.InputNamed(name); !ok {
			unknown = append(unknown, name)
		}
	}

	slices.Sort(unknown)

	for _, name := range unknown {
		out = append(out, Failure{
			Input: name, Code: FailureUnknownInput,
			Message: fmt.Sprintf("The blueprint %s has no input %s.", b.Name, name),
		})
	}

	return out
}

// ResultKind is the type that an expression must give.
type ResultKind string

// The result kinds of an expression.
const (
	ResultBool   ResultKind = "bool"
	ResultString ResultKind = "string"
)

// Limits of the built-in Expr engine. Without the node limit, the memory
// budget and the string limit, an expression of 200 bytes used all the memory
// of a test machine. The engine adds its own limits for the work of the string
// functions, the size of the environment and the runs that have not ended
// (package internal/expr), because a loop over the groups that reads a long
// string in each turn passed the time limit by seconds without them.
const (
	// MaxExprNodes is the node limit of the Expr compiler for one
	// expression.
	MaxExprNodes = 128
	// ExprMemoryBudget is the memory budget of the Expr virtual machine for
	// one run: the count of the items of the lists that the run makes.
	ExprMemoryBudget = 1000
	// MaxConcatBytes bounds the string that the operator + gives. The
	// engine runs + on two strings through a function that refuses a
	// longer result, because the memory budget does not count strings.
	MaxConcatBytes = 1 << 10
	// MaxEnvGroups bounds the groups of the requester in an Env. A
	// requester in more groups fails each expression with ErrRuleFailed
	// for a validation rule, and makes a policy or a quota rule fail to
	// run.
	MaxEnvGroups = 1000
	// MaxClosureDepth is the deepest nesting of closures, such as the
	// predicate of all or filter, that an expression can have.
	MaxClosureDepth = 1
)

// ExprBuiltins returns the builtins of Expr that an expression can call,
// sorted. The engine refuses each other builtin at compile: the builtins
// that read the time or the local time zone (now, date, timezone,
// duration), the builtins whose result order follows the order of a map
// (keys, values, toPairs), and the builtins that can make a string or a
// list larger than the memory budget counts (join, repeat, replace, string,
// toJSON, reduce and the others). The engine also refuses the syntax let
// and the range operator .., and a closure inside a closure.
func ExprBuiltins() []string {
	return []string{
		"abs", "all", "any", "ceil", "count", "filter", "find", "findIndex", "findLast", "findLastIndex",
		"first", "float", "floor", "get", "hasPrefix", "hasSuffix", "indexOf", "int", "last", "lastIndexOf",
		"len", "lower", "map", "max", "mean", "median", "min", "none", "one", "round", "sum", "trim",
		"trimPrefix", "trimSuffix", "upper",
	}
}

// Requester is the requester as an expression sees it.
type Requester struct {
	Subject string   `expr:"subject"`
	Agent   string   `expr:"agent"`
	Groups  []string `expr:"groups"`
}

// RequestEnv is the request as an expression sees it.
type RequestEnv struct {
	Requester Requester `expr:"requester"`
	Namespace string    `expr:"namespace"`
	Name      string    `expr:"name"`
}

// Env is the environment of an expression. Value is the answer that a
// validation rule checks, or nil. Inputs holds every answer, with the
// defaults filled in. Usage maps the key of each quota rule to its usage.
// Input is the policy input document, for a policy rule. An expression sees
// the fields by their expr tag.
//
// Env and each type in it are data only: they have no method, and each
// value in Inputs, Value and Input is a JSON value as encoding/json decodes
// it. So an expression can read data and cannot call code of the server.
type Env struct {
	Value   any            `expr:"value"`
	Inputs  map[string]any `expr:"inputs"`
	Usage   map[string]int `expr:"usage"`
	Input   any            `expr:"input"`
	Request RequestEnv     `expr:"request"`
}

// Evaluator is the port of the built-in Expr engine. The adapter compiles
// each expression with a type check against Env, the node limit
// MaxExprNodes, only the builtins of ExprBuiltins, no let, no range
// operator, closures nested at most MaxClosureDepth deep, and the operator
// + on two strings bounded by MaxConcatBytes. It runs each expression with
// the memory budget ExprMemoryBudget and a time limit of
// ExprTimeoutMillis. So an expression cannot read the time, the local time
// zone, random data or the network, and a rule gives the same result for
// the same inputs at each recheck point.
type Evaluator interface {
	// Version returns the version of the Expr library that the server
	// links, for example "v1.17.8". A publish records it in the blueprint.
	Version() string
	// Compile checks that source compiles to kind. It returns an error that
	// wraps ErrInvalid for an expression that does not compile or uses
	// syntax or a builtin that the engine refuses, and ErrLimit above
	// MaxExprBytes or MaxExprNodes.
	Compile(source string, kind ResultKind) error
	// Bool runs source in env and returns its boolean result. It returns an
	// error that wraps ErrRuleFailed for a run that fails, passes the
	// memory budget or passes the time limit. The caller never counts an
	// error as true. For a validation rule, the error fails the answer. For
	// a policy condition or a quota rule, the error means that the check
	// cannot run: the policy engine returns policy.ErrRuleFailed, the quota
	// check returns quota.ErrRuleFailed, and the recheck stops with no
	// change.
	Bool(ctx context.Context, source string, env *Env) (bool, error)
	// String runs source in env and returns its string result, with the
	// errors of Bool.
	String(ctx context.Context, source string, env *Env) (string, error)
}

// AnswerValues decodes the answers of d, with the defaults of b filled in,
// for the Inputs of an Env.
func AnswerValues(b *Blueprint, d *Document) (map[string]any, error) {
	out := make(map[string]any, len(b.Inputs))

	for i := range b.Inputs {
		in := &b.Inputs[i]

		raw, ok := d.Inputs[in.Name]
		if !ok {
			raw = in.Default
		}

		if len(raw) == 0 {
			continue
		}

		var v any
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, fmt.Errorf("%w: input %s: %w", ErrInvalid, in.Name, err)
		}

		out[in.Name] = v
	}

	return out, nil
}
