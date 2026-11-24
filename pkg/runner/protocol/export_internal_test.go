package protocol

import (
	"time"

	"opendustpan.org/opendustpan/pkg/namespace"
)

// NewClaimLeaseForTest exposes newClaimLease to the black-box tests.
func NewClaimLeaseForTest(
	holder string,
	fence, current namespace.Fence,
	expiry time.Time,
) error {
	_, err := newClaimLease(holder, fence, current, expiry)

	return err
}
