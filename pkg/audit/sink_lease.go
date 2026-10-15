package audit

import (
	"context"
	"errors"
	"time"
)

// Lease timing for audit sinks. Only the holder of the lease of a sink ships
// to it, so two instances never ship one entry twice. The holder renews well
// before the lease ends, and the sink cursor stays the source of truth, so a
// new holder goes on from the cursor.
const (
	// SinkLeaseDuration is how long a taken or renewed sink lease lasts.
	SinkLeaseDuration = 30 * time.Second
	// SinkLeaseRenewInterval is how often the holder renews its sink lease.
	SinkLeaseRenewInterval = 10 * time.Second
)

// ErrInvalidSinkLease reports a sink lease call with an unusable sink name,
// holder or time.
var ErrInvalidSinkLease = errors.New("invalid sink lease")

// SinkLeaser keeps the lease of each log sink: which instance ships to it
// and when that right ends. The SQL stores implement it next to Feed.
type SinkLeaser interface {
	// AcquireSinkLease takes or renews the lease of sink for holder for ttl
	// from the database clock, in one conditional write. It answers true
	// when the caller holds the lease after the call: no row existed, the
	// stored lease had ended, or the caller already held it. It answers
	// false when another instance holds a live lease and changes nothing.
	// It reports ErrInvalidSinkLease for an unusable sink name, an empty
	// holder or a non-positive ttl.
	AcquireSinkLease(ctx context.Context, sink, holder string, ttl time.Duration) (bool, error)
	// ReleaseSinkLease releases the lease of sink only when holder holds it.
	// It changes nothing when no row exists or another instance holds the
	// lease. It reports ErrInvalidSinkLease for an unusable sink name or an
	// empty holder.
	ReleaseSinkLease(ctx context.Context, sink, holder string) error
}
