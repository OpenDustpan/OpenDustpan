package registrytest_test

import (
	"context"
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

func (m *mem) Addresses(_ context.Context, ns namespace.Name, kind registry.Kind) ([]registry.Address, error) {
	probe := registry.Address{Namespace: ns, Kind: kind, Name: "probe", System: ""}
	if kind == registry.KindModule {
		probe.System = "probe"
	}

	if err := probe.Validate(); err != nil {
		return nil, err
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

	slices.SortFunc(out, func(a, b registry.Address) int {
		return strings.Compare(a.Name+"/"+a.System, b.Name+"/"+b.System)
	})

	return out, nil
}

func TestMemStore(t *testing.T) {
	t.Parallel()

	registrytest.RunStore(t, func(*testing.T) registrytest.Harness {
		m := &mem{
			fences:   map[namespace.Name]int64{},
			releases: map[key]registry.Release{},
			entries:  map[namespace.Name][]audit.Entry{},
			mu:       sync.Mutex{},
		}

		return registrytest.Harness{
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

func TestVerifierSkeletonSkips(t *testing.T) {
	t.Parallel()

	registrytest.RunVerifier(t, func(*testing.T) registrytest.VerifierHarness {
		return registrytest.VerifierHarness{
			Verifier: nil, Key: registry.TrustedKey{Fingerprint: "", Armored: nil}, Sums: nil, Signature: nil,
			OtherKey: registry.TrustedKey{Fingerprint: "", Armored: nil},
		}
	})
}
