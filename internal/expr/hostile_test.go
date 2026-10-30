package expr_test

import (
	"errors"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/catalog"
	"opendustpan.org/opendustpan/internal/expr"
)

func hostileEnv() *catalog.Env {
	return &catalog.Env{
		Value:  "x",
		Inputs: map[string]any{"s": "abc", "tags": []any{"a", "b"}},
		Usage:  map[string]int{"units": 1},
		Input:  nil,
		Request: catalog.RequestEnv{
			Requester: catalog.Requester{Subject: "ana", Agent: "", Groups: []string{"dev"}},
			Namespace: "payments",
			Name:      "orders-db",
		},
	}
}

func TestVersion(t *testing.T) {
	t.Parallel()

	if got := expr.New().Version(); !regexp.MustCompile(`^v\d+\.\d+\.\d+$`).MatchString(got) {
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
		{"empty", "", catalog.ErrLimit},
		{"huge string literal", "'" + strings.Repeat("a", 1<<20) + "' == ''", catalog.ErrLimit},
		{"one byte over", strings.Repeat(" ", catalog.MaxExprBytes) + "true", catalog.ErrLimit},
		{"deep unary", strings.Repeat("!", 2000) + "true", catalog.ErrLimit},
		{"deep array", strings.Repeat("[", 1000) + strings.Repeat("]", 1000) + " == nil", catalog.ErrLimit},
		{"deep call", strings.Repeat("len(", 500) + "inputs" + strings.Repeat(")", 500) + " > 0", catalog.ErrLimit},
		{"deep ternary", strings.Repeat("true ? ", 600) + "true" + strings.Repeat(" : false", 600), catalog.ErrLimit},
		{"many terms", strings.Repeat("1 + ", 300) + "1 > 0", catalog.ErrLimit},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if err := expr.New().Compile(tc.source, catalog.ResultBool); !errors.Is(err, tc.want) {
				t.Fatalf("Compile() = %v, want %v", err, tc.want)
			}

			got, err := expr.New().Bool(t.Context(), tc.source, hostileEnv())
			if got || !errors.Is(err, catalog.ErrRuleFailed) {
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
		{"go type name", "catalog.Env != nil"},
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

			if err := expr.New().Compile(tc.source, catalog.ResultBool); !errors.Is(err, catalog.ErrInvalid) {
				t.Fatalf("Compile() = %v, want ErrInvalid", err)
			}

			got, err := expr.New().Bool(t.Context(), tc.source, hostileEnv())
			if got || !errors.Is(err, catalog.ErrRuleFailed) {
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
		got, err := expr.New().Bool(t.Context(), source, hostileEnv())
		if got || !errors.Is(err, catalog.ErrRuleFailed) {
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
		got, err := expr.New().Bool(t.Context(), source, hostileEnv())
		if got {
			t.Fatalf("Bool(%q) = true, want false: the expression read the process environment", source)
		}

		if err != nil && !errors.Is(err, catalog.ErrRuleFailed) {
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

			if err := expr.New().Compile(tc.source, catalog.ResultBool); !errors.Is(err, catalog.ErrInvalid) {
				t.Fatalf("Compile() = %v, want ErrInvalid", err)
			}

			got, err := expr.New().Bool(t.Context(), tc.source, hostileEnv())
			if got || !errors.Is(err, catalog.ErrRuleFailed) {
				t.Fatalf("Bool() = %v, %v, want false, ErrRuleFailed", got, err)
			}
		})
	}
}

// TestMemoryLayer sends loops and lists that grow past the memory budget.
func TestMemoryLayer(t *testing.T) {
	t.Parallel()

	env := hostileEnv()
	env.Request.Requester.Groups = make([]string, catalog.MaxEnvGroups)

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

			got, err := expr.New().Bool(t.Context(), tc.source, env)
			if got || !errors.Is(err, catalog.ErrRuleFailed) {
				t.Fatalf("Bool() = %v, %v, want false, ErrRuleFailed", got, err)
			}
		})
	}
}

// TestConcatLayer sends strings that grow with the operator +.
func TestConcatLayer(t *testing.T) {
	t.Parallel()

	env := hostileEnv()
	env.Inputs["half"] = strings.Repeat("a", catalog.MaxConcatBytes/2+1)
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

			got, err := expr.New().String(t.Context(), tc.source, env)
			if got != "" || !errors.Is(err, catalog.ErrRuleFailed) {
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

			got, err := expr.New().Bool(t.Context(), tc.source, env)
			if got || !errors.Is(err, catalog.ErrRuleFailed) {
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
	env.Request.Requester.Groups = make([]string, catalog.MaxEnvGroups+1)

	got, err := expr.New().Bool(t.Context(), "true", env)
	if got || !errors.Is(err, catalog.ErrRuleFailed) {
		t.Fatalf("Bool() = %v, %v, want false, ErrRuleFailed", got, err)
	}
}

// TestFromLoopsWithinTheLimits checks that the slowest rules that the limits
// allow still run: 1,000 groups in a closure, and a regular expression.
func TestFromLoopsWithinTheLimits(t *testing.T) {
	t.Parallel()

	env := hostileEnv()
	env.Request.Requester.Groups = make([]string, catalog.MaxEnvGroups-1)
	env.Inputs["long"] = strings.Repeat("a", 1<<10)

	for _, source := range []string{
		"all(request.requester.groups, # == '')",
		"count(request.requester.groups, # == '') == 999",
		"inputs.long matches '^(a|aa)+$'",
	} {
		got, err := expr.New().Bool(t.Context(), source, env)
		if err != nil || !got {
			t.Fatalf("Bool(%q) = %v, %v, want true", source, got, err)
		}
	}
}

// reviewerBig is the string of the expressions that a review found: a
// regular expression, read once for each group.
func reviewerEnv() *catalog.Env {
	env := hostileEnv()
	env.Inputs["big"] = strings.Repeat("ab", 8<<10)
	env.Request.Requester.Groups = make([]string, catalog.MaxEnvGroups)

	return env
}

// TestWorkLayerReviewerExpressions checks that an expression that reads a
// long string in each turn of a loop over the groups ends at the work budget
// and not after seconds. The first expression ran for 5.4 s before the budget.
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

			start := time.Now()

			got, err := expr.New().Bool(t.Context(), tc.source, reviewerEnv())
			if got || !errors.Is(err, catalog.ErrRuleFailed) {
				t.Fatalf("Bool() = %v, %v, want false, ErrRuleFailed", got, err)
			}

			// The bound is the time limit and the work of the calls that the
			// budget allows, with room for a slow machine and the race detector.
			if elapsed := time.Since(start); elapsed > time.Second {
				t.Fatalf("Bool() took %v, want less than 1s", elapsed)
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
		edit func(env *catalog.Env)
	}{
		{"a long string", func(env *catalog.Env) { env.Inputs["big"] = strings.Repeat("a", 1<<20) }},
		{"many items", func(env *catalog.Env) { env.Inputs["many"] = many }},
		{"a long value", func(env *catalog.Env) { env.Value = strings.Repeat("a", 1<<20) }},
		{"a long input", func(env *catalog.Env) { env.Input = map[string]any{"k": strings.Repeat("a", 1<<20)} }},
		{"a long name", func(env *catalog.Env) { env.Request.Name = strings.Repeat("a", 1<<20) }},
		{"long group names", func(env *catalog.Env) {
			env.Request.Requester.Groups = slices.Repeat([]string{strings.Repeat("g", 1<<10)}, catalog.MaxEnvGroups)
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			env := hostileEnv()
			tc.edit(env)

			if got, err := expr.New().Bool(t.Context(), "true", env); got || !errors.Is(err, catalog.ErrRuleFailed) {
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
		if err := expr.New().Compile(source, catalog.ResultBool); !errors.Is(err, catalog.ErrInvalid) {
			t.Fatalf("Compile(%q) = %v, want ErrInvalid", source, err)
		}
	}
}
