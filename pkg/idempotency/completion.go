package idempotency

import (
	"context"
	"fmt"
	"sync"
)

// Completion asks a store to store the final answer of a request in the
// transaction of the operation. A crash then never leaves an answer without
// its operation, and an operation without its answer. A store method that
// writes in one transaction looks for a Completion in its context. If it
// finds one, it calls Finish with the result of the write before it commits.
// The documentation of each method says what the result is.
//
// A Completion is for one request. Its methods are safe to call from many
// goroutines.
type Completion struct {
	// Ref and Token name the pending record that Begin wrote.
	Ref   Ref
	Token string

	build  func(result any) (Answer, error)
	mu     sync.Mutex
	stored bool
}

// NewCompletion returns the Completion of the pending record that ref and
// token name. build makes the answer from the result of the write. It must
// not fail for a result that the store passes.
func NewCompletion(ref Ref, token string, build func(result any) (Answer, error)) *Completion {
	return &Completion{Ref: ref, Token: token, build: build, mu: sync.Mutex{}, stored: false}
}

type completionKey struct{}

// WithCompletion returns a context that carries c.
func WithCompletion(ctx context.Context, c *Completion) context.Context {
	return context.WithValue(ctx, completionKey{}, c)
}

// CompletionFrom returns the Completion that ctx carries.
func CompletionFrom(ctx context.Context) (*Completion, bool) {
	c, ok := ctx.Value(completionKey{}).(*Completion)

	return c, ok && c != nil
}

// Finish builds the answer for result and calls store with it, inside the
// transaction of the write. It returns the error of Build or of store. The
// store function returns ErrLost when the pending record of the request is
// not in place, and the transaction then rolls back.
func (c *Completion) Finish(result any, store func(token string, a Answer) error) error {
	a, err := c.build(result)
	if err != nil {
		return fmt.Errorf("build idempotent answer: %w", err)
	}

	if validateErr := a.Validate(); validateErr != nil {
		return validateErr
	}

	if storeErr := store(c.Token, a); storeErr != nil {
		return storeErr
	}

	c.mu.Lock()
	c.stored = true
	c.mu.Unlock()

	return nil
}

// Stored reports that a write stored the answer. The caller reads it after
// the write returns without an error, which means that the transaction
// committed.
func (c *Completion) Stored() bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.stored
}
