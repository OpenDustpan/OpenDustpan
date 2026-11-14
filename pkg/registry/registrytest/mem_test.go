package registrytest_test

import (
	"bytes"
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/registry"
	"opendustpan.org/opendustpan/pkg/registry/registrytest"
)

type key struct {
	addr    registry.Address
	version string
}

// mem is the reference adapter of Store. It runs the suite before any SQL
// adapter exists.
type mem struct {
	trust    *memKeys
	fences   map[namespace.Name]int64
	releases map[key]registry.Release
	entries  map[namespace.Name][]audit.Entry
	mu       sync.Mutex
}

func (m *mem) Publish(_ context.Context, r registry.Release, fence namespace.Fence, e audit.Entry) error {
	if err := r.Validate(); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if fence.Namespace != r.Address.Namespace || m.fences[fence.Namespace] != fence.FailoverVersion {
		return namespace.ErrFenced
	}

	if r.KeyFingerprint != "" && !m.trust.holds(r.Address.Namespace, r.KeyFingerprint) {
		return registry.ErrKeyNotFound
	}

	k := key{r.Address, r.Version}
	if old, ok := m.releases[k]; ok {
		if old.SameContent(r) {
			return nil
		}

		return registry.ErrVersionExists
	}

	r.Archives = slices.Clone(r.Archives)
	m.releases[k] = r
	m.entries[r.Address.Namespace] = append(m.entries[r.Address.Namespace], e)

	return nil
}

func (m *mem) Get(_ context.Context, addr registry.Address, version string) (registry.Release, error) {
	if err := addr.Validate(); err != nil {
		return registry.Release{}, err
	}

	if err := registry.ValidateVersion(version); err != nil {
		return registry.Release{}, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	r, ok := m.releases[key{addr, version}]
	if !ok {
		return registry.Release{}, registry.ErrNotFound
	}

	return clone(r), nil
}

// clone copies the slice of a release, so a caller never shares the memory of
// the store.
func clone(r registry.Release) registry.Release {
	r.Archives = slices.Clone(r.Archives)

	return r
}

func (m *mem) List(_ context.Context, addr registry.Address) ([]registry.Release, error) {
	if err := addr.Validate(); err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	var out []registry.Release

	for k := range m.releases {
		if k.addr == addr {
			out = append(out, clone(m.releases[k]))
		}
	}

	slices.SortFunc(out, func(a, b registry.Release) int { return registry.CompareVersions(a.Version, b.Version) })

	return out, nil
}

func (m *mem) Addresses(
	_ context.Context, ns namespace.Name, kind registry.Kind, after string, limit int,
) ([]registry.Address, string, error) {
	probe := registry.Address{Namespace: ns, Kind: kind, Name: "probe", System: ""}
	if kind == registry.KindModule {
		probe.System = "probe"
	}

	if err := probe.Validate(); err != nil {
		return nil, "", err
	}

	if limit < 1 || limit > registry.MaxAddressPage {
		return nil, "", fmt.Errorf("%w: limit %d", registry.ErrInvalid, limit)
	}

	// Defense in depth: the callers check the cursor too; the store refuses a
	// cursor that does not hold an address of the kind.
	cursor, err := registry.ParseAddressKey(ns, kind, after)
	if err != nil {
		return nil, "", err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	seen := map[registry.Address]bool{}

	for k := range m.releases {
		if k.addr.Namespace == ns && k.addr.Kind == kind {
			seen[k.addr] = true
		}
	}

	out := make([]registry.Address, 0, len(seen))
	for a := range seen {
		out = append(out, a)
	}

	slices.SortFunc(out, registry.CompareAddresses)

	if cursor.Name != "" {
		out = slices.DeleteFunc(out, func(a registry.Address) bool {
			return registry.CompareAddresses(a, cursor) <= 0
		})
	}

	// Read one address more than the page, as the SQL stores do, so that the
	// extra address tells whether a next page exists.
	out = out[:min(limit+1, len(out))]

	next := ""

	if len(out) > limit {
		out = out[:limit]
		next = registry.AddressKey(out[limit-1])
	}

	return out, next, nil
}

func TestMemStore(t *testing.T) {
	t.Parallel()

	registrytest.RunStore(t, func(*testing.T) registrytest.Harness {
		trust := &memKeys{
			fences:  map[namespace.Name]int64{},
			keys:    map[namespace.Name]map[string]registry.KeyRecord{},
			entries: map[namespace.Name][]audit.Entry{},
			mu:      sync.Mutex{},
		}
		m := &mem{
			trust:    trust,
			fences:   map[namespace.Name]int64{},
			releases: map[key]registry.Release{},
			entries:  map[namespace.Name][]audit.Entry{},
			mu:       sync.Mutex{},
		}

		return registrytest.Harness{
			Store: m,
			Keys:  trust,
			Namespace: func(_ *testing.T, name namespace.Name) namespace.Fence {
				m.mu.Lock()
				defer m.mu.Unlock()

				m.fences[name] = 1

				trust.mu.Lock()
				trust.fences[name] = 1
				trust.mu.Unlock()

				return namespace.Fence{Namespace: name, FailoverVersion: 1}
			},
			Entries: func(_ *testing.T, name namespace.Name) []audit.Entry {
				m.mu.Lock()
				defer m.mu.Unlock()

				trust.mu.Lock()
				defer trust.mu.Unlock()

				return append(slices.Clone(trust.entries[name]), m.entries[name]...)
			},
		}
	})
}

func TestVerifierSkeletonSkips(t *testing.T) {
	t.Parallel()

	registrytest.RunVerifier(t, func(*testing.T) registrytest.VerifierHarness {
		return registrytest.VerifierHarness{
			Verifier: nil, Key: registry.TrustedKey{Fingerprint: "", Armored: nil}, Sums: nil, Signature: nil,
			OtherKey: registry.TrustedKey{Fingerprint: "", Armored: nil},
		}
	})
}

// memKeys is the reference adapter of KeyStore.
type memKeys struct {
	fences  map[namespace.Name]int64
	keys    map[namespace.Name]map[string]registry.KeyRecord
	entries map[namespace.Name][]audit.Entry
	mu      sync.Mutex
}

func (m *memKeys) AddKey(_ context.Context, k registry.KeyRecord, fence namespace.Fence, e audit.Entry) error {
	if err := k.Validate(); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if fence.Namespace != k.Namespace {
		return namespace.ErrFenced
	}

	if err := m.fenced(fence); err != nil {
		return err
	}

	held := m.keys[k.Namespace]
	if old, ok := held[k.Key.Fingerprint]; ok {
		if bytes.Equal(old.Key.Armored, k.Key.Armored) {
			return nil
		}

		return registry.ErrKeyExists
	}

	if len(held) >= registry.MaxKeysPerNamespace {
		return registry.ErrKeyLimit
	}

	if held == nil {
		held = map[string]registry.KeyRecord{}
		m.keys[k.Namespace] = held
	}

	k.Key.Armored = slices.Clone(k.Key.Armored)
	held[k.Key.Fingerprint] = k
	m.entries[k.Namespace] = append(m.entries[k.Namespace], e)

	return nil
}

func (m *memKeys) RemoveKey(
	_ context.Context, ns namespace.Name, fingerprint string, fence namespace.Fence, e audit.Entry,
) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if fence.Namespace != ns {
		return namespace.ErrFenced
	}

	if err := m.fenced(fence); err != nil {
		return err
	}

	if _, ok := m.keys[ns][fingerprint]; !ok {
		return registry.ErrKeyNotFound
	}

	delete(m.keys[ns], fingerprint)
	m.entries[ns] = append(m.entries[ns], e)

	return nil
}

func (m *memKeys) Keys(_ context.Context, ns namespace.Name) ([]registry.KeyRecord, error) {
	if _, err := namespace.ParseName(ns.String()); err != nil {
		return nil, fmt.Errorf("%w: %w", registry.ErrInvalid, err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([]registry.KeyRecord, 0, len(m.keys[ns]))

	for _, k := range m.keys[ns] {
		k.Key.Armored = slices.Clone(k.Key.Armored)
		out = append(out, k)
	}

	slices.SortFunc(
		out,
		func(a, b registry.KeyRecord) int { return strings.Compare(a.Key.Fingerprint, b.Key.Fingerprint) },
	)

	return out, nil
}

// holds reports whether ns trusts the key with fingerprint.
func (m *memKeys) holds(ns namespace.Name, fingerprint string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	_, ok := m.keys[ns][fingerprint]

	return ok
}

func (m *memKeys) fenced(f namespace.Fence) error {
	if m.fences[f.Namespace] != f.FailoverVersion {
		return namespace.ErrFenced
	}

	return nil
}

func TestMemKeyStore(t *testing.T) {
	t.Parallel()

	registrytest.RunKeyStore(t, func(*testing.T) registrytest.KeyHarness {
		m := &memKeys{
			fences:  map[namespace.Name]int64{},
			keys:    map[namespace.Name]map[string]registry.KeyRecord{},
			entries: map[namespace.Name][]audit.Entry{},
			mu:      sync.Mutex{},
		}

		return registrytest.KeyHarness{
			Store: m,
			Namespace: func(_ *testing.T, name namespace.Name) namespace.Fence {
				m.mu.Lock()
				defer m.mu.Unlock()

				m.fences[name] = 1

				return namespace.Fence{Namespace: name, FailoverVersion: 1}
			},
			Entries: func(_ *testing.T, name namespace.Name) []audit.Entry {
				m.mu.Lock()
				defer m.mu.Unlock()

				return slices.Clone(m.entries[name])
			},
		}
	})
}
