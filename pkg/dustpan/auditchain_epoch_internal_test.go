package dustpan

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/metrics"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/blob/blobtest"
)

// epochScanBlobs wraps a blob store and counts the pages of the epoch list
// that the audit checkpoints read. onScan runs before the page of scan number
// n (counted from 1) is answered.
type epochScanBlobs struct {
	blob.Store

	onScan func(n int)
	scans  int
	mu     sync.Mutex
}

func (e *epochScanBlobs) ListPage(ctx context.Context, req blob.PageRequest) (blob.Page, error) {
	if strings.Contains(req.Prefix, "/epochs/") && req.After == "" {
		e.mu.Lock()
		e.scans++
		n := e.scans
		e.mu.Unlock()

		if e.onScan != nil {
			e.onScan(n)
		}
	}

	page, err := e.Store.ListPage(ctx, req)
	if err != nil {
		return blob.Page{}, fmt.Errorf("list page: %w", err)
	}

	return page, nil
}

func (e *epochScanBlobs) count() int {
	e.mu.Lock()
	defer e.mu.Unlock()

	return e.scans
}

// newEpochCycleChain returns a chain whose checkpoints read the blobs through
// the counting wrapper, and one server entry to sign.
func newEpochCycleChain(t *testing.T) (*auditChain, *epochScanBlobs, *audit.Checkpoints) {
	t.Helper()

	chain := newTestAuditChain(t, audit.ChainOn)
	raw := openTestBlobs(t)
	counted := &epochScanBlobs{Store: raw, onScan: nil, scans: 0, mu: sync.Mutex{}}

	reader, ok := chain.entries.(audit.EntryReader)
	if !ok {
		t.Fatal("audit store reads no single entry")
	}

	signer := newTestCheckpointSigner(t)

	chain.checkpoints = audit.NewCheckpoints(audit.CheckpointsConfig{
		Blobs: counted, Entries: reader, Signer: signer, Marks: nil, Now: nil,
	})
	chain.observed = nil

	if _, err := chain.server.AppendServer(t.Context(), pendingTestEntry()); err != nil {
		t.Fatalf("append server entry: %v", err)
	}

	rec, err := chain.store.ChainRecord(t.Context())
	if err != nil {
		t.Fatalf("chain record: %v", err)
	}

	rec.Pending = false

	if err := chain.store.SaveChainRecord(t.Context(), rec); err != nil {
		t.Fatalf("save chain record: %v", err)
	}

	// Another instance of the same signer starts epoch changes on the raw blobs.
	other := audit.NewCheckpoints(audit.CheckpointsConfig{
		Blobs: raw, Entries: reader, Signer: signer, Marks: nil, Now: nil,
	})

	return chain, counted, other
}

// TestCheckReadsTheEpochOncePerCycle proves that a check with no epoch change
// reads the epoch list once for the cycle plus once before the write of the
// one checkpoint.
func TestCheckReadsTheEpochOncePerCycle(t *testing.T) {
	t.Parallel()

	chain, counted, _ := newEpochCycleChain(t)

	chain.check(t.Context())

	// One scan for the cycle and one recheck before the one write.
	if got, want := counted.count(), 2; got != want {
		t.Fatalf("epoch scans in one cycle: got %d, want %d", got, want)
	}

	if got := checkpointCountOf(t, counted); got != 1 {
		t.Fatalf("checkpoints after the cycle: got %d objects, want one", got)
	}
}

// TestCheckWritesNothingWhenTheEpochChangesMidCycle proves that an epoch that
// changes after the cycle read it stops the write: the recheck before the
// store refuses.
func TestCheckWritesNothingWhenTheEpochChangesMidCycle(t *testing.T) {
	t.Parallel()

	chain, counted, other := newEpochCycleChain(t)

	counted.onScan = func(n int) {
		// Scan 1 is the cycle read. Start a new epoch before the recheck.
		if n == 2 {
			if _, err := other.NewEpoch(t.Context(), time.Unix(1_700_000_000, 0)); err != nil {
				t.Errorf("new epoch: %v", err)
			}
		}
	}

	chain.check(t.Context())

	if got := checkpointCountOf(t, counted); got != 0 {
		t.Fatalf("checkpoints after an epoch change mid-cycle: got %d objects, want none", got)
	}

	if got := counted.count(); got < 2 {
		t.Fatalf("epoch scans: got %d, want the recheck to run", got)
	}
}

// TestCheckFailsClosedWhenTheEpochReadFails proves that a cycle whose epoch
// read fails falls back to the reads of each scope: a bad epoch object fails
// the scope, the scope counts as invalid, and nothing is signed. The chain is
// pending, so no write can hide the result.
func TestCheckFailsClosedWhenTheEpochReadFails(t *testing.T) {
	t.Parallel()

	chain, counted, _ := newEpochCycleChain(t)
	chain.observed = metrics.NewServerMetrics("test", time.Unix(1_700_000_000, 0))

	rec, err := chain.store.ChainRecord(t.Context())
	if err != nil {
		t.Fatalf("chain record: %v", err)
	}

	rec.Pending = true

	if err = chain.store.SaveChainRecord(t.Context(), rec); err != nil {
		t.Fatalf("save chain record: %v", err)
	}

	// A key under the epoch directory that the server does not write.
	bad := blob.Key(audit.CheckpointPrefix + "/epochs/not-an-epoch.json")
	if err = counted.Put(t.Context(), bad, strings.NewReader("{}")); err != nil {
		t.Fatalf("plant bad epoch object: %v", err)
	}

	chain.check(t.Context())

	if got := metricLine(t, chain); !strings.Contains(got, "dustpan_audit_chain_invalid 1") {
		t.Fatalf("metrics after a failed epoch read:\n%s\nwant one invalid scope", got)
	}

	if got := checkpointCountOf(t, counted); got != 0 {
		t.Fatalf("checkpoints after a failed epoch read: got %d objects, want none", got)
	}
}

// checkpointCountOf counts the checkpoint objects (not epoch objects) behind
// the wrapper.
func checkpointCountOf(t *testing.T, s blob.Store) int {
	t.Helper()

	objects, err := blobtest.ListAll(t.Context(), s, audit.CheckpointPrefix+"/")
	if err != nil {
		t.Fatalf("list checkpoints: %v", err)
	}

	n := 0

	for _, o := range objects {
		if !strings.Contains(string(o.Key), "/epochs/") {
			n++
		}
	}

	return n
}
