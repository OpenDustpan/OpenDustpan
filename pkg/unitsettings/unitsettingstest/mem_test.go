package unitsettingstest_test

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/idempotency"
	"opendustpan.org/opendustpan/pkg/idempotency/idempotencytest"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/unitsettings"
	"opendustpan.org/opendustpan/pkg/unitsettings/unitsettingstest"
)

// memFence is the failover version of a namespace in memory.
const memFence = 5

// memSpace is one namespace of memStore: its units, records, defaults and the
// audit entries of its writes.
type memSpace struct {
	units    map[string]string // stable ID to reference text
	records  map[string]unitsettings.Settings
	defaults unitsettings.NamespaceDefaults
	audit    []audit.Entry
}

// memStore is a unitsettings.Store in memory. It proves that the cases hold
// for a correct adapter.
type memStore struct {
	spaces  map[namespace.Name]*memSpace
	records *idempotencytest.Memory
	mu      sync.Mutex
}

var _ unitsettings.Store = (*memStore)(nil)

func newMemStore() *memStore {
	return &memStore{spaces: map[namespace.Name]*memSpace{}, records: idempotencytest.NewMemory(), mu: sync.Mutex{}}
}

// Get implements unitsettings.Store.
func (m *memStore) Get(_ context.Context, unit identity.Object) (unitsettings.Settings, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	sp, ok := m.spaces[unit.Namespace()]
	if !ok || sp.units[unit.UnitID()] != unit.String() {
		return zeroSettings(), unitsettings.ErrUnknownUnit
	}

	s, ok := sp.records[unit.UnitID()]
	if !ok {
		return zeroSettings(), unitsettings.ErrNotFound
	}

	return s, nil
}

// Put implements unitsettings.Store.
func (m *memStore) Put(
	ctx context.Context, s *unitsettings.Settings, expected int64, fence namespace.Fence, e audit.Entry,
) (unitsettings.PutResult, error) {
	if err := s.Validate(); err != nil {
		return unitsettings.PutResult{}, err
	}

	if err := checkEntry(fence, e); err != nil {
		return unitsettings.PutResult{}, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	sp, err := m.space(s.Unit.Namespace(), fence)
	if err != nil {
		return unitsettings.PutResult{}, err
	}

	if sp.units[s.Unit.UnitID()] != s.Unit.String() {
		return unitsettings.PutResult{}, unitsettings.ErrUnknownUnit
	}

	// The defaults come from the same critical section as the write, as the
	// SQL adapters read them in the transaction of the write.
	defaults := sp.defaults
	defaults.Namespace = s.Unit.Namespace()

	old, has := sp.records[s.Unit.UnitID()]
	if old.Version != expected {
		return unitsettings.PutResult{}, unitsettings.ErrConflict
	}

	if has && unitsettings.Equal(&old, s) {
		result := unitsettings.PutResult{Settings: old, Defaults: defaults}

		if err := m.finish(ctx, result); err != nil {
			return unitsettings.PutResult{}, err
		}

		return result, nil
	}

	var prev *unitsettings.Settings
	if has {
		prev = &old
	}

	next := *s
	next.Version = expected + 1
	next.UpdatedAt = e.At

	result := unitsettings.PutResult{Settings: next, Defaults: defaults}

	// The answer is the last step. A lost record changes nothing.
	if err := m.finish(ctx, result); err != nil {
		return unitsettings.PutResult{}, err
	}

	sp.records[s.Unit.UnitID()] = next
	sp.audit = append(sp.audit, unitsettings.ChangeEntry(e, prev, &next))

	return result, nil
}

// Delete implements unitsettings.Store.
func (m *memStore) Delete(
	ctx context.Context, unit identity.Object, expected int64, fence namespace.Fence, e audit.Entry,
) error {
	if err := checkEntry(fence, e); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	sp, err := m.space(unit.Namespace(), fence)
	if err != nil {
		return err
	}

	old, ok := sp.records[unit.UnitID()]
	if !ok {
		return unitsettings.ErrNotFound
	}

	if old.Version != expected {
		return unitsettings.ErrConflict
	}

	var gone unitsettings.Settings

	gone.Unit = unit

	if err := m.finish(ctx, gone); err != nil {
		return err
	}

	delete(sp.records, unit.UnitID())
	sp.audit = append(sp.audit, unitsettings.ResetEntry(e, &old))

	return nil
}

// Defaults implements unitsettings.Store.
func (m *memStore) Defaults(_ context.Context, ns namespace.Name) (unitsettings.NamespaceDefaults, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	sp, ok := m.spaces[ns]
	if !ok {
		return emptyDefaults(), namespace.ErrNotFound
	}

	d := sp.defaults
	d.Namespace = ns

	return d, nil
}

// SetDefaults implements unitsettings.Store.
func (m *memStore) SetDefaults(
	ctx context.Context, d unitsettings.NamespaceDefaults, expected int64, fence namespace.Fence, e audit.Entry,
) (unitsettings.NamespaceDefaults, error) {
	if err := d.Validate(); err != nil {
		return emptyDefaults(), err
	}

	if err := checkEntry(fence, e); err != nil {
		return emptyDefaults(), err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	sp, err := m.space(d.Namespace, fence)
	if err != nil {
		return emptyDefaults(), err
	}

	if sp.defaults.Version != expected {
		return emptyDefaults(), unitsettings.ErrConflict
	}

	if sp.defaults.Pool == d.Pool {
		if err := m.finish(ctx, sp.defaults); err != nil {
			return emptyDefaults(), err
		}

		return sp.defaults, nil
	}

	old := sp.defaults
	next := unitsettings.NamespaceDefaults{
		Namespace: d.Namespace,
		Pool:      d.Pool,
		Version:   expected + 1,
		UpdatedAt: e.At,
	}

	if err := m.finish(ctx, next); err != nil {
		return emptyDefaults(), err
	}

	sp.defaults = next
	sp.audit = append(sp.audit, unitsettings.DefaultsEntry(e, old, sp.defaults))

	return sp.defaults, nil
}

func (m *memStore) harness() unitsettingstest.Harness {
	return unitsettingstest.Harness{
		Records:   m.records,
		Store:     m,
		Namespace: m.makeNamespace,
		Unit:      m.makeUnit,
		Audit:     m.entries,
	}
}

func (m *memStore) makeNamespace(t *testing.T, name string) namespace.Fence {
	t.Helper()

	ns, err := namespace.ParseName(name)
	if err != nil {
		t.Fatal(err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.spaces[ns]; !ok {
		m.spaces[ns] = &memSpace{
			units: map[string]string{}, records: map[string]unitsettings.Settings{},
			defaults: emptyDefaults(), audit: nil,
		}
	}

	return namespace.Fence{Namespace: ns, FailoverVersion: memFence}
}

func (m *memStore) makeUnit(t *testing.T, nsName, tool, name string) identity.Object {
	t.Helper()

	ns, err := namespace.ParseName(nsName)
	if err != nil {
		t.Fatal(err)
	}

	ref, err := identity.UnitObject(tool, ns, name)
	if err != nil {
		t.Fatal(err)
	}

	id := "unit-" + tool + "-" + name

	m.mu.Lock()
	defer m.mu.Unlock()

	sp, ok := m.spaces[ns]
	if !ok {
		t.Fatalf("namespace %s does not exist", nsName)
	}

	sp.units[id] = ref.String()

	return ref.WithUnitID(id)
}

func (m *memStore) entries(t *testing.T, nsName string) []audit.Entry {
	t.Helper()

	ns, err := namespace.ParseName(nsName)
	if err != nil {
		t.Fatal(err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	sp, ok := m.spaces[ns]
	if !ok {
		t.Fatalf("namespace %s does not exist", nsName)
	}

	return append([]audit.Entry(nil), sp.audit...)
}

// finish stores the answer of the request in ctx, when it carries one. It runs
// before the write is applied, so an error leaves the write undone.
func (m *memStore) finish(ctx context.Context, result any) error {
	c, ok := idempotency.CompletionFrom(ctx)
	if !ok {
		return nil
	}

	err := c.Finish(result, func(token string, a idempotency.Answer) error {
		return m.records.Complete(ctx, c.Ref, token, a)
	})
	if err != nil {
		return fmt.Errorf("store the answer: %w", err)
	}

	return nil
}

// space returns the namespace ns after it checked the fence.
func (m *memStore) space(ns namespace.Name, fence namespace.Fence) (*memSpace, error) {
	sp, ok := m.spaces[ns]
	if !ok {
		return nil, namespace.ErrNotFound
	}

	if fence.Namespace != ns || fence.FailoverVersion != memFence {
		return nil, namespace.ErrFenced
	}

	return sp, nil
}

// checkEntry refuses an audit entry that fails validation or that names another
// namespace than the fence.
func checkEntry(fence namespace.Fence, e audit.Entry) error {
	if err := e.Validate(); err != nil {
		return fmt.Errorf("%w: audit entry: %w", unitsettings.ErrInvalid, err)
	}

	if e.Namespace != fence.Namespace {
		return fmt.Errorf("%w: entry names %q: %w", unitsettings.ErrInvalid, e.Namespace, audit.ErrInvalidEntry)
	}

	return nil
}

func zeroSettings() unitsettings.Settings {
	var s unitsettings.Settings

	return s
}

func TestSuiteOverAMemoryStore(t *testing.T) {
	t.Parallel()

	unitsettingstest.Run(t, func(t *testing.T) unitsettingstest.Harness {
		t.Helper()

		return newMemStore().harness()
	})
}

func TestCaseNamesAreUnique(t *testing.T) {
	t.Parallel()

	seen := map[string]bool{}

	for _, name := range unitsettingstest.CaseNames() {
		if seen[name] {
			t.Fatalf("case %q is in the suite twice", name)
		}

		seen[name] = true
	}

	if len(seen) == 0 {
		t.Fatal("the suite has no case")
	}
}

func emptyDefaults() unitsettings.NamespaceDefaults {
	var d unitsettings.NamespaceDefaults

	return d
}
