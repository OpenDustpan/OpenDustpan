package expr_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/expr"
	"opendustpan.org/opendustpan/internal/selfservice"
)

func hostileEnv() *selfservice.Env {
	return &selfservice.Env{
		Value:  "x",
		Inputs: map[string]any{"s": "abc", "tags": []any{"a", "b"}},
		Usage:  map[string]int{"units": 1},
		Input:  nil,
		Request: selfservice.RequestEnv{
			Requester: selfservice.Requester{Subject: "ana", Agent: "", Groups: []string{"dev"}},
			Namespace: "payments",
			Name:      "orders-db",
		},
	}
}

func TestVersion(t *testing.T) {
	t.Parallel()

	if got := newEvaluator(t).Version(); !regexp.MustCompile(`^v\d+\.\d+\.\d+$`).MatchString(got) {
		t.Fatalf("Version() = %q, want a release such as v1.17.8", got)
	}
}

// TestSizeLayer sends text that is too large or too deep to the size limits.
func TestSizeLayer(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		want   error
	}{
		{"empty", "", selfservice.ErrLimit},
		{"huge string literal", "'" + strings.Repeat("a", 1<<20) + "' == ''", selfservice.ErrLimit},
		{"one byte over", strings.Repeat(" ", selfservice.MaxExprBytes) + "true", selfservice.ErrLimit},
		{"deep unary", strings.Repeat("!", 2000) + "true", selfservice.ErrLimit},
		{"deep array", strings.Repeat("[", 1000) + strings.Repeat("]", 1000) + " == nil", selfservice.ErrLimit},
		{"deep call", strings.Repeat("len(", 500) + "inputs" + strings.Repeat(")", 500) + " > 0", selfservice.ErrLimit},
		{
			"deep ternary",
			strings.Repeat("true ? ", 600) + "true" + strings.Repeat(" : false", 600),
			selfservice.ErrLimit,
		},
		{"many terms", strings.Repeat("1 + ", 300) + "1 > 0", selfservice.ErrLimit},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if err := newEvaluator(t).Compile(tc.source, selfservice.ResultBool); !errors.Is(err, tc.want) {
				t.Fatalf("Compile() = %v, want %v", err, tc.want)
			}

			got, err := newEvaluator(t).Bool(t.Context(), tc.source, hostileEnv())
			if got || !errors.Is(err, selfservice.ErrRuleFailed) {
				t.Fatalf("Bool() = %v, %v, want false, ErrRuleFailed", got, err)
			}
		})
	}
}

// TestNameLayer sends calls of code of the server, of Go and of the process.
func TestNameLayer(t *testing.T) {
	t.Parallel()

	tests := []struct{ name, source string }{
		{"method of a string", "request.name.String() == ''"},
		{"method of the environment", "$env.Value.Error() == ''"},
		{"go field name", "request.Requester.Subject == 'ana'"},
		{"go type name", "selfservice.Env != nil"},
		{"reflect", "reflect.TypeOf(value) != nil"},
		{"os getenv", "os.Getenv('OPENDUSTPAN_EXPR_TEST') != ''"},
		{"env function", "env('OPENDUSTPAN_EXPR_TEST') != ''"},
		{"getenv function", "getenv('OPENDUSTPAN_EXPR_TEST') != ''"},
		{"fmt", "fmt.Sprintf('%v', value) != ''"},
		{"exec", "exec('id') != ''"},
		{"read a file", "readFile('/etc/passwd') != ''"},
		{"now", "now() != nil"},
		{"date", "date('2026-01-01') != nil"},
		{"timezone", "timezone('UTC') != nil"},
		{"duration", "duration('1s') != nil"},
		{"keys", "len(keys(inputs)) > 0"},
		{"values", "len(values(inputs)) > 0"},
		{"to pairs", "len(toPairs(inputs)) > 0"},
		{"join", "join(inputs.tags, ',') != ''"},
		{"repeat", "len(repeat('a', 1000000)) > 0"},
		{"replace", "replace('a', 'a', 'bb') != ''"},
		{"string", "string(1) != ''"},
		{"to JSON", "toJSON(inputs) != ''"},
		{"type", "type(1) == 'int'"},
		{"reduce", "reduce(inputs.tags, # + #acc, '') != ''"},
		{"sort by", "len(sortBy(inputs.tags, #)) > 0"},
		{"group by", "len(groupBy(inputs.tags, #)) > 0"},
		{"reduce in a pipe", "inputs.tags | reduce(# + #acc, '') != ''"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := newEvaluator(t).Compile(tc.source, selfservice.ResultBool)
			if !errors.Is(err, selfservice.ErrInvalid) {
				t.Fatalf("Compile() = %v, want ErrInvalid", err)
			}

			got, err := newEvaluator(t).Bool(t.Context(), tc.source, hostileEnv())
			if got || !errors.Is(err, selfservice.ErrRuleFailed) {
				t.Fatalf("Bool() = %v, %v, want false, ErrRuleFailed", got, err)
			}
		})
	}
}

// TestNameLayerRunsNoMethod checks a call that the compiler cannot refuse,
// because the value is a JSON value of unknown type. The run fails.
func TestNameLayerRunsNoMethod(t *testing.T) {
	t.Parallel()

	for _, source := range []string{"inputs.Len() > 0", "inputs.s.Len() > 0", "inputs.tags.Len() > 0"} {
		got, err := newEvaluator(t).Bool(t.Context(), source, hostileEnv())
		if got || !errors.Is(err, selfservice.ErrRuleFailed) {
			t.Fatalf("Bool(%q) = %v, %v, want false, ErrRuleFailed", source, got, err)
		}
	}
}

// TestNameLayerGivesNoEnvironment checks that no expression can read a
// variable of the process, whatever the name.
func TestNameLayerGivesNoEnvironment(t *testing.T) {
	t.Setenv("OPENDUSTPAN_EXPR_TEST", "secret-value")

	if _, ok := os.LookupEnv("OPENDUSTPAN_EXPR_TEST"); !ok {
		t.Fatal("the variable is not set")
	}

	for _, source := range []string{
		"$env['OPENDUSTPAN_EXPR_TEST'] == 'secret-value'",
		"inputs['OPENDUSTPAN_EXPR_TEST'] == 'secret-value'",
		"OPENDUSTPAN_EXPR_TEST == 'secret-value'",
	} {
		got, err := newEvaluator(t).Bool(t.Context(), source, hostileEnv())
		if got {
			t.Fatalf("Bool(%q) = true, want false: the expression read the process environment", source)
		}

		if err != nil && !errors.Is(err, selfservice.ErrRuleFailed) {
			t.Fatalf("Bool(%q) = %v, want nil or ErrRuleFailed", source, err)
		}
	}
}

// TestSyntaxLayer sends the syntax that the engine refuses.
func TestSyntaxLayer(t *testing.T) {
	t.Parallel()

	tests := []struct{ name, source string }{
		{"let", "let x = 1; x == 1"},
		{"let chain", "let a = 'aaaaaaaa'; let b = a + a; let c = b + b; len(c) > 0"},
		{"range", "len(1..100000000) > 0"},
		{"range in a closure", "all(1..10, # > 0)"},
		{"closure in a closure", "all(inputs.tags, any(inputs.tags, # != ''))"},
		{"closure in a closure of a map", "len(map(inputs.tags, filter(inputs.tags, true))) > 0"},
		{"pattern from the environment", "request.name matches inputs.s"},
		{"pattern from a call", "request.name matches upper('a')"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := newEvaluator(t).Compile(tc.source, selfservice.ResultBool)
			if !errors.Is(err, selfservice.ErrInvalid) {
				t.Fatalf("Compile() = %v, want ErrInvalid", err)
			}

			got, err := newEvaluator(t).Bool(t.Context(), tc.source, hostileEnv())
			if got || !errors.Is(err, selfservice.ErrRuleFailed) {
				t.Fatalf("Bool() = %v, %v, want false, ErrRuleFailed", got, err)
			}
		})
	}
}

// TestMemoryLayer sends loops and lists that grow past the memory budget.
func TestMemoryLayer(t *testing.T) {
	t.Parallel()

	env := hostileEnv()
	env.Request.Requester.Groups = make([]string, selfservice.MaxEnvGroups)

	tests := []struct{ name, source string }{
		{"map of a map", "len(map(map(request.requester.groups, #), #)) > 0"},
		{"map and filter", "len(map(request.requester.groups, #)) + len(filter(request.requester.groups, true)) > 0"},
		{"list literals in a loop", "len(map(request.requester.groups, [1, 2, 3])) > 0"},
		{"list literals in a loop 2", "len(map(request.requester.groups, [#, #, #, #, #, #, #, #, #, #])) > 0"},
		{"many lists", strings.Repeat("len(map(request.requester.groups, #)) > 0 && ", 3) + "true"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := newEvaluator(t).Bool(t.Context(), tc.source, env)
			if got || !errors.Is(err, selfservice.ErrRuleFailed) {
				t.Fatalf("Bool() = %v, %v, want false, ErrRuleFailed", got, err)
			}
		})
	}
}

// TestConcatLayer sends strings that grow with the operator +.
func TestConcatLayer(t *testing.T) {
	t.Parallel()

	env := hostileEnv()
	env.Inputs["half"] = strings.Repeat("a", selfservice.MaxConcatBytes/2+1)
	env.Inputs["huge"] = strings.Repeat("a", 1<<20)

	tests := []struct{ name, source string }{
		{"two halves", "inputs.half + inputs.half"},
		{"a huge string", "inputs.huge + ''"},
		{"a doubling chain", "inputs.half + inputs.half + inputs.half + inputs.half"},
		{"upper of a huge string", "upper(inputs.huge) + 'a'"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := newEvaluator(t).String(t.Context(), tc.source, env)
			if got != "" || !errors.Is(err, selfservice.ErrRuleFailed) {
				t.Fatalf("String() = %d bytes, %v, want empty, ErrRuleFailed", len(got), err)
			}
		})
	}
}

// TestResultLayer checks that the result must have the type of the kind and
// that a failure is never true.
func TestResultLayer(t *testing.T) {
	t.Parallel()

	env := hostileEnv()

	tests := []struct{ name, source string }{
		{"nil", "inputs.nothing"},
		{"number for a boolean", "inputs.n"},
		{"string for a boolean", "inputs.s"},
	}

	env.Inputs["n"] = float64(1)

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := newEvaluator(t).Bool(t.Context(), tc.source, env)
			if got || !errors.Is(err, selfservice.ErrRuleFailed) {
				t.Fatalf("Bool() = %v, %v, want false, ErrRuleFailed", got, err)
			}
		})
	}
}

// TestGroupsLayer checks the bound of the requester groups at the evaluator
// itself, apart from any check of the caller.
func TestGroupsLayer(t *testing.T) {
	t.Parallel()

	env := hostileEnv()
	env.Request.Requester.Groups = make([]string, selfservice.MaxEnvGroups+1)

	got, err := newEvaluator(t).Bool(t.Context(), "true", env)
	if got || !errors.Is(err, selfservice.ErrRuleFailed) {
		t.Fatalf("Bool() = %v, %v, want false, ErrRuleFailed", got, err)
	}
}

// TestFromLoopsWithinTheLimits checks that the slowest rules that the limits
// allow still run: 1,000 groups in a closure, and a regular expression.
// It uses the default evaluator, with its default time limit, because the
// test is about that limit. A loaded machine (the race detector, many CPUs)
// can stop one run at the time limit, so each source gets up to three runs
// and the test passes on the first run that returns true. A source that the
// limits refuse fails all three runs.
func TestFromLoopsWithinTheLimits(t *testing.T) {
	t.Parallel()

	const attempts = 3

	env := hostileEnv()
	env.Request.Requester.Groups = make([]string, selfservice.MaxEnvGroups-1)
	env.Inputs["long"] = strings.Repeat("a", 1<<10)

	for _, source := range []string{
		"all(request.requester.groups, # == '')",
		"count(request.requester.groups, # == '') == 999",
		"inputs.long matches '^(a|aa)+$'",
	} {
		var (
			got bool
			err error
		)

		for range attempts {
			got, err = newEvaluator(t).Bool(t.Context(), source, env)
			if err == nil && got {
				break
			}
		}

		if err != nil || !got {
			t.Fatalf("Bool(%q) = %v, %v after %d runs, want true", source, got, err, attempts)
		}
	}
}

// reviewerBig is the string of the expressions that a review found: a
// regular expression, read once for each group.
func reviewerEnv() *selfservice.Env {
	env := hostileEnv()
	env.Inputs["big"] = strings.Repeat("ab", 8<<10)
	env.Request.Requester.Groups = make([]string, selfservice.MaxEnvGroups)

	return env
}

// TestWorkLayerReviewerExpressions checks that an expression that reads a
// long string in each turn of a loop over the groups ends at the work budget.
// The time limit is a minute, so it cannot end the run first. The first
// expression ran for 5.4 s before the budget.
func TestWorkLayerReviewerExpressions(t *testing.T) {
	t.Parallel()

	const clause = "none(request.requester.groups, {inputs.big matches '^(a|b|ab|ba)*c$'})"

	tests := []struct{ name, source string }{
		{"one clause", clause},
		{"nine clauses", strings.TrimSuffix(strings.Repeat(clause+" && ", 9), " && ")},
		{"contains in a loop", "none(request.requester.groups, inputs.big contains 'c')"},
		{"starts with in a loop", "none(request.requester.groups, inputs.big startsWith 'c')"},
		{"ends with in a loop", "none(request.requester.groups, inputs.big endsWith 'c')"},
		{"lower in a loop", "all(request.requester.groups, lower(inputs.big) != '')"},
		{"index of in a loop", "all(request.requester.groups, indexOf(inputs.big, 'c') < 0)"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			e, err := expr.NewWithTimeout(expr.NewLimiter(), time.Minute)
			if err != nil {
				t.Fatalf("NewWithTimeout() = %v", err)
			}

			got, err := e.Bool(t.Context(), tc.source, reviewerEnv())
			if got || !errors.Is(err, selfservice.ErrRuleFailed) || !errors.Is(err, expr.ErrWork) {
				t.Fatalf("Bool() = %v, %v, want false, ErrRuleFailed from the work budget", got, err)
			}

			if errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("Bool() = %v, want the work budget and not the time limit", err)
			}
		})
	}
}

// TestEnvLayer sends an environment above the limit straight to the run.
func TestEnvLayer(t *testing.T) {
	t.Parallel()

	many := make([]any, 0, 200_000)
	for range 200_000 {
		many = append(many, "x")
	}

	tests := []struct {
		name string
		edit func(env *selfservice.Env)
	}{
		{"a long string", func(env *selfservice.Env) { env.Inputs["big"] = strings.Repeat("a", 1<<20) }},
		{"many items", func(env *selfservice.Env) { env.Inputs["many"] = many }},
		{"a long value", func(env *selfservice.Env) { env.Value = strings.Repeat("a", 1<<20) }},
		{"a long input", func(env *selfservice.Env) { env.Input = map[string]any{"k": strings.Repeat("a", 1<<20)} }},
		{"a long name", func(env *selfservice.Env) { env.Request.Name = strings.Repeat("a", 1<<20) }},
		{"long group names", func(env *selfservice.Env) {
			env.Request.Requester.Groups = slices.Repeat([]string{strings.Repeat("g", 1<<10)}, selfservice.MaxEnvGroups)
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			env := hostileEnv()
			tc.edit(env)

			got, err := newEvaluator(t).Bool(t.Context(), "true", env)
			if got || !errors.Is(err, selfservice.ErrRuleFailed) {
				t.Fatalf("Bool() = %v, %v, want false, ErrRuleFailed", got, err)
			}
		})
	}
}

// TestOperatorNamesCannotBeWritten checks that no expression can call the
// functions that replace the operators, with a pattern of its own.
func TestOperatorNamesCannotBeWritten(t *testing.T) {
	t.Parallel()

	for _, source := range []string{
		"concat('a', 'b') == 'ab'",
		"dustpan matches(request.name, inputs.s)",
		`"dustpan matches"(request.name, inputs.s)`,
		"`dustpan matches`(request.name, inputs.s)",
		"matches(request.name, inputs.s)",
		"request.name matches '('",
	} {
		if err := newEvaluator(t).Compile(source, selfservice.ResultBool); !errors.Is(err, selfservice.ErrInvalid) {
			t.Fatalf("Compile(%q) = %v, want ErrInvalid", source, err)
		}
	}
}

// TestEnvTypeLayer sends values that are not JSON values, whose size the
// check of the environment cannot know unless it refuses them.
func TestEnvTypeLayer(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value any
	}{
		{"1 MiB list of strings", make([]string, 1<<20)},
		{"a list of 1 MiB strings", [][]byte{make([]byte, 1<<20)}},
		{"a map of strings", map[string]string{"k": strings.Repeat("a", 1<<20)}},
		{"a list of integers", make([]int, 1<<20)},
		{"a byte slice", make([]byte, 1<<20)},
		{"an integer", 7},
		{"a pointer", new(string)},
		{"a struct", struct{ S string }{S: strings.Repeat("a", 1<<20)}},
		{"a function", func() {}},
		{"a list that holds a typed list", []any{make([]string, 1<<20)}},
		{"an object that holds a typed object", map[string]any{"k": map[string]string{"k": "v"}}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			for place, edit := range map[string]func(env *selfservice.Env){
				"inputs": func(env *selfservice.Env) { env.Inputs["v"] = tc.value },
				"value":  func(env *selfservice.Env) { env.Value = tc.value },
				"input":  func(env *selfservice.Env) { env.Input = tc.value },
			} {
				env := hostileEnv()
				edit(env)

				got, err := newEvaluator(t).Bool(t.Context(), "true", env)
				if got || !errors.Is(err, selfservice.ErrRuleFailed) {
					t.Fatalf("%s: Bool() = %v, %v, want false, ErrRuleFailed", place, got, err)
				}
			}
		})
	}
}

// TestEnvTypesAreAccepted checks the JSON types that the check allows.
func TestEnvTypesAreAccepted(t *testing.T) {
	t.Parallel()

	env := hostileEnv()
	env.Value = nil
	env.Input = map[string]any{"n": json.Number("5"), "f": float64(1.5), "b": true, "z": nil, "l": []any{"a", 1.0}}
	env.Inputs["list"] = []any{map[string]any{"k": "v"}}

	got, err := newPatientEvaluator(t).Bool(t.Context(), "input.b && len(inputs.list) == 1", env)
	if err != nil || !got {
		t.Fatalf("Bool() = %v, %v, want true", got, err)
	}
}

// TestRunErrorIsShort checks that the error of a failed run never holds a
// whole value of the environment, which the library quotes.
func TestRunErrorIsShort(t *testing.T) {
	t.Parallel()

	env := hostileEnv()
	env.Inputs["s"] = strings.Repeat("Z", 100_000)

	got, err := newEvaluator(t).Bool(t.Context(), "int(inputs.s) > 0", env)
	if got || !errors.Is(err, selfservice.ErrRuleFailed) {
		t.Fatalf("Bool() = %v, %v, want false, ErrRuleFailed", got, err)
	}

	if n := len(err.Error()); n > 300 || strings.Contains(err.Error(), strings.Repeat("Z", 300)) {
		t.Fatalf("Bool() error has %d bytes, want at most 300 and no long part of the value", n)
	}
}

// newEvaluator returns an Evaluator with the default time limit and a Limiter
// of its own, so that parallel tests do not share a budget.
func newEvaluator(t *testing.T) *expr.Evaluator {
	t.Helper()

	e, err := expr.New(expr.NewLimiter())
	if err != nil {
		t.Fatalf("New() = %v", err)
	}

	return e
}

// newPatientEvaluator returns an Evaluator with a time limit of one minute and
// a Limiter of its own. A test that expects a result uses it, so that a slow
// host and the race detector cannot end a valid run at the default limit.
func newPatientEvaluator(t *testing.T) *expr.Evaluator {
	t.Helper()

	e, err := expr.NewWithTimeout(expr.NewLimiter(), time.Minute)
	if err != nil {
		t.Fatalf("NewWithTimeout() = %v", err)
	}

	return e
}
