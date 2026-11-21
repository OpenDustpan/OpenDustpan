package dustpan

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

// profileSessionSweepEvery is the period of the deletion of the ended open
// sessions.
const profileSessionSweepEvery = time.Hour

// profileSessionSweepLimit caps the ended open sessions that one sweep
// deletes.
const profileSessionSweepLimit = 1000

// sweepProfileSessions deletes the ended open sessions once each
// profileSessionsEvery, until ctx is done.
func (s *Server) sweepProfileSessions(ctx context.Context) {
	if s.profileSessions == nil {
		return
	}

	every := s.profileSessionsEvery
	if every <= 0 {
		every = profileSessionSweepEvery
	}

	ticker := time.NewTicker(every)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.sweepProfileSessionsOnce(ctx)
		}
	}
}

// sweepProfileSessionsOnce deletes the ended open sessions once. A failed run
// logs no error text, which may hold driver detail, and the next run tries
// again.
func (s *Server) sweepProfileSessionsOnce(ctx context.Context) {
	n, err := s.profileSessions.Sweep(ctx, s.now(), profileSessionSweepLimit)

	switch {
	case err != nil && !errors.Is(ctx.Err(), context.Canceled):
		s.logger.WarnContext(ctx, "sweep of the ended open sessions failed")
	case n > 0:
		s.logger.InfoContext(ctx, "deleted the ended open sessions", slog.Int("sessions", n))
	default:
	}
}
