package expr

import (
	"errors"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/catalog"
)

// TestTimeLayer checks that a run past the time limit returns ErrRuleFailed
// at the limit, and not when the run ends.
func TestTimeLayer(t *testing.T) {
	t.Parallel()

	e := New()
	e.timeout = time.Microsecond

	env := &catalog.Env{
		Value:   nil,
		Inputs:  map[string]any{"big": strings.Repeat("ab", 16<<10)},
		Usage:   nil,
		Input:   nil,
		Request: schema().Request,
	}

	start := time.Now()

	got, err := e.Bool(t.Context(), "inputs.big matches '^(a|b|ab|ba)*c$'", env)
	if got || !errors.Is(err, catalog.ErrRuleFailed) {
		t.Fatalf("Bool() = %v, %v, want false, ErrRuleFailed", got, err)
	}

	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("Bool() returned after %v, want about the limit of %v", elapsed, e.timeout)
	}
}

func TestNewTimeLimit(t *testing.T) {
	t.Parallel()

	if got, want := New().timeout, catalog.ExprTimeoutMillis*time.Millisecond; got != want {
		t.Fatalf("timeout = %v, want %v", got, want)
	}
}
