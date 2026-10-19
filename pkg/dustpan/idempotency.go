package dustpan

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"opendustpan.org/opendustpan/internal/api/idempotent"
)

// idempotencyBodyLimit bounds a request body that the Idempotency-Key
// middleware reads to fingerprint it.
const idempotencyBodyLimit = 1 << 20

// idempotencySweepEvery is the period of the deletion of the expired replay
// records.
const idempotencySweepEvery = time.Hour

// idempotencyMiddleware returns the middleware of the Idempotency-Key
// header, or nil when no store of replay records is set.
func idempotencyMiddleware(cfg *settings) (*idempotent.Middleware, error) {
	if cfg.idempotencyStore == nil {
		return nil, nil //nolint:nilnil // No store: a request with the header answers 400.
	}

	mw, err := idempotent.New(idempotent.Config{
		Store: cfg.idempotencyStore, Logger: cfg.logger, Now: cfg.now, MaxBodyBytes: idempotencyBodyLimit,
	})
	if err != nil {
		return nil, fmt.Errorf("idempotency middleware: %w", err)
	}

	return mw, nil
}

// sweepIdempotency deletes the expired replay records once each
// idempotencyEvery, until ctx is done.
func (s *Server) sweepIdempotency(ctx context.Context) {
	if s.idempotency == nil {
		return
	}

	every := s.idempotencyEvery
	if every <= 0 {
		every = idempotencySweepEvery
	}

	ticker := time.NewTicker(every)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.sweepIdempotencyOnce(ctx)
		}
	}
}

// sweepIdempotencyOnce deletes the expired replay records once. A failed run
// logs no error text, which may hold driver detail, and the next run tries
// again.
func (s *Server) sweepIdempotencyOnce(ctx context.Context) {
	n, err := s.idempotency.Sweep(ctx, time.Now())

	switch {
	case err != nil && !errors.Is(ctx.Err(), context.Canceled):
		s.logger.WarnContext(ctx, "sweep of the expired idempotency records failed")
	case n > 0:
		s.logger.InfoContext(ctx, "deleted the expired idempotency records", slog.Int("records", n))
	default:
	}
}
