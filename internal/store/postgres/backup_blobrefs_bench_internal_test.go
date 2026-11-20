package postgres

import (
	"fmt"
	"os"
	"strconv"
	"testing"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/internal/membench"
)

// benchChunksEnv names the variable that sets the number of log chunks that
// BenchmarkBackupBlobRefs seeds. The default is small; set it to 1000000 for
// the large case.
const benchChunksEnv = "DUSTPAN_TEST_BENCH_CHUNKS"

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

// seedChunks adds one namespace, benchLogs logs and n confirmed chunks that
// each hold the size and the MD5 of their stored bytes.
func seedChunks(tb testing.TB, s *Store, n int) {
	tb.Helper()

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
		if _, err := s.pool.Exec(tb.Context(), stmt); err != nil {
			tb.Fatalf("seed: %v", err)
		}
	}
}

// BenchmarkBackupBlobRefs measures the blob references that the PostgreSQL
// export reads for a database with many confirmed log chunks: the heap that
// the references keep after the read, the heap peak during it, and the bytes
// that it allocates in total.
func BenchmarkBackupBlobRefs(b *testing.B) {
	n := benchChunkCount(b)
	s := benchStore(b)
	ctx := b.Context()

	seedChunks(b, s, n)
	b.ReportAllocs()
	b.ResetTimer()

	for range b.N {
		meter := membench.Start()

		var attrs pgAttrs

		err := s.inLongTx(ctx, backupBound(), s.snapshotOptions(), func(tx pgx.Tx) error {
			var attrErr error

			attrs, attrErr = backupAttrs(ctx, s.queries.WithTx(tx), tx)

			return attrErr
		})
		if err != nil {
			b.Fatalf("backup attrs: %v", err)
		}

		if len(attrs.blobs) != n {
			b.Fatalf("references = %d, want %d", len(attrs.blobs), n)
		}

		meter.Stop(b, attrs)
	}
}
