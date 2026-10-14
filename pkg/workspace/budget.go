package workspace

import (
	"context"
	"errors"
	"fmt"
)

// ErrBusy reports that the server holds no more body budget. The caller
// tries the upload again in one second.
var ErrBusy = errors.New("workspace: server is busy")

// Budget lets a read of unbounded size ask for more memory.
type Budget interface {
	Grow(n int64) bool
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
func charge(ctx context.Context, n int64) error {
	b := budgetFrom(ctx)
	if b == nil {
		return nil
	}

	if !b.Grow(n) {
		return fmt.Errorf("%w: the body budget is full: try again in one second", ErrBusy)
	}

	return nil
}
