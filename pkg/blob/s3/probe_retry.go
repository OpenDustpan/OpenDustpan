package s3

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"syscall"
	"time"

	"opendustpan.org/opendustpan/pkg/blob"
)

// defaultProbeDeadline is the total time Open spends on the probe, retries
// and backoff included. WithProbeDeadline changes it.
const defaultProbeDeadline = 30 * time.Second

// WithProbeDeadline sets the total time that Open spends on the open-time
// probe. A probe step that gets a transient network error, a 5xx answer or
// SlowDown tries again until this time ends. The default is 30 seconds.
func WithProbeDeadline(d time.Duration) Option {
	return func(s *Store) {
		if d > 0 {
			s.probeDeadline = d
		}
	}
}

// WithLogger sets the logger that Open uses to report each probe retry. The
// default logger discards records.
func WithLogger(logger *slog.Logger) Option {
	return func(s *Store) {
		if logger != nil {
			s.logger = logger
		}
	}
}

// isTransient reports whether err is a network error or an answer that can
// pass on a second try: connection reset, EOF, timeout, 5xx, 429 or SlowDown.
// A canceled context, an access error, a missing bucket and a bad signature
// are not transient. They fail at once.
func isTransient(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}

	if retryErr, ok := errors.AsType[*retryableError](err); ok && retryErr != nil {
		return true
	}

	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.ECONNABORTED) ||
		errors.Is(err, syscall.EPIPE) {
		return true
	}

	netErr, ok := errors.AsType[net.Error](err)

	return ok && netErr.Timeout()
}

// retryProbe runs op until it succeeds, fails with an error that is not
// transient, or ctx ends. It waits with the exponential backoff of the store
// between tries and logs each retry. When ctx ends, the error names the
// last cause.
func (s *Store) retryProbe(ctx context.Context, step string, op func(ctx context.Context) error) error {
	for attempt := 0; ; attempt++ {
		err := op(ctx)
		if err == nil || !isTransient(err) {
			return err
		}

		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("probe %s stopped after %d tries: %w: last cause: %w", step, attempt+1, ctxErr, err)
		}

		delay := s.backoff(attempt)

		s.logger.WarnContext(ctx, "s3 probe step failed with a transient error; trying again",
			slog.String("step", step),
			slog.Int("attempt", attempt+1),
			slog.Duration("retry_in", delay),
			slog.String("error", err.Error()),
		)

		if waitErr := s.sleep(ctx, delay); waitErr != nil {
			return fmt.Errorf("probe %s stopped after %d tries: %w: last cause: %w", step, attempt+1, waitErr, err)
		}
	}
}

// probePut writes the probe object with retries on transient errors.
func (s *Store) probePut(ctx context.Context, key blob.Key) error {
	return s.retryProbe(ctx, "write", func(ctx context.Context) error {
		return s.Put(ctx, key, bytes.NewReader([]byte(probeBody)))
	})
}

// probeDelete removes the probe object. It uses its own deadline and ignores
// a cancel of ctx, so a canceled start still removes the probe.
func (s *Store) probeDelete(ctx context.Context, key blob.Key) error {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.probeDeadline)
	defer cancel()

	return s.retryProbe(cleanupCtx, "delete", func(ctx context.Context) error {
		return s.Delete(ctx, key)
	})
}
