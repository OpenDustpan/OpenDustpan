package dustpan

import (
	"context"
	"database/sql/driver"
	"errors"
	"io"
	"net"
	"syscall"

	"opendustpan.org/opendustpan/pkg/namespace"
)

// RetryAfterSeconds is the wait that the readiness probe names in its
// Retry-After header while the store cannot be reached.
const RetryAfterSeconds = 30

// pingOutage reports whether a failed readiness ping means that the store
// cannot be reached. A canceled request is not an outage: the caller stopped
// the probe, so no answer is read.
func pingOutage(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}

	if errors.Is(err, namespace.ErrUnavailable) || errors.Is(err, namespace.ErrUnreachable) {
		return true
	}

	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, driver.ErrBadConn) ||
		errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, syscall.EPIPE) {
		return true
	}

	var opErr *net.OpError

	return errors.As(err, &opErr)
}
