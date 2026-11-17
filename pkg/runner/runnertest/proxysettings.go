package runnertest

import (
	"bytes"
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/runner"
)

// ProxyHarness is one fresh, empty database for one case of the pool proxy
// settings. Pools and Proxy share the database, as the store adapters of one
// installation do.
type ProxyHarness struct {
	Pools  runner.PoolStore
	Proxy  runner.PoolProxyStore
	Domain Domain
}

// ProxyFactory returns a fresh pool proxy harness for one case.
type ProxyFactory func(t *testing.T) ProxyHarness

// RunPoolProxyStore executes the conformance suite of runner.PoolProxyStore
// against harnesses from newHarness.
func RunPoolProxyStore(t *testing.T, newHarness ProxyFactory) {
	t.Helper()

	cases := []struct {
		name string
		run  func(t *testing.T, h ProxyHarness)
	}{
		{"a pool with no row has no settings", testProxyNoRow},
		{"a save stores the settings and the sealed credential as they are", testProxySave},
		{"a save needs the stored version", testProxyVersion},
		{"a save with a stale fence changes nothing and appends nothing", testProxyStaleFence},
		{"a save for a pool that does not exist is not found", testProxyMissingPool},
		{"the settings go with the pool", testProxyDeletedWithPool},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.run(t, newHarness(t))
		})
	}
}

// proxyFixture is the namespace acme with the pool prod, in the harness h.
type proxyFixture struct {
	h     ProxyHarness
	fence namespace.Fence
	pool  runner.Pool
}

func setUpProxy(t *testing.T, h ProxyHarness) proxyFixture {
	t.Helper()

	fence, err := h.Domain.CreateNamespace(t.Context(), "acme")
	if err != nil {
		t.Fatalf("CreateNamespace: %v", err)
	}

	p, err := h.Pools.CreatePool(t.Context(), newPool("acme", "prod", runner.PurposeJobs), fence, entries(fence))
	if err != nil {
		t.Fatalf("CreatePool: %v", err)
	}

	return proxyFixture{h: h, fence: fence, pool: p}
}

// proxyRecord returns the record of the settings with one range, one upstream
// URL and the sealed credential, to save with the version version.
func proxyRecord(t *testing.T, sealed []byte) runner.ProxySettingsRecord {
	t.Helper()

	s, err := runner.NewProxySettings([]string{"10.0.0.0/8"}, "http://proxy.example:3128")
	if err != nil {
		t.Fatalf("NewProxySettings: %v", err)
	}

	return runner.ProxySettingsRecord{
		Settings: s, Credential: sealed, Version: 0, UpdatedBy: "alice", UpdatedAt: now,
	}
}

func testProxyNoRow(t *testing.T, h ProxyHarness) {
	t.Helper()

	f := setUpProxy(t, h)

	_, err := h.Proxy.ProxySettings(t.Context(), f.pool.ID)
	wantErr(t, "ProxySettings of a pool with no row", err, runner.ErrNotFound)
}

func testProxySave(t *testing.T, h ProxyHarness) {
	t.Helper()

	f := setUpProxy(t, h)
	sealed := []byte{0x00, 0xff, 'c', 'r', 'e', 'd'}

	got, err := h.Proxy.SetProxySettings(t.Context(), f.fence, f.pool.ID, proxyRecord(t, sealed), 0, entries(f.fence)[0])
	if err != nil {
		t.Fatalf("SetProxySettings: %v", err)
	}

	if got.Version != 1 {
		t.Fatalf("SetProxySettings version = %d, want 1", got.Version)
	}

	read, err := h.Proxy.ProxySettings(t.Context(), f.pool.ID)
	if err != nil {
		t.Fatalf("ProxySettings: %v", err)
	}

	if !bytes.Equal(read.Credential, sealed) {
		t.Fatalf("ProxySettings credential = %x, want %x", read.Credential, sealed)
	}

	if read.Version != 1 || read.UpdatedBy != "alice" || !read.UpdatedAt.Equal(now) {
		t.Fatalf("ProxySettings = %+v, want version 1 by alice at the clock of the cases", read)
	}

	if ranges, want := read.Settings.AllowedRanges(), []string{"10.0.0.0/8"}; !equalStrings(ranges, want) {
		t.Fatalf("AllowedRanges = %v, want %v", ranges, want)
	}

	if got, want := read.Settings.UpstreamURL(), "http://proxy.example:3128"; got != want {
		t.Fatalf("UpstreamURL = %q, want %q", got, want)
	}
}

func testProxyVersion(t *testing.T, h ProxyHarness) {
	t.Helper()

	f := setUpProxy(t, h)
	rec := proxyRecord(t, nil)

	if _, err := h.Proxy.SetProxySettings(t.Context(), f.fence, f.pool.ID, rec, 0, entries(f.fence)[0]); err != nil {
		t.Fatalf("SetProxySettings: %v", err)
	}

	_, err := h.Proxy.SetProxySettings(t.Context(), f.fence, f.pool.ID, rec, 0, entries(f.fence)[0])
	wantErr(t, "SetProxySettings with version 0 for a row with version 1", err, runner.ErrConflict)

	_, err = h.Proxy.SetProxySettings(t.Context(), f.fence, f.pool.ID, rec, 5, entries(f.fence)[0])
	wantErr(t, "SetProxySettings with version 5 for a row with version 1", err, runner.ErrConflict)

	got, err := h.Proxy.SetProxySettings(t.Context(), f.fence, f.pool.ID, rec, 1, entries(f.fence)[0])
	if err != nil || got.Version != 2 {
		t.Fatalf("SetProxySettings with version 1 = %+v, %v; want version 2", got, err)
	}
}

func testProxyStaleFence(t *testing.T, h ProxyHarness) {
	t.Helper()

	f := setUpProxy(t, h)

	fresh, err := h.Domain.BumpFence(t.Context(), f.fence)
	if err != nil {
		t.Fatalf("BumpFence: %v", err)
	}

	before, err := h.Domain.AuditEntries(t.Context(), "acme")
	if err != nil {
		t.Fatalf("AuditEntries: %v", err)
	}

	_, err = h.Proxy.SetProxySettings(t.Context(), f.fence, f.pool.ID, proxyRecord(t, nil), 0, entries(f.fence)[0])
	wantErr(t, "SetProxySettings with a stale fence", err, namespace.ErrFenced)

	_, err = h.Proxy.ProxySettings(t.Context(), f.pool.ID)
	wantErr(t, "ProxySettings after a refused save", err, runner.ErrNotFound)

	after, err := h.Domain.AuditEntries(t.Context(), "acme")
	if err != nil || len(after) != len(before) {
		t.Fatalf("AuditEntries after a refused save = %d entries, %v; want %d", len(after), err, len(before))
	}

	if _, err = h.Proxy.SetProxySettings(t.Context(), fresh, f.pool.ID, proxyRecord(t, nil), 0, entries(fresh)[0]); err != nil {
		t.Fatalf("SetProxySettings with the new fence: %v", err)
	}
}

func testProxyMissingPool(t *testing.T, h ProxyHarness) {
	t.Helper()

	f := setUpProxy(t, h)

	_, err := h.Proxy.SetProxySettings(t.Context(), f.fence, "pool-missing", proxyRecord(t, nil), 0, entries(f.fence)[0])
	wantErr(t, "SetProxySettings for a pool that does not exist", err, runner.ErrNotFound)
}

func testProxyDeletedWithPool(t *testing.T, h ProxyHarness) {
	t.Helper()

	f := setUpProxy(t, h)

	if _, err := h.Proxy.SetProxySettings(t.Context(), f.fence, f.pool.ID, proxyRecord(t, nil), 0, entries(f.fence)[0]); err != nil {
		t.Fatalf("SetProxySettings: %v", err)
	}

	if err := h.Pools.DeletePool(t.Context(), f.pool.ID, f.fence, entries(f.fence)); err != nil {
		t.Fatalf("DeletePool: %v", err)
	}

	_, err := h.Proxy.ProxySettings(t.Context(), f.pool.ID)
	if !errors.Is(err, runner.ErrNotFound) {
		t.Fatalf("ProxySettings of a deleted pool = %v, want %v", err, runner.ErrNotFound)
	}
}

// equalStrings reports whether a and b hold the same strings in one order.
func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}

	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}

	return true
}
