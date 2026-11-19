package backup

import (
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/blob"
)

// benchBlobsEnv names the variable that sets the number of blob references
// that BenchmarkManifestBlobs uses. The default is small; set it to 1000000
// for the large case.
const benchBlobsEnv = "DUSTPAN_BENCH_BLOBS"

// discardTarget is a blob store that reads each object it is given and keeps
// nothing. Only Put is used.
type discardTarget struct {
	blob.Store

	bytes int64
}

// Put reads r to the end and counts the bytes.
func (d *discardTarget) Put(_ context.Context, _ blob.Key, r io.Reader) error {
	n, err := io.Copy(io.Discard, r)
	d.bytes = n

	return err
}

// benchRefs returns n references in key order, as an export returns them.
func benchRefs(n int) []BlobRef {
	refs := make([]BlobRef, n)

	for i := range refs {
		refs[i] = BlobRef{
			MD5: [16]byte{byte(i), byte(i >> 8), byte(i >> 16), byte(i >> 24)},
			Key: fmt.Sprintf("namespaces/ns-bench/job/job-bench/log/jlv-%020d/%08d-jla-x", i/10_000, i%10_000),
			Size: 9, Sealed: false,
		}
	}

	return refs
}

// BenchmarkManifestBlobs measures the stages of a backup that hold the blob
// list: the ordering, the list of manifest entries that the copy returns and
// the write of the manifest. The copy itself is not in the measure: each
// entry is built from its reference as a copy of a plain blob builds it.
func BenchmarkManifestBlobs(b *testing.B) {
	n := 20_000

	if raw := os.Getenv(benchBlobsEnv); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v <= 0 {
			b.Fatalf("%s = %q, want a positive number", benchBlobsEnv, raw)
		}

		n = v
	}

	id, err := ParseID("bkp-bench")
	if err != nil {
		b.Fatalf("id: %v", err)
	}

	prefix, err := newAttemptPrefix(id, "1")
	if err != nil {
		b.Fatalf("prefix: %v", err)
	}

	b.ReportAllocs()

	for range b.N {
		exported := benchRefs(n)
		target := &discardTarget{Store: nil, bytes: 0}
		svc := &Service{target: target, version: "bench"}

		runtime.GC()

		var before, after runtime.MemStats

		runtime.ReadMemStats(&before)

		var (
			peak atomic.Uint64
			stop = make(chan struct{})
			wg   sync.WaitGroup
		)

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

		ordered := orderBlobs(exported)
		stored := make([]ManifestBlob, 0, len(ordered))

		for _, ref := range ordered {
			stored = append(stored, ManifestBlob{
				Key: ref.Key, Size: ref.Size, MD5: hex.EncodeToString(ref.MD5[:]), Sealed: ref.Sealed,
			})
		}

		if err := svc.writeManifest(b.Context(), manifestInput{
			id: id, prefix: prefix, ordered: stored, kind: "", sum: Summary{}, startedAt: time.Time{},
		}); err != nil {
			b.Fatalf("write manifest: %v", err)
		}

		close(stop)
		wg.Wait()
		runtime.ReadMemStats(&after)
		runtime.KeepAlive(stored)
		runtime.KeepAlive(exported)

		const mib = 1 << 20

		b.ReportMetric(float64(peak.Load())/mib, "peak-MiB")
		b.ReportMetric(float64(after.TotalAlloc-before.TotalAlloc)/mib, "alloc-MiB")
		b.ReportMetric(float64(target.bytes)/mib, "manifest-MiB")
	}
}
