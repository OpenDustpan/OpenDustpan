package expr

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	exprlang "github.com/expr-lang/expr"
	"github.com/expr-lang/expr/vm"

	"opendustpan.org/opendustpan/internal/selfservice"
	"opendustpan.org/opendustpan/pkg/profile"
)

var _ profile.Evaluator = (*Evaluator)(nil)

// valueEnvKey is the only name of the environment of a profile expression.
const valueEnvKey = "values"

// forbiddenValueNames are the expression roots that read outside values.
// values.<name> still reads a values key called that: the check skips a name
// after a dot or a quoted index.
var forbiddenValueNames = regexp.MustCompile(`(^|[^.\w'"])` + `(providers|imports|profiles|context)` + `\b`)

// valueIdent is one name in a path under values, as in document_taint.go of
// pkg/profile: a letter, underscore or dollar sign, then letters, digits,
// underscores or dollar signs.
const valueIdent = `[\p{L}_$][\p{L}\p{Nd}_$]*`

var (
	valueRef   = regexp.MustCompile(`\bvalues((?:\.` + valueIdent + `|\[\s*"[^"\\]*"\s*\]|\[\s*'[^'\\]*'\s*\])*)`)
	valueSeg   = regexp.MustCompile(`\.(` + valueIdent + `)|\[\s*"([^"\\]*)"\s*\]|\[\s*'([^'\\]*)'\s*\]`)
	valueEnvRx = regexp.MustCompile(`\$env\b`)
)

// Eval implements profile.Evaluator: it computes source against the merged
// plain values. The environment holds values only.
func (e *Evaluator) Eval(ctx context.Context, source string, values map[string]any) (profile.Evaluation, error) {
	if err := checkValueSource(source); err != nil {
		return profile.Evaluation{}, err
	}

	if err := ctx.Err(); err != nil {
		return profile.Evaluation{}, fmt.Errorf("%w: %w", profile.ErrReference, err)
	}

	env := map[string]any{valueEnvKey: values}

	prog, err := compileValues(source, env, newMeter(ctx.Err))
	if err != nil {
		return profile.Evaluation{}, err
	}

	ctx, cancel := context.WithTimeout(ctx, e.timeout)
	defer cancel()

	select {
	case e.limiter.slots <- struct{}{}:
	default:
		return profile.Evaluation{}, fmt.Errorf("%w: no run is free", profile.ErrReference)
	}

	// The virtual machine of Expr has no way to stop a run from outside. The
	// channel has room for the result, so the goroutine always ends: at the
	// end of the program, or at its next counted call after the time limit
	// (see the package comment). It gives its slot back when it ends, not
	// when the caller stops waiting.
	done := make(chan runResult, 1)

	go func() {
		defer func() { <-e.limiter.slots }()

		var out any

		var runErr error

		func() {
			defer func() {
				if r := recover(); r != nil {
					runErr = fmt.Errorf("%w: %v", profile.ErrReference, r)
				}
			}()

			machine := vm.VM{
				Stack:        nil,
				Scopes:       nil,
				Variables:    nil,
				MemoryBudget: selfservice.ExprMemoryBudget,
			}
			out, runErr = machine.Run(prog, env)
		}()

		done <- runResult{out: out, err: runErr}
	}()

	select {
	case r := <-done:
		if err := ctx.Err(); err != nil {
			return profile.Evaluation{}, fmt.Errorf("%w: %w", profile.ErrReference, err)
		}

		if r.err != nil {
			return profile.Evaluation{}, fmt.Errorf("%w: %s", profile.ErrReference, firstLine(clip(r.err).Error()))
		}

		return profile.Evaluation{Value: r.out, Kind: profile.EvalKindOf(r.out), Paths: valuePaths(source)}, nil
	case <-ctx.Done():
		return profile.Evaluation{}, fmt.Errorf("%w: %w", profile.ErrReference, ctx.Err())
	}
}

// checkValueSource refuses an empty or oversize source and the names that
// read outside values. A name after a dot or a quoted index still reads a
// values key: values.providers is a key called providers. The check reads the
// source with its quoted strings blanked: text in quotes names no name.
func checkValueSource(source string) error {
	if source == "" || len(source) > selfservice.MaxExprBytes {
		return fmt.Errorf("%w: an expression has 1 to %d bytes, this one has %d",
			profile.ErrLimit, selfservice.MaxExprBytes, len(source))
	}

	bare := blankQuoted(source)

	if valueEnvRx.MatchString(bare) {
		return fmt.Errorf("%w: $env is not a name of an open: the environment holds values only",
			profile.ErrReference)
	}

	if m := forbiddenValueNames.FindStringSubmatch(bare); m != nil {
		return fmt.Errorf("%w: %s is not a name of an open: the environment holds values only",
			profile.ErrReference, m[2])
	}

	return nil
}

// blankQuoted blanks the quoted strings of an expression with spaces. A
// quoted span that is an index key (after "["), such as values["db-host"],
// stays: it names the key that the expression reads.
func blankQuoted(source string) string {
	var sb strings.Builder

	sb.Grow(len(source))

	scan := quoteScan{quote: 0, escaped: false, index: false}

	for i := range len(source) {
		scan.step(source[i], &sb)
	}

	return sb.String()
}

// quoteScan holds the quoted-string state of blankQuoted.
type quoteScan struct {
	quote   byte
	escaped bool
	// index reports a "[" as the last byte outside a string: the next quoted
	// span is an index key.
	index bool
}

// step writes one byte of source to sb, blanking quoted text that is not an
// index key.
func (q *quoteScan) step(c byte, sb *strings.Builder) {
	if q.quote != 0 {
		q.stepInside(c, sb)

		return
	}

	q.stepOutside(c, sb)
}

// stepInside writes one byte inside a quoted span.
func (q *quoteScan) stepInside(c byte, sb *strings.Builder) {
	if q.index {
		sb.WriteByte(c)
	} else {
		sb.WriteByte(' ')
	}

	q.quote, q.escaped = advanceQuote(q.quote, q.escaped, c)
}

// stepOutside writes one byte outside a quoted span.
func (q *quoteScan) stepOutside(c byte, sb *strings.Builder) {
	switch c {
	case '"', '\'', '`':
		q.quote, q.escaped = c, false

		if q.index {
			sb.WriteByte(c)
		} else {
			sb.WriteByte(' ')
		}
	default:
		sb.WriteByte(c)

		if c != ' ' && c != '\t' && c != '\n' && c != '\r' {
			q.index = c == '['
		}
	}
}

// advanceQuote moves the quoted-string state past c: it answers the quote
// and escaped values after c.
func advanceQuote(quote byte, escaped bool, c byte) (byte, bool) {
	switch {
	case escaped:
		return quote, false
	case c == '\\':
		return quote, true
	case c == quote:
		return 0, false
	default:
		return quote, false
	}
}

// compileValues compiles source with the environment env. It checks the
// limits and the syntax, as compile does. An unknown name fails with
// profile.ErrReference, because the environment holds values only. Any other
// failure fails with profile.ErrReference too: an expression that does not
// compute never opens. A tree above the node limit fails with
// profile.ErrLimit.
func compileValues(source string, env map[string]any, m *meter) (*vm.Program, error) {
	options := []exprlang.Option{
		exprlang.Env(env),
		exprlang.MaxNodes(selfservice.MaxExprNodes),
		exprlang.AsAny(),
		exprlang.DisableAllBuiltins(),
		exprlang.Function(concatName, concat, concatTypes()...),
		exprlang.Operator("+", concatName),
	}

	options = append(options, m.stringFunctions()...)

	for _, name := range selfservice.ExprBuiltins() {
		// The meter gives these as functions that count their work.
		if !isMetered(name) {
			options = append(options, exprlang.EnableBuiltin(name))
		}
	}

	refused := &refusals{found: nil}
	rewrite := &rewriter{regexps: m.regexps, failed: nil}
	options = append(options, exprlang.Patch(refused), exprlang.Patch(rewrite))

	prog, err := safeCompile(source, options)

	if found := append(append([]string(nil), refused.found...), rewrite.failed...); len(found) > 0 {
		return nil, fmt.Errorf("%w: %s", profile.ErrReference, found[0])
	}

	if err != nil {
		return nil, classifyValueErr(err)
	}

	return prog, nil
}

// classifyValueErr turns a compile error into ErrLimit for the node limit
// and ErrReference for the rest: a name the environment does not hold, and
// any other failure, never opens.
func classifyValueErr(err error) error {
	if isNodeLimit(err.Error()) {
		return fmt.Errorf("%w: an expression has at most %d nodes", profile.ErrLimit, selfservice.MaxExprNodes)
	}

	return fmt.Errorf("%w: %s", profile.ErrReference, firstLine(err.Error()))
}

// valuePaths returns the paths under values that the expressions in source
// read, each as segments. A computed index stops the path, so the path then
// names the whole subtree. It mirrors refsInText of pkg/profile: the open
// carries the marks of each path that it names. Source is the inner
// expression, not template text, so it is scanned directly.
func valuePaths(source string) [][]string {
	matches := valueRef.FindAllStringSubmatch(source, -1)
	out := make([][]string, 0, len(matches))

	for _, m := range matches {
		parts := valueSeg.FindAllStringSubmatch(m[1], -1)
		segs := make([]string, 0, len(parts))

		for _, s := range parts {
			segs = append(segs, s[1]+s[2]+s[3])
		}

		out = append(out, segs)
	}

	return out
}
