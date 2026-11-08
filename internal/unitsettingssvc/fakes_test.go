package unitsettingssvc_test

import (
	"context"
	"sync"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/forge"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/runner"
	"opendustpan.org/opendustpan/pkg/unitsettings"
)

// memStore is a unitsettings.Store in memory for one namespace. It keeps the
// entries it was given, so a test sees what the service stamped on them.
type memStore struct {
	mu       sync.Mutex
	records  map[string]unitsettings.Settings
	defaults unitsettings.NamespaceDefaults
	entries  []audit.Entry
	fences   []namespace.Fence
	// defaultsAtPut, when set, replaces the defaults at the start of every
	// Put, as another writer that commits between the read of the service and
	// the write. The write then returns the new defaults.
	defaultsAtPut *unitsettings.NamespaceDefaults
}

func newMemStore() *memStore {
	return &memStore{
		mu:            sync.Mutex{},
		records:       map[string]unitsettings.Settings{},
		defaults:      unitsettings.NamespaceDefaults{Namespace: "", Pool: "", Version: 0, UpdatedAt: time.Time{}},
		entries:       nil,
		fences:        nil,
		defaultsAtPut: nil,
	}
}

func (m *memStore) Get(_ context.Context, unit identity.Object) (unitsettings.Settings, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	s, ok := m.records[unit.UnitID()]
	if !ok {
		return unitsettings.Settings{}, unitsettings.ErrNotFound
	}

	return s, nil
}

func (m *memStore) Put(
	_ context.Context, s *unitsettings.Settings, expected int64, fence namespace.Fence, e audit.Entry,
) (unitsettings.PutResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.defaultsAtPut != nil {
		m.defaults = *m.defaultsAtPut
	}

	old := m.records[s.Unit.UnitID()]
	if old.Version != expected {
		return unitsettings.PutResult{}, unitsettings.ErrConflict
	}

	next := *s
	next.Version = expected + 1
	next.UpdatedAt = e.At
	m.records[s.Unit.UnitID()] = next
	m.entries = append(m.entries, e)
	m.fences = append(m.fences, fence)

	defaults := m.defaults
	defaults.Namespace = s.Unit.Namespace()

	return unitsettings.PutResult{Settings: next, Defaults: defaults}, nil
}

func (m *memStore) Delete(
	_ context.Context, unit identity.Object, expected int64, fence namespace.Fence, e audit.Entry,
) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	old, ok := m.records[unit.UnitID()]
	if !ok {
		return unitsettings.ErrNotFound
	}

	if old.Version != expected {
		return unitsettings.ErrConflict
	}

	delete(m.records, unit.UnitID())
	m.entries = append(m.entries, e)
	m.fences = append(m.fences, fence)

	return nil
}

func (m *memStore) Defaults(context.Context, namespace.Name) (unitsettings.NamespaceDefaults, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.defaults, nil
}

func (m *memStore) SetDefaults(
	_ context.Context, d unitsettings.NamespaceDefaults, expected int64, fence namespace.Fence, e audit.Entry,
) (unitsettings.NamespaceDefaults, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.defaults.Version != expected {
		return unitsettings.NamespaceDefaults{}, unitsettings.ErrConflict
	}

	d.Version = expected + 1
	d.UpdatedAt = e.At
	m.defaults = d
	m.entries = append(m.entries, e)
	m.fences = append(m.fences, fence)

	return d, nil
}

// fixedFences gives the fence of version 7.
type fixedFences struct{}

func (fixedFences) Fence(_ context.Context, ns namespace.Name) (namespace.Fence, error) {
	return namespace.Fence{Namespace: ns, FailoverVersion: 7}, nil
}

// memConnections holds the connections of acme.
type memConnections struct{ records []forge.ConnectionRecord }

func (m memConnections) GetConnection(
	_ context.Context, ns namespace.Name, id string,
) (forge.ConnectionRecord, error) {
	for i := range m.records {
		if m.records[i].Namespace == ns && m.records[i].ID == id {
			return m.records[i], nil
		}
	}

	return forge.ConnectionRecord{}, forge.ErrConnectionNotFound
}

func (m memConnections) ListConnections(_ context.Context, ns namespace.Name) ([]forge.ConnectionRecord, error) {
	var out []forge.ConnectionRecord

	for i := range m.records {
		if m.records[i].Namespace == ns {
			out = append(out, m.records[i])
		}
	}

	return out, nil
}

// memPools holds runner pools.
type memPools struct{ pools []runner.Pool }

func (m memPools) Pool(_ context.Context, id string) (runner.Pool, error) {
	for i := range m.pools {
		if m.pools[i].ID == id {
			return m.pools[i], nil
		}
	}

	return runner.Pool{}, runner.ErrNotFound
}

func (m memPools) Pools(_ context.Context, ns namespace.Name) ([]runner.Pool, error) {
	var out []runner.Pool

	for i := range m.pools {
		if m.pools[i].Namespace == ns {
			out = append(out, m.pools[i])
		}
	}

	return out, nil
}

func fixedNow() time.Time { return time.Unix(1_800_000_000, 0) }

// connection is a connection of the namespace ns.
func connection(ns namespace.Name, id string, push, isDefault bool) forge.ConnectionRecord {
	return forge.ConnectionRecord{
		CreatedAt: time.Unix(0, 0), UpdatedAt: time.Unix(0, 0), Namespace: ns, ID: id, Kind: forge.KindGitHub,
		Auth: forge.AuthApp, Scope: forge.AppScopeInstallation, InstallationID: "1", Version: 1, Push: push,
		Default: isDefault,
	}
}

// pool is a runner pool of the namespace ns.
func pool(id string, ns namespace.Name, purpose runner.Purpose) runner.Pool {
	return runner.Pool{
		ID: id, Namespace: ns, Name: "ci", Purpose: purpose, Lease: 0, Concurrency: 0, Isolation: nil,
		GitCredentials: runner.GitCredentials{Mode: "", Helper: ""}, CreatedAt: time.Unix(0, 0),
		UpdatedAt: time.Unix(0, 0),
	}
}

// settingsFor is the empty record of unit: no field is set.
func settingsFor(unit identity.Object) unitsettings.Settings {
	return unitsettings.Settings{
		Unit: unit, Executor: "", Pool: "",
		CodeLocation:   unitsettings.CodeLocation{Connection: "", Repository: "", Ref: "", Directory: ""},
		WriteBack:      unitsettings.WriteBack{Delivery: "", Files: nil},
		ReviewRequests: unitsettings.ReviewRequests{Enabled: false}, Steps: nil, Version: 0, UpdatedAt: time.Time{},
	}
}
