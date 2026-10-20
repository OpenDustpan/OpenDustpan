// Package stacktest holds test doubles for the ports of the stack package.
package stacktest

import (
	"context"
	"sync"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/stack"
)

// Refusal is one refusal that Refusals saw.
type Refusal struct {
	Namespace namespace.ID
	Op        string
	Where     stack.OutOfScopeWhere
}

// Refusals is a stack.OutOfScopeRecorder that keeps the refusals in memory.
// Set Err before use to make each record fail.
type Refusals struct {
	// Err is the error that each record returns after it keeps the refusal.
	Err error

	got []Refusal
	mu  sync.Mutex
}

var _ stack.OutOfScopeRecorder = (*Refusals)(nil)

// NewRefusals returns an empty recorder.
func NewRefusals() *Refusals {
	return &Refusals{Err: nil, got: nil, mu: sync.Mutex{}}
}

// RecordOutOfScope implements stack.OutOfScopeRecorder.
func (r *Refusals) RecordOutOfScope(
	_ context.Context, ns namespace.ID, op string, where stack.OutOfScopeWhere,
) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.got = append(r.got, Refusal{Namespace: ns, Op: op, Where: where})

	return r.Err
}

// Records returns a copy of the refusals so far, oldest first.
func (r *Refusals) Records() []Refusal {
	r.mu.Lock()
	defer r.mu.Unlock()

	return append([]Refusal(nil), r.got...)
}
