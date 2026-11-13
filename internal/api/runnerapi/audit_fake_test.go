package runnerapi_test

import (
	"context"
	"sync"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// testAudit keeps the entries of a handler in memory, for the tests that do
// not read them. It checks each entry with audit.Entry.Validate, as the
// stores do.
func newTestAudit() *testAudit {
	return &testAudit{mu: sync.Mutex{}, entries: nil}
}

type testAudit struct {
	mu      sync.Mutex
	entries []audit.Entry
}

var (
	_ audit.Store       = (*testAudit)(nil)
	_ audit.ServerStore = (*testAudit)(nil)
)

func (a *testAudit) Append(_ context.Context, fence namespace.Fence, e audit.Entry) (audit.Entry, error) {
	if err := e.Validate(); err != nil {
		return audit.Entry{}, err
	}

	if e.Namespace != fence.Namespace {
		return audit.Entry{}, audit.ErrInvalidEntry
	}

	return a.keep(e), nil
}

func (a *testAudit) AppendServer(_ context.Context, e audit.Entry) (audit.Entry, error) {
	if err := e.Validate(); err != nil {
		return audit.Entry{}, err
	}

	return a.keep(e), nil
}

func (a *testAudit) List(context.Context, namespace.Name, audit.Page) (audit.Listing, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	return audit.Listing{Entries: append([]audit.Entry(nil), a.entries...), Total: int64(len(a.entries))}, nil
}

func (a *testAudit) Check(context.Context, namespace.Name) (audit.CheckResult, error) {
	return audit.CheckResult{Checked: 0, BadSequence: 0, Unchained: 0, Valid: true}, nil
}

// denied returns the entries that the handler kept.
func (a *testAudit) denied() []audit.Entry {
	a.mu.Lock()
	defer a.mu.Unlock()

	return append([]audit.Entry(nil), a.entries...)
}

func (a *testAudit) keep(e audit.Entry) audit.Entry {
	a.mu.Lock()
	defer a.mu.Unlock()

	e.Sequence = int64(len(a.entries)) + 1
	a.entries = append(a.entries, e)

	return e
}
