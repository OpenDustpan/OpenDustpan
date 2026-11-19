package backup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strconv"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/membench"
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
}

// Put reads r to the end and counts the bytes.
func (d *discardTarget) Put(_ context.Context, _ blob.Key, r io.Reader) error {
	if _, err := io.Copy(io.Discard, r); err != nil {
		return fmt.Errorf("discard the manifest: %w", err)
	}

	return nil
}

// benchRefs returns n references in key order, as an export returns them.
func benchRefs(n int) []BlobRef {
	refs := make([]BlobRef, n)

	for i := range refs {
		refs[i] = BlobRef{
			MD5:  [16]byte{byte(i), byte(i >> 8), byte(i >> 16), byte(i >> 24)},
			Key:  fmt.Sprintf("namespaces/ns-bench/job/job-bench/log/jlv-%020d/%08d-jla-x", i/10_000, i%10_000),
			Size: 9, Sealed: false,
		}
	}

	return refs
}

// benchBlobCount returns the number of references of the benchmark.
func benchBlobCount(b *testing.B) int {
	b.Helper()

	raw := os.Getenv(benchBlobsEnv)
	if raw == "" {
		return 20_000
	}

	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		b.Fatalf("%s = %q, want a positive number", benchBlobsEnv, raw)
	}

	return n
}

// storedOf builds the manifest entry of each reference as the copy of a plain
// blob builds it. The copy itself is not in the measure.
func storedOf(ordered []BlobRef) []ManifestBlob {
	stored := make([]ManifestBlob, 0, len(ordered))

	for _, ref := range ordered {
		stored = append(stored, ManifestBlob{
			Key: ref.Key, Size: ref.Size, MD5: hex.EncodeToString(ref.MD5[:]), Sealed: ref.Sealed,
		})
	}

	return stored
}

// BenchmarkManifestBlobs measures the stages of a backup that hold the blob
// list: the ordering, the list of manifest entries that the copy returns and
// the write of the manifest.
func BenchmarkManifestBlobs(b *testing.B) {
	n := benchBlobCount(b)

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
		svc := serviceOver(&discardTarget{Store: nil})
		meter := membench.Start()

		stored := storedOf(orderBlobs(exported))
		input := manifestInput{
			startedAt: time.Time{}, kind: "", id: id, sum: emptySummary(), files: nil, ordered: stored,
			damaged: nil, size: 0, sha: [sha256.Size]byte{}, prefix: prefix,
		}

		if writeErr := svc.writeManifest(b.Context(), input); writeErr != nil {
			b.Fatalf("write manifest: %v", writeErr)
		}

		meter.Stop(b, stored, exported)
	}
}
