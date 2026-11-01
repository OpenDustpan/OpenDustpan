package setting_test

import (
	"context"
	"fmt"
	"maps"
	"sync"
	"testing"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/setting"
	"opendustpan.org/opendustpan/pkg/setting/settingtest"
)

// scopeState is what the memory store keeps for one scope.
type scopeState struct {
	values   map[string]string
	revision int64
	digest   string
}

func (s scopeState) clone() scopeState {
	return scopeState{values: maps.Clone(s.values), revision: s.revision, digest: s.digest}
}

// memStore is a Store in memory. It exists to run the conformance suite
// against the rules of Change without a database.
type memStore struct {
	mu       sync.Mutex
	scopes   map[setting.Scope]scopeState
	failover map[namespace.Name]int64
	entries  map[namespace.Name][]audit.Entry
}

var _ setting.Store = (*memStore)(nil)

func newMemStore() *memStore {
	return &memStore{
		mu: sync.Mutex{}, scopes: map[setting.Scope]scopeState{}, failover: map[namespace.Name]int64{},
		entries: map[namespace.Name][]audit.Entry{},
	}
}

// memTx is a transaction over a copy of the state. Commit swaps it in.
type memTx struct {
	s       *memStore
	scopes  map[setting.Scope]scopeState
	entries []audit.Entry
}

func (tx *memTx) Load(_ context.Context, scope setting.Scope) (setting.Stored, error) {
	if !scope.IsInstallation() {
		if _, ok := tx.s.failover[scope.Namespace]; !ok {
			return setting.Stored{Values: nil, Revision: 0, Digest: ""}, namespace.ErrNotFound
		}
	}

	st := tx.state(scope).clone()
	if st.values == nil {
		st.values = map[string]string{}
	}

	return setting.Stored{Values: st.values, Revision: st.revision, Digest: st.digest}, nil
}

func (tx *memTx) Put(_ context.Context, scope setting.Scope, key, value string, _ int64) error {
	st := tx.state(scope).clone()
	if st.values == nil {
		st.values = map[string]string{}
	}

	st.values[key] = value
	tx.scopes[scope] = st

	return nil
}

func (tx *memTx) Remove(_ context.Context, scope setting.Scope, key string) error {
	st := tx.state(scope).clone()
	delete(st.values, key)
	tx.scopes[scope] = st

	return nil
}

func (tx *memTx) SetRevision(_ context.Context, scope setting.Scope, revision int64) error {
	st := tx.state(scope).clone()
	st.revision = revision
	tx.scopes[scope] = st

	return nil
}

func (tx *memTx) SetDigest(_ context.Context, digest string, revision int64) error {
	scope := setting.InstallationScope()
	st := tx.state(scope).clone()
	st.digest, st.revision = digest, revision
	tx.scopes[scope] = st

	return nil
}

func (tx *memTx) Append(_ context.Context, e audit.Entry) error {
	if err := e.Validate(); err != nil {
		return fmt.Errorf("entry: %w", err)
	}

	tx.entries = append(tx.entries, e)

	return nil
}

func (tx *memTx) state(scope setting.Scope) scopeState {
	if st, ok := tx.scopes[scope]; ok {
		return st
	}

	return scopeState{values: map[string]string{}, revision: 0, digest: ""}
}

func (m *memStore) Load(ctx context.Context, scope setting.Scope) (setting.Stored, error) {
	var out setting.Stored

	err := m.run(func(tx *memTx) error {
		var err error

		out, err = tx.Load(ctx, scope)

		return err
	})

	return out, err
}

func (m *memStore) Update(
	ctx context.Context, scope setting.Scope, fence namespace.Fence, in setting.ChangeInput,
) (setting.Result, error) {
	var out setting.Result

	err := m.run(func(tx *memTx) error {
		want := audit.ServerScope
		if !scope.IsInstallation() {
			want = scope.Namespace

			version, ok := m.failover[scope.Namespace]
			if !ok {
				return namespace.ErrNotFound
			}

			if fence.Namespace != scope.Namespace || fence.FailoverVersion != version {
				return namespace.ErrFenced
			}
		}

		if in.Entry.Namespace != want {
			return fmt.Errorf("%w: entry names %q, want %q", audit.ErrInvalidEntry, in.Entry.Namespace, want)
		}

		var err error

		out, err = setting.Change(ctx, tx, scope, in)

		return err
	})

	return out, err
}

func (m *memStore) Sync(ctx context.Context, digest string) (int64, bool, error) {
	var (
		revision int64
		changed  bool
	)

	err := m.run(func(tx *memTx) error {
		var err error

		revision, changed, err = setting.Sync(ctx, tx, digest)

		return err
	})

	return revision, changed, err
}

// run runs fn in a transaction and commits it when fn succeeds.
func (m *memStore) run(fn func(tx *memTx) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	tx := &memTx{s: m, scopes: maps.Clone(m.scopes), entries: nil}

	if err := fn(tx); err != nil {
		return err
	}

	m.scopes = tx.scopes

	for i := range tx.entries {
		e := tx.entries[i]
		m.entries[e.Namespace] = append(m.entries[e.Namespace], e)
	}

	return nil
}

func TestMemoryStoreConformance(t *testing.T) {
	t.Parallel()

	settingtest.Run(t, func(t *testing.T) settingtest.Harness {
		t.Helper()

		m := newMemStore()

		return settingtest.Harness{
			Store: m,
			Namespace: func(t *testing.T, name string) namespace.Fence {
				t.Helper()

				m.mu.Lock()
				defer m.mu.Unlock()

				m.failover[namespace.Name(name)] = 1

				return namespace.Fence{Namespace: namespace.Name(name), FailoverVersion: 1}
			},
			Audit: func(t *testing.T, scope namespace.Name) []audit.Entry {
				t.Helper()

				m.mu.Lock()
				defer m.mu.Unlock()

				return append([]audit.Entry(nil), m.entries[scope]...)
			},
		}
	})
}
