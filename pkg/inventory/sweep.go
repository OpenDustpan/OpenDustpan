package inventory

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// BlobPurgeGrace is how long a blob purge record stays before the sweep can
// finish it. A checkpoint save or a state upload can pass its checks
// before a force-delete and a purge, then write its blob after the first
// delete of the blobs of the unit, and then fail its SQL commit. The sweep
// deletes the blobs of a record again only when the record is older than this
// period, and finishes it only when that delete leaves no blob. The server
// sets no write timeout on a request, so the period is a wide safety margin
// over the longest save or upload with its retries, not a policy. It is not a
// setting.
const BlobPurgeGrace = 24 * time.Hour

// SweepPageSize is the number of expired deleted units that the sweep reads
// in one call to the store.
const SweepPageSize = 100

// SweepBlobPurgeLimit is the largest number of blob purge records that one
// sweep reads.
const SweepBlobPurgeLimit = 1000

// ExpiredCursor is the position after which a page of expired deleted units
// starts: the order key of the last entry of the page before.
type ExpiredCursor struct {
	PurgeAfter time.Time
	Tool       string
	UnitID     string
}

// ExpiredDeleted reads the deleted units whose retention period has ended,
// across every namespace.
type ExpiredDeleted interface {
	// ListExpiredDeleted returns at most limit deleted units whose PurgeAfter
	// is at or before now, ordered by PurgeAfter, then tool, then unit ID.
	// With a cursor, it returns only the units after the cursor in that
	// order. It defines no sentinel error.
	ListExpiredDeleted(ctx context.Context, now time.Time, after *ExpiredCursor, limit int) ([]DeletedEntry, error)
}

// PurgedBlobs deletes the blobs of the purged units of one tool. Each tool
// implements it with its own blob layout.
type PurgedBlobs interface {
	// DeletePurgedBlobs deletes every blob of the purged unit unitID. It
	// returns nil only when no blob of the unit remains.
	DeletePurgedBlobs(ctx context.Context, unitID string) error
}

// Reference returns the unit reference of e in the form
// <tool>:<namespace>/<name>, with the name of the unit at its delete.
func (e DeletedEntry) Reference() string {
	return e.Tool + ":" + e.Namespace.String() + "/" + e.Name
}

// SweepResult is the outcome of one sweep. Purged holds the units that the
// sweep purged. Failed counts the purges, the blob deletes and the record
// finishes that failed.
// Skipped counts the expired units that the sweep left to another
// installation or that a restore took first. FinishedBlobPurges counts the
// blob purge records that the sweep finished, and PendingBlobPurges the
// records that stay.
type SweepResult struct {
	Purged             []DeletedEntry
	Failed             int
	Skipped            int
	FinishedBlobPurges int
	PendingBlobPurges  int
}

// SweeperConfig holds the ports of a Sweeper. Lifecycles and Blobs map each
// stored tool code to its port. Now defaults to time.Now.
type SweeperConfig struct {
	Expired    ExpiredDeleted
	BlobPurges BlobPurges
	Lifecycles map[string]Lifecycle
	Blobs      map[string]PurgedBlobs
	Logger     *slog.Logger
	Now        func() time.Time
}

// ErrMissingDependency reports a SweeperConfig without a required port.
var ErrMissingDependency = errors.New("missing dependency")

// Sweeper runs the retention sweep: the final delete of each
// deleted unit whose retention period has ended, and the finish of the blob
// purge records. One sweep runs at a time.
type Sweeper struct {
	cfg SweeperConfig
	mu  sync.Mutex
}

// NewSweeper returns a Sweeper, or ErrMissingDependency when cfg lacks a port.
func NewSweeper(cfg SweeperConfig) (*Sweeper, error) {
	switch {
	case cfg.Expired == nil:
		return nil, fmt.Errorf("%w: expired deleted units", ErrMissingDependency)
	case cfg.BlobPurges == nil:
		return nil, fmt.Errorf("%w: blob purge records", ErrMissingDependency)
	case len(cfg.Lifecycles) == 0:
		return nil, fmt.Errorf("%w: unit lifecycles", ErrMissingDependency)
	case len(cfg.Blobs) == 0:
		return nil, fmt.Errorf("%w: purged blobs", ErrMissingDependency)
	case cfg.Logger == nil:
		return nil, fmt.Errorf("%w: logger", ErrMissingDependency)
	}

	if cfg.Now == nil {
		cfg.Now = time.Now
	}

	return &Sweeper{cfg: cfg, mu: sync.Mutex{}}, nil
}

// Sweep runs one sweep. It purges each deleted unit whose retention period
// has ended, through the lifecycle of its tool, with the audit entry e: the
// sweep sets the namespace, the object and the object ID of the unit. Then it
// deletes the blobs of each blob purge record older than BlobPurgeGrace
// again, and finishes the record when no blob remains. A failed purge or blob
// delete does not stop the sweep: Sweep counts it, logs a warning with its
// error kind, and the next sweep tries again. Sweep returns an error only
// when it cannot read the units or the records.
func (s *Sweeper) Sweep(ctx context.Context, e audit.Entry) (SweepResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.cfg.Now()
	res := SweepResult{Purged: []DeletedEntry{}, Failed: 0, Skipped: 0, FinishedBlobPurges: 0, PendingBlobPurges: 0}

	if err := s.purgeExpired(ctx, now, e, &res); err != nil {
		return res, err
	}

	if err := s.finishBlobPurges(ctx, now, &res); err != nil {
		return res, err
	}

	return res, nil
}

// purgeExpired purges every deleted unit whose retention period ended at or
// before now, page by page.
func (s *Sweeper) purgeExpired(ctx context.Context, now time.Time, e audit.Entry, res *SweepResult) error {
	var cursor *ExpiredCursor

	for {
		page, err := s.cfg.Expired.ListExpiredDeleted(ctx, now, cursor, SweepPageSize)
		if err != nil {
			return fmt.Errorf("list expired deleted units: %w", err)
		}

		for i := range page {
			s.purgeOne(ctx, page[i], e, res)
		}

		if len(page) < SweepPageSize {
			return nil
		}

		last := page[len(page)-1]
		cursor = &ExpiredCursor{PurgeAfter: last.PurgeAfter, Tool: last.Tool, UnitID: last.UnitID}
	}
}

// purgeOne purges the deleted unit d and records the outcome in res.
func (s *Sweeper) purgeOne(ctx context.Context, d DeletedEntry, e audit.Entry, res *SweepResult) {
	lifecycle, ok := s.cfg.Lifecycles[d.Tool]
	if !ok {
		res.Failed++
		s.cfg.Logger.WarnContext(ctx, "retention sweep cannot purge a unit of an unknown tool",
			slog.String("reference", d.Reference()), slog.String("error_kind", "unknown_tool"))

		return
	}

	e.Namespace = d.Namespace
	e.Object = d.Reference()
	e.ObjectID = d.UnitID

	err := lifecycle.Purge(ctx, d.Namespace, d.UnitID, e)

	switch {
	case err == nil:
		res.Purged = append(res.Purged, d)
	case errors.Is(err, ErrNotFound), errors.Is(err, namespace.ErrNotHome):
		// A restore took the unit first, or another installation is the
		// home of its namespace and purges it there.
		res.Skipped++
	default:
		res.Failed++
		s.cfg.Logger.WarnContext(ctx, "retention sweep failed to purge a unit",
			slog.String("reference", d.Reference()), slog.String("error_kind", ErrorKind(err)))
	}
}

// finishBlobPurges deletes the blobs of each record older than
// BlobPurgeGrace again and finishes the record when no blob remains.
func (s *Sweeper) finishBlobPurges(ctx context.Context, now time.Time, res *SweepResult) error {
	records, err := s.cfg.BlobPurges.ListBlobPurges(ctx, SweepBlobPurgeLimit)
	if err != nil {
		return fmt.Errorf("list blob purge records: %w", err)
	}

	for _, r := range records {
		if now.Sub(r.PurgedAt) < BlobPurgeGrace {
			res.PendingBlobPurges++

			continue
		}

		if !s.deletePurgedBlobs(ctx, r) {
			res.Failed++
			res.PendingBlobPurges++

			continue
		}

		if finishErr := s.cfg.BlobPurges.FinishBlobPurge(ctx, r.Tool, r.UnitID); finishErr != nil {
			res.Failed++
			res.PendingBlobPurges++
			s.cfg.Logger.WarnContext(ctx, "retention sweep failed to finish a blob purge record",
				slog.String("tool", r.Tool), slog.String("unit_id", r.UnitID),
				slog.String("error_kind", ErrorKind(finishErr)))

			continue
		}

		res.FinishedBlobPurges++
	}

	return nil
}

// deletePurgedBlobs deletes the blobs of the unit of r and reports whether
// no blob remains. A failure logs a warning without a blob key, and the
// record stays pending.
func (s *Sweeper) deletePurgedBlobs(ctx context.Context, r BlobPurge) bool {
	blobs, ok := s.cfg.Blobs[r.Tool]
	if !ok {
		s.cfg.Logger.WarnContext(ctx, "retention sweep cannot delete the blobs of an unknown tool",
			slog.String("tool", r.Tool), slog.String("unit_id", r.UnitID), slog.String("error_kind", "unknown_tool"))

		return false
	}

	if err := blobs.DeletePurgedBlobs(ctx, r.UnitID); err != nil {
		s.cfg.Logger.WarnContext(ctx, "blobs of a purged unit remain after the retention sweep",
			slog.String("tool", r.Tool), slog.String("unit_id", r.UnitID), slog.String("error_kind", ErrorKind(err)))

		return false
	}

	return true
}

// ErrorKind names the class of err for a log line, from the namespace
// sentinels that a fenced write can return, so that the log never holds the
// text of a driver error. A context error is "canceled" or "deadline", and
// any other error is "store".
func ErrorKind(err error) string {
	switch {
	case errors.Is(err, namespace.ErrFenced):
		return "fenced"
	case errors.Is(err, namespace.ErrNotHome):
		return "not_home"
	case errors.Is(err, namespace.ErrNotFound):
		return "namespace_not_found"
	case errors.Is(err, namespace.ErrUnavailable):
		return "unavailable"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline"
	default:
		return "store"
	}
}

// expiredAfter reports whether e comes after the cursor c in the order of
// ListExpiredDeleted.
func expiredAfter(e DeletedEntry, c ExpiredCursor) bool {
	switch {
	case !e.PurgeAfter.Equal(c.PurgeAfter):
		return e.PurgeAfter.After(c.PurgeAfter)
	case e.Tool != c.Tool:
		return e.Tool > c.Tool
	default:
		return e.UnitID > c.UnitID
	}
}

// PageExpired defines the result of ListExpiredDeleted for a list of deleted
// units: at most limit units whose PurgeAfter is at or before now, after the
// cursor when one is set, in the order of the port. Store conformance tests
// compare each adapter with it.
func PageExpired(entries []DeletedEntry, now time.Time, after *ExpiredCursor, limit int) []DeletedEntry {
	out := make([]DeletedEntry, 0, len(entries))

	for _, e := range entries {
		if e.PurgeAfter.After(now) || (after != nil && !expiredAfter(e, *after)) {
			continue
		}

		out = append(out, e)
	}

	slices.SortFunc(out, func(a, b DeletedEntry) int {
		switch {
		case !a.PurgeAfter.Equal(b.PurgeAfter):
			return a.PurgeAfter.Compare(b.PurgeAfter)
		case a.Tool != b.Tool:
			return strings.Compare(a.Tool, b.Tool)
		default:
			return strings.Compare(a.UnitID, b.UnitID)
		}
	})

	if len(out) > limit {
		out = out[:limit]
	}

	return out
}
