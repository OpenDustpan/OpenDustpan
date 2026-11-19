package postgres

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// benchChunksEnv names the variable that sets the number of log chunks that
// BenchmarkBackupBlobRefs seeds. The default is small; set it to 1000000 for
// the large case.
const benchChunksEnv = "DUSTPAN_BENCH_CHUNKS"

// benchLogs is the number of logs that the seeded chunks belong to.
const benchLogs = 100

// BenchmarkBackupBlobRefs measures the blob references that the PostgreSQL
// export reads for a database with many confirmed log chunks: the heap that
// the references keep after the read, the heap peak during it, and the bytes
// that it allocates in total.
func BenchmarkBackupBlobRefs(b *testing.B) {
	n := 20_000

	if raw := os.Getenv(benchChunksEnv); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v <= 0 {
			b.Fatalf("%s = %q, want a positive number", benchChunksEnv, raw)
		}

		n = v
	}

	s := benchStore(b)
	ctx := b.Context()

	stmts := []string{
		`INSERT INTO namespaces (name, primary_installation, failover_version, created_at_ms, id)
		VALUES ('bench', 'inst', 1, 0, 'ns-bench')`,
		fmt.Sprintf(`INSERT INTO job_logs (id, namespace_id, job_id, phase, step, token, created_at_ms)
		SELECT format('jlv-%%s', lpad(i::text, 20, '0')), 'ns-bench', 'job-bench', 0, 'step', 1, 0
		FROM generate_series(0, %d) AS i`, benchLogs-1),
		fmt.Sprintf(`INSERT INTO job_log_chunks (log_id, seq, namespace_id, sha256, size, confirmed, stored_size,
			stored_md5, blob_key)
		SELECT format('jlv-%%s', lpad((i %% %d)::text, 20, '0')), i / %d, 'ns-bench', 'x', 9, 1, 9,
			'\x00112233445566778899aabbccddeeff'::bytea,
			format('job/job-bench/log/jlv-%%s/%%s-jla-x', lpad((i %% %d)::text, 20, '0'), lpad((i / %d)::text, 8, '0'))
		FROM generate_series(0, %d) AS i`, benchLogs, benchLogs, benchLogs, benchLogs, n-1),
	}

	for _, stmt := range stmts {
		if _, err := s.pool.Exec(ctx, stmt); err != nil {
			b.Fatalf("seed: %v", err)
		}
	}

	b.ReportAllocs()
	b.ResetTimer()

	for range b.N {
		var (
			attrs         pgAttrs
			before, after runtime.MemStats
			peak          atomic.Uint64
			stop          = make(chan struct{})
			wg            sync.WaitGroup
		)

		runtime.GC()
		runtime.ReadMemStats(&before)

		wg.Go(func() {
			tick := time.NewTicker(5 * time.Millisecond)
			defer tick.Stop()

			var m runtime.MemStats

			for {
				select {
				case <-stop:
					return
				case <-tick.C:
					runtime.ReadMemStats(&m)

					if m.HeapInuse > peak.Load() {
						peak.Store(m.HeapInuse)
					}
				}
			}
		})

		err := s.inLongTx(ctx, backupBound(), s.snapshotOptions(), func(tx pgx.Tx) error {
			var attrErr error

			attrs, attrErr = backupAttrs(ctx, s.queries.WithTx(tx), tx)

			return attrErr
		})
		if err != nil {
			b.Fatalf("backup attrs: %v", err)
		}

		close(stop)
		wg.Wait()
		runtime.GC()
		runtime.ReadMemStats(&after)

		if len(attrs.blobs) != n {
			b.Fatalf("references = %d, want %d", len(attrs.blobs), n)
		}

		runtime.KeepAlive(attrs)

		const mib = 1 << 20

		b.ReportMetric(float64(after.HeapAlloc-before.HeapAlloc)/mib, "live-MiB")
		b.ReportMetric(float64(peak.Load())/mib, "peak-MiB")
		b.ReportMetric(float64(after.TotalAlloc-before.TotalAlloc)/mib, "alloc-MiB")
	}
}
