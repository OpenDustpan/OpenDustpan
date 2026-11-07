package audit_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/blob/blobtest"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// epochCounter wraps a blob store. It counts the first page of each list of
// the epoch directory, and runs onScan before it answers scan n.
type epochCounter struct {
	blob.Store

	onScan func(n int)
	scans  int
	mu     sync.Mutex
}

func (e *epochCounter) ListPage(ctx context.Context, req blob.PageRequest) (blob.Page, error) {
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

func (e *epochCounter) count() int {
	e.mu.Lock()
	defer e.mu.Unlock()

	return e.scans
}

// TestCheckpointsAtReuseTheEpochOfTheCycle proves that one cycle over
// several scopes reads the epoch once for the cycle plus once before each
// write, and never in VerifyAt or in the gates of WriteAt.
func TestCheckpointsAtReuseTheEpochOfTheCycle(t *testing.T) {
	t.Parallel()

	f := newCheckpointFixture(t, 3)
	counted := &epochCounter{Store: f.blobs, onScan: nil, scans: 0, mu: sync.Mutex{}}
	cps := audit.NewCheckpoints(audit.CheckpointsConfig{
		Blobs: counted, Entries: f.entries, Signer: newSigner(t, 7), Marks: nil, Now: nil,
	})

	epoch, err := cps.Epoch(t.Context())
	if err != nil {
		t.Fatalf("epoch: %v", err)
	}

	for _, scope := range []string{"acme", "beta", "gamma"} {
		res, err := cps.VerifyAt(t.Context(), namespace.Name(scope), epoch)
		if err != nil || !res.Valid {
			t.Fatalf("verify %s: %+v, %v", scope, res, err)
		}

		wrote, err := cps.WriteAt(t.Context(), namespace.Name(scope), time.Unix(1_700_000_000, 0), epoch)
		if err != nil || !wrote {
			t.Fatalf("write %s: %v, %v", scope, wrote, err)
		}
	}

	// One scan for the cycle and one recheck before each of three writes.
	if got, want := counted.count(), 4; got != want {
		t.Fatalf("epoch scans: got %d, want %d", got, want)
	}
}

// TestCheckpointsWriteAtStopsAfterAnEpochChange proves that the recheck
// before the store refuses when the epoch changed after the cycle read it.
func TestCheckpointsWriteAtStopsAfterAnEpochChange(t *testing.T) {
	t.Parallel()

	f := newCheckpointFixture(t, 2)
	counted := &epochCounter{Store: f.blobs, onScan: nil, scans: 0, mu: sync.Mutex{}}
	cps := audit.NewCheckpoints(audit.CheckpointsConfig{
		Blobs: counted, Entries: f.entries, Signer: newSigner(t, 7), Marks: nil, Now: nil,
	})

	epoch, err := cps.Epoch(t.Context())
	if err != nil {
		t.Fatalf("epoch: %v", err)
	}

	if _, newErr := f.cps.NewEpoch(t.Context(), time.Unix(1_700_000_000, 0)); newErr != nil {
		t.Fatalf("new epoch: %v", newErr)
	}

	wrote, err := cps.WriteAt(t.Context(), "acme", time.Unix(1_700_000_001, 0), epoch)
	if err != nil || wrote {
		t.Fatalf("write after an epoch change: wrote %v, err %v, want false and no error", wrote, err)
	}

	objects, err := blobtest.ListAll(t.Context(), f.blobs, audit.CheckpointPrefix+"/")
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	for _, o := range objects {
		if !strings.Contains(string(o.Key), "/epochs/") {
			t.Fatalf("checkpoint %s stored after an epoch change", o.Key)
		}
	}
}
