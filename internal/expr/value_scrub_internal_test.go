package expr

import (
	"errors"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/profile"
)

// scrubSecret is a value that no error of a run may hold.
const scrubSecret = "hunter2-not-for-an-error"

// TestEvalRunErrorNamesNoValue fails when Eval puts the text of the engine in
// the error of a run: int(values.s) and float(values.s) fail with a message
// that quotes the value.
func TestEvalRunErrorNamesNoValue(t *testing.T) {
	t.Parallel()

	e := mustNew(t, NewLimiter())
	values := map[string]any{"s": scrubSecret, "n": 1}

	for _, source := range []string{
		"int(values.s)",
		"float(values.s)",
		"values.s + values.n",
		"values.n / (values.n - 1) + int(values.s)",
		"values.s[100]",
	} {
		_, err := e.Eval(t.Context(), source, values)
		if err == nil {
			t.Errorf("%q: Eval answered no error", source)

			continue
		}

		if !errors.Is(err, profile.ErrReference) {
			t.Errorf("%q: error = %v, want ErrReference", source, err)
		}

		if strings.Contains(err.Error(), scrubSecret) {
			t.Errorf("%q: the error holds the value: %v", source, err)
		}
	}
}

// TestRunValuesPanicNamesNoValue fails when the recovered panic of a run puts
// its text in the error: a nil program panics inside the machine.
func TestRunValuesPanicNamesNoValue(t *testing.T) {
	t.Parallel()

	_, err := runValues(nil, map[string]any{valueEnvKey: map[string]any{"s": scrubSecret}})
	if !errors.Is(err, profile.ErrReference) {
		t.Fatalf("error = %v, want ErrReference", err)
	}

	if want := profile.ErrReference.Error() + ": " + errValueRun; err.Error() != want {
		t.Errorf("error = %q, want the fixed text %q", err, want)
	}
}

// TestEvalFreesTheSlotBeforeItAnswers fails when a run gives its slot back
// after it sends its result: with one slot, the next Eval then meets a full
// limiter.
func TestEvalFreesTheSlotBeforeItAnswers(t *testing.T) {
	t.Parallel()

	e, err := NewWithTimeout(limiterOf(1), time.Minute)
	if err != nil {
		t.Fatalf("NewWithTimeout: %v", err)
	}

	for i := range 2000 {
		if _, err = e.Eval(t.Context(), "values.n + 1", map[string]any{"n": 1}); err != nil {
			t.Fatalf("run %d: %v", i, err)
		}

		if n := len(e.limiter.slots); n != 0 {
			t.Fatalf("run %d: %d slots are taken after Eval answered, want 0", i, n)
		}
	}
}

// TestNewWithTimeoutCapsTheLimit fails when NewWithTimeout takes a limit above
// MaxTestTimeout.
func TestNewWithTimeoutCapsTheLimit(t *testing.T) {
	t.Parallel()

	if _, err := NewWithTimeout(NewLimiter(), MaxTestTimeout); err != nil {
		t.Errorf("the cap itself: error = %v, want none", err)
	}

	for _, d := range []time.Duration{MaxTestTimeout + 1, time.Hour, 1<<63 - 1} {
		if _, err := NewWithTimeout(NewLimiter(), d); !errors.Is(err, ErrTimeout) {
			t.Errorf("limit %v: error = %v, want ErrTimeout", d, err)
		}
	}
}

// TestCheckValueSourceRefusesEachOutsideName calls the source check
// directly, as if Eval did not run it first.
func TestCheckValueSourceRefusesEachOutsideName(t *testing.T) {
	t.Parallel()

	for _, source := range []string{
		"providers.x", "providers.vault.token", "imports", "imports[0]", "profiles.a", "context.user",
		"$env", "$env.HOME", `values.a + providers.x`, "(imports)",
	} {
		if err := checkValueSource(source); !errors.Is(err, profile.ErrReference) {
			t.Errorf("%q: error = %v, want ErrReference", source, err)
		}
	}

	for _, source := range []string{"", strings.Repeat("a", 1<<20)} {
		if err := checkValueSource(source); !errors.Is(err, profile.ErrLimit) {
			t.Errorf("source of %d bytes: error = %v, want ErrLimit", len(source), err)
		}
	}

	for _, source := range []string{"values.providers", `values["imports"]`, `"providers"`} {
		if err := checkValueSource(source); err != nil {
			t.Errorf("%q: error = %v, want none: it reads a values key or a string", source, err)
		}
	}
}
