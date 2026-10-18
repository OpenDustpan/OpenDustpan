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

// TestItemKeySuite runs the suite against an in-memory store, so the
// cases state their rules before a SQL adapter implements the port.
func TestItemKeySuite(t *testing.T) {
	t.Parallel()
	secrettest.RunItemKeys(t, func(*testing.T) secrettest.ItemKeyStore { return newMemItemKeys() })
}

// memItemKeys keeps namespaces and item key records in memory.
type memItemKeys struct {
	namespaces map[namespace.Name]namespace.Namespace
	keys       map[secret.ItemRef]secret.ItemKey
	mu         sync.Mutex
}

func newMemItemKeys() *memItemKeys {
	return &memItemKeys{
		namespaces: map[namespace.Name]namespace.Namespace{},
		keys:       map[secret.ItemRef]secret.ItemKey{},
		mu:         sync.Mutex{},
	}
}

func (m *memItemKeys) CreateNamespace(_ context.Context, ns namespace.Namespace) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.namespaces[ns.Name]; ok {
		return namespace.ErrAlreadyExists
	}

	m.namespaces[ns.Name] = ns

	return nil
}

func (m *memItemKeys) GetNamespace(_ context.Context, name namespace.Name) (namespace.Namespace, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	ns, ok := m.namespaces[name]
	if !ok {
		return namespace.Namespace{}, namespace.ErrNotFound
	}

	return ns, nil
}

func (m *memItemKeys) SetOverrides(context.Context, namespace.Name, namespace.Overrides, namespace.Fence) error {
	return nil
}

func (m *memItemKeys) ListNamespaces(context.Context) ([]namespace.Namespace, error) {
	return nil, nil
}

func (m *memItemKeys) Ping(context.Context) error {
	return nil
}

func (m *memItemKeys) CreateItemKey(
	_ context.Context, k secret.ItemKey, fence namespace.Fence,
) (secret.ItemKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkFence(fence); err != nil {
		return secret.ItemKey{}, err
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

func (m *memItemKeys) GetItemKey(_ context.Context, ref secret.ItemRef) (secret.ItemKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	k, ok := m.keys[ref]
	if !ok {
		return secret.ItemKey{}, secret.ErrKeyNotFound
	}

	if k.Shredded() {
		return k, secret.ErrShredded
	}

	return k, nil
}

func (m *memItemKeys) ShredItemKey(
	_ context.Context, ref secret.ItemRef, at time.Time, fence namespace.Fence,
) (secret.ItemKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkFence(fence); err != nil {
		return secret.ItemKey{}, err
	}

	k, ok := m.keys[ref]
	if !ok {
		return secret.ItemKey{}, secret.ErrKeyNotFound
	}

	if !k.Shredded() {
		k.ShreddedAt = at
		m.keys[ref] = k
	}

	return k, nil
}

func (m *memItemKeys) ListItemKeys(
	_ context.Context, scope secret.Scope, after secret.ItemKeyID, limit int,
) ([]secret.ItemKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var out []secret.ItemKey

	for ref := range m.keys {
		k := m.keys[ref]
		if ref.Scope == scope && k.ID > after {
			out = append(out, k)
		}
	}

	slices.SortFunc(out, func(a, b secret.ItemKey) int { return cmp.Compare(a.ID, b.ID) })

	return out[:min(limit, len(out))], nil
}

func (m *memItemKeys) ListItemKeyWrapperIDs(context.Context) ([]string, error) {
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

// checkFence answers namespace.ErrFenced for a fence older than the
// namespace. The caller holds the lock.
func (m *memItemKeys) checkFence(fence namespace.Fence) error {
	ns, ok := m.namespaces[fence.Namespace]
	if !ok {
		return namespace.ErrNotFound
	}

	if fence.FailoverVersion != ns.FailoverVersion {
		return namespace.ErrFenced
	}

	return nil
}
