package inventory

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/backup/blobguard"
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

// SweepPageSize is the number of expired deleted units, or of blob purge
// records, that the sweep reads in one call to the store.
const SweepPageSize = 100

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

// SweepTool is the part of one tool that the retention sweep needs. Each
// tool implements it with its own model and blob layout.
type SweepTool interface {
	// PurgeExpired removes the deleted unit unitID of ns for good, as
	// Lifecycle.Purge does, only when its retention period ended at or
	// before dueBy. The tool checks the end of the retention in the purge
	// transaction. It returns ErrNotFound if ns has no deleted unit unitID
	// whose retention period ended by then: a restore and a new delete after
	// the sweep read the unit gave it a new retention period.
	PurgeExpired(ctx context.Context, ns namespace.Name, unitID string, dueBy time.Time, e audit.Entry) error
	// DeletePurgedBlobs deletes every blob of the purged unit unitID of the
	// namespace with the ID ns. The blob purge record carries the ID from the
	// join with the namespace row, and a tool that keeps the blobs of a unit
	// under the prefix of its namespace needs it. It returns nil only when no
	// blob of the unit remains.
	DeletePurgedBlobs(ctx context.Context, ns namespace.ID, unitID string) error
}

// Reference returns the unit reference of e in the form
// <tool>:<namespace>/<name>, with the name of the unit at its delete.
func (e DeletedEntry) Reference() string {
	return e.Tool + ":" + e.Namespace.String() + "/" + e.Name
}

// SweepResult is the outcome of one sweep. Purged holds the units that the
// sweep purged. Failed counts the purges, the blob deletes and the record
// finishes that failed. Skipped counts the expired units that the sweep left:
// another installation is the home of their namespace, a restore took them
// first, or the server stopped the sweep before their purge.
// FinishedBlobPurges counts the blob purge records that the sweep finished,
// and PendingBlobPurges the records that stay. WaitingBlobPurges counts the
// records that stay because a backup or a restore runs, and WaitingFor names
// the last of that work that the sweep saw, for example "backup bkp-...", or is
// empty when no record waited. A record that waits
// counts in PendingBlobPurges too, and not in Failed.
type SweepResult struct {
	Purged             []DeletedEntry
	Failed             int
	Skipped            int
	FinishedBlobPurges int
	PendingBlobPurges  int
	WaitingBlobPurges  int
	WaitingFor         string
}

// SweepLocker makes one retention sweep run at a time on a store, for every
// server process that uses the store.
type SweepLocker interface {
	// TryLockSweep takes the sweep lock of the store without a wait. It
	// returns ErrSweepRunning when a sweep of any server process holds the
	// lock. Otherwise it returns a release function. The caller calls it
	// one time, when its sweep ends, also after its ctx ended. A server
	// process that stops without a release gives up the lock too.
	TryLockSweep(ctx context.Context) (release func(), err error)
}

// SweeperConfig holds the ports of a Sweeper. Tools maps each stored tool
// code to its port. Now defaults to time.Now.
type SweeperConfig struct {
	Expired    ExpiredDeleted
	BlobPurges BlobPurges
	Lock       SweepLocker
	Tools      map[string]SweepTool
	Logger     *slog.Logger
	Now        func() time.Time
}

// ErrMissingDependency reports a SweeperConfig without a required port.
var ErrMissingDependency = errors.New("missing dependency")

// ErrSweepRunning reports a sweep that cannot start because another sweep
// holds the sweep lock of the store. The other sweep can run in this server
// process or in another one. The caller can try again later.
var ErrSweepRunning = errors.New("a retention sweep is running")

// Sweeper runs the retention sweep: the final delete of each deleted unit
// whose retention period has ended, and the finish of the blob purge
// records. One sweep runs at a time on a store, across all server processes
// that use it: the sweep lock of the store (SweepLocker) allows it.
//
// The end of a retention period and the age of a record compare with the
// clock of the server that runs the sweep (Now), not with the database clock.
// Servers of one installation with a clock skew purge a unit earlier or later
// by that skew. The retention period counts in hours, so a skew of seconds
// does not matter.
type Sweeper struct {
	cfg SweeperConfig
}

// NewSweeper returns a Sweeper, or ErrMissingDependency when cfg lacks a port.
func NewSweeper(cfg SweeperConfig) (*Sweeper, error) {
	switch {
	case cfg.Expired == nil:
		return nil, fmt.Errorf("%w: expired deleted units", ErrMissingDependency)
	case cfg.BlobPurges == nil:
		return nil, fmt.Errorf("%w: blob purge records", ErrMissingDependency)
	case cfg.Lock == nil:
		return nil, fmt.Errorf("%w: sweep lock", ErrMissingDependency)
	case len(cfg.Tools) == 0:
		return nil, fmt.Errorf("%w: sweep tools", ErrMissingDependency)
	case cfg.Logger == nil:
		return nil, fmt.Errorf("%w: logger", ErrMissingDependency)
	}

	if cfg.Now == nil {
		cfg.Now = time.Now
	}

	return &Sweeper{cfg: cfg}, nil
}

// Sweep runs one sweep. It purges each deleted unit whose retention period
// ended at the start of the sweep, through the tool of the unit, with the
// audit entry e: the sweep sets the namespace, the object and the object ID
// of the unit. Then it deletes the blobs of each blob purge record older than
// BlobPurgeGrace again, and finishes the record when no blob remains. A
// failed purge or blob delete does not stop the sweep: Sweep counts it, logs
// a warning with its error kind, and the next sweep tries again. When ctx
// ends, Sweep stops and returns what it did, without an error. Sweep returns
// ErrSweepRunning at once when another sweep holds the sweep lock of the
// store, also a sweep of another server process, and another error only when
// it cannot take the lock, or cannot read the units or the records.
func (s *Sweeper) Sweep(ctx context.Context, e audit.Entry) (SweepResult, error) {
	res := SweepResult{
		Purged: []DeletedEntry{}, Failed: 0, Skipped: 0, FinishedBlobPurges: 0, PendingBlobPurges: 0,
		WaitingBlobPurges: 0, WaitingFor: "",
	}

	release, lockErr := s.cfg.Lock.TryLockSweep(ctx)
	if lockErr != nil {
		return res, fmt.Errorf("take the sweep lock: %w", lockErr)
	}
	defer release()

	now := s.cfg.Now()

	if err := s.purgeExpired(ctx, now, e, &res); err != nil {
		return res, err
	}

	if err := s.finishBlobPurges(ctx, now, &res); err != nil {
		return res, err
	}

	return res, nil
}

// CountDue returns the number of deleted units whose retention period has
// ended now: the units that the next sweep purges, unless a restore takes
// them first.
func (s *Sweeper) CountDue(ctx context.Context) (int, error) {
	now := s.cfg.Now()

	var (
		cursor *ExpiredCursor
		count  int
	)

	for {
		page, err := s.cfg.Expired.ListExpiredDeleted(ctx, now, cursor, SweepPageSize)
		if err != nil {
			return count, fmt.Errorf("list expired deleted units: %w", err)
		}

		count += len(page)

		if len(page) < SweepPageSize {
			return count, nil
		}

		last := page[len(page)-1]
		cursor = &ExpiredCursor{PurgeAfter: last.PurgeAfter, Tool: last.Tool, UnitID: last.UnitID}
	}
}

// stopped reports whether ctx has ended. A sweep that the server stops, or
// that reaches its deadline, returns what it did so far.
func stopped(ctx context.Context) bool {
	return ctx.Err() != nil
}

// purgeExpired purges every deleted unit whose retention period ended at or
// before now, page by page.
func (s *Sweeper) purgeExpired(ctx context.Context, now time.Time, e audit.Entry, res *SweepResult) error {
	var cursor *ExpiredCursor

	for !stopped(ctx) {
		page, err := s.cfg.Expired.ListExpiredDeleted(ctx, now, cursor, SweepPageSize)
		if stopped(ctx) {
			return nil
		}

		if err != nil {
			return fmt.Errorf("list expired deleted units: %w", err)
		}

		for i := range page {
			s.purgeOne(ctx, page[i], now, e, res)
		}

		if len(page) < SweepPageSize {
			return nil
		}

		last := page[len(page)-1]
		cursor = &ExpiredCursor{PurgeAfter: last.PurgeAfter, Tool: last.Tool, UnitID: last.UnitID}
	}

	return nil
}

// purgeOne purges the deleted unit d when its retention period ended at or
// before now, and records the outcome in res.
func (s *Sweeper) purgeOne(ctx context.Context, d DeletedEntry, now time.Time, e audit.Entry, res *SweepResult) {
	if stopped(ctx) {
		res.Skipped++

		return
	}

	tool, ok := s.cfg.Tools[d.Tool]
	if !ok {
		res.Failed++
		s.cfg.Logger.WarnContext(ctx, "retention sweep cannot purge a unit of an unknown tool",
			slog.String("reference", d.Reference()), slog.String("error_kind", "unknown_tool"))

		return
	}

	e.Namespace = d.Namespace
	e.Object = d.Reference()
	e.ObjectID = d.UnitID

	err := tool.PurgeExpired(ctx, d.Namespace, d.UnitID, now, e)

	switch {
	case err == nil:
		res.Purged = append(res.Purged, d)
	case errors.Is(err, ErrNotFound), errors.Is(err, namespace.ErrNotHome), stopped(ctx):
		// A restore took the unit first, a new delete gave it a new
		// retention period, another installation is the home of its
		// namespace, or the server stopped the sweep.
		res.Skipped++
	default:
		res.Failed++
		s.cfg.Logger.WarnContext(ctx, "retention sweep failed to purge a unit",
			slog.String("reference", d.Reference()), slog.String("error_kind", ErrorKind(err)))
	}
}

// finishBlobPurges deletes the blobs of each record older than
// BlobPurgeGrace again and finishes the record when no blob remains. It
// reads the records page by page, so records whose delete keeps failing do
// not hide the newer ones.
func (s *Sweeper) finishBlobPurges(ctx context.Context, now time.Time, res *SweepResult) error {
	var cursor *BlobPurgeCursor

	for !stopped(ctx) {
		records, err := s.cfg.BlobPurges.ListBlobPurges(ctx, cursor, SweepPageSize)
		if stopped(ctx) {
			return nil
		}

		if err != nil {
			return fmt.Errorf("list blob purge records: %w", err)
		}

		for _, r := range records {
			s.finishOne(ctx, r, now, res)
		}

		if len(records) < SweepPageSize {
			return nil
		}

		last := records[len(records)-1]
		cursor = &BlobPurgeCursor{PurgedAt: last.PurgedAt, Tool: last.Tool, UnitID: last.UnitID}
	}

	return nil
}

// finishOne deletes the blobs of the record r again when it is older than
// BlobPurgeGrace, finishes it when no blob remains, and records the outcome
// in res.
func (s *Sweeper) finishOne(ctx context.Context, r BlobPurge, now time.Time, res *SweepResult) {
	if now.Sub(r.PurgedAt) < BlobPurgeGrace || stopped(ctx) {
		res.PendingBlobPurges++

		return
	}

	if waiting, ok := s.deletePurgedBlobs(ctx, r); !ok {
		res.PendingBlobPurges++

		if waiting != "" {
			res.WaitingBlobPurges++
			res.WaitingFor = waiting
		} else if !stopped(ctx) {
			res.Failed++
		}

		return
	}

	if err := s.cfg.BlobPurges.FinishBlobPurge(ctx, r.Tool, r.UnitID); err != nil {
		res.PendingBlobPurges++

		if !stopped(ctx) {
			res.Failed++
			s.cfg.Logger.WarnContext(ctx, "retention sweep failed to finish a blob purge record",
				slog.String("tool", r.Tool), slog.String("unit_id", r.UnitID),
				slog.String("error_kind", ErrorKind(err)))
		}

		return
	}

	res.FinishedBlobPurges++
}

// deletePurgedBlobs deletes the blobs of the unit of r and reports whether
// no blob remains. When a backup or a restore runs, the tool keeps the blobs:
// the first result names that work, for example "backup bkp-...", and the
// record stays pending with an INFO line and no failure. Any other failure
// logs a warning without a blob key, unless the server stopped the sweep, and
// the record stays pending.
func (s *Sweeper) deletePurgedBlobs(ctx context.Context, r BlobPurge) (string, bool) {
	tool, ok := s.cfg.Tools[r.Tool]
	if !ok {
		s.cfg.Logger.WarnContext(ctx, "retention sweep cannot delete the blobs of an unknown tool",
			slog.String("tool", r.Tool), slog.String("unit_id", r.UnitID), slog.String("error_kind", "unknown_tool"))

		return "", false
	}

	// A record whose namespace could not be matched has no namespace ID, and a
	// record with a malformed ID is just as unusable. The delete
	// below would fail closed on each run with the kind "store", so the record
	// gets its own kind and the stuck record is easy to find.
	//
	// Defense in depth: the tool checks the namespace ID too. This check stays
	// so that a tool that does not never gets an empty scope from a record.
	if _, err := namespace.ParseID(r.NamespaceID.String()); err != nil {
		s.cfg.Logger.WarnContext(ctx, "blobs of a purged unit remain: the record has no namespace",
			slog.String("tool", r.Tool), slog.String("unit_id", r.UnitID),
			slog.String("error_kind", errorKindNoNamespace))

		return "", false
	}

	if err := tool.DeletePurgedBlobs(ctx, r.NamespaceID, r.UnitID); err != nil {
		if wait, isWait := errors.AsType[*blobguard.WaitError](err); isWait {
			s.cfg.Logger.InfoContext(ctx, "blobs of a purged unit wait for a running backup or restore",
				slog.String("tool", r.Tool), slog.String("unit_id", r.UnitID),
				slog.String("running", wait.Work.String()))

			return wait.Work.String(), false
		}

		if !stopped(ctx) {
			s.cfg.Logger.WarnContext(ctx, "blobs of a purged unit remain after the retention sweep",
				slog.String("tool", r.Tool), slog.String("unit_id", r.UnitID),
				slog.String("error_kind", ErrorKind(err)))
		}

		return "", false
	}

	return "", true
}

// errorKindNoNamespace is the error kind of a blob purge record whose
// namespace row is gone.
const errorKindNoNamespace = "namespace_missing"

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

// blobPurgeAfter reports whether r comes after the cursor c in the order of
// ListBlobPurges.
func blobPurgeAfter(r BlobPurge, c BlobPurgeCursor) bool {
	switch {
	case !r.PurgedAt.Equal(c.PurgedAt):
		return r.PurgedAt.After(c.PurgedAt)
	case r.Tool != c.Tool:
		return r.Tool > c.Tool
	default:
		return r.UnitID > c.UnitID
	}
}

// PageBlobPurges defines the result of ListBlobPurges for a list of
// records: at most limit records after the cursor when one is set, in the
// order of the port. Store conformance tests compare each adapter with it.
func PageBlobPurges(records []BlobPurge, after *BlobPurgeCursor, limit int) []BlobPurge {
	out := make([]BlobPurge, 0, len(records))

	for _, r := range records {
		if after == nil || blobPurgeAfter(r, *after) {
			out = append(out, r)
		}
	}

	slices.SortFunc(out, func(a, b BlobPurge) int {
		switch {
		case !a.PurgedAt.Equal(b.PurgedAt):
			return a.PurgedAt.Compare(b.PurgedAt)
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
