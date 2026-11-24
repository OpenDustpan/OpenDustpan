package protocol

import (
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/namespace"
)

// TestNewClaimLeaseRejectsHostileInput proves that the constructor of the
// claim lease refuses an empty holder, a stale fence and a non-positive
// expiry.
func TestNewClaimLeaseRejectsHostileInput(t *testing.T) {
	t.Parallel()

	fresh := namespace.Fence{Namespace: namespace.Name("acme"), FailoverVersion: 1}
	stale := namespace.Fence{Namespace: namespace.Name("acme"), FailoverVersion: 0}
	now := time.UnixMilli(1_700_000_000_000).UTC()

	if _, err := newClaimLease("", fresh, fresh, now.Add(time.Duration(ClaimLease))); err == nil {
		t.Fatal("newClaimLease with an empty holder accepted the lease")
	}

	if _, err := newClaimLease("rn-1", stale, fresh, now.Add(time.Duration(ClaimLease))); err == nil {
		t.Fatal("newClaimLease with a stale fence accepted the lease")
	}

	if _, err := newClaimLease("rn-1", fresh, fresh, time.Time{}); err == nil {
		t.Fatal("newClaimLease with a non-positive expiry accepted the lease")
	}
}
