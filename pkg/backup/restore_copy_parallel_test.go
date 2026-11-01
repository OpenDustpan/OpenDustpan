package backup_test

import (
	"context"
	"io"
	"sync"
	"testing"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/blob"
)

// overlapStore holds each Put until a second Put is in flight. It is a
// barrier, not a timer: a restore that copies one blob at a time never lets
// a second Put start, so the test does not end until the test timeout. It
// records the most Puts that were in flight together.
type overlapStore struct {
	blob.Store

	arrived  chan struct{}
	mu       sync.Mutex
	inflight int
	maxSeen  int
	once     sync.Once
}

func (o *overlapStore) Put(ctx context.Context, key blob.Key, r io.Reader) error {
	o.mu.Lock()
	o.inflight++
	o.maxSeen = max(o.maxSeen, o.inflight)

	if o.inflight >= 2 {
		o.once.Do(func() { close(o.arrived) })
	}

	o.mu.Unlock()

	select {
	case <-o.arrived:
	case <-ctx.Done():
	}

	o.mu.Lock()
	o.inflight--
	o.mu.Unlock()

	return o.Store.Put(ctx, key, r)
}

func (o *overlapStore) most() int {
	o.mu.Lock()
	defer o.mu.Unlock()

	return o.maxSeen
}

// TestRestoreCopiesBlobsAtTheSameTime proves that a restore of a backup
// with two blobs writes the two blobs to the live store at the same time,
// and that the restore still succeeds.
func TestRestoreCopiesBlobsAtTheSameTime(t *testing.T) {
	t.Parallel()

	overlap := &overlapStore{
		Store:    nil,
		arrived:  make(chan struct{}),
		mu:       sync.Mutex{},
		inflight: 0,
		maxSeen:  0,
		once:     sync.Once{},
	}

	setup := newRestoreServiceSetupWith(t, func(cfg *backup.RestoreConfig) {
		overlap.Store = cfg.Blobs
		cfg.Blobs = overlap
	})

	const id = backup.RestoreID("rst-copyparallelaaaaaaaa")

	err := setup.service.Start(t.Context(), id, backup.RestoreRequest{
		BackupID:         setup.id,
		PreBackupID:      "",
		CheckOnly:        false,
		Force:            false,
		RequestedBy:      "tester",
		KeepAccessTables: false,
	})
	if err != nil {
		t.Fatalf("start restore: %v", err)
	}

	row := waitRestoreEnd(t, setup.regs, id)
	if row.State != backup.RestoreSucceeded {
		t.Fatalf("restore = %s %q, want succeeded", row.State, row.Error)
	}

	if got := overlap.most(); got < 2 {
		t.Errorf("most writes in flight = %d, want 2 or more", got)
	}
}
