package s3

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"
	"syscall"
	"time"
	"unicode"

	"opendustpan.org/opendustpan/pkg/blob"
)

const (
	// defaultProbeDeadline is the total time that Open spends on the write
	// steps of the open-time probe, retries and backoff included: 30 seconds.
	defaultProbeDeadline = 30 * time.Second
	// probeCleanupDeadline is the separate time for the delete of the probe
	// object after a failure or a cancel: 5 seconds. The worst case of Open
	// is the sum of both: about 35 seconds. WithProbeDeadline does not change
	// this value.
	probeCleanupDeadline = 5 * time.Second
	// maxLogErrorBytes caps the error text in a retry log record.
	maxLogErrorBytes = 512
)

// WithProbeDeadline sets the total time that Open spends on the write steps
// of the open-time probe. A probe step that gets a transient error tries
// again until this time ends. The default is 30 seconds. The delete of the
// probe object after a failure has its own fixed deadline of 5 seconds, so
// the worst case is this value plus 5 seconds. This is an exported option
// for tests and for callers of the SDK. The serve command has no setting for
// it.
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
// pass on a second try: connection reset, connection refused, EOF, timeout,
// a temporary DNS error, 500, 502, 503, 504, 429 or SlowDown. A canceled
// context, an access error, a missing bucket, a bad signature, 501, 505, a
// name that does not exist and every other answer fail at once.
func isTransient(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}

	if retryErr, ok := errors.AsType[*retryableError](err); ok && retryErr != nil {
		return true
	}

	// A DNS error is transient only when the resolver marks it so. A name
	// that does not exist fails at once.
	if dnsErr, ok := errors.AsType[*net.DNSError](err); ok && dnsErr != nil {
		return dnsErr.IsTemporary || dnsErr.IsTimeout
	}

	return isTransientNetwork(err)
}

// isTransientNetwork reports whether err is a connection error that can pass
// on a second try: reset, refused, aborted, broken pipe, EOF or a timeout.
func isTransientNetwork(err error) bool {
	for _, target := range []error{
		io.EOF, io.ErrUnexpectedEOF, syscall.ECONNRESET, syscall.ECONNABORTED,
		syscall.ECONNREFUSED, syscall.EPIPE,
	} {
		if errors.Is(err, target) {
			return true
		}
	}

	netErr, ok := errors.AsType[net.Error](err)

	return ok && netErr.Timeout()
}

// logText returns err text that is safe for a log record: it drops CR, LF
// and the other control characters and it holds at most maxLogErrorBytes
// bytes. The text can hold data from the store, so it is untrusted.
func logText(err error) string {
	text := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}

		return r
	}, err.Error())

	if len(text) <= maxLogErrorBytes {
		return text
	}

	const marker = "..."

	return strings.ToValidUTF8(text[:maxLogErrorBytes-len(marker)], "") + marker
}

// retryProbe runs op until it succeeds, fails with an error that is not
// transient, or ctx ends. It waits with the exponential backoff of the store
// between tries and logs each retry. When ctx ends, the error names the last
// cause. It returns the number of tries with the error.
func (s *Store) retryProbe(
	ctx context.Context,
	step string,
	op func(ctx context.Context) error,
) (int, error) {
	// cause is the last error that did not come from the end of ctx.
	var cause error

	for attempt := 0; ; attempt++ {
		err := op(ctx)
		if err == nil || !isTransient(err) {
			return attempt + 1, err
		}

		ctxErr := ctx.Err()
		if ctxErr == nil || cause == nil {
			cause = err
		}

		if ctxErr != nil {
			return attempt + 1, fmt.Errorf(
				"probe %s stopped after %d tries: %w: last cause: %w", step, attempt+1, ctxErr, cause,
			)
		}

		delay := s.backoff(attempt)

		s.logger.WarnContext(ctx, "s3 probe step failed with a transient error; trying again",
			slog.String("step", step),
			slog.Int("attempt", attempt+1),
			slog.Duration("retry_in", delay),
			slog.String("error", logText(err)),
		)

		if waitErr := s.sleep(ctx, delay); waitErr != nil {
			return attempt + 1, fmt.Errorf(
				"probe %s stopped after %d tries: %w: last cause: %w", step, attempt+1, waitErr, cause,
			)
		}
	}
}

// probePut writes the probe object with retries on transient errors. It
// returns the number of tries.
func (s *Store) probePut(ctx context.Context, key blob.Key) (int, error) {
	tries, err := s.retryProbe(ctx, "write", func(ctx context.Context) error {
		return s.Put(ctx, key, bytes.NewReader([]byte(probeBody)))
	})

	return tries, err
}

// probeDelete removes the probe object. It uses its own deadline of 5
// seconds and ignores a cancel of ctx, so a failed or canceled start still
// removes the probe.
func (s *Store) probeDelete(ctx context.Context, key blob.Key) error {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), probeCleanupDeadline)
	defer cancel()

	_, err := s.retryProbe(cleanupCtx, "delete", func(ctx context.Context) error {
		return s.Delete(ctx, key)
	})

	return err
}
