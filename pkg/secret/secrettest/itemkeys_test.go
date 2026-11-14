package secrettest_test

import (
	"cmp"
	"context"
	"errors"
	"maps"
	"slices"
	"sync"
	"testing"
	"time"

	blobfs "opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/secret/secrettest"
)

// TestItemKeySuite runs the suite against an in-memory store, so the
// cases state their rules before a SQL adapter implements the port.
func TestItemKeySuite(t *testing.T) {
	t.Parallel()
	// Real time: the case of a late commit proves that a real context deadline ends it.
	secrettest.RunItemKeys(t, func(*testing.T) secrettest.ItemKeyStore { return newMemItemKeys(time.Now) })
}

// fixedNow is the clock of the suites that need no real time.
func fixedNow() time.Time { return time.UnixMilli(1_700_000_000_000).UTC() }

// memItemKeys keeps namespaces and item key records in memory.
type memItemKeys struct {
	namespaces map[namespace.Name]namespace.Namespace
	keys       map[secret.ItemRef]secret.ItemKey
	// now is the clock of the attempt deadline. Real time only for the item key
	// suite: its late commit case proves that a real context deadline ends it.
	now func() time.Time
	mu  sync.Mutex
}

func newMemItemKeys(now func() time.Time) *memItemKeys {
	return &memItemKeys{
		namespaces: map[namespace.Name]namespace.Namespace{},
		keys:       map[secret.ItemRef]secret.ItemKey{},
		now:        now,
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

// memItemKeyTx is the transaction of the in-memory store. The caller of
// InItemKeyTx holds the lock.
type memItemKeyTx struct {
	m     *memItemKeys
	fence namespace.Fence
}

func (t memItemKeyTx) CreateItemKey(_ context.Context, k secret.ItemKey) (secret.ItemKey, error) {
	if k.Ref.Scope.Namespace != t.fence.Namespace {
		return secret.ItemKey{}, namespace.ErrFenced
	}

	if b := k.Blob; b.Version < 1 || b.Version > secret.MaxItemKeyVersion || b.Size < 1 ||
		b.Size > secret.MaxWrappedItemKeyBytes || !secret.ValidItemKeyAttempt(b.Attempt) {
		return secret.ItemKey{}, secret.ErrInvalidItemRef
	}

	if stored, ok := t.m.keys[k.Ref]; ok {
		if stored.Shredded() {
			return stored, secret.ErrShredded
		}

		return stored, nil
	}

	t.m.keys[k.Ref] = k

	return k, nil
}

func (t memItemKeyTx) GetNamespace(_ context.Context, name namespace.Name) (namespace.Namespace, error) {
	ns, ok := t.m.namespaces[name]
	if !ok {
		return namespace.Namespace{}, namespace.ErrNotFound
	}

	return ns, nil
}

func (t memItemKeyTx) GetItemKey(_ context.Context, ref secret.ItemRef) (secret.ItemKey, error) {
	k, ok := t.m.keys[ref]
	if !ok {
		return secret.ItemKey{}, secret.ErrKeyNotFound
	}

	if k.Shredded() {
		return k, secret.ErrShredded
	}

	return k, nil
}

// InItemKeyTx runs fn on a copy of the records and keeps the copy only when fn
// returns nil before the attempt deadline, so a failed or late function leaves
// no record.
func (m *memItemKeys) InItemKeyTx(ctx context.Context, fence namespace.Fence, fn func(secret.ItemKeyTx) error) error {
	deadline := secret.AttemptDeadline(ctx, m.now())

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkFence(fence); err != nil {
		return err
	}

	before := maps.Clone(m.keys)

	if err := fn(memItemKeyTx{m: m, fence: fence}); err != nil {
		m.keys = before

		return err
	}

	if err := secret.CheckAttemptDeadline(deadline, m.now()); err != nil {
		m.keys = before

		return err
	}

	return nil
}

func (m *memItemKeys) CreateItemKey(
	ctx context.Context, k secret.ItemKey, fence namespace.Fence,
) (secret.ItemKey, error) {
	var stored secret.ItemKey

	err := m.InItemKeyTx(ctx, fence, func(tx secret.ItemKeyTx) error {
		got, err := tx.CreateItemKey(ctx, k)
		stored = got

		return err
	})
	if err != nil && !errors.Is(err, secret.ErrShredded) {
		return secret.ItemKey{}, err
	}

	return stored, err
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

	if ref.Scope.Namespace != fence.Namespace {
		return secret.ItemKey{}, namespace.ErrFenced
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

// TestItemSealerSuite runs the sealer suite over the in-memory records and a
// directory of blobs.
func TestItemSealerSuite(t *testing.T) {
	t.Parallel()
	secrettest.RunItemSealer(t, func(t *testing.T) *secrettest.SealerRig {
		t.Helper()

		blobs, err := blobfs.Open(t.TempDir())
		if err != nil {
			t.Fatalf("open blob store: %v", err)
		}

		t.Cleanup(func() { _ = blobs.Close() }) //nolint:errcheck // Test cleanup.

		return secrettest.NewSealerRig(t, newMemItemKeys(fixedNow), blobs)
	})
}
