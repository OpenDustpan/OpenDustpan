package secrettest_test

import (
	"cmp"
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/secret/secrettest"
)

// TestContentKeySuite runs the suite against an in-memory store, so the
// cases state their rules before a SQL adapter implements the port.
func TestContentKeySuite(t *testing.T) {
	t.Parallel()
	secrettest.RunContentKeys(t, func(*testing.T) secrettest.ContentKeyStore { return newMemContentKeys() })
}

// memContentKeys keeps namespaces and content key records in memory.
type memContentKeys struct {
	namespaces map[namespace.Name]namespace.Namespace
	keys       map[secret.ContentRef]secret.ContentKey
	mu         sync.Mutex
}

func newMemContentKeys() *memContentKeys {
	return &memContentKeys{
		namespaces: map[namespace.Name]namespace.Namespace{},
		keys:       map[secret.ContentRef]secret.ContentKey{},
		mu:         sync.Mutex{},
	}
}

func (m *memContentKeys) CreateNamespace(_ context.Context, ns namespace.Namespace) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.namespaces[ns.Name]; ok {
		return namespace.ErrAlreadyExists
	}

	m.namespaces[ns.Name] = ns

	return nil
}

func (m *memContentKeys) GetNamespace(_ context.Context, name namespace.Name) (namespace.Namespace, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	ns, ok := m.namespaces[name]
	if !ok {
		return namespace.Namespace{}, namespace.ErrNotFound
	}

	return ns, nil
}

func (m *memContentKeys) SetOverrides(context.Context, namespace.Name, namespace.Overrides, namespace.Fence) error {
	return nil
}

func (m *memContentKeys) ListNamespaces(context.Context) ([]namespace.Namespace, error) {
	return nil, nil
}

func (m *memContentKeys) Ping(context.Context) error {
	return nil
}

// checkFence answers namespace.ErrFenced for a fence older than the
// namespace. The caller holds the lock.
func (m *memContentKeys) CreateContentKey(
	_ context.Context, k secret.ContentKey, fence namespace.Fence,
) (secret.ContentKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkFence(fence); err != nil {
		return secret.ContentKey{}, err
	}

	if stored, ok := m.keys[k.Ref]; ok {
		if stored.Shredded() {
			return stored, secret.ErrShredded
		}

		return stored, nil
	}

	m.keys[k.Ref] = k

	return k, nil
}

func (m *memContentKeys) GetContentKey(_ context.Context, ref secret.ContentRef) (secret.ContentKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	k, ok := m.keys[ref]
	if !ok {
		return secret.ContentKey{}, secret.ErrKeyNotFound
	}

	if k.Shredded() {
		return k, secret.ErrShredded
	}

	return k, nil
}

func (m *memContentKeys) ShredContentKey(
	_ context.Context, ref secret.ContentRef, at time.Time, fence namespace.Fence,
) (secret.ContentKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkFence(fence); err != nil {
		return secret.ContentKey{}, err
	}

	k, ok := m.keys[ref]
	if !ok {
		return secret.ContentKey{}, secret.ErrKeyNotFound
	}

	if !k.Shredded() {
		k.ShreddedAt = at
		m.keys[ref] = k
	}

	return k, nil
}

func (m *memContentKeys) ListContentKeys(
	_ context.Context, scope secret.Scope, after secret.ContentKeyID, limit int,
) ([]secret.ContentKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var out []secret.ContentKey

	for ref := range m.keys {
		k := m.keys[ref]
		if ref.Scope == scope && k.ID > after {
			out = append(out, k)
		}
	}

	slices.SortFunc(out, func(a, b secret.ContentKey) int { return cmp.Compare(a.ID, b.ID) })

	return out[:min(limit, len(out))], nil
}

func (m *memContentKeys) ListContentWrapperIDs(context.Context) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	ids := []string{}

	for i := range m.keys {
		k := m.keys[i]
		if !k.Shredded() && !slices.Contains(ids, k.WrapperID) {
			ids = append(ids, k.WrapperID)
		}
	}

	slices.Sort(ids)

	return ids, nil
}

func (m *memContentKeys) checkFence(fence namespace.Fence) error {
	ns, ok := m.namespaces[fence.Namespace]
	if !ok {
		return namespace.ErrNotFound
	}

	if fence.FailoverVersion != ns.FailoverVersion {
		return namespace.ErrFenced
	}

	return nil
}
