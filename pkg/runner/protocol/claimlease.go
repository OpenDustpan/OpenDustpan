package protocol

import (
	"errors"
	"time"

	"opendustpan.org/opendustpan/pkg/namespace"
)

// claimLease is the lease of one claim: one holder, one fence and one
// expiry. A claim without a holder or without a fence is impossible, and the
// expiry moves only through HeartbeatClaim, which returns a new lease.
type claimLease struct {
	holder    string
	fence     namespace.Fence
	expiresAt time.Time
}

// newClaimLease makes the lease of one claim for holder, taken on the fence
// fence and holding the work until expiry. It rejects an empty holder, a
// fence that the namespace of the fence has moved past (a stale fence), and an
// expiry that is not positive.
func newClaimLease(holder string, fence namespace.Fence, current namespace.Fence, expiry time.Time) (*claimLease, error) {
	if holder == "" {
		return nil, errors.New("a claim has a holder")
	}

	if fence.FailoverVersion < current.FailoverVersion {
		return nil, errors.New("the fence of the claim is stale")
	}

	if !expiry.After(time.Time{}) {
		return nil, errors.New("the expiry of a claim must be positive")
	}

	return &claimLease{holder: holder, fence: fence, expiresAt: expiry}, nil
}

// renew returns the lease of the claim after a heartbeat that moves the
// expiry by d from now, and rejects a fence that the namespace has moved past
// since the claim.
func (l *claimLease) renew(current namespace.Fence, now time.Time, d time.Duration) (*claimLease, error) {
	if l.fence.FailoverVersion < current.FailoverVersion {
		return nil, errors.New("the fence of the claim is stale")
	}

	if d <= 0 {
		return nil, errors.New("a lease must be positive")
	}

	return newClaimLease(l.holder, l.fence, current, now.Add(d))
}
