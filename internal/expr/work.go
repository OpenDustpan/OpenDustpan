package expr

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	exprlang "github.com/expr-lang/expr"
	"github.com/expr-lang/expr/ast"

	"opendustpan.org/opendustpan/internal/catalog"
)

// The work budget of one run. Every call of a string function, and every
// operator that reads a string, adds the bytes that it reads to the work of
// the run. A call that would pass the budget fails before it reads, so one
// call is never the cause of a long run, and a run that passed the time limit
// ends at its next call.
const (
	// MaxWorkUnits is the work budget of one run.
	MaxWorkUnits = 1 << 22
	// MatchesUnitsPerByte is the work of one byte that matches reads. A
	// regular expression of Go reads each byte in time that is linear, but
	// the constant is high. The weight keeps the input that one run can give
	// to matches near 128 KiB.
	MatchesUnitsPerByte = 32
	// MaxEnvBytes bounds the bytes of the strings in the environment of one
	// run, and the count of its items.
	MaxEnvBytes = 256 << 10
	// MaxConcurrentRuns bounds the runs of one Limiter that have not ended.
	// A run that passed its time limit counts until its goroutine ends, so
	// hostile expressions cannot pile up goroutines.
	MaxConcurrentRuns = 64
)

var (
	errWork       = errors.New("the work budget of the run is spent")
	errAlive      = errors.New("the run has ended")
	errNotString  = errors.New("a string function got another type")
	errEnvTooBig  = errors.New("the environment is above the limit")
	errEnvType    = errors.New("the environment holds a value of another type than JSON")
	errBusy       = errors.New("too many runs have not ended")
	errBadPattern = errors.New("the pattern is not a valid regular expression")
)

// The names of the functions that replace an operator. They hold a space, so
// no expression can write the name: the rewrite puts the call in the tree
// where it found the operator, after the checks of the syntax.
const (
	matchesName    = "dustpan matches"
	containsName   = "dustpan contains"
	startsWithName = "dustpan startsWith"
	endsWithName   = "dustpan endsWith"
)

// Limiter is the cap on the runs that have not ended, shared by the
// Evaluators that use it. One process must make one Limiter and pass it to each
// Evaluator that it makes: the server wiring creates it at start. A run counts
// until its goroutine ends, also after the caller got its error, so hostile
// expressions cannot pile up goroutines. A Limiter is safe for use by many
// goroutines.
type Limiter struct {
	slots chan struct{}
}

// NewLimiter returns a Limiter with MaxConcurrentRuns slots.
func NewLimiter() *Limiter {
	return limiterOf(MaxConcurrentRuns)
}

func limiterOf(n int) *Limiter {
	return &Limiter{slots: make(chan struct{}, n)}
}

// meter counts the work of one run. Only the goroutine of the virtual
// machine calls it. alive returns an error when the run must end, for example
// when its context ended.
type meter struct {
	alive   func() error
	left    int
	regexps map[string]*regexp.Regexp
}

func newMeter(alive func() error) *meter {
	return &meter{alive: alive, left: MaxWorkUnits, regexps: map[string]*regexp.Regexp{}}
}

// charge adds the work of a call that reads n bytes, weighted by units for
// each byte. It fails before the call when the run ended or the budget is
// spent.
func (m *meter) charge(n, units int) error {
	if err := m.alive(); err != nil {
		return fmt.Errorf("%w: %w", errAlive, err)
	}

	if n > m.left/units {
		return fmt.Errorf("%w: %d units, the budget is %d", errWork, MaxWorkUnits-m.left+n*units, MaxWorkUnits)
	}

	m.left -= n * units

	return nil
}

// strings reads the arguments of a call as strings and charges their bytes.
func (m *meter) strings(units int, args ...any) ([]string, error) {
	out := make([]string, len(args))
	total := 0

	for i, a := range args {
		s, ok := a.(string)
		if !ok {
			return nil, errNotString
		}

		out[i], total = s, total+len(s)
	}

	return out, m.charge(total, units)
}

// stringFunctions returns the options that give the string functions of
// catalog.ExprBuiltins that read a string, and the operators that read one,
// as functions that count their work in m.
func (m *meter) stringFunctions() []exprlang.Option {
	oneS := func(string) string { return "" }
	twoS := func(string, string) string { return "" }
	twoB := func(string, string) bool { return false }
	twoI := func(string, string) int { return 0 }

	str := func(f func(a []string) any) func(args ...any) (any, error) {
		return func(args ...any) (any, error) {
			ss, err := m.strings(1, args...)
			if err != nil {
				return nil, err
			}

			return f(ss), nil
		}
	}

	return []exprlang.Option{
		exprlang.Function("upper", str(func(a []string) any { return strings.ToUpper(a[0]) }), oneS),
		exprlang.Function("lower", str(func(a []string) any { return strings.ToLower(a[0]) }), oneS),
		exprlang.Function("trim", str(func(a []string) any {
			if len(a) == 1 {
				return strings.TrimSpace(a[0])
			}

			return strings.Trim(a[0], a[1])
		}), oneS, twoS),
		exprlang.Function("trimPrefix", str(func(a []string) any {
			return strings.TrimPrefix(a[0], affix(a))
		}), oneS, twoS),
		exprlang.Function("trimSuffix", str(func(a []string) any {
			return strings.TrimSuffix(a[0], affix(a))
		}), oneS, twoS),
		exprlang.Function("hasPrefix", str(func(a []string) any { return strings.HasPrefix(a[0], a[1]) }), twoB),
		exprlang.Function("hasSuffix", str(func(a []string) any { return strings.HasSuffix(a[0], a[1]) }), twoB),
		exprlang.Function("indexOf", str(func(a []string) any { return strings.Index(a[0], a[1]) }), twoI),
		exprlang.Function("lastIndexOf", str(func(a []string) any { return strings.LastIndex(a[0], a[1]) }), twoI),
		exprlang.Function(containsName, str(func(a []string) any { return strings.Contains(a[0], a[1]) }), twoB),
		exprlang.Function(startsWithName, str(func(a []string) any { return strings.HasPrefix(a[0], a[1]) }), twoB),
		exprlang.Function(endsWithName, str(func(a []string) any { return strings.HasSuffix(a[0], a[1]) }), twoB),
		exprlang.Function(matchesName, m.matches, twoB),
	}
}

// affix returns the second string of a call of trimPrefix or trimSuffix. With
// one string, the affix is a space, as in Expr.
func affix(a []string) string {
	if len(a) == twoArgs {
		return a[1]
	}

	return " "
}

// matches is the operator matches. The rewrite compiles each pattern, which is
// a string literal, into m.regexps; a pattern that is not there fails.
func (m *meter) matches(args ...any) (any, error) {
	if len(args) != twoArgs {
		return nil, errNotString
	}

	subject, ok := args[0].(string)
	pattern, okp := args[1].(string)

	if !ok || !okp {
		return nil, errNotString
	}

	re, found := m.regexps[pattern]
	if !found {
		return nil, errBadPattern
	}

	if err := m.charge(len(subject), MatchesUnitsPerByte); err != nil {
		return nil, err
	}

	return re.MatchString(subject), nil
}

// rewriter replaces the operators that read a string with calls of the
// functions of the meter. It runs after refusals, in the same pass.
type rewriter struct {
	regexps map[string]*regexp.Regexp
	failed  []string
}

// opMatches is the operator whose pattern the rewrite compiles.
const opMatches = "matches"

// operatorFunction returns the name of the function that replaces the
// operator op, or false for an operator that reads no string.
func operatorFunction(op string) (string, bool) {
	switch op {
	case opMatches:
		return matchesName, true
	case "contains":
		return containsName, true
	case "startsWith":
		return startsWithName, true
	case "endsWith":
		return endsWithName, true
	default:
		return "", false
	}
}

// Visit implements ast.Visitor.
func (r *rewriter) Visit(node nodeRef) {
	n, ok := (*node).(*ast.BinaryNode)
	if !ok {
		return
	}

	name, ok := operatorFunction(n.Operator)
	if !ok {
		return
	}

	if n.Operator == opMatches && !r.prepare(n.Right) {
		return
	}

	call := &ast.CallNode{Callee: &ast.IdentifierNode{Value: name}, Arguments: []ast.Node{n.Left, n.Right}}
	ast.Patch(node, call)
}

// twoArgs is the count of arguments of matches.
const twoArgs = 2

// isMetered reports whether name is a builtin of catalog.ExprBuiltins that the
// meter gives as a function that counts its work.
func isMetered(name string) bool {
	return slices.Contains([]string{
		"upper", "lower", "trim", "trimPrefix", "trimSuffix", "hasPrefix", "hasSuffix", "indexOf", "lastIndexOf",
	}, name)
}

// envSize counts the bytes of the strings and the items of an environment, and
// refuses a value that is not a JSON value.
type envSize struct {
	left int
	err  error
}

func (s *envSize) take(n int) bool {
	s.left -= n

	if s.left < 0 && s.err == nil {
		s.err = fmt.Errorf("%w: the limit is %d bytes", errEnvTooBig, MaxEnvBytes)
	}

	return s.err == nil
}

// value counts one JSON value, as encoding/json decodes it: nil, a boolean, a
// number (float64 or json.Number), a string, a list or an object. A value of
// another type is refused, because its size is not known: the size of a
// []string of 1 MiB must not count as one item.
func (s *envSize) value(v any) bool {
	if !s.take(1) {
		return false
	}

	switch x := v.(type) {
	case nil, bool, float64, json.Number:
		return true
	case string:
		return s.take(len(x))
	case []any:
		return s.list(x)
	case map[string]any:
		return s.object(x)
	default:
		if s.err == nil {
			s.err = fmt.Errorf("%w: %T is not a JSON value", errEnvType, v)
		}

		return false
	}
}

func (s *envSize) list(items []any) bool {
	for _, item := range items {
		if !s.value(item) {
			return false
		}
	}

	return true
}

func (s *envSize) object(fields map[string]any) bool {
	for k, item := range fields {
		if !s.take(len(k)) || !s.value(item) {
			return false
		}
	}

	return true
}

// checkEnvSize refuses an environment with more than MaxEnvBytes bytes of
// strings and items, and a value that is not a JSON value.
func checkEnvSize(env *catalog.Env) error {
	s := &envSize{left: MaxEnvBytes, err: nil}
	req := &env.Request

	ok := s.value(env.Value) && s.value(env.Input) && s.object(env.Inputs) &&
		s.take(len(req.Requester.Subject)+len(req.Requester.Agent)+len(req.Namespace)+len(req.Name))

	for k := range env.Usage {
		ok = ok && s.take(len(k))
	}

	for _, g := range req.Requester.Groups {
		ok = ok && s.take(len(g)+1)
	}

	if !ok {
		return s.err
	}

	return nil
}

// MaxErrorBytes bounds the text of the error of a failed run. The library
// quotes values of the environment in its messages, and a value can be large.
const MaxErrorBytes = 256

// clipped is an error with a message of at most MaxErrorBytes bytes. It
// unwraps to the error that it clips, so errors.Is still finds the cause.
type clipped struct {
	msg string
	err error
}

func (c *clipped) Error() string { return c.msg }

func (c *clipped) Unwrap() error { return c.err }

// clip returns err with a message of at most MaxErrorBytes bytes, cut at a
// character boundary. An error with a short message is returned as it is.
func clip(err error) error {
	msg := err.Error()
	if len(msg) <= MaxErrorBytes {
		return err
	}

	msg = strings.ToValidUTF8(msg[:MaxErrorBytes], "")

	return &clipped{msg: msg + "...", err: err}
}

// prepare compiles the pattern of a matches into r.regexps. It returns false
// for a pattern that is not a string literal (refusals reports it) and for a
// pattern that does not compile (r.failed holds the reason).
func (r *rewriter) prepare(pattern ast.Node) bool {
	lit, ok := pattern.(*ast.StringNode)
	if !ok {
		return false
	}

	if _, done := r.regexps[lit.Value]; done {
		return true
	}

	re, err := regexp.Compile(lit.Value)
	if err != nil {
		r.failed = append(r.failed, errBadPattern.Error())

		return false
	}

	r.regexps[lit.Value] = re

	return true
}
