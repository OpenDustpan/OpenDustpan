package metrics

import (
	"context"
	"sync/atomic"
	"time"
)

// blobPurgePendingName names the count of blob purge records that still have
// blobs to delete.
const blobPurgePendingName = "dustpan_blob_purge_pending"

// blobPurgeOldestName names the age of the oldest blob purge record.
const blobPurgeOldestName = "dustpan_blob_purge_oldest_age_seconds"

// blobPurgeReadTimeout bounds the store read of one scrape.
const blobPurgeReadTimeout = 5 * time.Second

// blobPurgeTTL is how long a read of the blob purge backlog stays valid. The
// two gauges of one scrape share one read of the store within this time.
const blobPurgeTTL = 2 * time.Second

// BlobPurgeReader reads the blob purge backlog: the number of records, and the
// purge time of the oldest record, which is the zero time when no record
// exists. It returns an error when the read fails.
type BlobPurgeReader func(ctx context.Context) (pending int64, oldest time.Time, err error)

// blobPurgeSnapshot is one read of the blob purge backlog.
type blobPurgeSnapshot struct {
	at      time.Time
	pending int64
	oldest  time.Time
}

// blobPurgeGauges serves the two blob purge gauges from one read of the store.
type blobPurgeGauges struct {
	read BlobPurgeReader
	now  func() time.Time
	last atomic.Pointer[blobPurgeSnapshot]
}

// snapshot returns the last read of the backlog, or reads the store again when
// that read is older than blobPurgeTTL. A failed read gives zero for both
// gauges, as the worker gauges do. Two scrapes may read at the same time; the
// last read wins, so the lock is not held across the read.
func (g *blobPurgeGauges) snapshot() blobPurgeSnapshot {
	now := g.now()
	if s := g.last.Load(); s != nil && now.Sub(s.at) < blobPurgeTTL {
		return *s
	}

	ctx, cancel := context.WithTimeout(context.Background(), blobPurgeReadTimeout)
	defer cancel()

	s := blobPurgeSnapshot{at: now, pending: 0, oldest: time.Time{}}

	pending, oldest, err := g.read(ctx)
	if err == nil {
		s.pending = pending
		s.oldest = oldest
	}

	g.last.Store(&s)

	return s
}

// pendingValue returns dustpan_blob_purge_pending at scrape time.
func (g *blobPurgeGauges) pendingValue() float64 {
	return float64(g.snapshot().pending)
}

// oldestAgeValue returns dustpan_blob_purge_oldest_age_seconds at scrape time:
// the age of the oldest record, or 0 when none exists. The age is never
// negative, so a record that carries a later time than this process clock
// gives 0.
func (g *blobPurgeGauges) oldestAgeValue() float64 {
	s := g.snapshot()
	if s.pending == 0 {
		return 0
	}

	return max(0, g.now().Sub(s.oldest).Seconds())
}

// RegisterBlobPurgeBacklog adds dustpan_blob_purge_pending and
// dustpan_blob_purge_oldest_age_seconds to reg. Both gauges have no labels and
// count every namespace. A scrape calls read at most once per blobPurgeTTL. now
// gives the clock of the age.
func RegisterBlobPurgeBacklog(reg *Registry, read BlobPurgeReader, now func() time.Time) {
	g := &blobPurgeGauges{read: read, now: now, last: atomic.Pointer[blobPurgeSnapshot]{}}

	reg.RegisterFunc(blobPurgePendingName,
		"Blob purge records that still have blobs to delete, 0 when a read fails.",
		g.pendingValue)

	reg.RegisterFunc(blobPurgeOldestName,
		"Seconds since the oldest blob purge record was made, 0 when none exists.",
		g.oldestAgeValue)
}
