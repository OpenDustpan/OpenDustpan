package codec_test

import (
	"crypto/sha256"
	"errors"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/token"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
)

// TestToCheckpointRowBoundsMemory proves the save path holds at most one
// decompressed copy of a checkpoint: converting a deployment with many
// resources peaks below twice its size plus slack. The conversion keeps no
// decoded copy of the deployment, since the row stores no resource count, so
// a retained second copy fails this test.
//
//nolint:paralleltest // The heap bound needs a quiet heap; the parallel tests of this package run after it.
func TestToCheckpointRowBoundsMemory(t *testing.T) {
	const resources = 200000

	deployment := bigDeployment(resources)

	cp := stack.Checkpoint{
		NamespaceID: "", StackID: "",
		SavedAt:       time.Now(),
		Features:      []string{"verbatim"},
		Deployment:    deployment,
		Update:        stack.UpdateID("upd-test"),
		Ref:           mustRef(t),
		Lease:         token.Digest{},
		SchemaVersion: 3,
		Sequence:      3,
		Digest:        sha256.Sum256(deployment),
		Invalid:       false,
		BlobKey:       mustBlobKey(t, "namespaces/ns-test/pulumi/stk-test/upd-test/3"),
		BlobSize:      0,
		BlobMD5:       [16]byte{},

		AttemptStartedAt: time.Time{}, Origin: identity.StateOrigin{Job: "", Commit: ""},
	}

	if _, err := codec.ToCheckpointRow(cp, stack.ID("stk-test")); err != nil {
		t.Fatalf("convert checkpoint: %v", err)
	}

	base := heapBase()

	peak := peakHeapDuring(func() {
		for range 3 {
			if _, err := codec.ToCheckpointRow(cp, stack.ID("stk-test")); err != nil {
				t.Errorf("convert checkpoint: %v", err)

				return
			}
		}
	})

	var growth uint64

	if peak > base {
		growth = peak - base
	}

	limit := 2*uint64(len(deployment)) + (4 << 20)
	t.Logf("deployment %d bytes, heap growth %d bytes", len(deployment), growth)

	if growth > limit {
		t.Fatalf(
			"heap growth %d bytes over twice the %d byte deployment, want below %d",
			growth,
			len(deployment),
			limit,
		)
	}
}

// TestToCheckpointRowRefusesNoKey checks that a checkpoint without a blob
// key converts to no row: the store keeps no content, so such a row could
// never be read.
func TestToCheckpointRowRefusesNoKey(t *testing.T) {
	t.Parallel()

	cp := stack.Checkpoint{
		NamespaceID: "", StackID: "",
		SavedAt:       time.Now(),
		Features:      nil,
		Deployment:    []byte(`{}`),
		Update:        stack.UpdateID("upd-test"),
		Ref:           mustRef(t),
		Lease:         token.Digest{},
		SchemaVersion: 3,
		Sequence:      0,
		Digest:        sha256.Sum256([]byte(`{}`)),
		Invalid:       false,
		BlobKey:       "",
		BlobSize:      0,
		BlobMD5:       [16]byte{},

		AttemptStartedAt: time.Time{}, Origin: identity.StateOrigin{Job: "", Commit: ""},
	}

	if _, err := codec.ToCheckpointRow(cp, stack.ID("stk-test")); !errors.Is(err, stack.ErrCheckpointNoKey) {
		t.Fatalf("convert checkpoint without a key: err = %v, want ErrCheckpointNoKey", err)
	}
}

// TestFromCheckpointRowRefusesNoKey checks that a row without a blob key
// reads as a corrupt row, not as a checkpoint without content.
func TestFromCheckpointRowRefusesNoKey(t *testing.T) {
	t.Parallel()

	digest := sha256.Sum256([]byte(`{}`))
	row := codec.CheckpointRow{
		NamespaceID:   "",
		StackID:       "stk-test",
		UpdateID:      "upd-test",
		LeaseDigest:   []byte{},
		Digest:        digest[:],
		Features:      []byte(`[]`),
		SchemaVersion: 3,
		Sequence:      0,
		SavedAtMs:     0,
		Invalid:       false,
		BlobKey:       "",
		Size:          2,
		BlobSize:      0,
		BlobMD5:       nil,
	}

	if _, err := codec.FromCheckpointRow(row, mustRef(t)); !errors.Is(err, codec.ErrCorruptRow) {
		t.Fatalf("read a row without a key: err = %v, want ErrCorruptRow", err)
	}
}

// heapBase returns the current live heap, after a collection.
func heapBase() uint64 {
	runtime.GC() //nolint:revive // call-to-gc: the memory bound needs a stable baseline.

	var ms runtime.MemStats

	runtime.ReadMemStats(&ms)

	return ms.HeapAlloc
}

// peakHeapDuring runs run while a sampler records the highest live heap. The
// sampler reads MemStats in a tight loop, so thousands of samples land during
// a conversion of tens of milliseconds: a retained second copy cannot hide
// between two samples.
func peakHeapDuring(run func()) uint64 {
	var peak atomic.Uint64

	stop := make(chan struct{})
	done := make(chan struct{})

	go func() {
		defer close(done)

		var ms runtime.MemStats

		for {
			select {
			case <-stop:
				return
			default:
				runtime.ReadMemStats(&ms)

				for {
					current := peak.Load()
					if ms.HeapAlloc <= current || peak.CompareAndSwap(current, ms.HeapAlloc) {
						break
					}
				}
			}
		}
	}()

	run()
	close(stop)
	<-done

	return peak.Load()
}

// bigDeployment returns a deployment with n managed resources: a resources
// array of n typed entries, several megabytes of JSON the row must count.
func bigDeployment(n int) []byte {
	var sb strings.Builder

	sb.WriteString(`{"resources":[`)

	for i := range n {
		if i > 0 {
			sb.WriteByte(',')
		}

		sb.WriteString(`{"type":"a:resource`)
		sb.WriteString(strconv.Itoa(i))
		sb.WriteString(`:X"}`)
	}

	sb.WriteString(`]}`)

	return []byte(sb.String())
}

// mustRef returns the test stack reference, failing the test on error.
func mustRef(t *testing.T) stack.Ref {
	t.Helper()

	ref, err := stack.ParseRef("acme", "web", "dev")
	if err != nil {
		t.Fatalf("parse ref: %v", err)
	}

	return ref
}

// mustBlobKey parses key, failing the test on error.
func mustBlobKey(t *testing.T, key string) blob.Key {
	t.Helper()

	parsed, err := blob.ParseKey(key)
	if err != nil {
		t.Fatalf("parse blob key: %v", err)
	}

	return parsed
}
