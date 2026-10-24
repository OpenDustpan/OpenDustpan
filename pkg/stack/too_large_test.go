package stack_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/stack"
)

// overBoundError is a replay bound refusal of an implementation of the replayer
// port, which declares ReplayLimit as the same unnamed struct type.
type overBoundError struct{}

func (overBoundError) Error() string { return "12 journal entries exceed the limit of 10" }

func (overBoundError) ReplayBound() struct {
	Bound string
	Count int64
	Limit int64
} {
	return struct {
		Bound string
		Count int64
		Limit int64
	}{Bound: "journal entries", Count: 12, Limit: 10}
}

// The compile-time check fails the build when a struct of standard types stops
// satisfying ReplayBound, which an implementation outside this module uses.
var _ stack.ReplayBound = overBoundError{}

// TestTooLargeRefusalsNameOneRemedy proves that the two size refusals of a
// journaled update, a journal batch over its bound and a replay past a bound,
// answer 413 with the same remedy, and that their error texts, which the log
// holds, end with that remedy and name no other one.
func TestTooLargeRefusalsNameOneRemedy(t *testing.T) {
	t.Parallel()

	const remedy = ": run `pulumi up` with fewer resources"

	tests := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "journal batch over its bound", err: stack.BatchOverLimit(9, 8),
			want: "This journal batch has 9 bytes, more than the limit of 8 bytes" + remedy + ".",
		},
		{
			name: "replay past a bound", err: stack.MapReplayBound("u-1", fmt.Errorf("replay: %w", overBoundError{})),
			want: "This update has 12 journal entries, more than the limit of 10" + remedy + ".",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if !errors.Is(tc.err, stack.ErrTooLarge) {
				t.Fatalf("err = %v, want ErrTooLarge", tc.err)
			}

			msg, ok := stack.TooLargeMessage(tc.err)
			if !ok || msg != tc.want {
				t.Fatalf("TooLargeMessage = %q, %t; want %q", msg, ok, tc.want)
			}

			if text := tc.err.Error(); !strings.HasSuffix(text, remedy) || strings.Count(text, "pulumi up") != 1 {
				t.Fatalf("err = %q, want an error text that ends with %q and names one command", text, remedy)
			}
		})
	}
}
