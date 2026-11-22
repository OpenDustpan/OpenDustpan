package expr_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/expr"
	"opendustpan.org/opendustpan/pkg/profile"
)

// newValueEvaluator returns the values-only evaluator of an open.
func newValueEvaluator(t *testing.T) *expr.Evaluator {
	t.Helper()

	eval, err := expr.New(expr.NewLimiter())
	if err != nil {
		t.Fatalf("expr evaluator: %v", err)
	}

	return eval
}

func TestValueEval(t *testing.T) {
	t.Parallel()

	t.Run("whole string keeps its type", testValueWholeString)
	t.Run("quoted index names the key", testValueQuotedIndex)
	t.Run("text in quotes names no name", testValueQuotedText)
	t.Run("other name is a reference", testValueOtherName)
	t.Run("quoted provider is a values key", testValueQuotedProvider)
	t.Run("failure is a reference and names no value", testValueFailureNoValue)
	t.Run("empty and oversize are a limit", testValueEmptyOversize)
	t.Run("canceled context is a reference", testValueCanceled)
	t.Run("timeout is a reference", testValueTimeout)
}

// testValueWholeString checks that a whole string keeps its type.
func testValueWholeString(t *testing.T) {
	t.Parallel()

	eval := newValueEvaluator(t)
	values := map[string]any{"n": 3, "db": map[string]any{"password": "s3cr3t"}}

	got, err := eval.Eval(t.Context(), `values.n * 2`, values)
	if err != nil {
		t.Fatalf("eval: %v", err)
	}

	if got.Kind != profile.EvalNumber {
		t.Errorf("kind = %q, want number", got.Kind)
	}

	if len(got.Paths) != 1 || len(got.Paths[0]) != 1 || got.Paths[0][0] != "n" {
		t.Errorf("paths = %v, want [[n]]", got.Paths)
	}
}

// testValueQuotedIndex checks that a quoted index names the key.
func testValueQuotedIndex(t *testing.T) {
	t.Parallel()

	eval := newValueEvaluator(t)
	values := map[string]any{"n": 3, "db": map[string]any{"password": "s3cr3t"}}

	got, err := eval.Eval(t.Context(), `values["db"].password`, values)
	if err != nil {
		t.Fatalf("eval: %v", err)
	}

	if got.Value != "s3cr3t" || got.Kind != profile.EvalString {
		t.Errorf("value = %v (%s), want s3cr3t string", got.Value, got.Kind)
	}

	if len(got.Paths) != 1 {
		t.Fatalf("paths = %v, want one path", got.Paths)
	}
}

// testValueQuotedText checks that text in quotes names no name.
func testValueQuotedText(t *testing.T) {
	t.Parallel()

	eval := newValueEvaluator(t)
	values := map[string]any{"n": 3, "db": map[string]any{"password": "s3cr3t"}}

	// "providers" is a quoted string, not a name: the expression reads
	// values only.
	got, err := eval.Eval(t.Context(), `"providers"`, values)
	if err != nil {
		t.Fatalf("eval: %v", err)
	}

	if got.Value != "providers" || len(got.Paths) != 0 {
		t.Errorf("value = %v paths = %v", got.Value, got.Paths)
	}
}

// testValueOtherName checks that another name is a reference.
func testValueOtherName(t *testing.T) {
	t.Parallel()

	eval := newValueEvaluator(t)
	values := map[string]any{"n": 3, "db": map[string]any{"password": "s3cr3t"}}

	for _, source := range []string{
		`${providers.db.password}`,
		`${imports["base"].values.x}`,
		`${profiles["base"].values.x}`,
		`${context.namespace}`,
		`${$env.region}`,
		`${values.absent.deeper}`,
	} {
		_, err := eval.Eval(t.Context(), source, values)
		if !errors.Is(err, profile.ErrReference) {
			t.Errorf("%s: eval error = %v, want ErrReference", source, err)
		}
	}
}

// testValueQuotedProvider checks that a quoted provider is a values key.
func testValueQuotedProvider(t *testing.T) {
	t.Parallel()

	eval := newValueEvaluator(t)
	withKey := map[string]any{"providers": "kept"}

	got, err := eval.Eval(t.Context(), `values.providers`, withKey)
	if err != nil {
		t.Fatalf("eval: %v", err)
	}

	if got.Value != "kept" {
		t.Errorf("value = %v, want kept", got.Value)
	}
}

// testValueFailureNoValue checks that a failure names no value.
func testValueFailureNoValue(t *testing.T) {
	t.Parallel()

	eval := newValueEvaluator(t)
	values := map[string]any{"n": 3, "db": map[string]any{"password": "s3cr3t"}}

	_, err := eval.Eval(t.Context(), `values.n + "x"`, values)
	if !errors.Is(err, profile.ErrReference) {
		t.Fatalf("eval error = %v, want ErrReference", err)
	}

	if strings.Contains(err.Error(), "s3cr3t") {
		t.Errorf("eval error names a value: %v", err)
	}
}

// testValueEmptyOversize checks that an empty source is a limit.
func testValueEmptyOversize(t *testing.T) {
	t.Parallel()

	eval := newValueEvaluator(t)
	values := map[string]any{"n": 3, "db": map[string]any{"password": "s3cr3t"}}

	if _, err := eval.Eval(t.Context(), ``, values); !errors.Is(err, profile.ErrLimit) {
		t.Errorf("empty: eval error = %v, want ErrLimit", err)
	}
}

// testValueCanceled checks that a canceled context is a reference.
func testValueCanceled(t *testing.T) {
	t.Parallel()

	eval := newValueEvaluator(t)
	values := map[string]any{"n": 3, "db": map[string]any{"password": "s3cr3t"}}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := eval.Eval(ctx, `values.n`, values); !errors.Is(err, profile.ErrReference) {
		t.Errorf("eval error = %v, want ErrReference", err)
	}
}

// testValueTimeout checks that a timeout is a reference.
func testValueTimeout(t *testing.T) {
	t.Parallel()

	eval := newValueEvaluator(t)
	values := map[string]any{"n": 3, "db": map[string]any{"password": "s3cr3t"}}

	ctx, cancel := context.WithTimeout(t.Context(), time.Nanosecond)
	defer cancel()

	<-ctx.Done()

	if _, err := eval.Eval(ctx, `values.n`, values); !errors.Is(err, profile.ErrReference) {
		t.Errorf("eval error = %v, want ErrReference", err)
	}
}
