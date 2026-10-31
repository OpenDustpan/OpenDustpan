package storecheck

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"opendustpan.org/opendustpan/pkg/blob"
)

// ReapPrefix deletes the objects under prefix, which must be exactly the
// prefix of the ended check id, on the store of target. It is the prefix
// reaper. It uses the same allowlist and the same view as the check, so it
// refuses every other prefix with ErrPrefixRefused and writes a denial entry.
// It returns ErrRunning for a check that runs, ErrNotFound for an ID with no
// row, and the count of objects that stayed.
func (s *Service) ReapPrefix(ctx context.Context, target Target, id ID, prefix string) (int, error) {
	store, ok := s.stores[target]
	if !ok {
		return 0, fmt.Errorf("%w: %s", ErrNoTarget, target)
	}

	view, err := s.viewOf(ctx, store, id, prefix)
	if err != nil {
		return 0, err
	}

	// Only a prefix of a check that this installation made and that ended is
	// ours to delete. A store can be shared with another installation, and a
	// running check still writes.
	rec, err := s.records.GetCheck(ctx, id)
	if err != nil {
		return 0, fmt.Errorf("read store check: %w", err)
	}

	if rec.Target != target {
		return 0, ErrNotFound
	}

	if rec.State == StateRunning {
		return 0, ErrRunning
	}

	remaining, err := deletePrefix(ctx, view, view.Prefix())
	if err != nil {
		return remaining, fmt.Errorf("delete the prefix of store check %s: %w", id, err)
	}

	return remaining, nil
}

// reapEnded deletes what the checks that ended in the last day left under
// their prefixes. A check that ended well removed its objects, so a listing
// shows nothing. A target with object lock refuses the delete, and the window
// ends the retries.
func (s *Service) reapEnded(ctx context.Context) {
	recs, err := s.records.ListChecks(ctx)
	if err != nil {
		if ctx.Err() == nil {
			s.logger.WarnContext(ctx, "store check prefixes were not reaped", slog.String("error", errorClass(err)))
		}

		return
	}

	cutoff := s.now().Add(-reapWindow)

	for i := range recs {
		rec := &recs[i]
		if rec.State == StateRunning || rec.EndedAt.Before(cutoff) {
			continue
		}

		if _, reapErr := s.ReapPrefix(ctx, rec.Target, rec.ID, rec.ID.Prefix()); reapErr != nil &&
			!errors.Is(reapErr, blob.ErrNotFound) && ctx.Err() == nil {
			s.logger.WarnContext(ctx, "store check prefix was not reaped",
				slog.String("check", rec.ID.String()), slog.String("error", errorClass(reapErr)))
		}
	}
}
