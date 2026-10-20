// Package workspacetest holds test doubles for the ports of package workspace.
package workspacetest

import (
	"context"
	"sync"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
)

// Refusal is one recorded refusal of a blob key outside a namespace.
type Refusal struct {
	Namespace namespace.ID
	Op        string
	Where     secret.OutOfScopeWhere
}

// Refusals is a secret.OutOfScopeRecorder that keeps each refusal in
// memory. It is safe for concurrent use.
type Refusals struct {
	mu      sync.Mutex
	entries []Refusal
	// Err is the error that RecordOutOfScope returns after it keeps the
	// refusal. Set it before the first call.
	Err error
}

var _ secret.OutOfScopeRecorder = (*Refusals)(nil)

// NewRefusals returns an empty recorder.
func NewRefusals() *Refusals {
	return &Refusals{mu: sync.Mutex{}, entries: nil, Err: nil}
}

// RecordOutOfScope keeps the refusal.
func (r *Refusals) RecordOutOfScope(
	_ context.Context, ns namespace.ID, op string, where secret.OutOfScopeWhere,
) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.entries = append(r.entries, Refusal{Namespace: ns, Op: op, Where: where})

	return r.Err
}

// All returns a copy of the recorded refusals, oldest first.
func (r *Refusals) All() []Refusal {
	r.mu.Lock()
	defer r.mu.Unlock()

	return append([]Refusal(nil), r.entries...)
}
