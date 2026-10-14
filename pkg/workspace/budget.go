package workspace

import (
	"context"
	"errors"
	"fmt"
)

// ErrBusy reports that the server holds no more body budget. The caller
// tries the upload again after the time in Retry-After.
var ErrBusy = errors.New("workspace: server is busy")

// ErrBudgetTooSmall reports that the upload needs more body budget than the
// server holds in total. The caller must raise the budget; a retry cannot
// succeed.
var ErrBudgetTooSmall = errors.New("workspace: body budget is too small for this upload")

// Budget lets a read of unbounded size ask for more memory.
type Budget interface {
	Grow(n int64) bool
	Held() int64
	Capacity() int64
}

// budgetKey is the context key of the body budget.
type budgetKey struct{}

// WithBudget returns a context that carries the body budget b.
func WithBudget(ctx context.Context, b Budget) context.Context {
	return context.WithValue(ctx, budgetKey{}, b)
}

// budgetFrom returns the budget in ctx, or nil when ctx carries none.
//
//nolint:ireturn // The budget is a small port carried in context; callers only need Grow.
func budgetFrom(ctx context.Context) Budget {
	v := ctx.Value(budgetKey{})

	b, ok := v.(Budget)
	if !ok {
		return nil
	}

	return b
}

// charge asks the budget in ctx for n bytes. No budget means no charge.
// A refusal that can never fit reports ErrBudgetTooSmall; any other refusal
// reports ErrBusy.
func charge(ctx context.Context, n int64) error {
	b := budgetFrom(ctx)
	if b == nil {
		return nil
	}

	if b.Grow(n) {
		return nil
	}

	need := b.Held() + n
	if need > b.Capacity() {
		return fmt.Errorf(
			"%w: this upload needs at least %d bytes of body budget and the server has %d bytes: set --max-body-bytes-in-flight to raise the budget",
			ErrBudgetTooSmall,
			need,
			b.Capacity(),
		)
	}

	return fmt.Errorf("%w: the body budget is full: try again after the Retry-After time", ErrBusy)
}
