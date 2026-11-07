package selfservicetest

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/selfservice"
)

// EvaluatorFactory returns the selfservice.Evaluator under test.
type EvaluatorFactory func(t *testing.T) selfservice.Evaluator

// RunEvaluator executes the suite of selfservice.Evaluator against evaluators
// from newEvaluator. Every adapter of the port runs it.
func RunEvaluator(t *testing.T, newEvaluator EvaluatorFactory) {
	t.Helper()

	cases := []struct {
		name string
		run  func(t *testing.T, e selfservice.Evaluator)
	}{
		{"the version is a release", testEvaluatorVersion},
		{"a source that compiles to its kind is accepted", testCompileAccepts},
		{"a source that does not compile is ErrInvalid", testCompileInvalid},
		{"a source above a size limit is ErrLimit", testCompileLimits},
		{"a source with a builtin or syntax that the engine refuses is ErrInvalid", testCompileRefuses},
		{"each builtin of ExprBuiltins compiles", testBuiltinsCompile},
		{"a boolean rule reads the whole environment", testBoolEnv},
		{"a string expression gives a string", testStringResults},
		{"a run that fails is ErrRuleFailed and never true", testRunFails},
		{"a run past the memory budget is ErrRuleFailed", testMemoryBudget},
		{"the operator + refuses a string above MaxConcatBytes", testConcatLimit},
		{"an environment with too many groups is ErrRuleFailed", testTooManyGroups},
		{"an ended context is ErrRuleFailed", testEndedContext},
		{"the same source and environment give the same result", testDeterministic},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tc.run(t, newEvaluator(t))
		})
	}
}

// evalEnv is an environment with a value in each field.
func evalEnv() *selfservice.Env {
	return &selfservice.Env{
		Value:  float64(40),
		Inputs: map[string]any{"size": "large", "storageGb": float64(40), "tags": []any{"a", "b", "c"}},
		Usage:  map[string]int{"units": 7},
		Input:  map[string]any{"cost": float64(12)},
		Request: selfservice.RequestEnv{
			Requester: selfservice.Requester{Subject: "ana", Agent: "", Groups: []string{"dev", "dba"}},
			Namespace: "payments",
			Name:      "orders-db",
		},
	}
}

func testEvaluatorVersion(t *testing.T, e selfservice.Evaluator) {
	t.Helper()

	v := e.Version()
	if !strings.HasPrefix(v, "v") || strings.Count(v, ".") != 2 {
		t.Fatalf("Version() = %q, want a release such as v1.17.8", v)
	}
}

func testCompileAccepts(t *testing.T, e selfservice.Evaluator) {
	t.Helper()

	for _, tc := range []struct {
		source string
		kind   selfservice.ResultKind
	}{
		{"value >= 20 && value <= 2000", selfservice.ResultBool},
		{"inputs.size == 'large'", selfservice.ResultBool},
		{"'dba' in request.requester.groups", selfservice.ResultBool},
		{"usage['units'] < 50", selfservice.ResultBool},
		{"all(request.requester.groups, # != '')", selfservice.ResultBool},
		{"value matches '^[a-z]+$'", selfservice.ResultBool},
		{"'databases/' + request.name", selfservice.ResultString},
		{"upper(request.namespace)", selfservice.ResultString},
		{"inputs.size ?? 'small'", selfservice.ResultString},
	} {
		if err := e.Compile(tc.source, tc.kind); err != nil {
			t.Fatalf("Compile(%q, %s) = %v, want nil", tc.source, tc.kind, err)
		}
	}
}

func testCompileInvalid(t *testing.T, e selfservice.Evaluator) {
	t.Helper()

	for _, tc := range []struct {
		name   string
		source string
		kind   selfservice.ResultKind
	}{
		{"syntax", "value >=", selfservice.ResultBool},
		{"unknown name", "nothing == 1", selfservice.ResultBool},
		{"unknown field", "request.nothing == 'x'", selfservice.ResultBool},
		{"string for a bool", "'x'", selfservice.ResultBool},
		{"number for a string", "1 + 2", selfservice.ResultString},
		{"unknown kind", "true", selfservice.ResultKind("number")},
	} {
		if err := e.Compile(tc.source, tc.kind); !errors.Is(err, selfservice.ErrInvalid) {
			t.Fatalf("%s: Compile(%q, %s) = %v, want ErrInvalid", tc.name, tc.source, tc.kind, err)
		}
	}
}

func testCompileLimits(t *testing.T, e selfservice.Evaluator) {
	t.Helper()

	nodes := strings.Repeat("1 + ", selfservice.MaxExprNodes) + "1 > 0"

	for _, tc := range []struct {
		name   string
		source string
	}{
		{"empty", ""},
		{"bytes", "'" + strings.Repeat("a", selfservice.MaxExprBytes) + "' != ''"},
		{"nodes", nodes},
	} {
		if err := e.Compile(tc.source, selfservice.ResultBool); !errors.Is(err, selfservice.ErrLimit) {
			t.Fatalf("%s: Compile() = %v, want ErrLimit", tc.name, err)
		}
	}
}

func testCompileRefuses(t *testing.T, e selfservice.Evaluator) {
	t.Helper()

	for _, source := range []string{
		"now() != nil", "date('2026-01-01') != nil", "timezone('UTC') != nil", "duration('1h') != nil",
		"len(keys(inputs)) > 0", "len(values(inputs)) > 0", "len(toPairs(inputs)) > 0",
		"join(request.requester.groups, ',') != ''", "len(repeat('a', 5)) > 0", "replace('a', 'a', 'b') != ''",
		"string(1) != ''", "toJSON(inputs) != ''", "reduce([1], # + #acc) > 0", "len(sortBy(inputs.tags, #)) > 0", "len(groupBy(inputs.tags, #)) > 0",
		"let x = 1; x > 0", "len(1..5) > 0",
		"all(request.requester.groups, any(request.requester.groups, # != ''))",
		"value matches inputs.size",
	} {
		if err := e.Compile(source, selfservice.ResultBool); !errors.Is(err, selfservice.ErrInvalid) {
			t.Fatalf("Compile(%q) = %v, want ErrInvalid", source, err)
		}
	}
}

func testBuiltinsCompile(t *testing.T, e selfservice.Evaluator) {
	t.Helper()

	// One call of each builtin, with arguments of the types it takes.
	calls := map[string]string{
		"abs": "abs(-1) > 0", "all": "all(inputs.tags, # != '')", "any": "any(inputs.tags, # == 'a')",
		"ceil": "ceil(1.5) > 1", "count": "count(inputs.tags, # == 'a') > 0",
		"filter": "len(filter(inputs.tags, # != 'a')) > 0", "find": "find(inputs.tags, # == 'a') == 'a'",
		"findIndex": "findIndex(inputs.tags, # == 'a') == 0", "findLast": "findLast(inputs.tags, # == 'a') == 'a'",
		"findLastIndex": "findLastIndex(inputs.tags, # == 'a') == 0", "first": "first(inputs.tags) == 'a'",
		"float": "float(1) > 0", "floor": "floor(1.5) > 0", "get": "get(inputs, 'size') == 'large'",
		"hasPrefix": "hasPrefix('abc', 'a')", "hasSuffix": "hasSuffix('abc', 'c')",
		"indexOf": "indexOf('abc', 'b') == 1", "int": "int(1.5) == 1", "last": "last(inputs.tags) == 'c'",
		"lastIndexOf": "lastIndexOf('abca', 'a') == 3", "len": "len(inputs.tags) == 3", "lower": "lower('A') == 'a'",
		"map": "len(map(inputs.tags, upper(#))) == 3", "max": "max(1, 2) == 2", "mean": "mean([1, 3]) == 2",
		"median": "median([1, 2, 3]) == 2", "min": "min(1, 2) == 1", "none": "none(inputs.tags, # == 'z')",
		"one": "one(inputs.tags, # == 'a')", "round": "round(1.5) == 2", "sum": "sum([1, 2]) == 3",
		"trim": "trim(' a ') == 'a'", "trimPrefix": "trimPrefix('ab', 'a') == 'b'",
		"trimSuffix": "trimSuffix('ab', 'b') == 'a'", "upper": "upper('a') == 'A'",
	}

	for _, name := range selfservice.ExprBuiltins() {
		source, ok := calls[name]
		if !ok {
			t.Fatalf("no call of the builtin %s in the suite", name)
		}

		got, err := e.Bool(t.Context(), source, evalEnv())
		if err != nil || !got {
			t.Fatalf("Bool(%q) = %v, %v, want true", source, got, err)
		}
	}

	if len(calls) != len(selfservice.ExprBuiltins()) {
		t.Fatalf("the suite calls %d builtins, ExprBuiltins() has %d", len(calls), len(selfservice.ExprBuiltins()))
	}
}

func testBoolEnv(t *testing.T, e selfservice.Evaluator) {
	t.Helper()

	for _, tc := range []struct {
		source string
		want   bool
	}{
		{"value >= 20 && value <= 2000", true},
		{"value > 100", false},
		{"inputs.size == 'large' && inputs.storageGb == 40", true},
		{"inputs.tags[1] == 'b'", true},
		{"usage['units'] + 1 == 8", true},
		{"input.cost > 10", true},
		{"request.requester.subject == 'ana' && request.requester.agent == ''", true},
		{"'dba' in request.requester.groups", true},
		{"'ops' in request.requester.groups", false},
		{"request.namespace == 'payments' && request.name == 'orders-db'", true},
		{"request.name matches '^orders-'", true},
		{"inputs.missing == nil", true},
		{"!(inputs.size == 'small') ? true : false", true},
	} {
		got, err := e.Bool(t.Context(), tc.source, evalEnv())
		if err != nil || got != tc.want {
			t.Fatalf("Bool(%q) = %v, %v, want %v", tc.source, got, err, tc.want)
		}
	}
}

func testStringResults(t *testing.T, e selfservice.Evaluator) {
	t.Helper()

	for _, tc := range []struct{ source, want string }{
		{"'databases/' + request.name", "databases/orders-db"},
		{"upper(request.namespace)", "PAYMENTS"},
		{"inputs.size + '-' + request.requester.subject", "large-ana"},
		{"inputs.missing ?? 'small'", "small"},
	} {
		got, err := e.String(t.Context(), tc.source, evalEnv())
		if err != nil || got != tc.want {
			t.Fatalf("String(%q) = %q, %v, want %q", tc.source, got, err, tc.want)
		}
	}
}

func testRunFails(t *testing.T, e selfservice.Evaluator) {
	t.Helper()

	for _, source := range []string{
		"value.nothing > 1",      // a field of a number
		"inputs.size > 1",        // a string and a number
		"inputs.tags[10] == 'a'", // an index out of range
		"inputs.size + 1 > 0",    // a string and a number
		"int(inputs.size) > 0",   // a string that is not a number
		"first(request.requester.groups) == nil && inputs.nothing.deep == 1",
	} {
		got, err := e.Bool(t.Context(), source, evalEnv())
		if got {
			t.Fatalf("Bool(%q) = true, want false for a rule that fails", source)
		}

		if err != nil && !errors.Is(err, selfservice.ErrRuleFailed) {
			t.Fatalf("Bool(%q) = %v, want nil or ErrRuleFailed", source, err)
		}
	}

	// A source that does not compile cannot run: ErrRuleFailed, never true.
	got, err := e.Bool(t.Context(), "nothing == 1", evalEnv())
	if got || !errors.Is(err, selfservice.ErrRuleFailed) {
		t.Fatalf("Bool(unknown name) = %v, %v, want false, ErrRuleFailed", got, err)
	}

	// A string expression of an environment where the value is not a string.
	s, err := e.String(t.Context(), "inputs.missing", evalEnv())
	if s != "" || !errors.Is(err, selfservice.ErrRuleFailed) {
		t.Fatalf("String(nil result) = %q, %v, want empty, ErrRuleFailed", s, err)
	}

	if got, err = e.Bool(t.Context(), "true", nil); got || !errors.Is(err, selfservice.ErrRuleFailed) {
		t.Fatalf("Bool(nil environment) = %v, %v, want false, ErrRuleFailed", got, err)
	}
}

func testMemoryBudget(t *testing.T, e selfservice.Evaluator) {
	t.Helper()

	env := evalEnv()
	env.Request.Requester.Groups = make([]string, selfservice.MaxEnvGroups)

	// 1,000 groups, mapped two times, make more than ExprMemoryBudget items.
	for _, source := range []string{
		"len(map(map(request.requester.groups, #), #)) > 0",
		"len(map(request.requester.groups, #)) + len(filter(request.requester.groups, true)) > 0",
	} {
		got, err := e.Bool(t.Context(), source, env)
		if got || !errors.Is(err, selfservice.ErrRuleFailed) {
			t.Fatalf("Bool(%q) = %v, %v, want false, ErrRuleFailed", source, got, err)
		}
	}
}

func testConcatLimit(t *testing.T, e selfservice.Evaluator) {
	t.Helper()

	env := evalEnv()
	env.Inputs["big"] = strings.Repeat("a", selfservice.MaxConcatBytes)

	if s, err := e.String(t.Context(), "inputs.big + ''", env); err != nil || len(s) != selfservice.MaxConcatBytes {
		t.Fatalf("String(at the limit) = %d bytes, %v, want %d bytes", len(s), err, selfservice.MaxConcatBytes)
	}

	for _, source := range []string{"inputs.big + 'a'", "inputs.big + inputs.big + inputs.big"} {
		s, err := e.String(t.Context(), source, env)
		if s != "" || !errors.Is(err, selfservice.ErrRuleFailed) {
			t.Fatalf("String(%q) = %d bytes, %v, want ErrRuleFailed", source, len(s), err)
		}
	}
}

func testTooManyGroups(t *testing.T, e selfservice.Evaluator) {
	t.Helper()

	env := evalEnv()
	env.Request.Requester.Groups = slices.Repeat([]string{"g"}, selfservice.MaxEnvGroups+1)

	if got, err := e.Bool(t.Context(), "true", env); got || !errors.Is(err, selfservice.ErrRuleFailed) {
		t.Fatalf("Bool() = %v, %v, want false, ErrRuleFailed", got, err)
	}
}

func testEndedContext(t *testing.T, e selfservice.Evaluator) {
	t.Helper()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if got, err := e.Bool(ctx, "true", evalEnv()); got || !errors.Is(err, selfservice.ErrRuleFailed) {
		t.Fatalf("Bool(ended context) = %v, %v, want false, ErrRuleFailed", got, err)
	}
}

func testDeterministic(t *testing.T, e selfservice.Evaluator) {
	t.Helper()

	const source = "len(filter(inputs.tags, # != 'b')) == 2 && 'dba' in request.requester.groups"

	for range 20 {
		if got, err := e.Bool(t.Context(), source, evalEnv()); err != nil || !got {
			t.Fatalf("Bool() = %v, %v, want true each time", got, err)
		}
	}
}
