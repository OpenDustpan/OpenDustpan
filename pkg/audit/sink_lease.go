package audit

import (
	"context"
	"errors"
	"time"

	"opendustpan.org/opendustpan/pkg/durable"
)

// Lease timing for audit sinks. While its lease holds, one instance ships
// a sink. After a lease is lost or a round fails, a sink can receive an
// entry twice. Delivery is at least once. The holder renews well before
// the lease ends, and the sink cursor stays the source of truth, so a new
// holder goes on from the cursor.
const (
	// SinkLeaseDuration is how long a taken or renewed sink lease lasts.
	SinkLeaseDuration = 30 * time.Second
	// SinkLeaseRenewInterval is how often the holder renews its sink lease.
	SinkLeaseRenewInterval = 10 * time.Second
)

// SinkLeaseKind names the durable operations that hold audit sink leases:
// one operation per sink. The lease of a sink is the live claim of its
// operation, with the claim rule of the durable port: the expiry is on the
// clock of the store, the token grows on each new claim, and every cursor
// write checks the token, so a holder that lost its claim cannot advance
// a cursor.
const SinkLeaseKind = durable.Kind("audit-sink")

// SinkLeaseKey answers the start key of the durable operation that holds
// the lease of sink.
func SinkLeaseKey(sink string) string {
	return "audit-sink/" + sink
}

// ErrInvalidSinkLease reports a sink lease call with an unusable sink name,
// holder or time.
var ErrInvalidSinkLease = errors.New("invalid sink lease")

// zeroClaim answers the zero claim: no operation, no owner, no token and
// no expiry. A shipper without a lease store ships with it, and an acquire
// answers it while another instance holds the lease.
func zeroClaim() durable.Claim {
	return durable.Claim{Operation: "", Owner: "", Token: 0, ExpiresAt: time.Time{}}
}

// SinkLeaser keeps the lease of each log sink: which instance ships to it
// and when that right ends. The SQL stores implement it on the durable
// operation of each sink, next to Feed.
type SinkLeaser interface {
	// AcquireSinkLease takes or renews the lease of sink for holder for ttl
	// from the database clock, in one transaction. It answers the live
	// claim of the sink: a new claim with a higher token when no claim is
	// live, the same claim with the expiry moved to the database clock
	// plus ttl when the caller already holds it, also when that expiry is
	// earlier than the one before. It answers durable.ErrNoWork, and changes nothing, while
	// another instance holds a live claim. It reports ErrInvalidSinkLease
	// for an unusable sink name, an empty holder or a non-positive ttl.
	AcquireSinkLease(ctx context.Context, sink, holder string, ttl time.Duration) (durable.Claim, error)
	// ReleaseSinkLease releases the claim on its sink operation. It answers
	// durable.ErrClaimLost, and changes nothing, when claim is not the
	// live claim of its operation.
	ReleaseSinkLease(ctx context.Context, claim durable.Claim) error
}
