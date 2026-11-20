package sqlite_test

import (
	"errors"
	"path/filepath"
	"testing"

	"opendustpan.org/opendustpan/internal/store/sqlite"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/runner"
)

// hostileProxyRows are rows that the save refuses but that a restore or
// another version could write. The read path checks them again.
type hostileProxyRow struct{ ranges, url string }

func hostileProxyRows() map[string]hostileProxyRow {
	return map[string]hostileProxyRow{
		"range of the whole address space": {ranges: `["0.0.0.0/0"]`, url: "https://proxy.example:3128"},
		"range with a host bit":            {ranges: `["10.0.0.1/8"]`, url: "https://proxy.example:3128"},
		"loopback range":                   {ranges: `["127.0.0.0/8"]`, url: "https://proxy.example:3128"},
		"loopback upstream URL":            {ranges: `["10.0.0.0/8"]`, url: "http://127.0.0.1:3128"},
		"ranges that are not a list":       {ranges: `"10.0.0.0/8"`, url: "https://proxy.example:3128"},
	}
}

// TestPoolProxyRefusesAHostileRow checks the layer that checks the settings
// of a stored row again on the read path. Raw SQL writes a hostile row and
// ProxySettings must return an error and no settings.
func TestPoolProxyRefusesAHostileRow(t *testing.T) {
	t.Parallel()

	for name, row := range hostileProxyRows() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), "dustpan.db")
			copyTemplate(t, path)

			s := openPath(t, path)
			fence := ensureNamespace(t, s, "acme")
			ensurePool(t, s, fence, "prod", runner.PurposeJobs)

			poolID := onlyPoolID(t, s, fence.Namespace)

			conn, err := openRawDB(path)
			if err != nil {
				t.Fatalf("open raw database: %v", err)
			}

			t.Cleanup(func() {
				if closeErr := conn.Close(); closeErr != nil {
					t.Errorf("close raw database: %v", closeErr)
				}
			})

			_, err = conn.ExecContext(t.Context(),
				`INSERT INTO runner_pool_proxy_settings
				 (pool_id, allowed_ranges, upstream_url, credential, version, updated_by, updated_at_ms)
				 VALUES (?, ?, ?, x'00', 1, 'alice', 1700000000000)`,
				poolID, row.ranges, row.url)
			if err != nil {
				t.Fatalf("write the hostile row: %v", err)
			}

			requireRefused(t, s.Runners(), poolID)
		})
	}
}

// requireRefused fails the test when ProxySettings of the pool returns
// settings, or reports a pool with no row instead of a decode error.
func requireRefused(t *testing.T, s runner.PoolProxyStore, poolID string) {
	t.Helper()

	rec, err := s.ProxySettings(t.Context(), poolID)
	if err == nil {
		t.Fatalf("ProxySettings of a hostile row = %+v, want an error", rec)
	}

	if errors.Is(err, runner.ErrNotFound) {
		t.Fatalf("ProxySettings error = %v, want a decode error", err)
	}
}

// onlyPoolID returns the ID of the one runner pool of ns.
func onlyPoolID(t *testing.T, s *sqlite.Store, ns namespace.Name) string {
	t.Helper()

	pools, err := s.Runners().Pools(t.Context(), ns)
	if err != nil || len(pools) != 1 {
		t.Fatalf("pools = %v, %v, want one pool", pools, err)
	}

	return pools[0].ID
}
