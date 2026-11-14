package backup

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"

	"opendustpan.org/opendustpan/pkg/durable"
)

// MaxUnverifiedKeys is the largest number of unverified keys that a backup may
// hold: the keys that rows of the SQL data name without a size and an MD5. The
// backup copies no such blob, and it lists the key in the manifest, so a
// restore accepts the list. A count above the bound stops the backup before it
// writes a manifest: such a count is a sign of a fault, and a restore of that
// manifest holds that many rows with no content. No setting changes the bound.
const MaxUnverifiedKeys = 10000

// ErrTooManyUnverified reports a backup whose SQL data names more blobs
// without a size and an MD5 than MaxUnverifiedKeys allows. The backup writes
// no manifest, so no restore applies it. A *TooManyUnverifiedError matches it.
var ErrTooManyUnverified = errors.New("too many unverified keys in the backup data")

// TooManyUnverifiedError carries the facts of ErrTooManyUnverified: the count
// of the keys that the data names without a size and an MD5, and the bound.
// Count is at least 1 and Limit is at least 0.
type TooManyUnverifiedError struct {
	Count int
	Limit int
}

// Error returns "the data names <count> blobs without a size and an MD5, more
// than the limit of <limit>. The backup wrote no manifest, so no restore
// applies it".
func (e *TooManyUnverifiedError) Error() string {
	return fmt.Sprintf(
		"the data names %d blobs without a size and an MD5, more than the limit of %d. "+
			"The backup wrote no manifest, so no restore applies it.",
		e.Count, e.Limit,
	)
}

// Is reports whether target is ErrTooManyUnverified.
func (e *TooManyUnverifiedError) Is(target error) bool {
	return target == ErrTooManyUnverified
}

// UnverifiedKeyLimit returns the largest number of unverified keys that a
// backup this service writes may hold. It is MaxUnverifiedKeys, or the smaller
// bound of a test.
func (s *Service) UnverifiedKeyLimit() int {
	return s.unverifiedLimit()
}

// unverifiedLimit returns the bound on the unverified keys of a backup:
// MaxUnverifiedKeys, or the smaller bound that a test set.
func (s *Service) unverifiedLimit() int {
	if s.maxUnverified > 0 {
		return s.maxUnverified
	}

	return MaxUnverifiedKeys
}

// refuseUnverified returns nil when keys holds at most limit unverified keys,
// else a *TooManyUnverifiedError. It never drops a key of the list.
func refuseUnverified(keys []string, limit int) error {
	if len(keys) <= limit {
		return nil
	}

	return &TooManyUnverifiedError{Count: len(keys), Limit: limit}
}

// unverifiedLimit returns the bound on the unverified keys of a backup that
// this restore applies: the bound of the backup service of the server, or the
// package bound when no backup service is wired.
func (s *RestoreService) unverifiedLimit() int {
	if s.backups == nil {
		return MaxUnverifiedKeys
	}

	return s.backups.UnverifiedKeyLimit()
}

// recordUnverifiedOverLimit records, as an audit event of the restore and in
// the log, that the manifest of the backup names more unverified keys than the
// bound allows. A second run of the step after a crash records the same
// object, and the store keeps one copy, so the restore has one such event. The
// log line can repeat. The restore goes on: the bound stops a new backup, and
// a manifest that an earlier version of the server wrote stays restorable.
func (s *RestoreService) recordUnverifiedOverLimit(
	ctx context.Context, fence durable.StepFence, in restoreInput, count, limit int,
) error {
	object := unverifiedOverLimitAuditObject(in, count, limit)

	if err := s.live.RecordRestoreEvent(ctx, fence, in.ID, object); err != nil {
		return fmt.Errorf("record the unverified keys of restore %s: %w", in.ID, err)
	}

	s.logger.WarnContext(ctx, "the restore applies a backup over the limit on blobs without a check value",
		slog.String("restore_id", in.ID.String()), slog.String("backup_id", in.BackupID.String()),
		slog.Int("blobs", count), slog.Int("limit", limit))

	return nil
}

// unverifiedOverLimitAuditObject names what restore in applies, for its audit
// entry: the backup data names more blobs without a size and an MD5 than the
// bound allows.
func unverifiedOverLimitAuditObject(in restoreInput, count, limit int) string {
	return "restore " + in.ID.String() + " applies backup " + in.BackupID.String() + ": the data names " +
		strconv.Itoa(count) + " blobs without a size and an MD5, more than the limit of " + strconv.Itoa(limit)
}
