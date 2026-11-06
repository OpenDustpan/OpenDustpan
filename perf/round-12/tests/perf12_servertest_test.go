package servertest_test

import (
	"fmt"
	"net/url"
	"os"
	"runtime"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/dustpan"
	"opendustpan.org/opendustpan/pkg/store/postgres"
)

func perfPct(d []time.Duration, p float64) float64 {
	s := slices.Clone(d)
	slices.Sort(s)
	i := int(float64(len(s)-1) * p)

	return float64(s[i].Microseconds()) / 1000
}

// TestPerf12LockLatency times lock and unlock requests (n=1000 each).
func TestPerf12LockLatency(t *testing.T) {
	h := newRoundTripHarness(t, traceRequest, dustpan.WithPublicURL("https://dustpan.example.com"))
	h.workspace = h.createWorkspace("web")

	for range 20 {
		h.lock()
		h.unlock()
	}

	const n = 1000

	var lk, ul []time.Duration

	var m0, m1 runtime.MemStats

	runtime.ReadMemStats(&m0)

	for range n {
		a := time.Now()
		h.lock()
		b := time.Now()
		h.unlock()
		c := time.Now()
		lk = append(lk, b.Sub(a))
		ul = append(ul, c.Sub(b))
	}

	runtime.ReadMemStats(&m1)
	t.Logf("PERF12 lock p50=%.3f p95=%.3f p99=%.3f", perfPct(lk, .5), perfPct(lk, .95), perfPct(lk, .99))
	t.Logf("PERF12 unlock p50=%.3f p95=%.3f p99=%.3f", perfPct(ul, .5), perfPct(ul, .95), perfPct(ul, .99))
	t.Logf("PERF12 lockunlock allocs/pair=%d", (m1.Mallocs-m0.Mallocs)/n)
}

// TestPerf12BackupRestoreTime times a backup and a restore of the small and the large data set.
func TestPerf12BackupRestoreTime(t *testing.T) {
	for _, units := range []int{3, 30} {
		target, err := fs.Open(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}

		h := newRoundTripHarness(t, traceBackup, dustpan.WithBackupTarget(target, "test"),
			dustpan.WithOperationLease(roundTripLease))
		h.seedUnits(units)

		start := time.Now()
		_ = h.runBackupAndRestore()
		t.Logf("PERF12 backuprestore units=%d total_ms=%d", units, time.Since(start).Milliseconds())
	}
}

func perfSchemaURL(t *testing.T, schema string) string {
	t.Helper()

	base := os.Getenv("DUSTPAN_TEST_POSTGRES_URL")
	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}

	conn, err := pgx.Connect(t.Context(), base)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(t.Context())

	if _, err := conn.Exec(t.Context(), "DROP SCHEMA IF EXISTS "+schema+" CASCADE"); err != nil {
		t.Fatal(err)
	}

	if _, err := conn.Exec(t.Context(), "DROP SCHEMA IF EXISTS "+schema+"_control CASCADE"); err != nil {
		t.Fatal(err)
	}

	if _, err := conn.Exec(t.Context(), "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}

	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()

	return u.String()
}

// TestPerf12PostgresOpen times the open of a fresh schema (migrate) and of a migrated schema.
func TestPerf12PostgresOpen(t *testing.T) {
	var fresh, migrated []time.Duration

	for i := range 5 {
		u := perfSchemaURL(t, fmt.Sprintf("perf12_open_%d", i))

		a := time.Now()

		s, err := postgres.Open(t.Context(), u)
		if err != nil {
			t.Fatal(err)
		}

		fresh = append(fresh, time.Since(a))

		s.Close()

		for range 5 {
			a = time.Now()

			s, err = postgres.Open(t.Context(), u)
			if err != nil {
				t.Fatal(err)
			}

			migrated = append(migrated, time.Since(a))

			s.Close()
		}
	}

	t.Logf("PERF12 pgopen fresh p50=%.1f p95=%.1f max=%.1f", perfPct(fresh, .5), perfPct(fresh, .95), perfPct(fresh, 1))
	t.Logf("PERF12 pgopen migrated p50=%.1f p95=%.1f max=%.1f", perfPct(migrated, .5), perfPct(migrated, .95), perfPct(migrated, 1))
}
