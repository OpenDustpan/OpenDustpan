package layertest_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"opendustpan.org/opendustpan/internal/testutil/layertest"
)

var (
	errGuard = errors.New("guard refused the input")
	errOther = errors.New("other failure")
)

// TestRunChecksTheSentinel runs a table where each case passes: a wrapped
// sentinel, a bare sentinel and no error.
func TestRunChecksTheSentinel(t *testing.T) {
	t.Parallel()

	cases := []layertest.Case[string]{
		{Name: "wrapped sentinel", In: "wrapped", Want: errGuard},
		{Name: "bare sentinel", In: "bare", Want: errGuard},
		{Name: "no error", In: "valid", Want: nil},
	}

	layertest.Run(t, cases, func(ctx context.Context, in string) error {
		if ctx.Err() != nil {
			return fmt.Errorf("context ended before the call: %w", ctx.Err())
		}

		switch in {
		case "wrapped":
			return fmt.Errorf("layer: %w", errGuard)
		case "bare":
			return errGuard
		}

		return nil
	})
}

// TestRunPassesTheInput proves that each case gets its own input.
func TestRunPassesTheInput(t *testing.T) {
	t.Parallel()

	cases := []layertest.Case[int]{
		{Name: "one", In: 1, Want: errOther},
		{Name: "two", In: 2, Want: errGuard},
	}

	layertest.Run(t, cases, func(_ context.Context, in int) error {
		if in == 1 {
			return errOther
		}

		return errGuard
	})
}
