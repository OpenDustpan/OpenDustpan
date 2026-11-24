package protocol

import (
	"errors"
	"time"

	"opendustpan.org/opendustpan/pkg/namespace"
)

var (
	errClaimNoHolder    = errors.New("a claim has a holder")
	errClaimStaleFence  = errors.New("the fence of the claim is stale")
	errClaimNoPosExpiry = errors.New("the expiry of a claim must be positive")
)

// claimLease is the lease of one claim: one holder, one fence and one
// expiry. A claim without a holder or without a fence is impossible, and the
// expiry moves only through HeartbeatClaim, which returns a new lease.
type claimLease struct {
	holder    string
	fence     namespace.Fence
	expiresAt time.Time
}

// newClaimLease makes the lease of one claim for holder, taken on fence and
// holding the work until expiry. It rejects an empty holder, a fence that the
// namespace has moved past (a stale fence, compared with current), and an
// expiry that is not positive.
func newClaimLease(
	holder string,
	fence, current namespace.Fence,
	expiry time.Time,
) (*claimLease, error) {
	if holder == "" {
		return nil, errClaimNoHolder
	}

	if fence.FailoverVersion < current.FailoverVersion {
		return nil, errClaimStaleFence
	}

	if !expiry.After(time.Time{}) {
		return nil, errClaimNoPosExpiry
	}

	return &claimLease{holder: holder, fence: fence, expiresAt: expiry}, nil
}
