package jobproxytest_test

import (
	"bytes"
	"context"
	"fmt"
	"slices"
	"sync"
	"testing"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/jobproxy"
	"opendustpan.org/opendustpan/pkg/jobproxy/jobproxytest"
	"opendustpan.org/opendustpan/pkg/jobproxy/rulestore"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// fakeStore is an in-memory RuleStore. It shows the rules of the port to
// the SQL adapters that come later.
type fakeStore struct {
	mu         sync.Mutex
	namespaces map[namespace.Name]int64
	sets       map[rulestore.Scope]rulestore.Stored
	audit      map[namespace.Name][]audit.Entry
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		mu:         sync.Mutex{},
		namespaces: map[namespace.Name]int64{},
		sets:       map[rulestore.Scope]rulestore.Stored{},
		audit:      map[namespace.Name][]audit.Entry{},
	}
}

// copyStored returns s with a copy of its document.
func copyStored(s rulestore.Stored) rulestore.Stored {
	s.Document = bytes.Clone(s.Document)

	return s
}

func (f *fakeStore) Get(_ context.Context, scope rulestore.Scope) (rulestore.Stored, error) {
	if err := rulestore.ValidScope(scope); err != nil {
		return rulestore.Stored{}, err
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	s, ok := f.sets[scope]
	if !ok {
		return rulestore.Stored{}, rulestore.ErrNotFound
	}

	return copyStored(s), nil
}

func (f *fakeStore) Put(
	_ context.Context, c rulestore.Change, fence namespace.Fence, e audit.Entry,
) (rulestore.Stored, error) {
	scope, doc, expected := c.Scope, c.Document, c.Expected

	// Defense in depth: the handler parses the document too; the store parses
	// it again so that a caller that skips that check stores nothing.
	if _, err := jobproxy.Parse(doc); err != nil {
		return rulestore.Stored{}, err
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	if err := f.check(scope, expected, fence, e); err != nil {
		return rulestore.Stored{}, err
	}

	s := rulestore.Stored{
		Scope:     scope,
		Document:  bytes.Clone(doc),
		Digest:    rulestore.DocumentDigest(doc),
		Version:   expected + 1,
		UpdatedAt: e.At.UnixMilli(),
	}
	f.sets[scope] = s
	f.audit[scope.Namespace] = append(f.audit[scope.Namespace], e)

	return copyStored(s), nil
}

func (f *fakeStore) Delete(
	_ context.Context, scope rulestore.Scope, expected int64, fence namespace.Fence, e audit.Entry,
) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if err := rulestore.ValidScope(scope); err != nil {
		return err
	}

	if _, ok := f.sets[scope]; !ok {
		return rulestore.ErrNotFound
	}

	if err := f.check(scope, expected, fence, e); err != nil {
		return err
	}

	delete(f.sets, scope)
	f.audit[scope.Namespace] = append(f.audit[scope.Namespace], e)

	return nil
}

// check runs the checks that each write shares, with f.mu held.
func (f *fakeStore) check(scope rulestore.Scope, expected int64, fence namespace.Fence, e audit.Entry) error {
	if err := rulestore.ValidScope(scope); err != nil {
		return err
	}

	if e.Namespace != scope.Namespace {
		return fmt.Errorf("%w: the audit entry names another namespace", jobproxy.ErrInvalid)
	}

	current, ok := f.namespaces[scope.Namespace]
	if !ok || fence.Namespace != scope.Namespace || fence.FailoverVersion != current {
		return namespace.ErrFenced
	}

	if f.sets[scope].Version != expected {
		return rulestore.ErrConflict
	}

	return nil
}

func TestRuleStoreOnTheFake(t *testing.T) {
	t.Parallel()

	jobproxytest.RunRuleStore(t, func(_ *testing.T) jobproxytest.Harness {
		f := newFakeStore()

		return jobproxytest.Harness{
			Store: f,
			Namespace: func(_ *testing.T, name string) namespace.Fence {
				f.mu.Lock()
				defer f.mu.Unlock()

				n := namespace.Name(name)
				if _, ok := f.namespaces[n]; !ok {
					f.namespaces[n] = 1
				}

				return namespace.Fence{Namespace: n, FailoverVersion: f.namespaces[n]}
			},
			Audit: func(_ *testing.T, ns string) []audit.Entry {
				f.mu.Lock()
				defer f.mu.Unlock()

				return slices.Clone(f.audit[namespace.Name(ns)])
			},
		}
	})
}

func TestCaseNamesAreUniqueAndNotEmpty(t *testing.T) {
	t.Parallel()

	names := jobproxytest.CaseNames()
	if len(names) == 0 {
		t.Fatal("the suite has no case")
	}

	seen := map[string]bool{}

	for _, name := range names {
		if name == "" || seen[name] {
			t.Fatalf("case name %q is empty or used twice", name)
		}

		seen[name] = true
	}
}
