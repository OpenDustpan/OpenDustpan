package sqlite

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// benchChunksEnv names the variable that sets the number of log chunks that
// BenchmarkBackupBlobRefs seeds. The default is small, so that a plain
// benchmark run stays fast; set it to 1000000 for the large case.
const benchChunksEnv = "DUSTPAN_BENCH_CHUNKS"

// benchLogs is the number of logs that the seeded chunks belong to.
const benchLogs = 100

// benchChunkCount returns the number of chunks to seed.
func benchChunkCount(tb testing.TB) int {
	tb.Helper()

	raw := os.Getenv(benchChunksEnv)
	if raw == "" {
		return 20_000
	}

	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		tb.Fatalf("%s = %q, want a positive number", benchChunksEnv, raw)
	}

	return n
}

// peakSampler records the highest HeapInuse that it sees while it runs.
type peakSampler struct {
	peak atomic.Uint64
	stop chan struct{}
	done sync.WaitGroup
}

// startPeakSampler starts a sampler that reads the heap every 5 ms.
func startPeakSampler() *peakSampler {
	s := &peakSampler{stop: make(chan struct{})}

	s.done.Go(func() {
		tick := time.NewTicker(5 * time.Millisecond)
		defer tick.Stop()

		var m runtime.MemStats

		for {
			select {
			case <-s.stop:
				return
			case <-tick.C:
				runtime.ReadMemStats(&m)

				if m.HeapInuse > s.peak.Load() {
					s.peak.Store(m.HeapInuse)
				}
			}
		}
	})

	return s
}

// finish stops the sampler and returns the highest HeapInuse.
func (s *peakSampler) finish() uint64 {
	close(s.stop)
	s.done.Wait()

	return s.peak.Load()
}

// BenchmarkBackupBlobRefs measures the blob references that the SQLite
// export reads for a database with many confirmed log chunks: the heap that
// the references keep after the read, the heap peak during it, and the bytes
// that it allocates in total.
func BenchmarkBackupBlobRefs(b *testing.B) {
	n := benchChunkCount(b)
	s := openInternalTemplated(b)
	ctx := b.Context()

	stmts := []string{
		`INSERT INTO namespaces (name, primary_installation, failover_version, created_at_ms, id)
		VALUES ('bench', 'inst', 1, 0, 'ns-bench')`,
		fmt.Sprintf(`WITH RECURSIVE n(i) AS (SELECT 0 UNION ALL SELECT i + 1 FROM n WHERE i + 1 < %d)
		INSERT INTO job_logs (id, namespace_id, job_id, phase, step, token, created_at_ms)
		SELECT printf('jlv-%%020d', i), 'ns-bench', 'job-bench', 0, 'step', 1, 0 FROM n`, benchLogs),
		fmt.Sprintf(`WITH RECURSIVE n(i) AS (SELECT 0 UNION ALL SELECT i + 1 FROM n WHERE i + 1 < %d)
		INSERT INTO job_log_chunks (log_id, seq, namespace_id, sha256, size, confirmed, stored_size, stored_md5, blob_key)
		SELECT printf('jlv-%%020d', i %% %d), i / %d, 'ns-bench', 'x', 9, 1, 9, X'00112233445566778899aabbccddeeff',
			'job/job-bench/log/' || printf('jlv-%%020d', i %% %d) || '/' || printf('%%08d', i / %d) || '-jla-x'
		FROM n`, n, benchLogs, benchLogs, benchLogs, benchLogs),
	}

	for _, stmt := range stmts {
		if _, err := s.conn.ExecContext(ctx, stmt); err != nil {
			b.Fatalf("seed: %v", err)
		}
	}

	b.ReportAllocs()
	b.ResetTimer()

	for range b.N {
		runtime.GC()

		var before, after runtime.MemStats

		runtime.ReadMemStats(&before)

		sampler := startPeakSampler()

		refs, err := blobRefs(ctx, s.conn)
		if err != nil {
			b.Fatalf("blob refs: %v", err)
		}

		peak := sampler.finish()

		runtime.GC()
		runtime.ReadMemStats(&after)

		if len(refs.Refs) != n {
			b.Fatalf("references = %d, want %d", len(refs.Refs), n)
		}

		runtime.KeepAlive(refs)

		const mib = 1 << 20

		b.ReportMetric(float64(after.HeapAlloc-before.HeapAlloc)/mib, "live-MiB")
		b.ReportMetric(float64(peak)/mib, "peak-MiB")
		b.ReportMetric(float64(after.TotalAlloc-before.TotalAlloc)/mib, "alloc-MiB")
	}
}
