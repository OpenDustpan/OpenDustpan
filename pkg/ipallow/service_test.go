package ipallow_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/ipallow"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// fakeStore is an in-memory ipallow.Store and namespace.Store that follows
// their contracts.
type fakeStore struct {
	mu         sync.Mutex
	namespaces map[namespace.Name]namespace.Namespace
	lists      map[namespace.Name]ipallow.List
	entries    []audit.Entry
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		mu:         sync.Mutex{},
		namespaces: map[namespace.Name]namespace.Namespace{},
		lists:      map[namespace.Name]ipallow.List{},
		entries:    []audit.Entry{},
	}
}

// CreateNamespace implements namespace.Store.
func (f *fakeStore) CreateNamespace(_ context.Context, ns namespace.Namespace) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if _, ok := f.namespaces[ns.Name]; ok {
		return namespace.ErrAlreadyExists
	}

	f.namespaces[ns.Name] = ns

	return nil
}

// GetNamespace implements namespace.Store.
func (f *fakeStore) GetNamespace(_ context.Context, name namespace.Name) (namespace.Namespace, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	ns, ok := f.namespaces[name]
	if !ok {
		return namespace.Namespace{}, namespace.ErrNotFound
	}

	return ns, nil
}

// SetOverrides implements namespace.Store.
func (f *fakeStore) SetOverrides(
	_ context.Context, name namespace.Name, ov namespace.Overrides, fence namespace.Fence,
) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if err := f.checkFence(fence); err != nil {
		return err
	}

	ns, ok := f.namespaces[name]
	if !ok {
		return namespace.ErrNotFound
	}

	ns.Overrides = ov
	f.namespaces[name] = ns

	return nil
}

// ListNamespaces implements namespace.Store.
func (f *fakeStore) ListNamespaces(_ context.Context) ([]namespace.Namespace, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	out := make([]namespace.Namespace, 0, len(f.namespaces))
	for _, ns := range f.namespaces {
		out = append(out, ns)
	}

	return out, nil
}

// Ping implements namespace.Store.
func (f *fakeStore) Ping(_ context.Context) error {
	return nil
}

// NamespaceList implements ipallow.Store.
func (f *fakeStore) NamespaceList(_ context.Context, ns namespace.Name) (ipallow.List, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if _, ok := f.namespaces[ns]; !ok {
		return ipallow.List{}, namespace.ErrNotFound
	}

	return f.lists[ns], nil
}

// NamespacesWithList implements ipallow.Store.
func (*fakeStore) NamespacesWithList(context.Context) ([]namespace.Name, error) {
	return nil, nil
}

// SetNamespaceList implements ipallow.Store.
func (f *fakeStore) SetNamespaceList(
	_ context.Context, ns namespace.Name, l ipallow.List, fence namespace.Fence, e audit.Entry,
) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if err := f.checkFence(fence); err != nil {
		return err
	}

	if _, ok := f.namespaces[ns]; !ok {
		return namespace.ErrNotFound
	}

	if err := e.Validate(); err != nil {
		return err
	}

	if e.Namespace != ns {
		return fmt.Errorf("%w: entry names %q, want %q", audit.ErrInvalidEntry, e.Namespace, ns)
	}

	f.lists[ns] = l
	f.entries = append(f.entries, e)

	return nil
}

// checkFence returns ErrNotFound for an unknown namespace and ErrFenced for a
// stale failover version. The caller holds the lock.
func (f *fakeStore) checkFence(fence namespace.Fence) error {
	ns, ok := f.namespaces[fence.Namespace]
	if !ok {
		return namespace.ErrNotFound
	}

	if ns.FailoverVersion != fence.FailoverVersion {
		return namespace.ErrFenced
	}

	return nil
}

// fixture holds a service with a fixed clock over one namespace.
type fixture struct {
	svc   *ipallow.Service
	store *fakeStore
	now   time.Time
	ns    namespace.Name
}

// newFixture returns a service over a fake store holding the namespace acme,
// whose primary installation is the local installation.
func newFixture(t *testing.T, enabled bool) fixture {
	t.Helper()

	store := newFakeStore()
	fixed := time.Date(2026, time.October, 14, 12, 0, 0, 0, time.UTC)

	namespaces, nsErr := namespace.NewService(namespace.Config{Store: store, Now: nil, Installation: "local"})
	if nsErr != nil {
		t.Fatalf("namespace service: %v", nsErr)
	}

	if _, createErr := namespaces.Create(t.Context(), "acme"); createErr != nil {
		t.Fatalf("create namespace: %v", createErr)
	}

	svc, err := ipallow.NewService(ipallow.Config{
		Store:      store,
		Namespaces: namespaces,
		Now:        func() time.Time { return fixed },
		Enabled:    enabled,
	})
	if err != nil {
		t.Fatalf("ipallow service: %v", err)
	}

	return fixture{svc: svc, store: store, now: fixed, ns: "acme"}
}

// testEntry returns the audit entry the tests pass: the action is set by the
// caller, the service stamps the time and the namespace.
func testEntry() audit.Entry {
	return audit.Entry{
		At:            time.Time{},
		Namespace:     "",
		Subject:       "alice@example.com",
		IDPSubject:    "",
		ObjectID:      "",
		TokenID:       "",
		Action:        "namespace:set-ip-allowlist",
		Object:        "acme",
		Version:       "",
		ValuePath:     "",
		ValueCount:    nil,
		Changes:       nil,
		SourceIP:      "192.0.2.1",
		UserAgent:     "test",
		Channel:       audit.ChannelAPI,
		Kind:          audit.KindChange,
		Outcome:       audit.OutcomeSuccess,
		RequestID:     "",
		PrevHash:      "",
		Hash:          "",
		Sequence:      0,
		FenceVersion:  0,
		Reason:        "",
		PrincipalKind: "",
		JobID:         "",
	}
}

// TestNewServiceMissingDependencies checks nil Store and nil Namespaces fail.
func TestNewServiceMissingDependencies(t *testing.T) {
	t.Parallel()

	namespaces, err := namespace.NewService(namespace.Config{Store: newFakeStore(), Now: nil, Installation: "local"})
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	_, err = ipallow.NewService(ipallow.Config{
		Store:      nil,
		Namespaces: namespaces,
		Now:        nil,
		Enabled:    true,
	})
	if err == nil {
		t.Fatal("nil store: err = nil, want an error")
	}

	_, err = ipallow.NewService(ipallow.Config{
		Store:      newFakeStore(),
		Namespaces: nil,
		Now:        nil,
		Enabled:    true,
	})
	if err == nil {
		t.Fatal("nil namespaces: err = nil, want an error")
	}
}

// TestSetStoresListAndStampsEntry checks Set stores the parsed list and
// stamps the entry with the time and the namespace.
func TestSetStoresListAndStampsEntry(t *testing.T) {
	t.Parallel()

	f := newFixture(t, true)
	items := []string{"10.0.0.0/8", "2001:db8::/32"}

	got, err := f.svc.Set(t.Context(), f.ns, items, testEntry())
	if err != nil {
		t.Fatalf("set list: %v", err)
	}

	if !slices.Equal(got.Strings(), items) {
		t.Fatalf("set list = %v, want %v", got.Strings(), items)
	}

	stored, err := f.svc.Get(t.Context(), f.ns)
	if err != nil {
		t.Fatalf("get list: %v", err)
	}

	if !slices.Equal(stored.Strings(), items) {
		t.Fatalf("stored list = %v, want %v", stored.Strings(), items)
	}

	if len(f.store.entries) != 1 {
		t.Fatalf("audit entries = %d, want 1", len(f.store.entries))
	}

	entry := f.store.entries[0]
	if !entry.At.Equal(f.now) || entry.Namespace != f.ns || entry.Action != "namespace:set-ip-allowlist" {
		t.Fatalf("audit entry = %+v, want the stamped time, %q and the caller action", entry, f.ns)
	}
}

// TestSetEmptyItemsRefused checks Set with no items fails with ErrInvalidList
// and leaves the store untouched.
func TestSetEmptyItemsRefused(t *testing.T) {
	t.Parallel()

	for _, items := range [][]string{nil, {}} {
		f := newFixture(t, true)

		_, err := f.svc.Set(t.Context(), f.ns, items, testEntry())
		if !errors.Is(err, ipallow.ErrInvalidList) {
			t.Fatalf("set %q: err = %v, want ErrInvalidList", items, err)
		}

		if len(f.store.entries) != 0 {
			t.Fatalf("set %q: audit entries = %d, want none", items, len(f.store.entries))
		}

		got, getErr := f.svc.Get(t.Context(), f.ns)
		if getErr != nil {
			t.Fatalf("get list: %v", getErr)
		}

		if !got.IsEmpty() {
			t.Fatalf("set %q: stored list = %v, want no rule", items, got.Strings())
		}
	}
}

// TestSetInvalidItemsRefused checks Set with a bad item fails and writes
// nothing.
func TestSetInvalidItemsRefused(t *testing.T) {
	t.Parallel()

	f := newFixture(t, true)

	_, err := f.svc.Set(t.Context(), f.ns, []string{"nope"}, testEntry())
	if !errors.Is(err, ipallow.ErrInvalidList) {
		t.Fatalf("set a bad item: err = %v, want ErrInvalidList", err)
	}

	if len(f.store.entries) != 0 {
		t.Fatalf("audit entries = %d, want none", len(f.store.entries))
	}
}

// TestSetDisabled checks Set while the feature is off fails with ErrDisabled.
func TestSetDisabled(t *testing.T) {
	t.Parallel()

	f := newFixture(t, false)

	_, err := f.svc.Set(t.Context(), f.ns, []string{"10.0.0.0/8"}, testEntry())
	if !errors.Is(err, ipallow.ErrDisabled) {
		t.Fatalf("set while disabled: err = %v, want ErrDisabled", err)
	}

	if len(f.store.entries) != 0 {
		t.Fatalf("audit entries = %d, want none", len(f.store.entries))
	}

	if f.svc.Enabled() {
		t.Fatal("Enabled = true, want false")
	}
}

// TestRemoveWhileDisabled checks Remove works while the feature is off.
func TestRemoveWhileDisabled(t *testing.T) {
	t.Parallel()

	f := newFixture(t, true)

	if _, err := f.svc.Set(t.Context(), f.ns, []string{"10.0.0.0/8"}, testEntry()); err != nil {
		t.Fatalf("set list: %v", err)
	}

	off, err := ipallow.NewService(ipallow.Config{
		Store:      f.store,
		Namespaces: serviceNamespaces(t, f.store),
		Now:        func() time.Time { return f.now },
		Enabled:    false,
	})
	if err != nil {
		t.Fatalf("disabled service: %v", err)
	}

	if removeErr := off.Remove(t.Context(), f.ns, testEntry()); removeErr != nil {
		t.Fatalf("remove while disabled: %v", removeErr)
	}

	got, err := off.Get(t.Context(), f.ns)
	if err != nil {
		t.Fatalf("get list: %v", err)
	}

	if !got.IsEmpty() {
		t.Fatalf("stored list = %v, want no rule", got.Strings())
	}
}

// serviceNamespaces returns the namespace service of the fixture store, for a
// second ipallow service over the same data.
func serviceNamespaces(t *testing.T, store *fakeStore) *namespace.Service {
	t.Helper()

	namespaces, err := namespace.NewService(namespace.Config{Store: store, Now: nil, Installation: "local"})
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	return namespaces
}

// TestUnknownNamespace checks Get, Set and Remove of an unknown namespace
// give namespace.ErrNotFound.
func TestUnknownNamespace(t *testing.T) {
	t.Parallel()

	f := newFixture(t, true)

	if _, err := f.svc.Get(t.Context(), "ghost"); !errors.Is(err, namespace.ErrNotFound) {
		t.Fatalf("get an unknown namespace: err = %v, want ErrNotFound", err)
	}

	_, err := f.svc.Set(t.Context(), "ghost", []string{"10.0.0.0/8"}, testEntry())
	if !errors.Is(err, namespace.ErrNotFound) {
		t.Fatalf("set an unknown namespace: err = %v, want ErrNotFound", err)
	}

	if err := f.svc.Remove(t.Context(), "ghost", testEntry()); !errors.Is(err, namespace.ErrNotFound) {
		t.Fatalf("remove an unknown namespace: err = %v, want ErrNotFound", err)
	}
}

// TestGetWithoutRule checks Get of a namespace without a rule gives the zero
// List.
func TestGetWithoutRule(t *testing.T) {
	t.Parallel()

	f := newFixture(t, true)

	got, err := f.svc.Get(t.Context(), f.ns)
	if err != nil {
		t.Fatalf("get list: %v", err)
	}

	if !got.IsEmpty() {
		t.Fatalf("stored list = %v, want no rule", got.Strings())
	}
}
