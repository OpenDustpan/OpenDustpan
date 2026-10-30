package profiletest_test

import (
	"context"
	"slices"
	"sync"
	"testing"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/profile"
	"opendustpan.org/opendustpan/pkg/profile/profiletest"
)

// TestMemoryIssuerStore runs the issuer suite against the reference store in
// memory.
func TestMemoryIssuerStore(t *testing.T) {
	t.Parallel()

	profiletest.RunIssuer(t, func(t *testing.T) profiletest.IssuerStore {
		t.Helper()

		m := &memIssuer{
			mu: sync.Mutex{}, namespaces: map[namespace.Name]namespace.Namespace{},
			keys: map[namespace.Name][]profile.Key{}, settings: map[namespace.Name]profile.IssuerSettings{},
		}

		return profiletest.IssuerStore{Issuer: m, Namespaces: memIssuerNamespaces{m: m}}
	})
}

// memIssuer is the reference store of the issuer: keys, settings and the
// namespaces over maps.
type memIssuer struct {
	mu         sync.Mutex
	namespaces map[namespace.Name]namespace.Namespace
	keys       map[namespace.Name][]profile.Key
	settings   map[namespace.Name]profile.IssuerSettings
}

type memIssuerNamespaces struct{ m *memIssuer }

func (n memIssuerNamespaces) CreateNamespace(_ context.Context, ns namespace.Namespace) error {
	n.m.mu.Lock()
	defer n.m.mu.Unlock()

	n.m.namespaces[ns.Name] = ns

	return nil
}

func (n memIssuerNamespaces) GetNamespace(_ context.Context, name namespace.Name) (namespace.Namespace, error) {
	n.m.mu.Lock()
	defer n.m.mu.Unlock()

	ns, ok := n.m.namespaces[name]
	if !ok {
		return namespace.Namespace{}, namespace.ErrNotFound
	}

	return ns, nil
}

func (memIssuerNamespaces) SetOverrides(context.Context, namespace.Name, namespace.Overrides, namespace.Fence) error {
	return nil
}

func (memIssuerNamespaces) ListNamespaces(context.Context) ([]namespace.Namespace, error) {
	return nil, nil
}

func (memIssuerNamespaces) Ping(context.Context) error { return nil }

// begin checks the fence and the entries of a write.
func (m *memIssuer) begin(f namespace.Fence, es []audit.Entry) error {
	ns, ok := m.namespaces[f.Namespace]
	if !ok {
		return namespace.ErrNotFound
	}

	if ns.FailoverVersion != f.FailoverVersion {
		return namespace.ErrFenced
	}

	if len(es) == 0 {
		return profile.ErrInvalid
	}

	for _, e := range es {
		if err := e.Validate(); err != nil || e.Namespace != f.Namespace {
			return audit.ErrInvalidEntry
		}
	}

	return nil
}

func (m *memIssuer) NamespaceByID(_ context.Context, id namespace.ID) (namespace.Namespace, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, ns := range m.namespaces {
		if ns.ID == id {
			return ns, nil
		}
	}

	return namespace.Namespace{}, namespace.ErrNotFound
}

func (m *memIssuer) read(ns namespace.Name, keep func(profile.Key) bool, secret bool) []profile.Key {
	var out []profile.Key

	for _, k := range m.keys[ns] {
		if !keep(k) {
			continue
		}

		if !secret {
			k.Sealed = nil
		}

		out = append(out, k)
	}

	return out
}

func (m *memIssuer) Published(_ context.Context, ns namespace.Name) ([]profile.Key, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.read(ns, func(k profile.Key) bool { return k.State.Published() }, false), nil
}

func (m *memIssuer) Live(_ context.Context, ns namespace.Name) ([]profile.Key, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.read(ns, func(k profile.Key) bool { return k.State.Published() }, true), nil
}

func (m *memIssuer) History(_ context.Context, ns namespace.Name) ([]profile.KeyInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var out []profile.KeyInfo
	for _, k := range m.keys[ns] {
		out = append(out, k.Info())
	}

	return out, nil
}

func (m *memIssuer) Apply(
	_ context.Context, ns namespace.Name, ch profile.KeyChange, f namespace.Fence, es []audit.Entry,
) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.begin(f, es); err != nil {
		return err
	}

	next, err := profile.ApplyKeys(ns, m.keys[ns], ch)
	if err != nil {
		return err
	}

	m.keys[ns] = next

	return nil
}

func (m *memIssuer) GetIssuerSettings(_ context.Context, ns namespace.Name) (profile.IssuerSettings, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.namespaces[ns]; !ok {
		return profile.IssuerSettings{}, namespace.ErrNotFound
	}

	if s, ok := m.settings[ns]; ok {
		return s, nil
	}

	return profile.DefaultIssuerSettings(ns), nil
}

func (m *memIssuer) PutIssuerSettings(
	_ context.Context, s profile.IssuerSettings, ifVersion int64, f namespace.Fence, es []audit.Entry,
) (profile.IssuerSettings, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.begin(f, es); err != nil {
		return profile.IssuerSettings{}, err
	}

	cur, ok := m.settings[s.Namespace]
	if !ok {
		cur = profile.DefaultIssuerSettings(s.Namespace)
	}

	if cur.Version != ifVersion {
		return profile.IssuerSettings{}, profile.ErrConflict
	}

	s.Version = ifVersion + 1
	s.CopyCheck = cur.CopyCheck
	m.settings[s.Namespace] = s

	return s, nil
}

func (m *memIssuer) RecordCopyCheck(
	_ context.Context, ns namespace.Name, c profile.CopyCheck, f namespace.Fence, es []audit.Entry,
) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.begin(f, es); err != nil {
		return err
	}

	cur, ok := m.settings[ns]
	if !ok {
		cur = profile.DefaultIssuerSettings(ns)
	}

	c.Verified = slices.DeleteFunc(slices.Clone(c.Verified), func(id profile.KeyID) bool {
		return !slices.ContainsFunc(m.keys[ns], func(k profile.Key) bool { return k.ID == id && k.State != profile.KeyDeleted })
	})
	cur.CopyCheck = &c
	m.settings[ns] = cur

	return nil
}
